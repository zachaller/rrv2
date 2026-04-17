/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package rollout

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// TestResolveCanaryScale_Replicas covers the absolute replica count branch.
func TestResolveCanaryScale_Replicas(t *testing.T) {
	ten := int32(10)
	three := int32(3)
	ro := &rolloutsv1alpha1.Rollout{Spec: rolloutsv1alpha1.RolloutSpec{Replicas: &ten}}
	step := &rolloutsv1alpha1.SetCanaryScaleStep{Replicas: &three}
	got, err := resolveCanaryScale(ro, step)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

// TestResolveCanaryScale_Percent: percent-of-spec.replicas rounds up so a
// 10% canary of 7 pods is 1 (not 0, which would have no signal).
func TestResolveCanaryScale_Percent(t *testing.T) {
	tests := []struct {
		name     string
		replicas int32
		percent  intstr.IntOrString
		want     int32
	}{
		{"25% of 10", 10, intstr.FromInt32(25), 3},
		{"10% of 7 rounds up", 7, intstr.FromInt32(10), 1},
		{"100% of 5", 5, intstr.FromInt32(100), 5},
		{"0% of 10", 10, intstr.FromInt32(0), 0},
		{"string percent", 20, intstr.FromString("50%"), 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ro := &rolloutsv1alpha1.Rollout{Spec: rolloutsv1alpha1.RolloutSpec{Replicas: &tc.replicas}}
			pct := tc.percent
			step := &rolloutsv1alpha1.SetCanaryScaleStep{Percent: &pct}
			got, err := resolveCanaryScale(ro, step)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestResolveCanaryScale_MatchTrafficWeight picks the most recent SetWeight
// earlier in the progression as the target percentage.
func TestResolveCanaryScale_MatchTrafficWeight(t *testing.T) {
	ten := int32(10)
	ro := &rolloutsv1alpha1.Rollout{
		Spec: rolloutsv1alpha1.RolloutSpec{
			Replicas: &ten,
			Progression: rolloutsv1alpha1.ProgressionSpec{
				Steps: []rolloutsv1alpha1.Step{
					{Name: "w25", SetWeight: &rolloutsv1alpha1.SetWeightStep{Weight: 25}},
					{Name: "scale", SetCanaryScale: &rolloutsv1alpha1.SetCanaryScaleStep{MatchTrafficWeight: true}},
				},
			},
		},
		Status: rolloutsv1alpha1.RolloutStatus{CurrentStep: "scale"},
	}
	step := &rolloutsv1alpha1.SetCanaryScaleStep{MatchTrafficWeight: true}
	got, err := resolveCanaryScale(ro, step)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 3 { // 25% of 10, rounded up
		t.Errorf("got %d, want 3 (25%% of 10)", got)
	}
}

// TestResolveCanaryScale_MatchTrafficWeight_NoPrior: if no SetWeight step
// has been seen, the target is 0 (the canary shouldn't exist yet).
func TestResolveCanaryScale_MatchTrafficWeight_NoPrior(t *testing.T) {
	ten := int32(10)
	ro := &rolloutsv1alpha1.Rollout{
		Spec: rolloutsv1alpha1.RolloutSpec{
			Replicas: &ten,
			Progression: rolloutsv1alpha1.ProgressionSpec{
				Steps: []rolloutsv1alpha1.Step{
					{Name: "scale", SetCanaryScale: &rolloutsv1alpha1.SetCanaryScaleStep{MatchTrafficWeight: true}},
				},
			},
		},
		Status: rolloutsv1alpha1.RolloutStatus{CurrentStep: "scale"},
	}
	step := &rolloutsv1alpha1.SetCanaryScaleStep{MatchTrafficWeight: true}
	got, err := resolveCanaryScale(ro, step)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// TestResolveCanaryScale_NoVariantSet is the defensive path: the CRD
// XValidation forbids this shape, but if the in-memory defaulter misses it
// the controller surfaces the error rather than silently scaling to 0.
func TestResolveCanaryScale_NoVariantSet(t *testing.T) {
	ten := int32(10)
	ro := &rolloutsv1alpha1.Rollout{Spec: rolloutsv1alpha1.RolloutSpec{Replicas: &ten}}
	step := &rolloutsv1alpha1.SetCanaryScaleStep{}
	_, err := resolveCanaryScale(ro, step)
	if err == nil {
		t.Errorf("expected error for empty SetCanaryScale, got nil")
	}
}

// TestLastSetWeight walks the progression backwards, returning the most
// recent SetWeight relative to CurrentStep.
func TestLastSetWeight(t *testing.T) {
	ro := &rolloutsv1alpha1.Rollout{
		Spec: rolloutsv1alpha1.RolloutSpec{
			Progression: rolloutsv1alpha1.ProgressionSpec{
				Steps: []rolloutsv1alpha1.Step{
					{Name: "w10", SetWeight: &rolloutsv1alpha1.SetWeightStep{Weight: 10}},
					{Name: "pause", Pause: &rolloutsv1alpha1.PauseStep{}},
					{Name: "w50", SetWeight: &rolloutsv1alpha1.SetWeightStep{Weight: 50}},
					{Name: "analysis", Analysis: &rolloutsv1alpha1.AnalysisStep{}},
				},
			},
		},
		Status: rolloutsv1alpha1.RolloutStatus{CurrentStep: "analysis"},
	}
	if got := lastSetWeight(ro); got != 50 {
		t.Errorf("got %d, want 50", got)
	}
}

// TestAnalysisRunName is a determinism test — two reconciles at the same
// revision must produce the same name so runAnalysis never accidentally
// spawns duplicate AnalysisRuns.
func TestAnalysisRunName(t *testing.T) {
	ro := &rolloutsv1alpha1.Rollout{}
	ro.Name = "my-app"
	ro.Status.CurrentPodHash = "abcd1234"
	step := rolloutsv1alpha1.Step{Name: "perf"}

	a := analysisRunName(ro, step)
	b := analysisRunName(ro, step)
	if a != b {
		t.Errorf("not deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Errorf("empty name")
	}
	if len(a) > 63 {
		t.Errorf("name too long (%d): %q", len(a), a)
	}
}

// TestTruncate64 verifies the 63-char clamp.
func TestTruncate64(t *testing.T) {
	short := "abc"
	if got := truncate64(short); got != short {
		t.Errorf("short passthrough: got %q, want %q", got, short)
	}
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := truncate64(long)
	if len(got) > 63 {
		t.Errorf("clamp failed: got %d chars", len(got))
	}
}
