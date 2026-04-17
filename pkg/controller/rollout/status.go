/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package rollout

import (
	appsv1 "k8s.io/api/apps/v1"

	rolloutsv1alpha1 "github.com/zaller/rollouts/pkg/apis/rollouts/v1alpha1"
)

// computeReplicaCounts summarizes per-RS replica counts into the aggregate
// counters the Rollout exposes on status. The values match
// apps.DeploymentStatus exactly — the forked syncRolloutStatus helper
// produces these for a synthetic Deployment view and we copy them back here.
func computeReplicaCounts(newRS *appsv1.ReplicaSet, rsList []*appsv1.ReplicaSet) (replicas, updated, ready, available int32) {
	for _, rs := range rsList {
		if rs == nil {
			continue
		}
		replicas += rs.Status.Replicas
		ready += rs.Status.ReadyReplicas
		available += rs.Status.AvailableReplicas
	}
	if newRS != nil {
		updated = newRS.Status.Replicas
	}
	return
}

// derivePhase converts low-level signals into the RolloutStatus.Phase enum.
// The executor also transitions phases directly (Paused, Aborted, Healthy);
// derivePhase fills in Progressing / Degraded for steady-state reconciles.
func derivePhase(ro *rolloutsv1alpha1.Rollout, progressDeadlineExceeded bool) string {
	if ro.Status.Phase == rolloutsv1alpha1.PhaseAborted {
		return rolloutsv1alpha1.PhaseAborted
	}
	if ro.Status.Phase == rolloutsv1alpha1.PhaseHealthy {
		return rolloutsv1alpha1.PhaseHealthy
	}
	if progressDeadlineExceeded {
		return rolloutsv1alpha1.PhaseDegraded
	}
	if len(ro.Status.PauseConditions) > 0 {
		return rolloutsv1alpha1.PhasePaused
	}
	if ro.Status.CurrentStep == "" {
		return rolloutsv1alpha1.PhasePending
	}
	return rolloutsv1alpha1.PhaseProgressing
}
