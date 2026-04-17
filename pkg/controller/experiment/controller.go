/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package experiment reconciles Experiment CRs. Each variant becomes a
// short-lived ReplicaSet, optionally with traffic weights via the configured
// router, optionally with an owning AnalysisRun per variant. The experiment
// terminates when its Duration elapses or its analyses complete.
//
// v0.1 scope: skeleton only.
package experiment

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// Reconciler reconciles Experiments.
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
		r.Recorder = mgr.GetEventRecorderFor("experiment-controller")
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&rolloutsv1alpha1.Experiment{}).
		Complete(r)
}

// Reconcile is a skeleton that sets Phase to Pending on first sight.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	exp := &rolloutsv1alpha1.Experiment{}
	if err := r.Client.Get(ctx, req.NamespacedName, exp); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if exp.Status.Phase == "" {
		patched := exp.DeepCopy()
		patched.Status.Phase = "Pending"
		if err := r.Client.Status().Update(ctx, patched); err != nil {
			return reconcile.Result{}, err
		}
	}
	// TODO(rollouts): spawn variant ReplicaSets, drive AnalysisRuns, tear down
	// at Duration expiry. v0.2.
	return reconcile.Result{RequeueAfter: 30 * time.Second}, nil
}
