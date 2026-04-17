/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package analysis

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// fakeProm is a deterministic PrometheusQuerier for tests. It serves values
// from a slice, looping when exhausted, and surfaces an explicit error when
// err is set.
type fakeProm struct {
	values []any
	calls  int
	err    error
}

func (f *fakeProm) Query(ctx context.Context, spec *rolloutsv1alpha1.PrometheusProvider) (any, error) {
	if f.err != nil {
		return nil, f.err
	}
	v := f.values[f.calls%len(f.values)]
	f.calls++
	return v, nil
}

// newTestScheme builds a scheme with our CRDs registered.
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := rolloutsv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

// makeRun returns an AnalysisRun with a single Prometheus metric.
func makeRun(name string, metric rolloutsv1alpha1.Metric, dryRun ...string) *rolloutsv1alpha1.AnalysisRun {
	run := &rolloutsv1alpha1.AnalysisRun{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: rolloutsv1alpha1.AnalysisRunSpec{
			Metrics: []rolloutsv1alpha1.Metric{metric},
		},
	}
	for _, d := range dryRun {
		run.Spec.DryRun = append(run.Spec.DryRun, rolloutsv1alpha1.DryRunMetric{MetricName: d})
	}
	return run
}

func mustGetRun(t *testing.T, c ctrlclient.Client, name string) *rolloutsv1alpha1.AnalysisRun {
	t.Helper()
	run := &rolloutsv1alpha1.AnalysisRun{}
	if err := c.Get(context.Background(), ctrlclient.ObjectKey{Namespace: "default", Name: name}, run); err != nil {
		t.Fatalf("get run %s: %v", name, err)
	}
	return run
}

// TestReconcile_SuccessfulPath walks an AnalysisRun from Pending through
// Running to Successful: success condition matches on the first sample, with
// Count=1 the metric finalizes immediately.
func TestReconcile_SuccessfulPath(t *testing.T) {
	count := intstr.FromInt32(1)
	run := makeRun("happy", rolloutsv1alpha1.Metric{
		Name:             "req-success",
		SuccessCondition: "result >= 0.99",
		Count:            &count,
		Provider: rolloutsv1alpha1.MetricProvider{
			Type:       "prometheus",
			Prometheus: &rolloutsv1alpha1.PrometheusProvider{Address: "http://prom", Query: "up"},
		},
	})

	scheme := newTestScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run).Build()
	now := time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	r := &Reconciler{Client: c, Scheme: scheme, Now: func() time.Time { return now }, Prometheus: &fakeProm{values: []any{0.99}}}

	// First reconcile: stamps StartedAt, samples once, reaches Successful.
	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: ctrlclient.ObjectKey{Namespace: "default", Name: "happy"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := mustGetRun(t, c, "happy")
	if got.Status.Phase != "Successful" {
		t.Fatalf("phase: got %q, want Successful", got.Status.Phase)
	}
	if got.Status.StartedAt == nil || got.Status.CompletedAt == nil {
		t.Errorf("StartedAt/CompletedAt must be populated; got %v / %v", got.Status.StartedAt, got.Status.CompletedAt)
	}
	if len(got.Status.MetricResults) != 1 || got.Status.MetricResults[0].Successful != 1 {
		t.Errorf("metric result: %+v", got.Status.MetricResults)
	}
}

// TestReconcile_FailurePath: the metric fails on the first sample with
// FailureLimit=0, so the metric phase becomes Failed and the run aggregates
// to Failed.
func TestReconcile_FailurePath(t *testing.T) {
	count := intstr.FromInt32(1)
	failLimit := intstr.FromInt32(0)
	run := makeRun("sad", rolloutsv1alpha1.Metric{
		Name:             "err-rate",
		FailureCondition: "result > 0.01",
		Count:            &count,
		FailureLimit:     &failLimit,
		Provider: rolloutsv1alpha1.MetricProvider{
			Type:       "prometheus",
			Prometheus: &rolloutsv1alpha1.PrometheusProvider{Address: "http://prom", Query: "errors"},
		},
	})

	scheme := newTestScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run).Build()
	r := &Reconciler{Client: c, Scheme: scheme, Now: time.Now, Prometheus: &fakeProm{values: []any{0.5}}}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: ctrlclient.ObjectKey{Namespace: "default", Name: "sad"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := mustGetRun(t, c, "sad")
	if got.Status.Phase != "Failed" {
		t.Errorf("phase: got %q, want Failed", got.Status.Phase)
	}
}

// TestReconcile_ProviderError ticks a metric whose provider returns an error.
// With ConsecutiveErrorLimit exceeded, the metric moves to Error and so does
// the run.
func TestReconcile_ProviderError(t *testing.T) {
	errLimit := intstr.FromInt32(0)
	run := makeRun("err", rolloutsv1alpha1.Metric{
		Name:                  "latency",
		SuccessCondition:      "result < 1",
		ConsecutiveErrorLimit: &errLimit,
		Provider: rolloutsv1alpha1.MetricProvider{
			Type:       "prometheus",
			Prometheus: &rolloutsv1alpha1.PrometheusProvider{Address: "http://prom", Query: "latency"},
		},
	})

	scheme := newTestScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run).Build()
	r := &Reconciler{Client: c, Scheme: scheme, Now: time.Now, Prometheus: &fakeProm{err: fmt.Errorf("boom")}}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: ctrlclient.ObjectKey{Namespace: "default", Name: "err"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := mustGetRun(t, c, "err")
	if got.Status.Phase != "Error" {
		t.Errorf("phase: got %q, want Error", got.Status.Phase)
	}
}

// TestReconcile_DryRunDoesNotFail: a failing metric listed in spec.dryRun
// must not fail the run.
func TestReconcile_DryRunDoesNotFail(t *testing.T) {
	count := intstr.FromInt32(1)
	failLimit := intstr.FromInt32(0)
	run := makeRun("dry", rolloutsv1alpha1.Metric{
		Name:             "noisy",
		FailureCondition: "result > 0",
		Count:            &count,
		FailureLimit:     &failLimit,
		Provider: rolloutsv1alpha1.MetricProvider{
			Type:       "prometheus",
			Prometheus: &rolloutsv1alpha1.PrometheusProvider{Address: "http://prom", Query: "noisy"},
		},
	}, "noisy")

	scheme := newTestScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run).Build()
	r := &Reconciler{Client: c, Scheme: scheme, Now: time.Now, Prometheus: &fakeProm{values: []any{0.5}}}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: ctrlclient.ObjectKey{Namespace: "default", Name: "dry"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := mustGetRun(t, c, "dry")
	if got.Status.Phase != "Successful" {
		t.Errorf("phase: got %q, want Successful (dryRun metric must not fail the run)", got.Status.Phase)
	}
}

// TestReconcile_Terminate: spec.terminate=true short-circuits the loop.
func TestReconcile_Terminate(t *testing.T) {
	run := makeRun("stop", rolloutsv1alpha1.Metric{
		Name:             "x",
		SuccessCondition: "result > 0",
		Provider: rolloutsv1alpha1.MetricProvider{
			Type:       "prometheus",
			Prometheus: &rolloutsv1alpha1.PrometheusProvider{Address: "http://prom", Query: "x"},
		},
	})
	run.Spec.Terminate = true

	scheme := newTestScheme(t)
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(run).WithStatusSubresource(run).Build()
	r := &Reconciler{Client: c, Scheme: scheme, Now: time.Now, Prometheus: &fakeProm{}}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: ctrlclient.ObjectKey{Namespace: "default", Name: "stop"}}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := mustGetRun(t, c, "stop")
	if got.Status.Phase != "Failed" {
		t.Errorf("phase: got %q, want Failed", got.Status.Phase)
	}
}
