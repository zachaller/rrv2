/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package analysis reconciles AnalysisRun CRs: it ticks each metric's
// provider on its configured interval, evaluates SuccessCondition /
// FailureCondition expressions, and surfaces a terminal Phase when metrics
// resolve.
//
// v0.1 scope: skeleton only. The Prometheus provider is the first target; the
// other providers (Datadog, NewRelic, Wavefront, CloudWatch, Kayenta, Web,
// Job) land in v0.2.
package analysis

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// Reconciler reconciles AnalysisRuns.
type Reconciler struct {
	Client   ctrlclient.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// SetupWithManager wires the reconciler.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("analysis-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&rolloutsv1alpha1.AnalysisRun{}).
		Complete(r)
}

// Reconcile is a skeleton that transitions Pending runs to Running. Real
// provider evaluation lands in v0.2.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	run := &rolloutsv1alpha1.AnalysisRun{}
	if err := r.Client.Get(ctx, req.NamespacedName, run); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if run.Status.Phase == "" {
		patched := run.DeepCopy()
		patched.Status.Phase = "Pending"
		now := metav1.NewTime(time.Now())
		patched.Status.StartedAt = &now
		if err := r.Client.Status().Update(ctx, patched); err != nil {
			return reconcile.Result{}, err
		}
	}
	// TODO(rollouts): tick provider queries on Metric.Interval; evaluate
	// SuccessCondition / FailureCondition expressions (gval).
	return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
}
