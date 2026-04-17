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

// TestEvaluate_ParseErrors documents the error paths.
func TestEvaluate_ParseErrors(t *testing.T) {
	cases := []string{
		"result > ",   // trailing operator
		"&& result",    // leading connective
		"result[]",     // empty index
		"result[abc]",  // non-numeric index
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			got, err := Evaluate(expr, 1.0)
			if err == nil {
				t.Errorf("expected parse error for %q, got nil (phase=%v)", expr, got)
			}
			if got != ExprError {
				t.Errorf("phase: got %v, want ExprError", got)
			}
		})
	}
}
