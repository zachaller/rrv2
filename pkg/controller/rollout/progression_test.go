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

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// TestResolveCurrentStep_ByName covers the named-step resolver's core contract:
// lookups are by name (not index), so inserting or reordering steps keeps the
// rollout pointing at the right step. The "vanished step" case is the one the
// named-step design was chosen to fix — a renamed step must surface as
// "progression complete" rather than silently continuing at a stale index.
func TestResolveCurrentStep_ByName(t *testing.T) {
	tests := []struct {
		name        string
		steps       []rolloutsv1alpha1.Step
		currentStep string
		wantName    string
		wantIdx     int
		wantOK      bool
	}{
		{
			name:        "empty progression",
			steps:       nil,
			currentStep: "",
			wantOK:      false,
		},
		{
			name: "first sync picks steps[0]",
			steps: []rolloutsv1alpha1.Step{
				{Name: "a"}, {Name: "b"},
			},
			currentStep: "",
			wantName:    "a",
			wantIdx:     0,
			wantOK:      true,
		},
		{
			name: "mid-progression resolves by name",
			steps: []rolloutsv1alpha1.Step{
				{Name: "a"}, {Name: "b"}, {Name: "c"},
			},
			currentStep: "b",
			wantName:    "b",
			wantIdx:     1,
			wantOK:      true,
		},
		{
			name: "insert a step before current; name still binds",
			steps: []rolloutsv1alpha1.Step{
				{Name: "pre"}, {Name: "a"}, {Name: "b"}, {Name: "c"},
			},
			currentStep: "b",
			wantName:    "b",
			wantIdx:     2, // index shifted by the inserted "pre"
			wantOK:      true,
		},
		{
			name: "vanished step falls off the end",
			steps: []rolloutsv1alpha1.Step{
				{Name: "a"}, {Name: "b"}, {Name: "c"},
			},
			currentStep: "removed",
			wantOK:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ro := &rolloutsv1alpha1.Rollout{
				Spec:   rolloutsv1alpha1.RolloutSpec{Progression: rolloutsv1alpha1.ProgressionSpec{Steps: tc.steps}},
				Status: rolloutsv1alpha1.RolloutStatus{CurrentStep: tc.currentStep},
			}
			got, gotIdx, gotOK := resolveCurrentStep(ro)
			if gotOK != tc.wantOK {
				t.Fatalf("ok: got %v, want %v", gotOK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.Name != tc.wantName {
				t.Errorf("name: got %q, want %q", got.Name, tc.wantName)
			}
			if gotIdx != tc.wantIdx {
				t.Errorf("idx: got %d, want %d", gotIdx, tc.wantIdx)
			}
		})
	}
}

// TestApplyDefaults_Enums verifies that the in-memory defaulter mirrors the
// kubebuilder +kubebuilder:default markers. If the controller ever reasons
// about a Rollout before admission defaulting runs (e.g. an older apiserver,
// or a test harness), the enum zero-values must land on the documented
// defaults — not on the empty string.
func TestApplyDefaults_Enums(t *testing.T) {
	ro := &rolloutsv1alpha1.Rollout{
		Spec: rolloutsv1alpha1.RolloutSpec{
			Progression: rolloutsv1alpha1.ProgressionSpec{
				Steps: []rolloutsv1alpha1.Step{
					{Name: "w", SetWeight: &rolloutsv1alpha1.SetWeightStep{Weight: 50}},
					{Name: "a", Analysis: &rolloutsv1alpha1.AnalysisStep{
						TemplateRefs: []rolloutsv1alpha1.TemplateRef{{Name: "t"}},
					}},
				},
			},
			TrafficRouting: &rolloutsv1alpha1.TrafficRoutingSpec{Provider: "istio"},
		},
	}

	applyDefaults(ro)

	if ro.Spec.AutoPromotion != rolloutsv1alpha1.AutoPromotionEnabled {
		t.Errorf("AutoPromotion: got %q, want %q", ro.Spec.AutoPromotion, rolloutsv1alpha1.AutoPromotionEnabled)
	}
	if ro.Spec.DynamicStableScale != rolloutsv1alpha1.DynamicStableScaleOff {
		t.Errorf("DynamicStableScale: got %q, want %q", ro.Spec.DynamicStableScale, rolloutsv1alpha1.DynamicStableScaleOff)
	}
	if ro.Spec.Progression.Steps[1].Analysis.FailurePolicy != "Rollback" {
		t.Errorf("Analysis.FailurePolicy: got %q, want Rollback", ro.Spec.Progression.Steps[1].Analysis.FailurePolicy)
	}
	if ro.Spec.TrafficRouting.VerifyWeight != "Enabled" {
		t.Errorf("VerifyWeight: got %q, want Enabled", ro.Spec.TrafficRouting.VerifyWeight)
	}
}

// TestApplyDefaults_PreservesExplicitValues is the counterpart to the prior
// test: defaulting must never overwrite an operator's explicit choice.
func TestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	ro := &rolloutsv1alpha1.Rollout{
		Spec: rolloutsv1alpha1.RolloutSpec{
			AutoPromotion:      rolloutsv1alpha1.AutoPromotionManual,
			DynamicStableScale: rolloutsv1alpha1.DynamicStableScaleAggressive,
			Progression: rolloutsv1alpha1.ProgressionSpec{
				Steps: []rolloutsv1alpha1.Step{
					{Name: "a", Analysis: &rolloutsv1alpha1.AnalysisStep{
						TemplateRefs:  []rolloutsv1alpha1.TemplateRef{{Name: "t"}},
						FailurePolicy: "Ignore",
					}},
				},
			},
			TrafficRouting: &rolloutsv1alpha1.TrafficRoutingSpec{
				Provider:     "istio",
				VerifyWeight: "Disabled",
			},
		},
	}

	applyDefaults(ro)

	if ro.Spec.AutoPromotion != rolloutsv1alpha1.AutoPromotionManual {
		t.Errorf("AutoPromotion was overwritten: got %q", ro.Spec.AutoPromotion)
	}
	if ro.Spec.DynamicStableScale != rolloutsv1alpha1.DynamicStableScaleAggressive {
		t.Errorf("DynamicStableScale was overwritten: got %q", ro.Spec.DynamicStableScale)
	}
	if ro.Spec.Progression.Steps[0].Analysis.FailurePolicy != "Ignore" {
		t.Errorf("Analysis.FailurePolicy was overwritten: got %q", ro.Spec.Progression.Steps[0].Analysis.FailurePolicy)
	}
	if ro.Spec.TrafficRouting.VerifyWeight != "Disabled" {
		t.Errorf("VerifyWeight was overwritten: got %q", ro.Spec.TrafficRouting.VerifyWeight)
	}
}

// TestShouldVerifyWeight documents the decision table: verification only runs
// when the rollout has traffic routing configured AND hasn't explicitly
// opted out. The default ("Enabled") is exercised by the zero-value branch.
func TestShouldVerifyWeight(t *testing.T) {
	tests := []struct {
		name string
		ro   *rolloutsv1alpha1.Rollout
		want bool
	}{
		{
			name: "no traffic routing",
			ro:   &rolloutsv1alpha1.Rollout{},
			want: false,
		},
		{
			name: "default verify",
			ro: &rolloutsv1alpha1.Rollout{Spec: rolloutsv1alpha1.RolloutSpec{
				TrafficRouting: &rolloutsv1alpha1.TrafficRoutingSpec{VerifyWeight: "Enabled"},
			}},
			want: true,
		},
		{
			name: "explicit disabled",
			ro: &rolloutsv1alpha1.Rollout{Spec: rolloutsv1alpha1.RolloutSpec{
				TrafficRouting: &rolloutsv1alpha1.TrafficRoutingSpec{VerifyWeight: "Disabled"},
			}},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldVerifyWeight(tc.ro); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
