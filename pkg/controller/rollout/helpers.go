/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package rollout

import (
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// applyDefaults fills zero-value enum fields and other defaulted knobs. Mirrors
// the kubebuilder +kubebuilder:default markers so in-memory reasoning is correct
// even when the spec bypasses admission defaulting.
func applyDefaults(ro *rolloutsv1alpha1.Rollout) {
	if ro.Spec.AutoPromotion == "" {
		ro.Spec.AutoPromotion = rolloutsv1alpha1.AutoPromotionEnabled
	}
	if ro.Spec.DynamicStableScale == "" {
		ro.Spec.DynamicStableScale = rolloutsv1alpha1.DynamicStableScaleOff
	}
	for i := range ro.Spec.Progression.Steps {
		step := &ro.Spec.Progression.Steps[i]
		if step.SetWeight != nil && step.SetWeight.ForRole == "" {
			step.SetWeight.ForRole = rolloutsv1alpha1.ServiceRoleCanary
		}
		if step.SetCanaryScale != nil && step.SetCanaryScale.ForRole == "" {
			step.SetCanaryScale.ForRole = rolloutsv1alpha1.ServiceRoleCanary
		}
		if step.Analysis != nil && step.Analysis.FailurePolicy == "" {
			step.Analysis.FailurePolicy = "Rollback"
		}
	}
	if ro.Spec.TrafficRouting != nil && ro.Spec.TrafficRouting.VerifyWeight == "" {
		ro.Spec.TrafficRouting.VerifyWeight = "Enabled"
	}
}

// isOwnedBy reports whether rs has a controller owner reference pointing at ro.
func isOwnedBy(rs metav1.Object, ro *rolloutsv1alpha1.Rollout) bool {
	for _, owner := range rs.GetOwnerReferences() {
		if owner.UID == ro.UID && owner.Controller != nil && *owner.Controller {
			return true
		}
	}
	return false
}

// labelSelectorString serializes a LabelSelector as a
// k8s.io/apimachinery/pkg/labels-parsable string. MatchExpressions are not
// supported yet — callers that need them should switch to
// metav1.LabelSelectorAsSelector.
func labelSelectorString(sel *metav1.LabelSelector) string {
	if sel == nil {
		return ""
	}
	keys := make([]string, 0, len(sel.MatchLabels))
	for k := range sel.MatchLabels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, sel.MatchLabels[k]))
	}
	return strings.Join(parts, ",")
}

func metav1Now() metav1.Time { return metav1.NewTime(time.Now()) }
