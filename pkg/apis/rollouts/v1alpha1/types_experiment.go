/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=experiments,shortName=exp

// Experiment is a short-lived A/B test: multiple ReplicaSets run side-by-side
// and are evaluated against AnalysisRuns. Owned by a Rollout step or created
// standalone.
type Experiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ExperimentSpec   `json:"spec"`
	Status            ExperimentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ExperimentList is a list of Experiments.
type ExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Experiment `json:"items"`
}

// ExperimentSpec is the desired state.
type ExperimentSpec struct {
	// Duration bounds the experiment. Nil runs until analyses complete.
	// +optional
	Duration *metav1.Duration `json:"duration,omitempty"`

	// Variants are the concurrent variants.
	// +kubebuilder:validation:MinItems=1
	Variants []ExperimentVariant `json:"variants"`

	// Analysis hooks scoped to this experiment.
	// +optional
	Analysis []AnalysisHook `json:"analysis,omitempty"`

	// TerminationPolicy controls what happens to experiment pods on completion.
	// +kubebuilder:validation:Enum=Terminate;Keep
	// +kubebuilder:default=Terminate
	// +optional
	TerminationPolicy string `json:"terminationPolicy,omitempty"`
}

// ExperimentStatus is the observed state.
type ExperimentStatus struct {
	// +kubebuilder:validation:Enum=Pending;Running;Successful;Failed;Inconclusive
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`

	// +optional
	VariantStatuses []ExperimentVariantStatus `json:"variantStatuses,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

// ExperimentVariantStatus reports one variant's ReplicaSet progress.
type ExperimentVariantStatus struct {
	Name             string `json:"name"`
	ReplicaSetName   string `json:"replicaSetName,omitempty"`
	ReadyReplicas    int32  `json:"readyReplicas,omitempty"`
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`
	Phase            string `json:"phase,omitempty"`
	Message          string `json:"message,omitempty"`
}
