/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package analysis reconciles AnalysisRun CRs. Each reconcile sweep samples
// every metric whose last measurement has aged past its Interval, evaluates
// the sample against SuccessCondition / FailureCondition, and aggregates a
// terminal Phase when every metric has reached a conclusion.
//
// The Prometheus provider is implemented; the other providers live under
// pkg/controller/analysis/providers/ and will slot into the Dispatch switch
// as they land.
package analysis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
	"github.com/zachaller/rrv2/pkg/controller/analysis/providers"
)

// Reconciler reconciles AnalysisRuns.
type Reconciler struct {
	Client   ctrlclient.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// Now is injectable for tests. Defaults to time.Now.
	Now func() time.Time

	// Prometheus is injectable for tests. Defaults to a live HTTP client.
	Prometheus PrometheusQuerier
}

// PrometheusQuerier is the method set the AnalysisRun controller uses on a
// Prometheus provider. Defined here (not in providers/) so tests can
// substitute fakes without importing the real provider package.
type PrometheusQuerier interface {
	Query(ctx context.Context, spec *rolloutsv1alpha1.PrometheusProvider) (any, error)
}

// SetupWithManager wires the reconciler into a Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Scheme == nil {
		r.Scheme = mgr.GetScheme()
	}
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("analysis-controller")
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Prometheus == nil {
		r.Prometheus = providers.NewPrometheus()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&rolloutsv1alpha1.AnalysisRun{}).
		Complete(r)
}

// Reconcile advances one AnalysisRun. It is designed to be idempotent and
// re-entrant: each sample is appended to the run's MetricResults with a
// timestamp and the terminal Phase is derived purely from those appended
// facts.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	run := &rolloutsv1alpha1.AnalysisRun{}
	if err := r.Client.Get(ctx, req.NamespacedName, run); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	if run.Spec.Terminate {
		return r.terminate(ctx, run)
	}
	if isTerminalPhase(run.Status.Phase) {
		return reconcile.Result{}, nil
	}

	// First entry: stamp StartedAt and move Pending -> Running.
	patched := run.DeepCopy()
	if patched.Status.Phase == "" || patched.Status.Phase == "Pending" {
		now := metav1.NewTime(r.Now())
		patched.Status.Phase = "Running"
		if patched.Status.StartedAt == nil {
			patched.Status.StartedAt = &now
		}
	}

	// Ensure every Metric has a MetricResult entry to accumulate into.
	patched.Status.MetricResults = ensureMetricResults(patched.Spec.Metrics, patched.Status.MetricResults)

	// Sample each metric that is due.
	nextSampleDelay := 24 * time.Hour
	for i, m := range patched.Spec.Metrics {
		mr := &patched.Status.MetricResults[i]
		if isTerminalPhase(mr.Phase) {
			continue
		}
		due, until := isMetricDue(r.Now(), m, mr, patched.Status.StartedAt)
		if !due {
			if until < nextSampleDelay {
				nextSampleDelay = until
			}
			continue
		}
		r.sampleMetric(ctx, patched, i, m, mr)
		// After a fresh sample, schedule the next one at m.Interval (or 10s default).
		interval := 10 * time.Second
		if m.Interval != nil && m.Interval.Duration > 0 {
			interval = m.Interval.Duration
		}
		if interval < nextSampleDelay {
			nextSampleDelay = interval
		}
	}

	// Aggregate the run's overall phase from per-metric phases. DryRun metrics
	// are visible in results but can never fail the run.
	dryRunSet := map[string]struct{}{}
	for _, d := range patched.Spec.DryRun {
		dryRunSet[d.MetricName] = struct{}{}
	}
	patched.Status.Phase = aggregatePhase(patched.Status.MetricResults, dryRunSet)

	if isTerminalPhase(patched.Status.Phase) {
		now := metav1.NewTime(r.Now())
		patched.Status.CompletedAt = &now
	}

	if err := r.Client.Status().Update(ctx, patched); err != nil {
		return reconcile.Result{}, err
	}

	if isTerminalPhase(patched.Status.Phase) {
		return reconcile.Result{}, nil
	}
	if nextSampleDelay < time.Second {
		nextSampleDelay = time.Second
	}
	return reconcile.Result{RequeueAfter: nextSampleDelay}, nil
}

// terminate is the Spec.Terminate=true path — mark the run Failed with a clear
// message, preserving any measurements already collected.
func (r *Reconciler) terminate(ctx context.Context, run *rolloutsv1alpha1.AnalysisRun) (reconcile.Result, error) {
	if isTerminalPhase(run.Status.Phase) {
		return reconcile.Result{}, nil
	}
	patched := run.DeepCopy()
	now := metav1.NewTime(r.Now())
	patched.Status.Phase = "Failed"
	patched.Status.Message = "terminated via spec.terminate"
	patched.Status.CompletedAt = &now
	return reconcile.Result{}, r.Client.Status().Update(ctx, patched)
}

// ensureMetricResults keeps status.MetricResults in 1-1 correspondence with
// spec.Metrics, initializing new entries to Pending.
func ensureMetricResults(metrics []rolloutsv1alpha1.Metric, existing []rolloutsv1alpha1.MetricResult) []rolloutsv1alpha1.MetricResult {
	byName := map[string]rolloutsv1alpha1.MetricResult{}
	for _, e := range existing {
		byName[e.Name] = e
	}
	out := make([]rolloutsv1alpha1.MetricResult, len(metrics))
	for i, m := range metrics {
		if e, ok := byName[m.Name]; ok {
			out[i] = e
			continue
		}
		out[i] = rolloutsv1alpha1.MetricResult{Name: m.Name, Phase: "Pending"}
	}
	return out
}

// isMetricDue returns whether this metric should be sampled now, and if not,
// how long to wait before checking again.
func isMetricDue(now time.Time, m rolloutsv1alpha1.Metric, mr *rolloutsv1alpha1.MetricResult, runStart *metav1.Time) (bool, time.Duration) {
	// InitialDelay from run start.
	if m.InitialDelay != nil && runStart != nil {
		earliest := runStart.Add(m.InitialDelay.Duration)
		if now.Before(earliest) {
			return false, earliest.Sub(now)
		}
	}
	if len(mr.Measurements) == 0 {
		return true, 0
	}
	interval := 10 * time.Second
	if m.Interval != nil && m.Interval.Duration > 0 {
		interval = m.Interval.Duration
	}
	last := mr.Measurements[len(mr.Measurements)-1].FinishedAt.Time
	nextAt := last.Add(interval)
	if now.Before(nextAt) {
		return false, nextAt.Sub(now)
	}
	return true, 0
}

// sampleMetric dispatches to the right provider, evaluates the result against
// Success/Failure conditions, and appends a Measurement to mr.
func (r *Reconciler) sampleMetric(ctx context.Context, run *rolloutsv1alpha1.AnalysisRun, idx int, m rolloutsv1alpha1.Metric, mr *rolloutsv1alpha1.MetricResult) {
	started := metav1.NewTime(r.Now())
	value, err := r.dispatch(ctx, m.Provider)
	finished := metav1.NewTime(r.Now())

	measurement := rolloutsv1alpha1.Measurement{StartedAt: started, FinishedAt: finished}
	if err != nil {
		measurement.Phase = "Error"
		measurement.Message = err.Error()
		mr.Error++
		mr.Measurements = append(mr.Measurements, measurement)
		mr.Count++
		mr.Phase = derivePerMetricPhase(m, mr)
		return
	}
	measurement.Value = formatValue(value)

	successMatch, _ := Evaluate(m.SuccessCondition, value)
	failureMatch, _ := Evaluate(m.FailureCondition, value)

	switch {
	case successMatch == ExprMatched:
		measurement.Phase = "Successful"
		mr.Successful++
	case failureMatch == ExprMatched:
		measurement.Phase = "Failed"
		mr.Failed++
	case successMatch == ExprError && failureMatch == ExprError:
		measurement.Phase = "Inconclusive"
		mr.Inconclusive++
		measurement.Message = "both SuccessCondition and FailureCondition failed to evaluate"
	default:
		// Neither matched and at least one evaluated cleanly — the sample is
		// inconclusive for this tick, but we don't increment Failed; it just
		// doesn't advance either counter until a conclusive sample arrives.
		measurement.Phase = "Inconclusive"
		mr.Inconclusive++
	}
	mr.Measurements = append(mr.Measurements, measurement)
	mr.Count++
	mr.Phase = derivePerMetricPhase(m, mr)
	_ = run // lint
	_ = idx
}

// dispatch routes to the registered provider by spec.Type. Unknown providers
// return an error rather than panic — the metric reports Error and the run
// fails only if the metric isn't in DryRun.
func (r *Reconciler) dispatch(ctx context.Context, p rolloutsv1alpha1.MetricProvider) (any, error) {
	switch p.Type {
	case "prometheus":
		return r.Prometheus.Query(ctx, p.Prometheus)
	default:
		return nil, fmt.Errorf("analysis: provider %q not implemented in this build", p.Type)
	}
}

// derivePerMetricPhase folds the counts in MetricResult into a single phase
// per the configured FailureLimit / ConsecutiveErrorLimit / Count budget.
func derivePerMetricPhase(m rolloutsv1alpha1.Metric, mr *rolloutsv1alpha1.MetricResult) string {
	failureLimit := int32(0)
	if m.FailureLimit != nil {
		if v, err := intstr.GetScaledValueFromIntOrPercent(m.FailureLimit, 100, true); err == nil {
			failureLimit = int32(v)
		}
	}
	errorLimit := int32(4)
	if m.ConsecutiveErrorLimit != nil {
		if v, err := intstr.GetScaledValueFromIntOrPercent(m.ConsecutiveErrorLimit, 100, true); err == nil {
			errorLimit = int32(v)
		}
	}

	// Consecutive errors from the tail.
	consecErrors := int32(0)
	for i := len(mr.Measurements) - 1; i >= 0; i-- {
		if mr.Measurements[i].Phase == "Error" {
			consecErrors++
			continue
		}
		break
	}
	if consecErrors > errorLimit {
		return "Error"
	}

	if mr.Failed > failureLimit {
		return "Failed"
	}

	// Count budget — if Count is set and we've taken that many conclusive
	// samples, finalize based on whether any failed.
	if m.Count != nil {
		if v, err := intstr.GetScaledValueFromIntOrPercent(m.Count, 100, true); err == nil {
			target := int32(v)
			conclusive := mr.Successful + mr.Failed
			if conclusive >= target {
				if mr.Failed > 0 {
					return "Failed"
				}
				return "Successful"
			}
		}
	}

	return "Running"
}

// aggregatePhase folds per-metric phases into the run's overall phase. DryRun
// metrics can report Failed/Error without failing the run.
func aggregatePhase(results []rolloutsv1alpha1.MetricResult, dryRun map[string]struct{}) string {
	anyRunning := false
	sawInconclusive := false
	for _, mr := range results {
		_, isDryRun := dryRun[mr.Name]
		switch mr.Phase {
		case "Failed":
			if !isDryRun {
				return "Failed"
			}
		case "Error":
			if !isDryRun {
				return "Error"
			}
		case "Inconclusive":
			sawInconclusive = true
		case "Running", "Pending", "":
			anyRunning = true
		}
	}
	if anyRunning {
		return "Running"
	}
	if sawInconclusive {
		return "Inconclusive"
	}
	return "Successful"
}

func isTerminalPhase(phase string) bool {
	switch phase {
	case "Successful", "Failed", "Error", "Inconclusive":
		return true
	}
	return false
}

// formatValue renders a sampled value for display in status.
func formatValue(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []float64:
		return fmt.Sprintf("%v", x)
	case string:
		return x
	}
	return fmt.Sprintf("%v", v)
}
