/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package analysis contains the AnalysisRun controller and its metric
// provider + expression-evaluator subsystems.
//
// The evaluator delegates to github.com/expr-lang/expr, a safe expression
// language with familiar syntax (comparison operators, &&/||, indexing,
// arithmetic, string ops) and no filesystem/network access.
//
// Expressions reference the sampled metric value via the `result` variable.
// Depending on the provider that was queried, `result` is either a numeric
// scalar or a slice; indexing (`result[0]`) works against slices.
package analysis

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// ExprResult is the outcome of evaluating one expression against a sample.
type ExprResult int

const (
	// ExprMatched means the boolean expression evaluated to true.
	ExprMatched ExprResult = iota
	// ExprNotMatched means the expression evaluated to false.
	ExprNotMatched
	// ExprError means the expression could not be compiled or evaluated
	// (syntax, type, or out-of-range lookup).
	ExprError
)

// Evaluate runs `source` against `result`. `result` is a scalar numeric value
// (any Go numeric type or a string that parses as float) or a slice of them
// (e.g. []float64 for a Prometheus multi-sample vector).
//
// Empty expression returns ExprNotMatched — callers are responsible for
// defaulting to "no gate" when both SuccessCondition and FailureCondition
// are empty.
//
// The returned bool is derived from the expression's value:
//
//   - Booleans are used directly.
//   - Numerics are truthy when non-zero.
//   - Everything else is an error.
func Evaluate(source string, result any) (ExprResult, error) {
	if strings.TrimSpace(source) == "" {
		return ExprNotMatched, nil
	}

	env := buildEnv(result)
	program, err := compile(source, env)
	if err != nil {
		return ExprError, fmt.Errorf("compile %q: %w", source, err)
	}

	out, err := vm.Run(program, env)
	if err != nil {
		return ExprError, fmt.Errorf("eval %q: %w", source, err)
	}

	matched, err := truthy(out)
	if err != nil {
		return ExprError, err
	}
	if matched {
		return ExprMatched, nil
	}
	return ExprNotMatched, nil
}

// compile builds an expr program. We pass the env via expr.AsAny() so
// scalar vs slice bindings are unified under a single runtime shape — expr
// handles both indexed access (`result[N]`) and scalar access (`result`)
// over a map[string]any value.
func compile(source string, env map[string]any) (*vm.Program, error) {
	return expr.Compile(source, expr.Env(env), expr.AsAny())
}

// buildEnv normalizes `result` into the single shape expr binds to. Scalars
// are kept as the caller's type; strings are parsed to float64 so numeric
// comparisons work without the user having to cast; slices of strings are
// parsed too so the evaluator can index into them directly.
func buildEnv(result any) map[string]any {
	return map[string]any{
		"result": normalizeResult(result),
	}
}

// normalizeResult converts provider-shaped values into a form expr can
// compare numerically without the caller having to know the wire type.
func normalizeResult(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case float64, float32, int, int32, int64, uint, uint32, uint64, bool:
		return x
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err == nil {
			return f
		}
		return x
	case []float64:
		return x
	case []int:
		out := make([]float64, len(x))
		for i, n := range x {
			out[i] = float64(n)
		}
		return out
	case []string:
		out := make([]float64, 0, len(x))
		ok := true
		for _, s := range x {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				ok = false
				break
			}
			out = append(out, f)
		}
		if ok {
			return out
		}
		// Fallback: hand expr the raw string slice so string comparisons work.
		return x
	case []any:
		return x
	default:
		return x
	}
}

// truthy derives the final boolean. We accept booleans directly (the common
// case — comparison operators always produce bool); numerics are mapped with
// non-zero == true so bare-expression usage (`result` as a truthiness test)
// behaves the way Argo Rollouts users expect.
func truthy(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case float64:
		return x != 0, nil
	case float32:
		return x != 0, nil
	case int:
		return x != 0, nil
	case int32:
		return x != 0, nil
	case int64:
		return x != 0, nil
	case uint:
		return x != 0, nil
	case uint32:
		return x != 0, nil
	case uint64:
		return x != 0, nil
	case string:
		return x != "", nil
	case nil:
		return false, nil
	}
	return false, errors.New("expression must evaluate to bool or number")
}
