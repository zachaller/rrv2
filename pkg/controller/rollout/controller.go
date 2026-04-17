/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package rollout is the Rollouts reconciler. It owns Rollout CRs, their
// ReplicaSets, and (indirectly) the traffic-routing resources they reference.
//
// Architecture mirrors the upstream Kubernetes Deployment controller: each
// Rollout is reconciled by syncRollout, which delegates steady-state RS
// bookkeeping (revision sync, RS creation, scaling math, progress deadline,
// cleanup) to the forked code under _forkedfrom_k8s/ via a synthetic Deployment
// view (see adapter.go). The piece that differs is strategy: instead of the
// upstream RollingUpdate/Recreate switch, we hand off to a progression
// Executor that interprets the rollout's named []Step sequence.
package rollout

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
	forked "github.com/zachaller/rrv2/pkg/controller/rollout/_forkedfrom_k8s"
	"github.com/zachaller/rrv2/pkg/trafficrouting"
)

// Reconciler reconciles Rollout objects.
type Reconciler struct {
	Client   ctrlclient.Client
	Kube     kubernetes.Interface
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// ResolveRouter builds the TrafficRouting plugin for a given Rollout. It
	// is overridable in tests.
	ResolveRouter func(ro *rolloutsv1alpha1.Rollout) (trafficrouting.Plugin, error)

	// forked is the stateless adapter around the upstream deployment
	// controller's reconcile primitives.
	forked *forked.DeploymentController
}

// SetupWithManager wires the Reconciler into a controller-runtime Manager:
// watch Rollouts, owned ReplicaSets, and Pods (for pod deletions that may
// force a resync), and enqueue reconciles accordingly.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("rollout-controller")
	}
	if r.ResolveRouter == nil {
		r.ResolveRouter = func(ro *rolloutsv1alpha1.Rollout) (trafficrouting.Plugin, error) {
			return trafficrouting.Resolve(ro, r.Kube, nil)
		}
	}
	if r.Kube == nil {
		kc, err := kubernetes.NewForConfig(mgr.GetConfig())
		if err != nil {
			return fmt.Errorf("build kubernetes clientset: %w", err)
		}
		r.Kube = kc
	}
	r.forked = forked.NewHeadless(r.Kube, r.Recorder, newClientsetRSLister(r.Kube))

	return ctrl.NewControllerManagedBy(mgr).
		For(&rolloutsv1alpha1.Rollout{}).
		Owns(&appsv1.ReplicaSet{}).
		WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.Pod{}, handler.TypedEnqueueRequestsFromMapFunc(r.mapPodToRollout))).
		Complete(r)
}

// mapPodToRollout returns the Rollout key for a Pod whose owner chain ends at a
// ReplicaSet owned by a Rollout. Used to requeue on pod deletions.
func (r *Reconciler) mapPodToRollout(ctx context.Context, pod *corev1.Pod) []reconcile.Request {
	rsName := ""
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" && owner.Controller != nil && *owner.Controller {
			rsName = owner.Name
			break
		}
	}
	if rsName == "" {
		return nil
	}
	rs := &appsv1.ReplicaSet{}
	if err := r.Client.Get(ctx, ctrlclient.ObjectKey{Namespace: pod.Namespace, Name: rsName}, rs); err != nil {
		return nil
	}
	for _, owner := range rs.OwnerReferences {
		if owner.Kind == "Rollout" && owner.Controller != nil && *owner.Controller {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}}}
		}
	}
	return nil
}

// Reconcile handles one Rollout.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := klog.FromContext(ctx).WithValues("rollout", req.NamespacedName)

	ro := &rolloutsv1alpha1.Rollout{}
	if err := r.Client.Get(ctx, req.NamespacedName, ro); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if !ro.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	applyDefaults(ro)

	rsList, err := r.listReplicaSets(ctx, ro)
	if err != nil {
		return reconcile.Result{}, err
	}

	requeueAfter, reconcileErr := r.syncRollout(ctx, ro, rsList)
	if reconcileErr != nil {
		logger.Error(reconcileErr, "syncRollout failed")
	}
	return reconcile.Result{RequeueAfter: requeueAfter}, reconcileErr
}

// listReplicaSets returns ReplicaSets in the rollout's namespace that match
// its selector AND are owned by this Rollout. We filter in two stages because
// label selectors alone can't disambiguate two Rollouts that happen to share
// a selector (a configuration error, but one that would otherwise cause the
// controllers to fight over each other's RSes).
func (r *Reconciler) listReplicaSets(ctx context.Context, ro *rolloutsv1alpha1.Rollout) ([]*appsv1.ReplicaSet, error) {
	if ro.Spec.Selector == nil {
		return nil, fmt.Errorf("rollout %s/%s has no selector", ro.Namespace, ro.Name)
	}
	sel, err := metav1.LabelSelectorAsSelector(ro.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("parse selector for %s/%s: %w", ro.Namespace, ro.Name, err)
	}

	list := &appsv1.ReplicaSetList{}
	if err := r.Client.List(ctx, list, ctrlclient.InNamespace(ro.Namespace), ctrlclient.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, err
	}
	out := make([]*appsv1.ReplicaSet, 0, len(list.Items))
	for i := range list.Items {
		rs := &list.Items[i]
		if !isOwnedBy(rs, ro) {
			continue
		}
		out = append(out, rs)
	}
	return out, nil
}

// syncRollout is the per-rollout reconcile entry point. It establishes the
// new ReplicaSet (creating it if necessary), reconciles replica counts
// against the current traffic split, then hands off to the progression
// Executor which interprets the current step.
func (r *Reconciler) syncRollout(ctx context.Context, ro *rolloutsv1alpha1.Rollout, rsList []*appsv1.ReplicaSet) (time.Duration, error) {
	if ro.Spec.Paused {
		return 0, nil
	}

	// Resolve the router first so abort can unwind traffic.
	router, err := r.ResolveRouter(ro)
	if err != nil {
		return 0, err
	}

	// Establish the new ReplicaSet. The forked helpers accept a synthetic
	// Deployment view; we own the real Rollout state and RS ownerReferences
	// separately.
	newRS, oldRSs, err := r.reconcileReplicaSets(ctx, ro, rsList)
	if err != nil {
		return 0, fmt.Errorf("reconcile replicasets: %w", err)
	}

	// Drive RS replica counts toward the (canary, stable) split implied by
	// the current traffic weight. Runs on every reconcile so external scale
	// changes — HPA in particular — propagate immediately, not just at step
	// boundaries.
	if err := r.reconcileReplicas(ctx, ro, newRS, oldRSs); err != nil {
		return 0, fmt.Errorf("reconcile replicas: %w", err)
	}

	// Push status counters that are safe to compute without the progression's
	// decisions. Phase transitions are owned by the Executor.
	if err := r.pushReplicaCounts(ctx, ro, newRS, append(oldRSs, newRS)); err != nil {
		return 0, fmt.Errorf("push replica counts: %w", err)
	}

	// Abort path short-circuits progression.
	if ro.Spec.Abort {
		return r.abort(ctx, ro, router, newRS, oldRSs)
	}

	// Ensure CurrentStep is set to the first step on first sync.
	if ro.Status.CurrentStep == "" && len(ro.Spec.Progression.Steps) > 0 {
		first := ro.Spec.Progression.Steps[0]
		patched := ro.DeepCopy()
		patched.Status.CurrentStep = first.Name
		now := metav1Now()
		patched.Status.StepStartedAt = &now
		patched.Status.Phase = rolloutsv1alpha1.PhaseProgressing
		if err := r.Client.Status().Update(ctx, patched); err != nil {
			return 0, err
		}
		return 0, nil
	}

	exec := &Executor{
		client:   r.Client,
		kube:     r.Kube,
		recorder: r.Recorder,
		router:   router,
		forked:   r.forked,
	}
	return exec.Execute(ctx, ro, newRS, oldRSs)
}

// reconcileReplicaSets delegates to the forked deployment controller to
// ensure the canary RS exists with the right pod-template-hash. The Rollout
// is the parent — we retrofit the ownerReference after the forked helper
// creates/updates the RS (the helper sets the owner to the synthetic
// Deployment, which isn't the real object).
//
// The forked code's initial-create path would otherwise size a new RS at
// the full spec.replicas, temporarily doubling cluster capacity while
// reconcileReplicas brings it back to the canary split. We sidestep that
// by handing it a zero-replicas view on first creation — reconcileReplicas
// then grows the RS to (canary, stable) in a single step.
func (r *Reconciler) reconcileReplicaSets(ctx context.Context, ro *rolloutsv1alpha1.Rollout, rsList []*appsv1.ReplicaSet) (newRS *appsv1.ReplicaSet, oldRSs []*appsv1.ReplicaSet, err error) {
	d := asDeploymentView(ro, &ro.Spec)

	// If no new RS exists yet and we're about to create one, clamp initial
	// replicas to zero so the create doesn't surge cluster capacity before
	// reconcileReplicas has a chance to split. The next reconcile will scale
	// it to the correct target.
	if !hasMatchingRS(ro, rsList) {
		zero := int32(0)
		d.Spec.Replicas = &zero
	}

	newRS, oldRSs, err = r.forked.GetAllReplicaSetsAndSyncRevision(ctx, d, rsList, true)
	if err != nil {
		return nil, nil, err
	}

	// Ensure the RS is owned by the real Rollout, not the synthetic
	// Deployment the forked helper inserted. Idempotent.
	if newRS != nil {
		if err := r.ensureOwnedByRollout(ctx, ro, newRS); err != nil {
			return nil, nil, err
		}
	}
	return newRS, oldRSs, nil
}

// hasMatchingRS reports whether the RS list already contains one whose pod
// template matches the rollout's current template. Used to decide whether a
// GetAllReplicaSetsAndSyncRevision call will create a new RS or adopt an
// existing one.
func hasMatchingRS(ro *rolloutsv1alpha1.Rollout, rsList []*appsv1.ReplicaSet) bool {
	// Best-effort: compare pod-template-hash labels. If the rollout has
	// not yet stamped status.CurrentPodHash, we fall through to "treat as
	// create" which is the safe default.
	target := ro.Status.CurrentPodHash
	if target == "" {
		for _, rs := range rsList {
			if rs.Labels["pod-template-hash"] != "" {
				return true
			}
		}
		return false
	}
	for _, rs := range rsList {
		if rs.Labels["pod-template-hash"] == target {
			return true
		}
	}
	return false
}

// ensureOwnedByRollout rewrites a ReplicaSet's controller ownerReference to
// point at the Rollout. Called once on creation; idempotent on re-sync.
func (r *Reconciler) ensureOwnedByRollout(ctx context.Context, ro *rolloutsv1alpha1.Rollout, rs *appsv1.ReplicaSet) error {
	if isOwnedBy(rs, ro) {
		return nil
	}
	isController := true
	blockOwnerDeletion := true
	rs.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         rolloutsv1alpha1.SchemeGroupVersion.String(),
		Kind:               "Rollout",
		Name:               ro.Name,
		UID:                ro.UID,
		Controller:         &isController,
		BlockOwnerDeletion: &blockOwnerDeletion,
	}}
	_, err := r.Kube.AppsV1().ReplicaSets(rs.Namespace).Update(ctx, rs, metav1.UpdateOptions{})
	return err
}

// pushReplicaCounts computes aggregate replica counters from all RSes and
// updates status if they changed. No phase transitions happen here.
//
// status.DesiredReplicas is stamped alongside so observedSpecReplicas can
// tell whether the reconciler has caught up with an external spec.replicas
// change (HPA, manual kubectl scale). That's what guards the
// scaleDown-after-promote path against HPA races.
func (r *Reconciler) pushReplicaCounts(ctx context.Context, ro *rolloutsv1alpha1.Rollout, newRS *appsv1.ReplicaSet, allRSs []*appsv1.ReplicaSet) error {
	replicas, updated, ready, available := computeReplicaCounts(newRS, allRSs)
	desired := desiredReplicas(ro)
	if ro.Status.Replicas == replicas &&
		ro.Status.UpdatedReplicas == updated &&
		ro.Status.ReadyReplicas == ready &&
		ro.Status.AvailableReplicas == available &&
		ro.Status.DesiredReplicas == desired &&
		ro.Status.ObservedGeneration == ro.Generation {
		return nil
	}
	patched := ro.DeepCopy()
	patched.Status.Replicas = replicas
	patched.Status.UpdatedReplicas = updated
	patched.Status.ReadyReplicas = ready
	patched.Status.AvailableReplicas = available
	patched.Status.DesiredReplicas = desired
	patched.Status.ObservedGeneration = ro.Generation
	return r.Client.Status().Update(ctx, patched)
}

// abort tears traffic back to the stable RS, scales the canary RS to zero,
// and marks the rollout Aborted. Re-setting Abort=false resumes from the
// current step.
func (r *Reconciler) abort(ctx context.Context, ro *rolloutsv1alpha1.Rollout, router trafficrouting.Plugin, newRS *appsv1.ReplicaSet, oldRSs []*appsv1.ReplicaSet) (time.Duration, error) {
	if router != nil {
		if err := router.SetWeight(ctx, ro, 0); err != nil {
			return 0, fmt.Errorf("abort: reset weight: %w", err)
		}
	}

	// Scale the canary RS (the new revision) to zero while leaving the stable
	// RSes untouched — operators can re-apply Abort=false to resume.
	if newRS != nil && newRS.Spec.Replicas != nil && *newRS.Spec.Replicas > 0 {
		d := asDeploymentView(ro, &ro.Spec)
		if _, _, err := r.forked.ScaleReplicaSet(ctx, newRS, 0, d, "aborted"); err != nil {
			return 0, fmt.Errorf("abort: scale canary to 0: %w", err)
		}
	}
	_ = oldRSs // stable RSes are intentionally left at their existing scale

	if ro.Status.Phase == rolloutsv1alpha1.PhaseAborted {
		return 0, nil
	}
	patched := ro.DeepCopy()
	patched.Status.Phase = rolloutsv1alpha1.PhaseAborted
	patched.Status.Message = "rollout aborted via spec.abort"
	return 0, r.Client.Status().Update(ctx, patched)
}
