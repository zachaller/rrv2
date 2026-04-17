/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package providers implements the metric-source interface used by the
// AnalysisRun controller. This file is the Prometheus implementation: an
// instant query against /api/v1/query, returning a single float (for scalar
// or single-sample vector results) or an error.
//
// The client is intentionally small — no dep on the official Prometheus Go
// client — because the controller only needs the query surface.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// Prometheus is the Prometheus metric provider.
type Prometheus struct {
	// HTTPClient is overridable for tests. Nil means http.DefaultClient.
	HTTPClient *http.Client
}

// NewPrometheus constructs a Prometheus provider with sensible timeouts.
func NewPrometheus() *Prometheus {
	return &Prometheus{HTTPClient: &http.Client{Timeout: 30 * time.Second}}
}

// Query executes an instant Prometheus query and returns the resulting
// scalar, single-sample vector value, or the unwrapped slice for a
// multi-sample result. Errors from the transport, non-200 responses, or a
// "status": "error" body are all surfaced to the caller.
func (p *Prometheus) Query(ctx context.Context, spec *rolloutsv1alpha1.PrometheusProvider) (any, error) {
	if spec == nil {
		return nil, fmt.Errorf("prometheus: nil provider spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("prometheus: address is required")
	}
	if spec.Query == "" {
		return nil, fmt.Errorf("prometheus: query is required")
	}

	endpoint, err := buildQueryURL(spec.Address, spec.Query)
	if err != nil {
		return nil, err
	}

	reqCtx := ctx
	if spec.Timeout != nil && spec.Timeout.Duration > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, spec.Timeout.Duration)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("prometheus: build request: %w", err)
	}
	for _, h := range spec.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	req.Header.Set("Accept", "application/json")

	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus: query: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("prometheus: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus: status %d: %s", resp.StatusCode, truncateBody(body))
	}
	return parseQueryResponse(body)
}

// buildQueryURL assembles the instant-query URL, handling both bare host and
// trailing-path addresses.
func buildQueryURL(address, query string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("prometheus: parse address %q: %w", address, err)
	}
	if u.Scheme == "" {
		u, err = url.Parse("http://" + address)
		if err != nil {
			return "", fmt.Errorf("prometheus: parse address %q: %w", address, err)
		}
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/v1/query"
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// parseQueryResponse decodes a Prometheus instant-query response into an
// analysis-friendly shape: a single float for scalar or a 1-sample vector,
// a []float64 for multi-sample vectors, or an error.
func parseQueryResponse(body []byte) (any, error) {
	var envelope struct {
		Status string          `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("prometheus: decode envelope: %w", err)
	}
	if envelope.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", envelope.Error)
	}

	var data struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return nil, fmt.Errorf("prometheus: decode data: %w", err)
	}

	switch data.ResultType {
	case "scalar":
		var pair [2]any
		if err := json.Unmarshal(data.Result, &pair); err != nil {
			return nil, fmt.Errorf("prometheus: decode scalar: %w", err)
		}
		return scalarValue(pair)
	case "vector":
		var samples []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(data.Result, &samples); err != nil {
			return nil, fmt.Errorf("prometheus: decode vector: %w", err)
		}
		if len(samples) == 0 {
			return nil, fmt.Errorf("prometheus: query returned no samples")
		}
		if len(samples) == 1 {
			return scalarValue(samples[0].Value)
		}
		out := make([]float64, len(samples))
		for i, s := range samples {
			v, err := scalarValue(s.Value)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	default:
		return nil, fmt.Errorf("prometheus: unsupported resultType %q", data.ResultType)
	}
}

// scalarValue extracts a float from Prometheus' [timestamp, "value"] pair.
func scalarValue(pair [2]any) (float64, error) {
	raw, ok := pair[1].(string)
	if !ok {
		return 0, fmt.Errorf("prometheus: expected string value, got %T", pair[1])
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("prometheus: parse value %q: %w", raw, err)
	}
	return f, nil
}

func truncateBody(body []byte) string {
	const max = 200
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max]) + "…"
}
