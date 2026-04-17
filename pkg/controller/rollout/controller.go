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
	"k8s.io/apimachinery/pkg/labels"
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
// its selector.
func (r *Reconciler) listReplicaSets(ctx context.Context, ro *rolloutsv1alpha1.Rollout) ([]*appsv1.ReplicaSet, error) {
	if ro.Spec.Selector == nil {
		return nil, fmt.Errorf("rollout %s/%s has no selector", ro.Namespace, ro.Name)
	}
	sel, err := labels.Parse(labelSelectorString(ro.Spec.Selector))
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
// new ReplicaSet (creating it if necessary), then hands off to the progression
// Executor which interprets the current step.
func (r *Reconciler) syncRollout(ctx context.Context, ro *rolloutsv1alpha1.Rollout, rsList []*appsv1.ReplicaSet) (time.Duration, error) {
	if ro.Spec.Paused {
		return 0, nil
	}

	// Handle abort before anything else — skips step progression entirely.
	if ro.Spec.Abort {
		return r.abort(ctx, ro, rsList)
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

	router, err := r.ResolveRouter(ro)
	if err != nil {
		return 0, err
	}

	exec := &Executor{
		client:   r.Client,
		kube:     r.Kube,
		recorder: r.Recorder,
		router:   router,
	}
	return exec.Execute(ctx, ro, rsList)
}

// abort tears traffic back to the stable RS and marks the rollout Aborted.
// ReplicaSets are left as-is — the user (or a follow-up reconcile with abort
// cleared) scales them to the stable state.
func (r *Reconciler) abort(ctx context.Context, ro *rolloutsv1alpha1.Rollout, _ []*appsv1.ReplicaSet) (time.Duration, error) {
	router, err := r.ResolveRouter(ro)
	if err != nil {
		return 0, err
	}
	if router != nil {
		if err := router.SetWeight(ctx, ro, 0); err != nil {
			return 0, fmt.Errorf("abort: reset weight: %w", err)
		}
	}
	patched := ro.DeepCopy()
	patched.Status.Phase = rolloutsv1alpha1.PhaseAborted
	patched.Status.Message = "rollout aborted via spec.abort"
	return 0, r.Client.Status().Update(ctx, patched)
}
