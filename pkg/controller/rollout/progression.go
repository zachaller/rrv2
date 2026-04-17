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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
	"github.com/zachaller/rrv2/pkg/trafficrouting"
)

// Executor walks a Rollout's progression, one step at a time. Each Execute
// call handles the step named by status.CurrentStep and returns the duration
// after which the controller should reconcile again.
//
// Step handlers are responsible for:
//   - Performing the step's side effect (setWeight, scale, analyze, etc.).
//   - Deciding whether the step is complete (returning 0 advances to the
//     next step; a non-zero duration reconciles again later).
//   - Patching the Rollout's status (phase, currentStep, stepStartedAt) when
//     transitioning between steps.
type Executor struct {
	client   ctrlclient.Client
	kube     kubernetes.Interface
	recorder record.EventRecorder
	router   trafficrouting.Plugin
}

// Execute dispatches the current step. Returns (requeueAfter, err).
func (e *Executor) Execute(ctx context.Context, ro *rolloutsv1alpha1.Rollout, rsList []*appsv1.ReplicaSet) (time.Duration, error) {
	step, idx, ok := resolveCurrentStep(ro)
	if !ok {
		return 0, e.finalize(ctx, ro, rsList)
	}

	switch {
	case step.SetWeight != nil:
		return e.runSetWeight(ctx, ro, step, idx)
	case step.SetCanaryScale != nil:
		return e.runSetCanaryScale(ctx, ro, step, idx, rsList)
	case step.Pause != nil:
		return e.runPause(ctx, ro, step, idx)
	case step.Analysis != nil:
		return e.runAnalysis(ctx, ro, step, idx)
	case step.Experiment != nil:
		return e.runExperiment(ctx, ro, step, idx)
	case step.Promote != nil:
		return e.runPromote(ctx, ro, step, idx, rsList)
	default:
		return 0, fmt.Errorf("step %q has no action populated", step.Name)
	}
}

// resolveCurrentStep looks up status.CurrentStep by name in the step list.
// If the step isn't found (e.g. it was renamed/removed mid-rollout) the
// controller re-pins to the first step whose name isn't in
// status.CompletedSteps. This keeps rollouts advancing rather than jamming —
// analysis hooks that referenced a vanished step are rejected by admission.
//
// Returns (step, index, true) when a step is active; (_, _, false) when the
// progression has run to completion.
func resolveCurrentStep(ro *rolloutsv1alpha1.Rollout) (rolloutsv1alpha1.Step, int, bool) {
	steps := ro.Spec.Progression.Steps
	if len(steps) == 0 {
		return rolloutsv1alpha1.Step{}, 0, false
	}
	if ro.Status.CurrentStep == "" {
		return steps[0], 0, true
	}
	for i, s := range steps {
		if s.Name == ro.Status.CurrentStep {
			return s, i, true
		}
	}
	// Named step vanished — fall off the end of the progression.
	return rolloutsv1alpha1.Step{}, 0, false
}

// advanceStep sets status.CurrentStep to the name of the step immediately
// after idx, or clears it if idx is the final step.
func (e *Executor) advanceStep(ctx context.Context, ro *rolloutsv1alpha1.Rollout, idx int) error {
	patched := ro.DeepCopy()
	steps := ro.Spec.Progression.Steps
	if idx+1 >= len(steps) {
		patched.Status.CurrentStep = ""
		patched.Status.StepStartedAt = nil
		patched.Status.Phase = rolloutsv1alpha1.PhaseHealthy
		return e.client.Status().Update(ctx, patched)
	}
	next := steps[idx+1]
	patched.Status.CurrentStep = next.Name
	now := metav1Now()
	patched.Status.StepStartedAt = &now
	patched.Status.PauseConditions = nil
	patched.Status.Phase = rolloutsv1alpha1.PhaseProgressing
	return e.client.Status().Update(ctx, patched)
}

// finalize marks the rollout Healthy when every step has completed.
func (e *Executor) finalize(ctx context.Context, ro *rolloutsv1alpha1.Rollout, _ []*appsv1.ReplicaSet) error {
	if ro.Status.Phase == rolloutsv1alpha1.PhaseHealthy {
		return nil
	}
	patched := ro.DeepCopy()
	patched.Status.Phase = rolloutsv1alpha1.PhaseHealthy
	patched.Status.Message = "rollout complete"
	patched.Status.PauseConditions = nil
	return e.client.Status().Update(ctx, patched)
}
