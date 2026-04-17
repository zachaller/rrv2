/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package rollout

import (
	"math"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	rolloutsv1alpha1 "github.com/zaller/rollouts/pkg/apis/rollouts/v1alpha1"
)

// asDeploymentView projects a Rollout into a synthetic apps.Deployment that
// carries exactly the fields the forked k8s deployment controller reads.
// Reads from this object are authoritative; writes to it are discarded —
// status flows back through a separate RolloutStatus update path.
//
// The projection is intentionally narrow: Replicas, Selector, Template (or the
// workload it dereferences to), MinReadySeconds, ProgressDeadlineSeconds,
// RevisionHistoryLimit, Paused, and a RollingUpdate strategy built from the
// rollout's progression MaxSurge/MaxUnavailable. No other fields are copied.
func asDeploymentView(ro *rolloutsv1alpha1.Rollout, template *rolloutsv1alpha1.RolloutSpec) *appsv1.Deployment {
	replicas := int32(1)
	if template.Replicas != nil {
		replicas = *template.Replicas
	}

	progressDeadline := int32(600)
	if template.ProgressDeadlineSeconds != nil {
		progressDeadline = *template.ProgressDeadlineSeconds
	}

	revisionHistoryLimit := int32(10)
	if template.RevisionHistoryLimit != nil {
		revisionHistoryLimit = *template.RevisionHistoryLimit
	}

	maxSurge := intstr.FromString("25%")
	if template.Progression.MaxSurge != nil {
		maxSurge = *template.Progression.MaxSurge
	}
	maxUnavailable := intstr.FromString("25%")
	if template.Progression.MaxUnavailable != nil {
		maxUnavailable = *template.Progression.MaxUnavailable
	}

	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ro.Name,
			Namespace:   ro.Namespace,
			UID:         ro.UID,
			Labels:      ro.Labels,
			Annotations: ro.Annotations,
			Generation:  ro.Generation,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:             &replicas,
			Selector:             template.Selector,
			MinReadySeconds:      template.MinReadySeconds,
			RevisionHistoryLimit: &revisionHistoryLimit,
			// Progress deadline capped: the forked controller treats MaxInt32 as "no deadline".
			ProgressDeadlineSeconds: int32Ptr(clampProgressDeadline(progressDeadline)),
			Paused:                  template.Paused,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxSurge:       &maxSurge,
					MaxUnavailable: &maxUnavailable,
				},
			},
		},
	}

	if template.Template != nil {
		d.Spec.Template = *template.Template
	}
	return d
}

func clampProgressDeadline(d int32) int32 {
	if d <= 0 {
		return math.MaxInt32
	}
	return d
}

func int32Ptr(v int32) *int32 { return &v }
