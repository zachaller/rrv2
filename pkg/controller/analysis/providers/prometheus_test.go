/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// TestPrometheus_QueryScalar covers the scalar resultType — less common in
// real deployments but used by `scalar(...)` wrappers in PromQL.
func TestPrometheus_QueryScalar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/api/v1/query"; got != want {
			t.Errorf("path: got %q, want %q", got, want)
		}
		if got := r.URL.Query().Get("query"); got != "up" {
			t.Errorf("query: got %q, want %q", got, "up")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[1714000000,"0.99"]}}`))
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	val, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{
		Address: srv.URL,
		Query:   "up",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	f, ok := val.(float64)
	if !ok {
		t.Fatalf("want float64, got %T", val)
	}
	if f != 0.99 {
		t.Errorf("got %v, want 0.99", f)
	}
}

// TestPrometheus_QueryVectorSingle verifies the single-sample vector is
// unwrapped to a scalar float — the common shape operators compare against.
func TestPrometheus_QueryVectorSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"job":"api"},"value":[1714000000,"200"]}]}}`))
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	val, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{
		Address: srv.URL,
		Query:   "sum(rate(http_requests_total{status=~\"2..\"}[1m]))",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if f, _ := val.(float64); f != 200 {
		t.Errorf("got %v, want 200", val)
	}
}

// TestPrometheus_QueryVectorMulti verifies multi-sample vectors surface as a
// []float64 so the evaluator can index into them.
func TestPrometheus_QueryVectorMulti(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"pod":"a"},"value":[1,"0.98"]},
			{"metric":{"pod":"b"},"value":[1,"0.42"]}
		]}}`))
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	val, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{
		Address: srv.URL,
		Query:   "up",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	slice, ok := val.([]float64)
	if !ok {
		t.Fatalf("want []float64, got %T", val)
	}
	if len(slice) != 2 || slice[0] != 0.98 || slice[1] != 0.42 {
		t.Errorf("got %v, want [0.98 0.42]", slice)
	}
}

// TestPrometheus_HonorsHeaders confirms that user-supplied Headers (the auth
// carrier in most deployments) reach the server.
func TestPrometheus_HonorsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer xyz" {
			t.Errorf("Authorization header: got %q, want %q", got, "Bearer xyz")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"scalar","result":[1,"1"]}}`))
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	_, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{
		Address: srv.URL,
		Query:   "up",
		Headers: []corev1.EnvVar{{Name: "Authorization", Value: "Bearer xyz"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
}

// TestPrometheus_StatusError surfaces a Prometheus-reported error cleanly.
func TestPrometheus_StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","error":"invalid parameter query"}`))
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	_, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{Address: srv.URL, Query: "bad"})
	if err == nil || !strings.Contains(err.Error(), "invalid parameter query") {
		t.Errorf("error did not propagate: %v", err)
	}
}

// TestPrometheus_HTTP500 surfaces transport-level failures.
func TestPrometheus_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	_, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{Address: srv.URL, Query: "up"})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected status 500 error, got %v", err)
	}
}

// TestPrometheus_HonorsTimeout verifies the per-query timeout cancels
// hanging requests rather than blocking the reconciler.
func TestPrometheus_HonorsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &Prometheus{HTTPClient: srv.Client()}
	start := time.Now()
	_, err := p.Query(context.Background(), &rolloutsv1alpha1.PrometheusProvider{
		Address: srv.URL,
		Query:   "up",
		Timeout: &metav1.Duration{Duration: 100 * time.Millisecond},
	})
	if err == nil {
		t.Errorf("expected timeout error, got nil")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("timeout did not kick in quickly: %v", time.Since(start))
	}
}

