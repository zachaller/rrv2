/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// This file is NOT part of the upstream import. It provides a constructor
// that builds a minimal DeploymentController usable by headless callers (no
// informers, no workqueue) — and exports the handful of reconcile primitives
// the Rollouts controller actually invokes. Keeping this file separate
// preserves the contract that the files taken from k8s.io/kubernetes/pkg/
// controller/deployment stay byte-identical to upstream (modulo the
// mechanical package rename).

package forkeddeployment

import (
	"context"

	apps "k8s.io/api/apps/v1"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"

	"github.com/zachaller/rrv2/pkg/internal/k8scontroller"
)

// NewHeadless builds a DeploymentController for direct, synchronous use from
// the Rollouts reconciler. The returned value has no informers, no workqueue,
// and no event broadcaster hookup; it simply forwards to the clientset.
//
// rsLister may be a real informer-backed lister or a thin clientset-backed
// shim (see ClientsetRSLister). It is only dereferenced by getNewReplicaSet
// during pod-template-hash collision recovery.
func NewHeadless(client clientset.Interface, recorder record.EventRecorder, rsLister appsv1listers.ReplicaSetLister) *DeploymentController {
	dc := &DeploymentController{
		client:        client,
		eventRecorder: recorder,
		rsLister:      rsLister,
	}
	dc.rsControl = k8scontroller.RealRSControl{KubeClient: client, Recorder: recorder}
	return dc
}

// GetAllReplicaSetsAndSyncRevision is the exported entry point for
// getAllReplicaSetsAndSyncRevision. Returns the new RS (creating it when
// createIfNotExisted is true) and the list of older RSes owned by the
// synthetic Deployment view.
func (dc *DeploymentController) GetAllReplicaSetsAndSyncRevision(ctx context.Context, d *apps.Deployment, rsList []*apps.ReplicaSet, createIfNotExisted bool) (*apps.ReplicaSet, []*apps.ReplicaSet, error) {
	return dc.getAllReplicaSetsAndSyncRevision(ctx, d, rsList, createIfNotExisted)
}

// Scale is the exported entry point for scale. Proportionally scales all RSes
// owned by the synthetic Deployment so that the sum of replicas converges on
// d.Spec.Replicas.
func (dc *DeploymentController) Scale(ctx context.Context, d *apps.Deployment, newRS *apps.ReplicaSet, oldRSs []*apps.ReplicaSet) error {
	return dc.scale(ctx, d, newRS, oldRSs)
}

// ScaleReplicaSet is the exported entry point for scaleReplicaSet. Scales a
// single RS to newScale, emitting a ScalingReplicaSet event on the Deployment.
func (dc *DeploymentController) ScaleReplicaSet(ctx context.Context, rs *apps.ReplicaSet, newScale int32, d *apps.Deployment, scalingOperation string) (bool, *apps.ReplicaSet, error) {
	return dc.scaleReplicaSet(ctx, rs, newScale, d, scalingOperation)
}

// CleanupDeployment is the exported entry point for cleanupDeployment. Trims
// the RS history to RevisionHistoryLimit.
func (dc *DeploymentController) CleanupDeployment(ctx context.Context, oldRSs []*apps.ReplicaSet, d *apps.Deployment) error {
	return dc.cleanupDeployment(ctx, oldRSs, d)
}
