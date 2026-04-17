/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package rollout

import (
	"context"
	"fmt"
	"math"

	appsv1 "k8s.io/api/apps/v1"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// reconcileReplicas is the single place where ReplicaSet pod counts are
// driven toward the (canary, stable) split implied by the rollout's current
// weight and total desired replicas.
//
// It runs at the top of every reconcile, before any step handler fires, so
// that external changes to spec.replicas — HPA in particular — are applied
// every sweep rather than only at step boundaries.
//
// Step handlers that need to override this (SetCanaryScale, Promote, Abort)
// are free to patch the same RSes directly; their writes are read back on
// the next reconcile and the split math rebuilds from the new baseline.
func (r *Reconciler) reconcileReplicas(ctx context.Context, ro *rolloutsv1alpha1.Rollout, newRS *appsv1.ReplicaSet, oldRSs []*appsv1.ReplicaSet) error {
	if newRS == nil {
		return nil
	}
	if ro.Spec.HPAStrategy == rolloutsv1alpha1.HPAStrategyDisabled {
		return nil
	}

	desired := desiredReplicas(ro)
	canaryDesired, stableDesired := splitReplicas(desired, ro.Status.CurrentWeight, ro.Spec.HPAStrategy, currentCanaryReplicas(newRS))

	d := asDeploymentView(ro, &ro.Spec)

	if err := r.scaleIfNeeded(ctx, newRS, canaryDesired, d, "canaryTarget"); err != nil {
		return err
	}

	// Pick the largest old RS as "the stable" — the one we drive to
	// stableDesired. Other old RSes stay at whatever the previous reconcile
	// left them at (typically zero, post-promote; intermediate during
	// rolling transitions). The forked CleanupDeployment prunes them by
	// revisionHistoryLimit.
	stableRS := largestOldRS(oldRSs)
	if stableRS != nil {
		if err := r.scaleIfNeeded(ctx, stableRS, stableDesired, d, "stableTarget"); err != nil {
			return err
		}
	}

	return nil
}

// scaleIfNeeded wraps the forked ScaleReplicaSet with an "already at target"
// short-circuit so we don't emit a stream of zero-delta scale events.
func (r *Reconciler) scaleIfNeeded(ctx context.Context, rs *appsv1.ReplicaSet, target int32, d *appsv1.Deployment, op string) error {
	if rs.Spec.Replicas != nil && *rs.Spec.Replicas == target {
		return nil
	}
	if _, _, err := r.forked.ScaleReplicaSet(ctx, rs, target, d, op); err != nil {
		return fmt.Errorf("scale %s to %d: %w", rs.Name, target, err)
	}
	return nil
}

// splitReplicas computes the (canary, stable) replica pair from the total
// desired count and the current traffic weight.
//
// The rounding rule is "canary rounds up so a non-zero weight never yields
// zero canary pods" — a canary with no pods can't be measured. The
// complement (stable = desired - canary) means a 10-pod rollout at 5%
// weight becomes (canary=1, stable=9), not (canary=1, stable=10) or
// (canary=0, stable=10).
//
// HPAStrategy influences the outcome:
//
//   - Preserve (default): split applies to the full desired count.
//   - StableOnly: canary keeps its current count; only the stable RS
//     absorbs external scale changes. canaryKeep is the existing canary
//     replica count, passed in so callers don't fight each other.
//   - Disabled: reconcileReplicas short-circuits before calling this
//     function, so we don't handle it here.
func splitReplicas(desired int32, weight int32, strategy rolloutsv1alpha1.HPAStrategyMode, canaryKeep int32) (canary, stable int32) {
	if desired <= 0 {
		return 0, 0
	}
	if strategy == rolloutsv1alpha1.HPAStrategyStableOnly {
		if canaryKeep > desired {
			canaryKeep = desired
		}
		return canaryKeep, desired - canaryKeep
	}

	if weight <= 0 {
		return 0, desired
	}
	if weight >= 100 {
		return desired, 0
	}
	c := int32(math.Ceil(float64(desired) * float64(weight) / 100))
	if c > desired {
		c = desired
	}
	return c, desired - c
}

// desiredReplicas resolves spec.replicas to a concrete int32, applying the
// default of 1 when unset.
func desiredReplicas(ro *rolloutsv1alpha1.Rollout) int32 {
	if ro.Spec.Replicas == nil {
		return 1
	}
	return *ro.Spec.Replicas
}

// currentCanaryReplicas returns newRS.Spec.Replicas, defaulting to 0 so the
// "StableOnly: preserve canary count" path has a safe starting value.
func currentCanaryReplicas(newRS *appsv1.ReplicaSet) int32 {
	if newRS == nil || newRS.Spec.Replicas == nil {
		return 0
	}
	return *newRS.Spec.Replicas
}

// largestOldRS picks the "stable" from a list of non-new ReplicaSets using
// replica count as the tiebreaker. The forked controller's oldRSs slice is
// already ordered by creation time (newest first); within it, the RS that
// currently carries the most pods is "the one traffic is currently on."
//
// Returns nil when oldRSs is empty — the common case for a first-ever
// rollout, where only the new RS exists.
func largestOldRS(oldRSs []*appsv1.ReplicaSet) *appsv1.ReplicaSet {
	var best *appsv1.ReplicaSet
	var bestCount int32 = -1
	for _, rs := range oldRSs {
		if rs == nil {
			continue
		}
		count := int32(0)
		if rs.Spec.Replicas != nil {
			count = *rs.Spec.Replicas
		}
		if count > bestCount {
			best = rs
			bestCount = count
		}
	}
	return best
}

// observedSpecReplicas returns true iff the reconciler has already accounted
// for the current spec.replicas. Callers use this to guard promote-time
// scaleDown: we must not scale the old RS to zero while an HPA-driven
// spec.replicas increase is still in flight.
func observedSpecReplicas(ro *rolloutsv1alpha1.Rollout) bool {
	return ro.Status.DesiredReplicas == desiredReplicas(ro) && ro.Status.ObservedGeneration == ro.Generation
}
