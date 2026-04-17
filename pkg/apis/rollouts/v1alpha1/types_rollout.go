/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.replicas,selectorpath=.status.selector
// +kubebuilder:resource:path=rollouts,shortName=ro,categories={rollouts}
// +kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name=Step,type=string,JSONPath=`.status.currentStep`
// +kubebuilder:printcolumn:name=Ready,type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name=Updated,type=integer,JSONPath=`.status.updatedReplicas`
// +kubebuilder:printcolumn:name=Available,type=integer,JSONPath=`.status.availableReplicas`
// +kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// Rollout is a progressive-delivery workload. It owns ReplicaSets the same way
// a Deployment does, but rolls them out through an explicit, named sequence of
// steps (traffic shifts, replica scales, pauses, analyses, experiments, and
// promotions). Both canary and blue-green strategies are expressed as step
// sequences — there is no separate mode.
type Rollout struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RolloutSpec   `json:"spec"`
	Status RolloutStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RolloutList is a list of Rollouts.
type RolloutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Rollout `json:"items"`
}

// RolloutSpec is the desired state of a Rollout.
type RolloutSpec struct {
	// Replicas is the desired pod count. Defaults to 1.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Selector matches pods owned by this rollout's ReplicaSets. Required.
	Selector *metav1.LabelSelector `json:"selector"`

	// Template is an inline pod template. Exactly one of Template or WorkloadRef must be set.
	// +optional
	Template *corev1.PodTemplateSpec `json:"template,omitempty"`

	// WorkloadRef points at an external workload whose pod template the rollout
	// will manage. Exactly one of Template or WorkloadRef must be set.
	// +optional
	WorkloadRef *WorkloadRef `json:"workloadRef,omitempty"`

	// CanaryServices name the Services routed to the canary ReplicaSet during
	// a canary-style rollout. Plural to allow multiple Services per rollout.
	// +optional
	CanaryServices []ServiceRef `json:"canaryServices,omitempty"`

	// StableServices name the Services routed to the stable ReplicaSet.
	// +optional
	StableServices []ServiceRef `json:"stableServices,omitempty"`

	// ActiveServices name the Services that receive production traffic during a
	// blue-green rollout. Their selectors are flipped at Promote.
	// +optional
	ActiveServices []ServiceRef `json:"activeServices,omitempty"`

	// PreviewServices name the Services that expose the preview ReplicaSet
	// before promotion.
	// +optional
	PreviewServices []ServiceRef `json:"previewServices,omitempty"`

	// Progression is the ordered step plan. Required.
	Progression ProgressionSpec `json:"progression"`

	// TrafficRouting configures the router provider used to shape Service/Ingress/Mesh traffic.
	// Exactly one provider's config must be populated, matching Provider.
	// +optional
	TrafficRouting *TrafficRoutingSpec `json:"trafficRouting,omitempty"`

	// AutoPromotion governs what happens when a Pause step elapses or a Promote
	// step is reached.
	// +kubebuilder:validation:Enum=Enabled;Manual;Disabled
	// +kubebuilder:default=Enabled
	// +optional
	AutoPromotion AutoPromotionMode `json:"autoPromotion,omitempty"`

	// DynamicStableScale controls whether the stable ReplicaSet scales down
	// proportionally as traffic shifts to the canary.
	// +kubebuilder:validation:Enum=Off;Proportional;Aggressive
	// +kubebuilder:default=Off
	// +optional
	DynamicStableScale DynamicStableScaleMode `json:"dynamicStableScale,omitempty"`

	// Analysis hooks fire at defined points in the progression. This single
	// list subsumes canary/bluegreen pre/post promotion analysis — the When
	// field distinguishes trigger points.
	// +optional
	Analysis []AnalysisHook `json:"analysis,omitempty"`

	// EphemeralMetadata is applied to pods of specific roles (canary, stable,
	// active, preview) for the duration they hold that role. When the role
	// changes (e.g. canary becomes stable), the controller removes these
	// labels/annotations without recreating pods.
	// +optional
	EphemeralMetadata []EphemeralMetadata `json:"ephemeralMetadata,omitempty"`

	// RestartAt, when set, triggers a rolling pod restart no later than this time.
	// +optional
	RestartAt *metav1.Time `json:"restartAt,omitempty"`

	// RollbackWindow bounds automatic fast-track rollback to the most recent N revisions.
	// +optional
	RollbackWindow *RollbackWindow `json:"rollbackWindow,omitempty"`

	// ProgressDeadlineSeconds is the maximum time a step can make no forward
	// progress before the rollout is marked Degraded. Defaults to 600.
	// +optional
	ProgressDeadlineSeconds *int32 `json:"progressDeadlineSeconds,omitempty"`

	// RevisionHistoryLimit bounds retained prior ReplicaSets for rollback. Defaults to 10.
	// +optional
	RevisionHistoryLimit *int32 `json:"revisionHistoryLimit,omitempty"`

	// MinReadySeconds mirrors Deployment.Spec.MinReadySeconds.
	// +optional
	MinReadySeconds int32 `json:"minReadySeconds,omitempty"`

	// Paused halts reconciliation of this Rollout.
	// +optional
	Paused bool `json:"paused,omitempty"`

	// Abort, when true, aborts the current rollout and reverts traffic + replicas
	// to the stable/active revision. Prefer setting this over annotations or
	// subresources — this is the one canonical abort channel.
	// +optional
	Abort bool `json:"abort,omitempty"`
}

// ServiceRef names a Service in the rollout's namespace.
type ServiceRef struct {
	Name string `json:"name"`
}

// WorkloadRef points at an external workload (typically a Deployment) whose
// pod template the Rollout manages. When set, the Rollout does not accept an
// inline Template.
type WorkloadRef struct {
	// APIVersion of the referenced workload, e.g. apps/v1.
	APIVersion string `json:"apiVersion"`

	// Kind of the referenced workload, e.g. Deployment.
	// +kubebuilder:validation:Enum=Deployment;ReplicaSet;StatefulSet
	Kind string `json:"kind"`

	// Name of the referenced workload in the same namespace.
	Name string `json:"name"`

	// ScaleDown governs what happens to the referenced workload while the
	// Rollout owns it. Never keeps it at its author's replicas; OnSuccess
	// scales it to zero once the rollout completes; Progressively scales it
	// down in lockstep with the canary ramp.
	// +kubebuilder:validation:Enum=Never;OnSuccess;Progressively
	// +kubebuilder:default=Never
	// +optional
	ScaleDown WorkloadScaleDownMode `json:"scaleDown,omitempty"`
}

// ProgressionSpec is the step-based execution plan.
type ProgressionSpec struct {
	// Steps executed in order. Each step has a unique, required name; status
	// and analysis hooks reference steps by name, not index, so insert/reorder
	// does not silently rebind them.
	// +kubebuilder:validation:MinItems=1
	Steps []Step `json:"steps"`

	// MaxUnavailable is used by scaleDown math on the path that doesn't go
	// through a router (pure replica-weighted canary). Defaults to 25%.
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`

	// MaxSurge mirrors Deployment.Spec.Strategy.RollingUpdate.MaxSurge. Defaults to 25%.
	// +optional
	MaxSurge *intstr.IntOrString `json:"maxSurge,omitempty"`

	// ScaleDownDelaySeconds is how long the previous active (bluegreen) or
	// stable (canary-with-dynamicStableScale) ReplicaSet is retained after a
	// successful Promote before the controller scales it down. Defaults to 30.
	// +optional
	ScaleDownDelaySeconds *int32 `json:"scaleDownDelaySeconds,omitempty"`
}

// Step is a tagged union — exactly one of the sub-structs must be populated.
// CRD-level XValidation enforces this; see the kubebuilder marker below.
//
// +kubebuilder:validation:XValidation:rule="(has(self.setWeight)?1:0) + (has(self.setCanaryScale)?1:0) + (has(self.pause)?1:0) + (has(self.analysis)?1:0) + (has(self.experiment)?1:0) + (has(self.promote)?1:0) == 1",message="exactly one step action must be set"
type Step struct {
	// Name uniquely identifies this step within the rollout. Required.
	// Analysis hooks reference steps by name, not index, so renaming or
	// reordering will surface as a validation error rather than silent breakage.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// SetWeight shifts router traffic to (or away from) the canary/preview role.
	// +optional
	SetWeight *SetWeightStep `json:"setWeight,omitempty"`

	// SetCanaryScale adjusts the canary/preview ReplicaSet's replica count
	// independent of traffic weight — useful for warmup, latency soak, or
	// matching traffic weight when the router lacks weight support.
	// +optional
	SetCanaryScale *SetCanaryScaleStep `json:"setCanaryScale,omitempty"`

	// Pause halts the progression until its Duration elapses or the Rollout is promoted.
	// +optional
	Pause *PauseStep `json:"pause,omitempty"`

	// Analysis runs an inline AnalysisRun (referencing one or more templates)
	// and blocks progression on its outcome.
	// +optional
	Analysis *AnalysisStep `json:"analysis,omitempty"`

	// Experiment runs a short-lived side-by-side experiment with multiple
	// variants, optionally with its own analysis.
	// +optional
	Experiment *ExperimentStep `json:"experiment,omitempty"`

	// Promote atomically flips active/stable to the current canary/preview RS
	// and, after ScaleDownDelaySeconds, scales the old one down.
	// +optional
	Promote *PromoteStep `json:"promote,omitempty"`
}

// SetWeightStep moves router traffic.
type SetWeightStep struct {
	// Weight is the percentage of traffic sent to ForRole. 0-100.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Weight int32 `json:"weight"`

	// ForRole selects which role receives Weight percent of traffic.
	// Defaults to canary.
	// +kubebuilder:validation:Enum=canary;preview
	// +kubebuilder:default=canary
	// +optional
	ForRole ServiceRole `json:"forRole,omitempty"`

	// Matches layers header/cookie/query match rules on top of weight routing,
	// when the router supports it (Istio, Nginx, ALB).
	// +optional
	Matches []RouteMatch `json:"matches,omitempty"`
}

// SetCanaryScaleStep adjusts replicas for a role independent of traffic weight.
// Exactly one of Replicas, Percent, or MatchTrafficWeight must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.replicas)?1:0) + (has(self.percent)?1:0) + (has(self.matchTrafficWeight) && self.matchTrafficWeight?1:0) == 1",message="exactly one of replicas, percent, or matchTrafficWeight must be set"
type SetCanaryScaleStep struct {
	// Replicas is an absolute replica count for ForRole's ReplicaSet.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Percent is a percentage of spec.replicas. e.g. percent: 25 with
	// replicas: 10 scales the canary to 3.
	// +optional
	Percent *intstr.IntOrString `json:"percent,omitempty"`

	// MatchTrafficWeight pins the canary replica count to track the most recent
	// SetWeight — a convenient shorthand for matching pod count to traffic %.
	// +optional
	MatchTrafficWeight bool `json:"matchTrafficWeight,omitempty"`

	// ForRole selects which role's ReplicaSet is scaled. Defaults to canary.
	// +kubebuilder:validation:Enum=canary;preview
	// +kubebuilder:default=canary
	// +optional
	ForRole ServiceRole `json:"forRole,omitempty"`
}

// PauseStep halts progression.
type PauseStep struct {
	// Duration is how long to pause. Nil means indefinite — advance only when
	// the user promotes (sets status.promotedAt), an analysis succeeds, or the
	// rollout is aborted.
	// +optional
	Duration *metav1.Duration `json:"duration,omitempty"`
}

// AnalysisStep runs an inline AnalysisRun and gates progression on its outcome.
type AnalysisStep struct {
	// TemplateRefs names one or more AnalysisTemplates (or ClusterAnalysisTemplates)
	// whose metrics are merged into the AnalysisRun.
	// +kubebuilder:validation:MinItems=1
	TemplateRefs []TemplateRef `json:"templateRefs"`

	// Args supplies values for template parameters.
	// +optional
	Args []AnalysisArg `json:"args,omitempty"`

	// DryRun marks specific metrics as non-blocking (they evaluate but can't fail the run).
	// +optional
	DryRun []DryRunMetric `json:"dryRun,omitempty"`

	// FailurePolicy picks what happens when the analysis fails.
	// +kubebuilder:validation:Enum=Rollback;Pause;Ignore
	// +kubebuilder:default=Rollback
	// +optional
	FailurePolicy string `json:"failurePolicy,omitempty"`
}

// ExperimentStep runs a side-by-side experiment.
type ExperimentStep struct {
	// Duration bounds how long the experiment runs. Nil means until analyses complete.
	// +optional
	Duration *metav1.Duration `json:"duration,omitempty"`

	// Variants are the concurrent variants. Typically a baseline + one or more
	// treatment variants.
	// +kubebuilder:validation:MinItems=1
	Variants []ExperimentVariant `json:"variants"`

	// Analysis hooks scoped to this experiment. Use these to gate the experiment
	// itself on metrics, independent of rollout-level analysis.
	// +optional
	Analysis []AnalysisHook `json:"analysis,omitempty"`
}

// ExperimentVariant describes one arm of an experiment.
type ExperimentVariant struct {
	Name     string                  `json:"name"`
	Replicas int32                   `json:"replicas"`
	Template *corev1.PodTemplateSpec `json:"template,omitempty"`

	// Weight shifts the router to send this fraction of traffic to this variant.
	// Uses the same scale as SetWeightStep.Weight (0-100).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Weight *int32 `json:"weight,omitempty"`
}

// PromoteStep atomically flips the active/stable ReplicaSet pointer.
// Intentionally empty — all configuration lives on the enclosing Rollout
// (AutoPromotion, ScaleDownDelaySeconds).
type PromoteStep struct{}

// AnalysisHook is a single trigger point for an AnalysisRun.
type AnalysisHook struct {
	// When selects the trigger point. preStep/postStep fire at a specific named
	// step (see AtStep); background runs for the full duration of the rollout;
	// prePromotion/postPromotion fire immediately before/after a Promote step.
	// +kubebuilder:validation:Enum=preStep;postStep;background;prePromotion;postPromotion
	When AnalysisWhen `json:"when"`

	// AtStep is the step name this hook attaches to. Required when When is
	// preStep or postStep; ignored otherwise.
	// +optional
	AtStep string `json:"atStep,omitempty"`

	// TemplateRefs are the AnalysisTemplates whose metrics are merged in.
	// +kubebuilder:validation:MinItems=1
	TemplateRefs []TemplateRef `json:"templateRefs"`

	// Args supplies values for template parameters.
	// +optional
	Args []AnalysisArg `json:"args,omitempty"`

	// DryRun marks specific metrics as non-blocking.
	// +optional
	DryRun []DryRunMetric `json:"dryRun,omitempty"`

	// InconclusivePolicy controls what happens on an inconclusive outcome.
	// +kubebuilder:validation:Enum=Abort;Pause;Ignore
	// +kubebuilder:default=Pause
	// +optional
	InconclusivePolicy string `json:"inconclusivePolicy,omitempty"`
}

// TemplateRef names an AnalysisTemplate.
type TemplateRef struct {
	Name string `json:"name"`

	// Kind selects the scope. AnalysisTemplate is namespaced (default);
	// ClusterAnalysisTemplate is cluster-scoped.
	// +kubebuilder:validation:Enum=AnalysisTemplate;ClusterAnalysisTemplate
	// +kubebuilder:default=AnalysisTemplate
	// +optional
	Kind string `json:"kind,omitempty"`
}

// AnalysisArg is a parameter value passed into a templated AnalysisRun.
type AnalysisArg struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
	// ValueFrom pulls the value from a field or Secret at run time.
	// +optional
	ValueFrom *ArgSource `json:"valueFrom,omitempty"`
}

// ArgSource picks a value from the rollout or a Secret.
type ArgSource struct {
	// FieldRef is a field path on the Rollout, e.g. "metadata.labels['app']"
	// +optional
	FieldRef *FieldRef `json:"fieldRef,omitempty"`
	// SecretKeyRef pulls the value from a Secret in the rollout's namespace.
	// +optional
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// FieldRef is a path into the Rollout object.
type FieldRef struct {
	FieldPath string `json:"fieldPath"`
}

// DryRunMetric names a metric whose result should not fail the analysis.
type DryRunMetric struct {
	MetricName string `json:"metricName"`
}

// RouteMatch describes a header/cookie/query match, used to layer
// conditional routing atop weighted routing.
type RouteMatch struct {
	// Headers matches HTTP request headers. Exact, prefix, or regex.
	// +optional
	Headers []StringMatch `json:"headers,omitempty"`
	// Cookies matches HTTP cookies.
	// +optional
	Cookies []StringMatch `json:"cookies,omitempty"`
	// QueryParams matches URL query parameters.
	// +optional
	QueryParams []StringMatch `json:"queryParams,omitempty"`
}

// StringMatch is a name + exact|prefix|regex match expression.
type StringMatch struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Enum=Exact;Prefix;Regex
	MatchType string `json:"matchType,omitempty"`
	Value     string `json:"value"`
}

// TrafficRoutingSpec is the single-provider router configuration.
//
// +kubebuilder:validation:XValidation:rule="(has(self.istio)?1:0) + (has(self.nginx)?1:0) + (has(self.alb)?1:0) + (has(self.smi)?1:0) + (has(self.traefik)?1:0) + (has(self.apisix)?1:0) == 1",message="exactly one provider config must be set"
// +kubebuilder:validation:XValidation:rule="(self.provider=='istio') == has(self.istio)",message="provider must match populated config"
type TrafficRoutingSpec struct {
	// Provider selects which router backend to use.
	// +kubebuilder:validation:Enum=istio;nginx;alb;smi;traefik;apisix
	Provider string `json:"provider"`

	// VerifyWeight makes the controller wait for the router to observe the
	// desired weight before advancing. Defaults to Enabled.
	// +kubebuilder:validation:Enum=Enabled;Disabled
	// +kubebuilder:default=Enabled
	// +optional
	VerifyWeight string `json:"verifyWeight,omitempty"`

	// ManagedRoutes declares named header/cookie/query routes the controller
	// creates and tears down around a rollout.
	// +optional
	ManagedRoutes []ManagedRoute `json:"managedRoutes,omitempty"`

	// +optional
	Istio *IstioConfig `json:"istio,omitempty"`
	// +optional
	Nginx *NginxConfig `json:"nginx,omitempty"`
	// +optional
	ALB *ALBConfig `json:"alb,omitempty"`
	// +optional
	SMI *SMIConfig `json:"smi,omitempty"`
	// +optional
	Traefik *TraefikConfig `json:"traefik,omitempty"`
	// +optional
	APISIX *APISIXConfig `json:"apisix,omitempty"`
}

// ManagedRoute is a named routing rule the controller owns for the lifetime of a rollout.
type ManagedRoute struct {
	Name    string       `json:"name"`
	Matches []RouteMatch `json:"matches,omitempty"`
}

// IstioConfig wires the rollout to Istio VirtualServices and DestinationRules.
type IstioConfig struct {
	// VirtualServices to edit. Plural — replaces Argo's singular `virtualService`.
	// +kubebuilder:validation:MinItems=1
	VirtualServices []IstioVirtualServiceRef `json:"virtualServices"`

	// DestinationRules to edit.
	// +optional
	DestinationRules []IstioDestinationRuleRef `json:"destinationRules,omitempty"`
}

// IstioVirtualServiceRef names a VirtualService and the routes within it.
type IstioVirtualServiceRef struct {
	Name string `json:"name"`
	// Routes are HTTP route names to edit. Empty means "all HTTP routes".
	// +optional
	Routes []string `json:"routes,omitempty"`
	// TLSRoutes are TLS route names to edit.
	// +optional
	TLSRoutes []string `json:"tlsRoutes,omitempty"`
	// TCPRoutes are TCP route names to edit.
	// +optional
	TCPRoutes []string `json:"tcpRoutes,omitempty"`
}

// IstioDestinationRuleRef names a DestinationRule and the subsets it exposes.
type IstioDestinationRuleRef struct {
	Name          string `json:"name"`
	CanarySubset  string `json:"canarySubset,omitempty"`
	StableSubset  string `json:"stableSubset,omitempty"`
}

// NginxConfig wires the rollout to an Nginx Ingress controller.
type NginxConfig struct {
	// Ingresses to manage. Plural from day one.
	// +kubebuilder:validation:MinItems=1
	Ingresses []string `json:"ingresses"`

	// AnnotationPrefix customizes the annotation namespace for Nginx controllers
	// that deviate from the default.
	// +optional
	AnnotationPrefix string `json:"annotationPrefix,omitempty"`

	// AdditionalAnnotations are copied onto the generated canary Ingress.
	// +optional
	AdditionalAnnotations map[string]string `json:"additionalAnnotations,omitempty"`
}

// ALBConfig wires the rollout to an AWS Load Balancer Controller Ingress.
type ALBConfig struct {
	// Ingresses to manage. Plural.
	// +kubebuilder:validation:MinItems=1
	Ingresses []string `json:"ingresses"`

	// ServicePort is the Service port on the target group.
	ServicePort int32 `json:"servicePort"`

	// AnnotationPrefix customizes the annotation namespace.
	// +optional
	AnnotationPrefix string `json:"annotationPrefix,omitempty"`

	// Stickiness configures target group stickiness.
	// +optional
	Stickiness *ALBStickiness `json:"stickiness,omitempty"`
}

// ALBStickiness is typed (not raw JSON) to avoid the leaky-abstraction problem.
type ALBStickiness struct {
	Enabled         bool   `json:"enabled"`
	DurationSeconds int64  `json:"durationSeconds,omitempty"`
	Type            string `json:"type,omitempty"`
}

// SMIConfig wires the rollout to a Service Mesh Interface TrafficSplit.
type SMIConfig struct {
	// TrafficSplits the controller should manage. If empty, the controller
	// creates one named after the rollout.
	// +optional
	TrafficSplits []string `json:"trafficSplits,omitempty"`

	// RootService is the Service that receives unrouted traffic.
	// +optional
	RootService string `json:"rootService,omitempty"`
}

// TraefikConfig wires the rollout to Traefik TraefikServices.
type TraefikConfig struct {
	// WeightedTraefikServices to manage. Plural.
	// +kubebuilder:validation:MinItems=1
	WeightedTraefikServices []string `json:"weightedTraefikServices"`
}

// APISIXConfig wires the rollout to ApisixRoutes.
type APISIXConfig struct {
	// Routes to manage. Plural.
	// +kubebuilder:validation:MinItems=1
	Routes []APISIXRoute `json:"routes"`
}

// APISIXRoute names an ApisixRoute and optionally specific rule names.
type APISIXRoute struct {
	Name  string   `json:"name"`
	Rules []string `json:"rules,omitempty"`
}

// RollbackWindow bounds auto-rollback targets.
type RollbackWindow struct {
	// Revisions is how many most-recent revisions are eligible for fast-track rollback.
	// +kubebuilder:validation:Minimum=1
	Revisions int32 `json:"revisions"`
}

// EphemeralMetadata applies labels/annotations to pods while they hold a role.
type EphemeralMetadata struct {
	// +kubebuilder:validation:Enum=canary;stable;active;preview
	Role        ServiceRole       `json:"role"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// RolloutStatus is the observed state of a Rollout.
type RolloutStatus struct {
	// ObservedGeneration is the generation of the Rollout spec last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Replicas, UpdatedReplicas, ReadyReplicas, AvailableReplicas match the
	// Deployment conventions exactly.
	Replicas          int32 `json:"replicas,omitempty"`
	UpdatedReplicas   int32 `json:"updatedReplicas,omitempty"`
	ReadyReplicas     int32 `json:"readyReplicas,omitempty"`
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

	// Selector is the LabelSelector serialized for the scale subresource.
	// +optional
	Selector string `json:"selector,omitempty"`

	// CurrentStep is the NAME of the step currently being executed (not its index).
	// Empty before the first step starts and after the final step completes.
	// +optional
	CurrentStep string `json:"currentStep,omitempty"`

	// CurrentStepHash is a hash of the step's spec; changes invalidate in-flight state.
	// +optional
	CurrentStepHash string `json:"currentStepHash,omitempty"`

	// CurrentPodHash is the pod-template-hash of the RS currently being rolled out.
	// +optional
	CurrentPodHash string `json:"currentPodHash,omitempty"`

	// StableRevision is the revision of the most recent fully-promoted RS.
	// +optional
	StableRevision string `json:"stableRevision,omitempty"`

	// CurrentRevision is the revision of the RS associated with CurrentStep.
	// +optional
	CurrentRevision string `json:"currentRevision,omitempty"`

	// StepStartedAt marks when CurrentStep began executing.
	// +optional
	StepStartedAt *metav1.Time `json:"stepStartedAt,omitempty"`

	// Phase is the high-level lifecycle phase of the rollout.
	// +kubebuilder:validation:Enum=Pending;Progressing;Paused;Promoting;Healthy;Degraded;Aborted
	// +optional
	Phase string `json:"phase,omitempty"`

	// Message is a human-readable description of Phase.
	// +optional
	Message string `json:"message,omitempty"`

	// PauseConditions is the list of active pauses (typed reasons, not free-form strings).
	// +optional
	PauseConditions []PauseCondition `json:"pauseConditions,omitempty"`

	// Conditions mirror the standard Deployment conditions (Available, Progressing,
	// ReplicaFailure) plus rollout-specific ones.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// AnalysisRunSummaries are compact pointers to child AnalysisRuns.
	// +optional
	AnalysisRunSummaries []AnalysisRunSummary `json:"analysisRunSummaries,omitempty"`

	// RestartedAt is the last time a RestartAt request was honored.
	// +optional
	RestartedAt *metav1.Time `json:"restartedAt,omitempty"`

	// CollisionCount avoids pod-template-hash collisions, same as Deployment.
	// +optional
	CollisionCount *int32 `json:"collisionCount,omitempty"`
}

// PauseCondition is a single, typed pause reason.
type PauseCondition struct {
	// +kubebuilder:validation:Enum=PausedStep;UserRequested;AnalysisInconclusive;AnalysisFailed;BlueGreenAutoPromotionDisabled
	Reason string `json:"reason"`

	StartTime metav1.Time `json:"startTime"`

	// Until is when this pause will elapse, if Duration is set.
	// +optional
	Until *metav1.Time `json:"until,omitempty"`

	// Message is a human-readable description.
	// +optional
	Message string `json:"message,omitempty"`
}

// AnalysisRunSummary is a compact pointer to a child AnalysisRun.
type AnalysisRunSummary struct {
	Name    string `json:"name"`
	AtStep  string `json:"atStep,omitempty"`
	Phase   string `json:"phase"`
	Message string `json:"message,omitempty"`
}

// String type aliases — kept as distinct types so the generated CRD schema
// surfaces them with their enum validations.

// AutoPromotionMode is the autoPromotion enum.
type AutoPromotionMode string

const (
	AutoPromotionEnabled  AutoPromotionMode = "Enabled"
	AutoPromotionManual   AutoPromotionMode = "Manual"
	AutoPromotionDisabled AutoPromotionMode = "Disabled"
)

// DynamicStableScaleMode is the dynamicStableScale enum.
type DynamicStableScaleMode string

const (
	DynamicStableScaleOff          DynamicStableScaleMode = "Off"
	DynamicStableScaleProportional DynamicStableScaleMode = "Proportional"
	DynamicStableScaleAggressive   DynamicStableScaleMode = "Aggressive"
)

// WorkloadScaleDownMode is the workloadRef.scaleDown enum.
type WorkloadScaleDownMode string

const (
	WorkloadScaleDownNever         WorkloadScaleDownMode = "Never"
	WorkloadScaleDownOnSuccess     WorkloadScaleDownMode = "OnSuccess"
	WorkloadScaleDownProgressively WorkloadScaleDownMode = "Progressively"
)

// ServiceRole tags a ServiceRef or applies to EphemeralMetadata / Step ForRole.
type ServiceRole string

const (
	ServiceRoleCanary  ServiceRole = "canary"
	ServiceRoleStable  ServiceRole = "stable"
	ServiceRoleActive  ServiceRole = "active"
	ServiceRolePreview ServiceRole = "preview"
)

// AnalysisWhen is the analysisHook.when enum.
type AnalysisWhen string

const (
	AnalysisWhenPreStep        AnalysisWhen = "preStep"
	AnalysisWhenPostStep       AnalysisWhen = "postStep"
	AnalysisWhenBackground     AnalysisWhen = "background"
	AnalysisWhenPrePromotion   AnalysisWhen = "prePromotion"
	AnalysisWhenPostPromotion  AnalysisWhen = "postPromotion"
)

// Phase constants for RolloutStatus.Phase.
const (
	PhasePending     = "Pending"
	PhaseProgressing = "Progressing"
	PhasePaused      = "Paused"
	PhasePromoting   = "Promoting"
	PhaseHealthy     = "Healthy"
	PhaseDegraded    = "Degraded"
	PhaseAborted     = "Aborted"
)

// Pause reason constants.
const (
	PauseReasonPausedStep                       = "PausedStep"
	PauseReasonUserRequested                    = "UserRequested"
	PauseReasonAnalysisInconclusive             = "AnalysisInconclusive"
	PauseReasonAnalysisFailed                   = "AnalysisFailed"
	PauseReasonBlueGreenAutoPromotionDisabled   = "BlueGreenAutoPromotionDisabled"
)
