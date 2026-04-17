/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package analysis

import "testing"

// TestEvaluate_Scalar covers the single-value `result` shape users see when
// a query returns one sample or a Prometheus scalar. The cases exercise all
// six comparison operators plus the truthy branch (bare expression).
func TestEvaluate_Scalar(t *testing.T) {
	tests := []struct {
		name    string
		expr    string
		result  any
		want    ExprResult
		wantErr bool
	}{
		{"greater-than match", "result > 0.5", 0.9, ExprMatched, false},
		{"greater-than miss", "result > 0.5", 0.3, ExprNotMatched, false},
		{"greater-equal match", "result >= 0.5", 0.5, ExprMatched, false},
		{"less-than match", "result < 0.5", 0.1, ExprMatched, false},
		{"less-equal match", "result <= 0.5", 0.5, ExprMatched, false},
		{"equal match", "result == 1", 1.0, ExprMatched, false},
		{"not-equal match", "result != 0", 0.3, ExprMatched, false},
		{"truthy non-zero", "result", 0.1, ExprMatched, false},
		{"truthy zero is not", "result", 0.0, ExprNotMatched, false},
		{"string coerces", "result > 0.5", "0.9", ExprMatched, false},
		{"empty expression", "", 1.0, ExprNotMatched, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evaluate(tc.expr, tc.result)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err: got %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEvaluate_Conjunction tests the && / || connectives and precedence —
// && binds tighter than || so `a || b && c` parses as `a || (b && c)`.
func TestEvaluate_Conjunction(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		result any
		want   ExprResult
	}{
		{"and both true", "result > 0.5 && result < 1", 0.8, ExprMatched},
		{"and left false", "result > 0.5 && result < 1", 0.4, ExprNotMatched},
		{"and right false", "result > 0.5 && result < 1", 1.5, ExprNotMatched},
		{"or left true", "result > 0.5 || result < 0.1", 0.8, ExprMatched},
		{"or right true", "result > 0.5 || result < 0.1", 0.05, ExprMatched},
		{"or both false", "result > 0.5 || result < 0.1", 0.3, ExprNotMatched},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evaluate(tc.expr, tc.result)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEvaluate_IndexedSlice covers the `result[N]` accessor. Out-of-range
// indices surface as ExprError so the sample phase records Inconclusive
// rather than silently succeeding.
func TestEvaluate_IndexedSlice(t *testing.T) {
	result := []float64{0.99, 0.4, 1.0}
	tests := []struct {
		name string
		expr string
		want ExprResult
	}{
		{"first >= 0.95", "result[0] >= 0.95", ExprMatched},
		{"second < 0.5", "result[1] < 0.5", ExprMatched},
		{"both match", "result[0] >= 0.99 && result[1] < 0.5", ExprMatched},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evaluate(tc.expr, result)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}

	// Out-of-range.
	if got, _ := Evaluate("result[5] > 0", result); got != ExprError {
		t.Errorf("out-of-range got %v, want ExprError", got)
	}
}

// TestEvaluate_CompileErrors exercises malformed expressions — anything the
// expr compiler rejects at parse time must surface as ExprError with a
// non-nil error, so the per-metric phase records Inconclusive rather than
// silently succeeding.
func TestEvaluate_CompileErrors(t *testing.T) {
	cases := []string{
		"result >",     // trailing operator
		"&& result",    // leading connective
		"result[",      // unclosed index
		"result ??? 5", // unknown tokens
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			got, err := Evaluate(expr, 1.0)
			if err == nil {
				t.Errorf("expected compile error for %q, got nil (phase=%v)", expr, got)
			}
			if got != ExprError {
				t.Errorf("phase: got %v, want ExprError", got)
			}
		})
	}
}

// TestEvaluate_RuntimeErrors exercises expressions that compile cleanly but
// fail at evaluation time — out-of-range indexing, division by zero, type
// errors. These also surface as ExprError.
func TestEvaluate_RuntimeErrors(t *testing.T) {
	result := []float64{0.99, 0.4}
	cases := []struct {
		name string
		expr string
	}{
		{"index out of range", "result[9] > 0"},
		{"undefined variable", "other > 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evaluate(tc.expr, result)
			if err == nil {
				t.Errorf("expected runtime error, got nil (phase=%v)", got)
			}
			if got != ExprError {
				t.Errorf("phase: got %v, want ExprError", got)
			}
		})
	}
}

// TestEvaluate_ExprIdioms documents the new syntax users gain from the
// switch to github.com/expr-lang/expr. These are not just nice-to-haves —
// they're the reason we moved off the hand-rolled parser.
func TestEvaluate_ExprIdioms(t *testing.T) {
	tests := []struct {
		name   string
		expr   string
		result any
		want   ExprResult
	}{
		{
			"ternary: pick a branch",
			"result >= 0.5 ? result > 0.9 : result > 0",
			0.95, ExprMatched,
		},
		{
			"not operator",
			"!(result < 0.5)",
			0.9, ExprMatched,
		},
		{
			"all(): every element passes",
			"all(result, {# >= 0.9})",
			[]float64{0.91, 0.98, 1.0}, ExprMatched,
		},
		{
			"any(): at least one passes",
			"any(result, {# < 0.1})",
			[]float64{0.5, 0.05, 0.7}, ExprMatched,
		},
		{
			"none(): rejects empty-match",
			"none(result, {# < 0})",
			[]float64{0.1, 0.2, 0.3}, ExprMatched,
		},
		{
			"count(): matches threshold",
			"count(result, {# > 0.5}) >= 2",
			[]float64{0.6, 0.7, 0.3}, ExprMatched,
		},
		{
			"arithmetic: ratio comparison",
			"result[0] / result[1] > 2",
			[]float64{100, 40}, ExprMatched,
		},
		{
			"len() on a slice",
			"len(result) > 2",
			[]float64{1, 2, 3, 4}, ExprMatched,
		},
		{
			"in operator with literal set",
			"result in [200, 204, 206]",
			200, ExprMatched,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Evaluate(tc.expr, tc.result)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
