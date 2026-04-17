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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
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

// runSetCanaryScale patches the canary ReplicaSet's replica count per the
// step's instructions. The target count is computed from Replicas (absolute),
// Percent (of spec.replicas), or MatchTrafficWeight (matches the most recent
// SetWeight). The RS is scaled through the forked scaleReplicaSet helper so
// it emits the same ScalingReplicaSet events upstream operators expect.
func (e *Executor) runSetCanaryScale(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int, newRS *appsv1.ReplicaSet, _ []*appsv1.ReplicaSet) (time.Duration, error) {
	if newRS == nil {
		return 0, fmt.Errorf("step %q: setCanaryScale has no canary ReplicaSet to scale", step.Name)
	}
	desired, err := resolveCanaryScale(ro, step.SetCanaryScale)
	if err != nil {
		return 0, fmt.Errorf("step %q: %w", step.Name, err)
	}

	if newRS.Spec.Replicas != nil && *newRS.Spec.Replicas == desired {
		return 0, e.advanceStep(ctx, ro, idx)
	}

	d := asDeploymentView(ro, &ro.Spec)
	if _, _, err := e.forked.ScaleReplicaSet(ctx, newRS, desired, d, "setCanaryScale"); err != nil {
		return 0, fmt.Errorf("step %q: scale canary RS: %w", step.Name, err)
	}
	return 0, e.advanceStep(ctx, ro, idx)
}

// resolveCanaryScale converts the three-way SetCanaryScaleStep union into a
// concrete target replica count. The kubebuilder XValidation on the CRD
// guarantees exactly one of Replicas/Percent/MatchTrafficWeight is set, but
// we defensively handle zero-populated by returning an error.
func resolveCanaryScale(ro *rolloutsv1alpha1.Rollout, step *rolloutsv1alpha1.SetCanaryScaleStep) (int32, error) {
	desiredReplicas := int32(1)
	if ro.Spec.Replicas != nil {
		desiredReplicas = *ro.Spec.Replicas
	}
	switch {
	case step.Replicas != nil:
		return *step.Replicas, nil
	case step.Percent != nil:
		n, err := percentOf(desiredReplicas, *step.Percent)
		if err != nil {
			return 0, fmt.Errorf("resolve percent: %w", err)
		}
		return n, nil
	case step.MatchTrafficWeight:
		weight := lastSetWeight(ro)
		n, err := percentOf(desiredReplicas, intstr.FromInt32(weight))
		if err != nil {
			return 0, fmt.Errorf("match traffic weight: %w", err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("setCanaryScale: no variant set")
	}
}

// percentOf computes (total * p) / 100, rounded up so a 10% of 7 replicas
// becomes 1 (not 0 — a zero-pod canary has no signal to evaluate). An int
// value in the IntOrString is treated as a percentage, not an absolute
// replica count; callers that want absolute should use SetCanaryScaleStep.Replicas.
func percentOf(total int32, p intstr.IntOrString) (int32, error) {
	// Coerce bare int into a percent-shaped IntOrString so the upstream helper
	// applies the rounding-up formula. IntOrString.Type == Int means "integer
	// count" in the upstream world, which is not what a percent field wants.
	if p.Type == intstr.Int {
		p = intstr.FromString(fmt.Sprintf("%d%%", p.IntVal))
	}
	v, err := intstr.GetScaledValueFromIntOrPercent(&p, int(total), true)
	if err != nil {
		return 0, err
	}
	if v < 0 {
		return 0, nil
	}
	if v > int(total) {
		return total, nil
	}
	return int32(v), nil
}

// lastSetWeight walks the progression back from the current step looking for
// the most recent SetWeight; defaults to 0 if none is found. This is how
// MatchTrafficWeight stays in sync without introducing a status field.
func lastSetWeight(ro *rolloutsv1alpha1.Rollout) int32 {
	steps := ro.Spec.Progression.Steps
	idx := -1
	for i, s := range steps {
		if s.Name == ro.Status.CurrentStep {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = len(steps) - 1
	}
	for i := idx; i >= 0; i-- {
		if sw := steps[i].SetWeight; sw != nil {
			return sw.Weight
		}
	}
	return 0
}

// runPause blocks until the Pause Duration elapses. Nil duration means
// "indefinite" — require a user promote (clearing pauseConditions) or abort.
func (e *Executor) runPause(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
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

	// Duration pause: check elapsed against the stamped StartTime of the
	// PausedStep condition (not StepStartedAt, which can be updated by later
	// transitions).
	startedAt := ro.Status.PauseConditions[0].StartTime
	elapsed := now.Sub(startedAt.Time)
	if elapsed < step.Pause.Duration.Duration {
		return step.Pause.Duration.Duration - elapsed, nil
	}
	return 0, e.advanceStep(ctx, ro, idx)
}

// runAnalysis creates an owning AnalysisRun for the step the first time it's
// reached, then polls its status. The AnalysisRun is named deterministically
// so re-entry after a requeue reads the same object rather than spawning
// duplicates.
func (e *Executor) runAnalysis(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
	runName := analysisRunName(ro, step)
	run, err := e.getOrCreateAnalysisRun(ctx, ro, step, runName)
	if err != nil {
		return 0, fmt.Errorf("step %q: get/create analysis run: %w", step.Name, err)
	}

	switch run.Status.Phase {
	case "Successful":
		if err := e.recordAnalysisRun(ctx, ro, step, run, "Successful"); err != nil {
			return 0, err
		}
		return 0, e.advanceStep(ctx, ro, idx)
	case "Failed", "Error":
		policy := step.Analysis.FailurePolicy
		if policy == "" {
			policy = "Rollback"
		}
		if err := e.recordAnalysisRun(ctx, ro, step, run, string(run.Status.Phase)); err != nil {
			return 0, err
		}
		return e.applyAnalysisFailure(ctx, ro, step, idx, policy, run)
	case "Inconclusive":
		return e.applyAnalysisInconclusive(ctx, ro, step, idx, run)
	default:
		// Pending / Running — requeue and let the AnalysisRun controller advance.
		return 10 * time.Second, nil
	}
}

// getOrCreateAnalysisRun upserts the AnalysisRun for this step. We re-read
// the templates each time rather than snapshotting their content onto the
// run; the AnalysisRun spec carries the materialized metrics directly.
func (e *Executor) getOrCreateAnalysisRun(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, name string) (*rolloutsv1alpha1.AnalysisRun, error) {
	existing := &rolloutsv1alpha1.AnalysisRun{}
	err := e.client.Get(ctx, types.NamespacedName{Namespace: ro.Namespace, Name: name}, existing)
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}

	metrics, args, dryRun, err := e.materializeTemplates(ctx, ro, step.Analysis.TemplateRefs, step.Analysis.Args, step.Analysis.DryRun)
	if err != nil {
		return nil, err
	}

	isController := true
	blockOwnerDeletion := true
	run := &rolloutsv1alpha1.AnalysisRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ro.Namespace,
			Labels: map[string]string{
				"rollouts.io/rollout": ro.Name,
				"rollouts.io/step":    step.Name,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         rolloutsv1alpha1.SchemeGroupVersion.String(),
				Kind:               "Rollout",
				Name:               ro.Name,
				UID:                ro.UID,
				Controller:         &isController,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
		},
		Spec: rolloutsv1alpha1.AnalysisRunSpec{
			Metrics: metrics,
			Args:    args,
			DryRun:  dryRun,
		},
	}
	if err := e.client.Create(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

// materializeTemplates loads each referenced AnalysisTemplate (or Cluster
// variant) and merges its metrics with the step-level args/dryRun overrides.
// The merge rules are intentionally simple: metrics from all templates are
// concatenated in declaration order; step args override template-provided
// defaults by name; dryRun names compose as a union.
func (e *Executor) materializeTemplates(ctx context.Context, ro *rolloutsv1alpha1.Rollout, refs []rolloutsv1alpha1.TemplateRef, argOverrides []rolloutsv1alpha1.AnalysisArg, dryRunOverrides []rolloutsv1alpha1.DryRunMetric) ([]rolloutsv1alpha1.Metric, []rolloutsv1alpha1.AnalysisArg, []rolloutsv1alpha1.DryRunMetric, error) {
	var metrics []rolloutsv1alpha1.Metric
	argMap := map[string]rolloutsv1alpha1.AnalysisArg{}
	dryRunSet := map[string]struct{}{}

	for _, ref := range refs {
		kind := ref.Kind
		if kind == "" {
			kind = "AnalysisTemplate"
		}
		switch kind {
		case "AnalysisTemplate":
			tpl := &rolloutsv1alpha1.AnalysisTemplate{}
			if err := e.client.Get(ctx, types.NamespacedName{Namespace: ro.Namespace, Name: ref.Name}, tpl); err != nil {
				return nil, nil, nil, fmt.Errorf("load AnalysisTemplate %s/%s: %w", ro.Namespace, ref.Name, err)
			}
			metrics = append(metrics, tpl.Spec.Metrics...)
			for _, a := range tpl.Spec.Args {
				if _, ok := argMap[a.Name]; !ok {
					argMap[a.Name] = a
				}
			}
			for _, d := range tpl.Spec.DryRun {
				dryRunSet[d.MetricName] = struct{}{}
			}
		case "ClusterAnalysisTemplate":
			tpl := &rolloutsv1alpha1.ClusterAnalysisTemplate{}
			if err := e.client.Get(ctx, types.NamespacedName{Name: ref.Name}, tpl); err != nil {
				return nil, nil, nil, fmt.Errorf("load ClusterAnalysisTemplate %s: %w", ref.Name, err)
			}
			metrics = append(metrics, tpl.Spec.Metrics...)
			for _, a := range tpl.Spec.Args {
				if _, ok := argMap[a.Name]; !ok {
					argMap[a.Name] = a
				}
			}
			for _, d := range tpl.Spec.DryRun {
				dryRunSet[d.MetricName] = struct{}{}
			}
		default:
			return nil, nil, nil, fmt.Errorf("templateRef kind %q not supported", kind)
		}
	}

	for _, a := range argOverrides {
		argMap[a.Name] = a
	}
	for _, d := range dryRunOverrides {
		dryRunSet[d.MetricName] = struct{}{}
	}

	args := make([]rolloutsv1alpha1.AnalysisArg, 0, len(argMap))
	for _, a := range argMap {
		args = append(args, a)
	}
	dryRun := make([]rolloutsv1alpha1.DryRunMetric, 0, len(dryRunSet))
	for name := range dryRunSet {
		dryRun = append(dryRun, rolloutsv1alpha1.DryRunMetric{MetricName: name})
	}
	return metrics, args, dryRun, nil
}

// applyAnalysisFailure enacts the FailurePolicy on the Rollout.
func (e *Executor) applyAnalysisFailure(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int, policy string, run *rolloutsv1alpha1.AnalysisRun) (time.Duration, error) {
	patched := ro.DeepCopy()
	msg := fmt.Sprintf("analysis %q failed: %s", run.Name, run.Status.Message)
	switch policy {
	case "Rollback":
		patched.Status.Phase = rolloutsv1alpha1.PhaseDegraded
		patched.Status.Message = msg
		patched.Spec.Abort = true // trigger abort path on next reconcile
		return 0, e.client.Update(ctx, patched)
	case "Pause":
		patched.Status.Phase = rolloutsv1alpha1.PhasePaused
		patched.Status.PauseConditions = []rolloutsv1alpha1.PauseCondition{{
			Reason:    rolloutsv1alpha1.PauseReasonAnalysisFailed,
			StartTime: metav1Now(),
			Message:   msg,
		}}
		return 0, e.client.Status().Update(ctx, patched)
	case "Ignore":
		return 0, e.advanceStep(ctx, ro, idx)
	default:
		return 0, fmt.Errorf("step %q: unknown FailurePolicy %q", step.Name, policy)
	}
}

// applyAnalysisInconclusive enacts the InconclusivePolicy.
func (e *Executor) applyAnalysisInconclusive(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int, run *rolloutsv1alpha1.AnalysisRun) (time.Duration, error) {
	policy := "Pause"
	// Walk templates for their InconclusivePolicy; the first non-empty wins.
	for _, ref := range step.Analysis.TemplateRefs {
		kind := ref.Kind
		if kind == "" {
			kind = "AnalysisTemplate"
		}
		if kind == "AnalysisTemplate" {
			tpl := &rolloutsv1alpha1.AnalysisTemplate{}
			if err := e.client.Get(ctx, types.NamespacedName{Namespace: ro.Namespace, Name: ref.Name}, tpl); err == nil {
				if tpl.Spec.InconclusivePolicy != "" {
					policy = tpl.Spec.InconclusivePolicy
					break
				}
			}
		}
	}

	patched := ro.DeepCopy()
	msg := fmt.Sprintf("analysis %q inconclusive: %s", run.Name, run.Status.Message)
	switch policy {
	case "Abort":
		patched.Status.Phase = rolloutsv1alpha1.PhaseDegraded
		patched.Status.Message = msg
		patched.Spec.Abort = true
		return 0, e.client.Update(ctx, patched)
	case "Pause":
		patched.Status.Phase = rolloutsv1alpha1.PhasePaused
		patched.Status.PauseConditions = []rolloutsv1alpha1.PauseCondition{{
			Reason:    rolloutsv1alpha1.PauseReasonAnalysisInconclusive,
			StartTime: metav1Now(),
			Message:   msg,
		}}
		return 0, e.client.Status().Update(ctx, patched)
	case "Ignore":
		return 0, e.advanceStep(ctx, ro, idx)
	default:
		return 0, fmt.Errorf("step %q: unknown InconclusivePolicy %q", step.Name, policy)
	}
}

// recordAnalysisRun appends the AR to status.analysisRunSummaries (dedup by Name).
func (e *Executor) recordAnalysisRun(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, run *rolloutsv1alpha1.AnalysisRun, phase string) error {
	patched := ro.DeepCopy()
	summary := rolloutsv1alpha1.AnalysisRunSummary{
		Name:    run.Name,
		AtStep:  step.Name,
		Phase:   phase,
		Message: run.Status.Message,
	}
	for i := range patched.Status.AnalysisRunSummaries {
		if patched.Status.AnalysisRunSummaries[i].Name == summary.Name {
			patched.Status.AnalysisRunSummaries[i] = summary
			return e.client.Status().Update(ctx, patched)
		}
	}
	patched.Status.AnalysisRunSummaries = append(patched.Status.AnalysisRunSummaries, summary)
	return e.client.Status().Update(ctx, patched)
}

// runExperiment is a stub. Full implementation creates a child Experiment CR
// and gates on its Phase.
func (e *Executor) runExperiment(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int) (time.Duration, error) {
	// Experiments land in a later revision; for now we advance to avoid
	// wedging rollouts that reference them.
	return 0, e.advanceStep(ctx, ro, idx)
}

// runPromote flips router traffic to 100% to the canary, then rewrites the
// stable/active Service selectors to pin them at the new RS's pod-template-
// hash. The previous stable RS is scaled to zero after
// scaleDownDelaySeconds.
func (e *Executor) runPromote(ctx context.Context, ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step, idx int, newRS *appsv1.ReplicaSet, oldRSs []*appsv1.ReplicaSet) (time.Duration, error) {
	// Honor AutoPromotion: Manual means we insert a pause if one isn't already
	// present, and wait for the user to clear it.
	if ro.Spec.AutoPromotion == rolloutsv1alpha1.AutoPromotionDisabled {
		return 0, fmt.Errorf("step %q: promote called but autoPromotion is Disabled", step.Name)
	}
	if ro.Spec.AutoPromotion == rolloutsv1alpha1.AutoPromotionManual && !hasUserPromotion(ro) {
		if len(ro.Status.PauseConditions) == 0 {
			patched := ro.DeepCopy()
			patched.Status.Phase = rolloutsv1alpha1.PhasePaused
			patched.Status.PauseConditions = []rolloutsv1alpha1.PauseCondition{{
				Reason:    rolloutsv1alpha1.PauseReasonAwaitingPromotion,
				StartTime: metav1Now(),
				Message:   "awaiting user promotion",
			}}
			return 0, e.client.Status().Update(ctx, patched)
		}
		// Already paused; nothing to do until user clears the condition.
		return 0, nil
	}

	// Mark phase Promoting for observability.
	if ro.Status.Phase != rolloutsv1alpha1.PhasePromoting {
		patched := ro.DeepCopy()
		patched.Status.Phase = rolloutsv1alpha1.PhasePromoting
		if err := e.client.Status().Update(ctx, patched); err != nil {
			return 0, err
		}
	}

	// 1. Flip router to 100% canary.
	if e.router != nil {
		if err := e.router.SetWeight(ctx, ro, 100); err != nil {
			return 0, fmt.Errorf("step %q: promote set weight: %w", step.Name, err)
		}
	}

	// 2. Repoint stable/active Services at the new RS's pod-template-hash.
	if newRS != nil {
		if err := e.repointServices(ctx, ro, newRS); err != nil {
			return 0, fmt.Errorf("step %q: repoint services: %w", step.Name, err)
		}
	}

	// 3. Schedule old-RS scale-down. The forked cleanup (in finalize) and the
	// scaleDownDelay logic below coexist: we scale down the old RS now if the
	// delay has elapsed, else requeue.
	requeue, err := e.scaleDownAfterDelay(ctx, ro, oldRSs)
	if err != nil {
		return 0, fmt.Errorf("step %q: scale down stable: %w", step.Name, err)
	}
	if requeue > 0 {
		return requeue, nil
	}
	return 0, e.advanceStep(ctx, ro, idx)
}

// repointServices updates each stableServices Service selector to include
// the new RS's pod-template-hash. This is the moment at which the new
// revision becomes "the stable one"; until Promote its traffic arrived via
// the router's weight rules or the canaryServices selector.
func (e *Executor) repointServices(ctx context.Context, ro *rolloutsv1alpha1.Rollout, newRS *appsv1.ReplicaSet) error {
	hash := newRS.Labels["pod-template-hash"]
	if hash == "" {
		return fmt.Errorf("new RS %s has no pod-template-hash label", newRS.Name)
	}

	for _, ref := range ro.Spec.StableServices {
		svc, err := e.kube.CoreV1().Services(ro.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get service %s: %w", ref.Name, err)
		}
		if svc.Spec.Selector == nil {
			svc.Spec.Selector = map[string]string{}
		}
		if svc.Spec.Selector["pod-template-hash"] == hash {
			continue
		}
		svc.Spec.Selector["pod-template-hash"] = hash
		if _, err := e.kube.CoreV1().Services(ro.Namespace).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("update service %s: %w", ref.Name, err)
		}
	}
	return nil
}

// scaleDownAfterDelay scales the non-new RSes to zero after the configured
// delay has elapsed since the current step started. Returns a positive
// duration to signal "not yet" — callers should requeue.
func (e *Executor) scaleDownAfterDelay(ctx context.Context, ro *rolloutsv1alpha1.Rollout, oldRSs []*appsv1.ReplicaSet) (time.Duration, error) {
	delay := time.Duration(0)
	if ro.Spec.Progression.ScaleDownDelaySeconds != nil {
		delay = time.Duration(*ro.Spec.Progression.ScaleDownDelaySeconds) * time.Second
	}

	startedAt := ro.Status.StepStartedAt
	if startedAt == nil {
		now := metav1Now()
		startedAt = &now
	}
	remaining := delay - time.Since(startedAt.Time)
	if remaining > 0 {
		return remaining, nil
	}

	d := asDeploymentView(ro, &ro.Spec)
	for _, rs := range oldRSs {
		if rs.Spec.Replicas == nil || *rs.Spec.Replicas == 0 {
			continue
		}
		if _, _, err := e.forked.ScaleReplicaSet(ctx, rs, 0, d, "scaledDownAfterPromote"); err != nil {
			return 0, err
		}
	}
	return 0, nil
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

// analysisRunName is a deterministic name so repeated entries into runAnalysis
// for the same (rollout, step, revision) converge on the same AR rather than
// creating duplicates.
func analysisRunName(ro *rolloutsv1alpha1.Rollout, step rolloutsv1alpha1.Step) string {
	rev := ro.Status.CurrentPodHash
	if rev == "" {
		rev = "initial"
	}
	return truncate64(fmt.Sprintf("%s-%s-%s", ro.Name, step.Name, rev))
}

// truncate64 clips a string to Kubernetes' 63-char name limit, preserving a
// short hash of the tail so uniqueness is retained even after truncation.
func truncate64(s string) string {
	const max = 63
	if len(s) <= max {
		return s
	}
	const keep = max - 6
	suffix := uint32(0)
	for i := keep; i < len(s); i++ {
		suffix = suffix*31 + uint32(s[i])
	}
	return fmt.Sprintf("%s-%05d", s[:keep], suffix%math.MaxUint16)
}
