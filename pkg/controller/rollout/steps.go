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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rolloutsv1alpha1 "github.com/zaller/rollouts/pkg/apis/rollouts/v1alpha1"
)

// runSetWeight shifts router traffic, optionally verifies the router observed
// the new weight, and advances to the next step on success. Requeues the
// caller when verification is still pending.
func (e *Executor) runSetWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
	if e.router == nil {
		return 0, fmt.Errorf("step %q: setWeight requires spec.trafficRouting", step.Name)
	}
	if err := e.router.SetWeight(ctx, ro, step.SetWeight.Weight); err != nil {
		return 0, fmt.Errorf("step %q: set weight: %w", step.Name, err)
	}
	if shouldVerifyWeight(ro) {
		ok, err := e.router.VerifyWeight(ctx, ro, step.SetWeight.Weight)
		if err != nil {
			return 0, fmt.Errorf("step %q: verify weight: %w", step.Name, err)
		}
		if !ok {
			return 2 * time.Second, nil
		}
	}
	return 0, e.advanceStep(ctx, ro, idx)
}

// runSetCanaryScale is a placeholder — the full implementation will patch the
// canary ReplicaSet's replica count using the forked scale helper. For v0.1
// we record the intent in status and advance.
func (e *Executor) runSetCanaryScale(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int, _ []*appsv1.ReplicaSet) (time.Duration, error) {
	// TODO(rollouts): scale canary RS via forkeddeployment.Scale. The
	// synthetic Deployment view in adapter.go already exposes replicas to the
	// forked helpers; we need to compute the target count here from
	// step.SetCanaryScale (Replicas / Percent / MatchTrafficWeight) and call
	// into the forked scale primitive. Tracked for v0.2.
	return 0, e.advanceStep(ctx, ro, idx)
}

// runPause blocks until the Pause Duration elapses. Nil duration means
// "indefinite" — require a user promote or abort to advance.
func (e *Executor) runPause(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
	startedAt := ro.Status.StepStartedAt
	now := metav1Now()

	// First entry: stamp pause start, emit PausedStep condition, requeue for duration.
	if len(ro.Status.PauseConditions) == 0 {
		patched := ro.DeepCopy()
		patched.Status.Phase = rolloutsv1alpha1.PhasePaused
		patched.Status.PauseConditions = []rolloutsv1alpha1.PauseCondition{{
			Reason:    rolloutsv1alpha1.PauseReasonPausedStep,
			StartTime: now,
			Until:     pauseUntil(now, step.Pause.Duration),
		}}
		patched.Status.StepStartedAt = &now
		if err := e.client.Status().Update(ctx, patched); err != nil {
			return 0, err
		}
		if step.Pause.Duration == nil {
			return 0, nil
		}
		return step.Pause.Duration.Duration, nil
	}

	// Indefinite pause: wait for user promote or abort.
	if step.Pause.Duration == nil {
		return 0, nil
	}

	// Duration pause: check elapsed.
	if startedAt == nil {
		startedAt = &now
	}
	elapsed := now.Sub(startedAt.Time)
	if elapsed < step.Pause.Duration.Duration {
		return step.Pause.Duration.Duration - elapsed, nil
	}
	return 0, e.advanceStep(ctx, ro, idx)
}

// runAnalysis is a stub. Full implementation creates an owning AnalysisRun and
// gates on its Phase.
func (e *Executor) runAnalysis(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
	// TODO(rollouts): create AnalysisRun owned by this Rollout, poll its
	// status, honor step.Analysis.FailurePolicy on failure, InconclusivePolicy
	// on inconclusive. Tracked for v0.2.
	return 0, e.advanceStep(ctx, ro, idx)
}

// runExperiment is a stub. Full implementation creates a child Experiment CR
// and gates on its Phase.
func (e *Executor) runExperiment(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
	// TODO(rollouts): create Experiment CR, gate on its Phase. v0.2.
	return 0, e.advanceStep(ctx, ro, idx)
}

// runPromote atomically flips the router (and, for blue-green, the active
// Service selector). For this v0.1 skeleton we drive the router only and
// leave Service selector manipulation to a follow-up.
func (e *Executor) runPromote(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int, _ []*appsv1.ReplicaSet) (time.Duration, error) {
	if ro.Spec.AutoPromotion == rolloutsv1alpha1.AutoPromotionDisabled {
		return 0, fmt.Errorf("step %q: promote called but autoPromotion is Disabled", step.Name)
	}
	if ro.Spec.AutoPromotion == rolloutsv1alpha1.AutoPromotionManual && !hasUserPromotion(ro) {
		patched := ro.DeepCopy()
		patched.Status.Phase = rolloutsv1alpha1.PhasePaused
		patched.Status.PauseConditions = []rolloutsv1alpha1.PauseCondition{{
			Reason:    rolloutsv1alpha1.PauseReasonBlueGreenAutoPromotionDisabled,
			StartTime: metav1Now(),
			Message:   "awaiting user promotion",
		}}
		return 0, e.client.Status().Update(ctx, patched)
	}

	if e.router != nil {
		if err := e.router.SetWeight(ctx, ro, 100); err != nil {
			return 0, fmt.Errorf("step %q: promote set weight: %w", step.Name, err)
		}
	}
	return 0, e.advanceStep(ctx, ro, idx)
}

// hasUserPromotion returns true when the user has cleared PauseConditions to
// signal promotion. This is intentionally minimal — future revisions will
// introduce a dedicated spec.promote field or subresource.
func hasUserPromotion(ro *rolloutsv1alpha1.Rollout) bool {
	return len(ro.Status.PauseConditions) == 0 && ro.Status.Phase != rolloutsv1alpha1.PhasePaused
}

// shouldVerifyWeight reports whether the rollout opted in to weight verification.
func shouldVerifyWeight(ro *rolloutsv1alpha1.Rollout) bool {
	if ro.Spec.TrafficRouting == nil {
		return false
	}
	return ro.Spec.TrafficRouting.VerifyWeight != "Disabled"
}

// pauseUntil returns the absolute time at which a pause with duration d
// ends, relative to start. Returns nil for indefinite pauses.
func pauseUntil(start metav1.Time, d *metav1.Duration) *metav1.Time {
	if d == nil {
		return nil
	}
	end := metav1.NewTime(start.Time.Add(d.Duration))
	return &end
}
