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
// +kubebuilder:resource:path=analysistemplates,scope=Namespaced,shortName=at

// AnalysisTemplate is a reusable set of metrics.
type AnalysisTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AnalysisTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// AnalysisTemplateList is a list of AnalysisTemplates.
type AnalysisTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AnalysisTemplate `json:"items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=clusteranalysistemplates,scope=Cluster,shortName=cat

// ClusterAnalysisTemplate is an AnalysisTemplate at cluster scope.
type ClusterAnalysisTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AnalysisTemplateSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ClusterAnalysisTemplateList is a list of ClusterAnalysisTemplates.
type ClusterAnalysisTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterAnalysisTemplate `json:"items"`
}

// AnalysisTemplateSpec is the shared body for namespaced and cluster templates.
type AnalysisTemplateSpec struct {
	// +kubebuilder:validation:MinItems=1
	Metrics []Metric `json:"metrics"`

	// Args declare template parameters.
	// +optional
	Args []AnalysisArg `json:"args,omitempty"`

	// DryRun metrics are evaluated but can't fail the run.
	// +optional
	DryRun []DryRunMetric `json:"dryRun,omitempty"`

	// InconclusivePolicy when inconclusive results are encountered.
	// +kubebuilder:validation:Enum=Abort;Pause;Ignore
	// +kubebuilder:default=Pause
	// +optional
	InconclusivePolicy string `json:"inconclusivePolicy,omitempty"`
}

// Metric is a single metric query with thresholds.
type Metric struct {
	Name string `json:"name"`

	// Interval between samples. Defaults to 10s.
	// +optional
	Interval *metav1.Duration `json:"interval,omitempty"`

	// InitialDelay before the first sample.
	// +optional
	InitialDelay *metav1.Duration `json:"initialDelay,omitempty"`

	// Count is how many samples to take. Defaults to run forever until the
	// rollout advances past the analysis step.
	// +optional
	Count *intstr.IntOrString `json:"count,omitempty"`

	// ConsecutiveErrorLimit is how many consecutive provider errors are
	// tolerated before the metric is considered errored.
	// +optional
	ConsecutiveErrorLimit *intstr.IntOrString `json:"consecutiveErrorLimit,omitempty"`

	// SuccessCondition is a gval expression evaluated against each sample.
	// +optional
	SuccessCondition string `json:"successCondition,omitempty"`

	// FailureCondition is a gval expression evaluated against each sample.
	// +optional
	FailureCondition string `json:"failureCondition,omitempty"`

	// FailureLimit is how many failing samples are tolerated before the metric
	// fails. Defaults to 0.
	// +optional
	FailureLimit *intstr.IntOrString `json:"failureLimit,omitempty"`

	// Provider is the metric backend.
	Provider MetricProvider `json:"provider"`
}

// MetricProvider is a discriminated union by Type.
//
// +kubebuilder:validation:XValidation:rule="(has(self.prometheus)?1:0) + (has(self.datadog)?1:0) + (has(self.newRelic)?1:0) + (has(self.wavefront)?1:0) + (has(self.cloudWatch)?1:0) + (has(self.kayenta)?1:0) + (has(self.web)?1:0) + (has(self.job)?1:0) == 1",message="exactly one provider config must be set"
type MetricProvider struct {
	// +kubebuilder:validation:Enum=prometheus;datadog;newRelic;wavefront;cloudWatch;kayenta;web;job
	Type string `json:"type"`

	// +optional
	Prometheus *PrometheusProvider `json:"prometheus,omitempty"`
	// +optional
	Datadog *DatadogProvider `json:"datadog,omitempty"`
	// +optional
	NewRelic *NewRelicProvider `json:"newRelic,omitempty"`
	// +optional
	Wavefront *WavefrontProvider `json:"wavefront,omitempty"`
	// +optional
	CloudWatch *CloudWatchProvider `json:"cloudWatch,omitempty"`
	// +optional
	Kayenta *KayentaProvider `json:"kayenta,omitempty"`
	// +optional
	Web *WebProvider `json:"web,omitempty"`
	// +optional
	Job *JobProvider `json:"job,omitempty"`
}

// PrometheusProvider queries a Prometheus endpoint.
type PrometheusProvider struct {
	Address string `json:"address"`
	Query   string `json:"query"`

	// Timeout per query.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Headers are injected on each query request.
	// +optional
	Headers []corev1.EnvVar `json:"headers,omitempty"`

	// Range, if set, issues a range query rather than an instant query.
	// +optional
	Range *PrometheusRange `json:"range,omitempty"`
}

// PrometheusRange parameters.
type PrometheusRange struct {
	Start metav1.Duration `json:"start"`
	End   metav1.Duration `json:"end"`
	Step  metav1.Duration `json:"step"`
}

// DatadogProvider queries Datadog API v1 or v2.
type DatadogProvider struct {
	// +kubebuilder:validation:Enum=v1;v2
	// +kubebuilder:default=v2
	APIVersion string           `json:"apiVersion,omitempty"`
	Interval   *metav1.Duration `json:"interval,omitempty"`
	Query      string           `json:"query,omitempty"`
	// Queries is the v2 formula-and-functions payload.
	// +optional
	Queries map[string]string `json:"queries,omitempty"`
	// Formula combines Queries.
	// +optional
	Formula string `json:"formula,omitempty"`
}

// NewRelicProvider queries NRDB via NRQL.
type NewRelicProvider struct {
	Profile string `json:"profile,omitempty"`
	Query   string `json:"query"`
}

// WavefrontProvider queries Wavefront.
type WavefrontProvider struct {
	Address string `json:"address"`
	Query   string `json:"query"`
}

// CloudWatchProvider queries CloudWatch via GetMetricData.
type CloudWatchProvider struct {
	Interval          *metav1.Duration         `json:"interval,omitempty"`
	MetricDataQueries []CloudWatchMetricQuery  `json:"metricDataQueries"`
}

// CloudWatchMetricQuery is a single entry in GetMetricData's MetricDataQueries.
type CloudWatchMetricQuery struct {
	ID         string                         `json:"id"`
	Expression string                         `json:"expression,omitempty"`
	Label      string                         `json:"label,omitempty"`
	MetricStat *CloudWatchMetricStat          `json:"metricStat,omitempty"`
	Period     *int32                         `json:"period,omitempty"`
	ReturnData *bool                          `json:"returnData,omitempty"`
}

// CloudWatchMetricStat is MetricStat from the CloudWatch API.
type CloudWatchMetricStat struct {
	Metric CloudWatchMetric `json:"metric"`
	Period int32            `json:"period"`
	Stat   string           `json:"stat"`
	Unit   string           `json:"unit,omitempty"`
}

// CloudWatchMetric identifies a metric.
type CloudWatchMetric struct {
	Namespace  string                     `json:"namespace"`
	MetricName string                     `json:"metricName"`
	Dimensions []CloudWatchMetricDimension `json:"dimensions,omitempty"`
}

// CloudWatchMetricDimension is a CW metric dimension.
type CloudWatchMetricDimension struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// KayentaProvider delegates to a Spinnaker Kayenta canary analysis service.
type KayentaProvider struct {
	Address               string  `json:"address"`
	Application           string  `json:"application"`
	CanaryConfigName      string  `json:"canaryConfigName"`
	MetricsAccountName    string  `json:"metricsAccountName"`
	ConfigurationAccountName string `json:"configurationAccountName"`
	StorageAccountName    string  `json:"storageAccountName"`
	Threshold             KayentaThreshold `json:"threshold"`
	Scopes                []KayentaScope   `json:"scopes"`
}

// KayentaThreshold is pass/marginal thresholds.
type KayentaThreshold struct {
	Pass     int32 `json:"pass"`
	Marginal int32 `json:"marginal"`
}

// KayentaScope identifies a scope to evaluate.
type KayentaScope struct {
	Name            string              `json:"name"`
	ControlScope    KayentaScopeDetail  `json:"controlScope"`
	ExperimentScope KayentaScopeDetail  `json:"experimentScope"`
}

// KayentaScopeDetail is one scope's parameters.
type KayentaScopeDetail struct {
	Scope  string `json:"scope"`
	Region string `json:"region"`
	Step   int32  `json:"step"`
	Start  string `json:"start"`
	End    string `json:"end"`
}

// WebProvider issues HTTP requests and extracts a value by jsonPath.
type WebProvider struct {
	URL string `json:"url"`
	// +kubebuilder:validation:Enum=GET;POST;PUT
	// +kubebuilder:default=GET
	Method       string          `json:"method,omitempty"`
	Headers      []corev1.EnvVar `json:"headers,omitempty"`
	Body         string          `json:"body,omitempty"`
	JSONPath     string          `json:"jsonPath,omitempty"`
	Timeout      *metav1.Duration `json:"timeout,omitempty"`
	InsecureSkipTLSVerify bool   `json:"insecureSkipTLSVerify,omitempty"`
}

// JobProvider runs a Kubernetes Job and uses its exit status as the signal.
type JobProvider struct {
	// Metadata is merged into the spawned Job's metadata.
	// +optional
	Metadata *metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec mirrors batchv1.JobSpec but accepted as the raw PodTemplate + common fields.
	Spec JobSpec `json:"spec"`
}

// JobSpec is the subset of batch/v1 JobSpec the controller honors.
type JobSpec struct {
	BackoffLimit *int32                 `json:"backoffLimit,omitempty"`
	Template     corev1.PodTemplateSpec `json:"template"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=analysisruns,shortName=ar

// AnalysisRun is a running instance of an AnalysisTemplate(s).
type AnalysisRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AnalysisRunSpec   `json:"spec"`
	Status            AnalysisRunStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AnalysisRunList is a list of AnalysisRuns.
type AnalysisRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AnalysisRun `json:"items"`
}

// AnalysisRunSpec is the materialized set of metrics to evaluate.
type AnalysisRunSpec struct {
	Metrics []Metric       `json:"metrics"`
	Args    []AnalysisArg  `json:"args,omitempty"`
	DryRun  []DryRunMetric `json:"dryRun,omitempty"`

	// Terminate, when set to true, halts the run.
	// +optional
	Terminate bool `json:"terminate,omitempty"`
}

// AnalysisRunStatus is the observed state.
type AnalysisRunStatus struct {
	// +kubebuilder:validation:Enum=Pending;Running;Successful;Failed;Error;Inconclusive
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`

	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// +optional
	MetricResults []MetricResult `json:"metricResults,omitempty"`
}

// MetricResult is the outcome of one metric in an AnalysisRun.
type MetricResult struct {
	Name string `json:"name"`

	// +kubebuilder:validation:Enum=Pending;Running;Successful;Failed;Error;Inconclusive
	Phase string `json:"phase"`

	// Measurements are the individual samples.
	// +optional
	Measurements []Measurement `json:"measurements,omitempty"`

	// +optional
	Count int32 `json:"count,omitempty"`
	// +optional
	Successful int32 `json:"successful,omitempty"`
	// +optional
	Failed int32 `json:"failed,omitempty"`
	// +optional
	Inconclusive int32 `json:"inconclusive,omitempty"`
	// +optional
	Error int32 `json:"error,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`
}

// Measurement is one metric sample.
type Measurement struct {
	StartedAt   metav1.Time `json:"startedAt"`
	FinishedAt  metav1.Time `json:"finishedAt"`
	// +kubebuilder:validation:Enum=Successful;Failed;Error;Inconclusive
	Phase       string      `json:"phase"`
	Value       string      `json:"value,omitempty"`
	Message     string      `json:"message,omitempty"`
}
