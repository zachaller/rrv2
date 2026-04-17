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
// The evaluator is deliberately scoped: it supports exactly the expression
// shape Argo Rollouts users already write — a single identifier (result or
// result[N]) compared against a numeric literal, optionally combined with
// && / || boolean connectives. That's enough to express
//     result[0] >= 0.99 && result[1] < 0.5
// without pulling in a dependency on a general-purpose expression language.
package analysis

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ExprResult is the outcome of evaluating one expression against a sample.
type ExprResult int

const (
	// ExprMatched means the boolean expression evaluated to true.
	ExprMatched ExprResult = iota
	// ExprNotMatched means the expression evaluated to false.
	ExprNotMatched
	// ExprError means the expression could not be evaluated (parse, type, or
	// out-of-range lookup).
	ExprError
)

// Evaluate runs `expr` against `result`. `result` is either a single numeric
// value (string or float) or a slice of them; the expression references it
// as `result` or `result[i]`.
//
// Empty expression returns ExprNotMatched — the caller is responsible for
// defaulting to "no gate" when both SuccessCondition and FailureCondition
// are empty.
func Evaluate(expr string, result any) (ExprResult, error) {
	if strings.TrimSpace(expr) == "" {
		return ExprNotMatched, nil
	}
	p := &parser{src: expr}
	v, err := p.parseOr()
	if err != nil {
		return ExprError, err
	}
	if p.pos < len(p.src) {
		p.skipSpace()
		if p.pos < len(p.src) {
			return ExprError, fmt.Errorf("unexpected trailing input at pos %d: %q", p.pos, p.src[p.pos:])
		}
	}
	b, err := v.boolAt(result)
	if err != nil {
		return ExprError, err
	}
	if b {
		return ExprMatched, nil
	}
	return ExprNotMatched, nil
}

// expr is a single parsed node. Implementations fall into two groups:
// booleans (whose boolAt returns a logical value) and numerics (whose numAt
// returns a float). Only the outermost node is asked for a bool.
type expr interface {
	boolAt(result any) (bool, error)
}

type numExpr interface {
	numAt(result any) (float64, error)
}

// ---- parser ----
//
// Tiny recursive-descent parser. Grammar:
//     or      := and ('||' and)*
//     and     := cmp ('&&' cmp)*
//     cmp     := num (op num)?   where op in {<,<=,>,>=,==,!=}
//     num     := lit | ref | '(' or ')'
//     ref     := 'result' ('[' int ']')?
//     lit     := float

type parser struct {
	src string
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && unicode.IsSpace(rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *parser) consume(s string) bool {
	p.skipSpace()
	if strings.HasPrefix(p.src[p.pos:], s) {
		p.pos += len(s)
		return true
	}
	return false
}

func (p *parser) parseOr() (expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.consume("||") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &boolOp{op: "||", l: left, r: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (expr, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for p.consume("&&") {
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = &boolOp{op: "&&", l: left, r: right}
	}
	return left, nil
}

func (p *parser) parseCmp() (expr, error) {
	left, err := p.parseNum()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	for _, op := range []string{"<=", ">=", "==", "!=", "<", ">"} {
		if p.consume(op) {
			right, err := p.parseNum()
			if err != nil {
				return nil, err
			}
			return &cmpOp{op: op, l: left, r: right}, nil
		}
	}
	// Bare numeric is a truthiness test (non-zero == true).
	return &truthy{n: left}, nil
}

func (p *parser) parseNum() (numExpr, error) {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return nil, errors.New("unexpected end of input")
	}
	if p.src[p.pos] == '(' {
		p.pos++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.pos >= len(p.src) || p.src[p.pos] != ')' {
			return nil, errors.New("expected ')'")
		}
		p.pos++
		return &parenNum{inner: inner}, nil
	}
	if strings.HasPrefix(p.src[p.pos:], "result") {
		p.pos += len("result")
		if p.consume("[") {
			start := p.pos
			for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
				p.pos++
			}
			if start == p.pos {
				return nil, errors.New("expected integer index")
			}
			idx, _ := strconv.Atoi(p.src[start:p.pos])
			if !p.consume("]") {
				return nil, errors.New("expected ']'")
			}
			return &ref{indexed: true, index: idx}, nil
		}
		return &ref{}, nil
	}
	// Numeric literal.
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E' {
			p.pos++
			continue
		}
		break
	}
	if start == p.pos {
		return nil, fmt.Errorf("unexpected token at pos %d: %q", p.pos, p.src[p.pos:])
	}
	v, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		return nil, fmt.Errorf("parse number: %w", err)
	}
	return &lit{v: v}, nil
}

// ---- ast nodes ----

type lit struct{ v float64 }

func (l *lit) numAt(any) (float64, error) { return l.v, nil }

type ref struct {
	indexed bool
	index   int
}

func (r *ref) numAt(result any) (float64, error) {
	if r.indexed {
		slice, err := toFloatSlice(result)
		if err != nil {
			return 0, fmt.Errorf("result[%d]: %w", r.index, err)
		}
		if r.index < 0 || r.index >= len(slice) {
			return 0, fmt.Errorf("result[%d] out of range (len %d)", r.index, len(slice))
		}
		return slice[r.index], nil
	}
	// Unindexed: if the result is a single value, use it; if it's a slice of
	// length 1, unwrap; else error.
	switch v := result.(type) {
	case []float64:
		if len(v) != 1 {
			return 0, fmt.Errorf("`result` used without index on multi-value result (len %d)", len(v))
		}
		return v[0], nil
	case []string:
		if len(v) != 1 {
			return 0, fmt.Errorf("`result` used without index on multi-value result (len %d)", len(v))
		}
		return strconv.ParseFloat(v[0], 64)
	}
	return toFloat(result)
}

type parenNum struct{ inner expr }

func (p *parenNum) numAt(result any) (float64, error) {
	b, err := p.inner.boolAt(result)
	if err != nil {
		return 0, err
	}
	if b {
		return 1, nil
	}
	return 0, nil
}

type cmpOp struct {
	op   string
	l, r numExpr
}

func (c *cmpOp) boolAt(result any) (bool, error) {
	lv, err := c.l.numAt(result)
	if err != nil {
		return false, err
	}
	rv, err := c.r.numAt(result)
	if err != nil {
		return false, err
	}
	switch c.op {
	case "<":
		return lv < rv, nil
	case "<=":
		return lv <= rv, nil
	case ">":
		return lv > rv, nil
	case ">=":
		return lv >= rv, nil
	case "==":
		return lv == rv, nil
	case "!=":
		return lv != rv, nil
	}
	return false, fmt.Errorf("unknown comparison %q", c.op)
}

type boolOp struct {
	op   string
	l, r expr
}

func (b *boolOp) boolAt(result any) (bool, error) {
	lv, err := b.l.boolAt(result)
	if err != nil {
		return false, err
	}
	switch b.op {
	case "&&":
		if !lv {
			return false, nil
		}
	case "||":
		if lv {
			return true, nil
		}
	}
	return b.r.boolAt(result)
}

type truthy struct{ n numExpr }

func (t *truthy) boolAt(result any) (bool, error) {
	v, err := t.n.numAt(result)
	if err != nil {
		return false, err
	}
	return v != 0, nil
}

// ---- helpers ----

func toFloat(v any) (float64, error) {
	switch v := v.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	}
	return 0, fmt.Errorf("cannot convert %T to float64", v)
}

func toFloatSlice(v any) ([]float64, error) {
	switch v := v.(type) {
	case []float64:
		return v, nil
	case []string:
		out := make([]float64, len(v))
		for i, s := range v {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, err
			}
			out[i] = f
		}
		return out, nil
	case []any:
		out := make([]float64, len(v))
		for i, x := range v {
			f, err := toFloat(x)
			if err != nil {
				return nil, err
			}
			out[i] = f
		}
		return out, nil
	}
	return nil, fmt.Errorf("cannot iterate %T as slice", v)
}
