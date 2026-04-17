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

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// TestSplitReplicas_Preserve covers the default HPA strategy. The rounding
// rule is deliberate: a non-zero weight always gives the canary at least one
// pod so analysis gates have a sample to measure.
func TestSplitReplicas_Preserve(t *testing.T) {
	tests := []struct {
		name    string
		desired int32
		weight  int32
		canary  int32
		stable  int32
	}{
		{"zero weight", 10, 0, 0, 10},
		{"full weight", 10, 100, 10, 0},
		{"25% of 10", 10, 25, 3, 7},
		{"10% of 7 rounds up", 7, 10, 1, 6},
		{"5% of 10 rounds up", 10, 5, 1, 9},
		{"50% of 5", 5, 50, 3, 2}, // ceil(2.5) = 3
		{"1% of 1", 1, 1, 1, 0},
		{"zero desired", 0, 50, 0, 0},
		{"weight > 100 clamps", 5, 150, 5, 0},
		{"negative desired clamps", -3, 50, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, s := splitReplicas(tc.desired, tc.weight, rolloutsv1alpha1.HPAStrategyPreserve, 0)
			if c != tc.canary || s != tc.stable {
				t.Errorf("got (canary=%d, stable=%d), want (canary=%d, stable=%d)", c, s, tc.canary, tc.stable)
			}
		})
	}
}

// TestSplitReplicas_StableOnly covers the opt-out strategy where the canary
// count stays put and HPA deltas only move the stable RS.
func TestSplitReplicas_StableOnly(t *testing.T) {
	tests := []struct {
		name       string
		desired    int32
		canaryKeep int32
		weight     int32 // ignored under StableOnly but included to prove that
		canary     int32
		stable     int32
	}{
		{"keep 3, grow stable", 15, 3, 50, 3, 12},
		{"keep 0, everything stable", 10, 0, 50, 0, 10},
		{"keep exceeds desired clamps", 5, 20, 50, 5, 0},
		{"weight ignored", 10, 2, 90, 2, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, s := splitReplicas(tc.desired, tc.weight, rolloutsv1alpha1.HPAStrategyStableOnly, tc.canaryKeep)
			if c != tc.canary || s != tc.stable {
				t.Errorf("got (canary=%d, stable=%d), want (canary=%d, stable=%d)", c, s, tc.canary, tc.stable)
			}
		})
	}
}

// TestSplitReplicas_SumsToDesired is the integrity property: canary + stable
// must always equal desired (when desired > 0). This would catch off-by-one
// errors in the rounding or clamp paths.
func TestSplitReplicas_SumsToDesired(t *testing.T) {
	for desired := int32(1); desired <= 20; desired++ {
		for weight := int32(0); weight <= 100; weight++ {
			c, s := splitReplicas(desired, weight, rolloutsv1alpha1.HPAStrategyPreserve, 0)
			if c+s != desired {
				t.Errorf("desired=%d weight=%d: canary=%d stable=%d sums to %d", desired, weight, c, s, c+s)
			}
			if c < 0 || s < 0 {
				t.Errorf("desired=%d weight=%d: negative split (%d, %d)", desired, weight, c, s)
			}
			if weight > 0 && c == 0 {
				t.Errorf("desired=%d weight=%d: zero canary for non-zero weight", desired, weight)
			}
		}
	}
}

// TestDesiredReplicas_Defaults: nil spec.replicas must default to 1 so the
// rollout isn't silently scaled to zero.
func TestDesiredReplicas_Defaults(t *testing.T) {
	if got := desiredReplicas(&rolloutsv1alpha1.Rollout{}); got != 1 {
		t.Errorf("nil replicas: got %d, want 1", got)
	}
	ten := int32(10)
	ro := &rolloutsv1alpha1.Rollout{Spec: rolloutsv1alpha1.RolloutSpec{Replicas: &ten}}
	if got := desiredReplicas(ro); got != 10 {
		t.Errorf("explicit replicas: got %d, want 10", got)
	}
}

// TestCurrentCanaryReplicas: a newRS with nil Spec.Replicas is treated as 0
// so the StableOnly branch has a safe starting point on first reconcile.
func TestCurrentCanaryReplicas(t *testing.T) {
	if got := currentCanaryReplicas(nil); got != 0 {
		t.Errorf("nil RS: got %d, want 0", got)
	}
	rs := &appsv1.ReplicaSet{}
	if got := currentCanaryReplicas(rs); got != 0 {
		t.Errorf("nil Spec.Replicas: got %d, want 0", got)
	}
	three := int32(3)
	rs.Spec.Replicas = &three
	if got := currentCanaryReplicas(rs); got != 3 {
		t.Errorf("populated: got %d, want 3", got)
	}
}

// TestLargestOldRS picks the RS with the most replicas. Ties go to the first
// encountered; callers should not depend on tie-breaking.
func TestLargestOldRS(t *testing.T) {
	mk := func(name string, replicas int32) *appsv1.ReplicaSet {
		return &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       appsv1.ReplicaSetSpec{Replicas: &replicas},
		}
	}
	tests := []struct {
		name   string
		rsList []*appsv1.ReplicaSet
		want   string // empty when nil is expected
	}{
		{"empty", nil, ""},
		{"single", []*appsv1.ReplicaSet{mk("a", 5)}, "a"},
		{"picks largest", []*appsv1.ReplicaSet{mk("a", 3), mk("b", 10), mk("c", 7)}, "b"},
		{"skips nil", []*appsv1.ReplicaSet{nil, mk("a", 3)}, "a"},
		{"all zero picks first", []*appsv1.ReplicaSet{mk("a", 0), mk("b", 0)}, "a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := largestOldRS(tc.rsList)
			if tc.want == "" {
				if got != nil {
					t.Errorf("want nil, got %s", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %s, got nil", tc.want)
			}
			if got.Name != tc.want {
				t.Errorf("got %s, want %s", got.Name, tc.want)
			}
		})
	}
}

// TestObservedSpecReplicas is the gate that prevents promote-time scaleDown
// from racing an HPA-driven spec.replicas change. Both generation and
// desiredReplicas must have caught up.
func TestObservedSpecReplicas(t *testing.T) {
	ten := int32(10)
	five := int32(5)

	caught := &rolloutsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Generation: 7},
		Spec:       rolloutsv1alpha1.RolloutSpec{Replicas: &ten},
		Status: rolloutsv1alpha1.RolloutStatus{
			ObservedGeneration: 7,
			DesiredReplicas:    10,
		},
	}
	if !observedSpecReplicas(caught) {
		t.Errorf("fully caught up should return true")
	}

	staleGen := caught.DeepCopy()
	staleGen.Status.ObservedGeneration = 6
	if observedSpecReplicas(staleGen) {
		t.Errorf("stale observedGeneration should return false")
	}

	staleReplicas := caught.DeepCopy()
	staleReplicas.Spec.Replicas = &five
	if observedSpecReplicas(staleReplicas) {
		t.Errorf("spec.Replicas changed without status catching up: should return false")
	}
}
