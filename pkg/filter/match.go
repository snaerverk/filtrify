package filter

import (
	"cmp"
	"slices"
	"strings"
	"time"

	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
)

// Field is a filterable field of T for in-memory matching.
type Field[T any] struct {
	Kind Kind
	// Get returns the field's values: a list field matches a comparison if
	// any element does, and a field without values (e.g. no expiry) matches
	// none. Use NOT(...) for "no element matches".
	Get func(T) []any
	// List marks a field that may hold several values; it can't be sorted
	// by.
	List bool
}

// Matcher compiles expr into a predicate over T. A nil expr matches all.
// has() is not supported, as T has no relations.
func Matcher[T any](fields map[string]Field[T], expr *astv1.Expr) (func(T) bool, error) {
	if expr == nil {
		return func(T) bool { return true }, nil
	}
	return compile(fields, expr)
}

func compile[T any](fields map[string]Field[T], e *astv1.Expr) (func(T) bool, error) {
	switch c := e.GetCondition().(type) {
	case *astv1.Expr_And, *astv1.Expr_Or:
		var conds []*astv1.Expr
		and := false
		if a, ok := c.(*astv1.Expr_And); ok {
			conds, and = a.And.GetConditions(), true
		} else {
			conds = c.(*astv1.Expr_Or).Or.GetConditions()
		}
		if len(conds) == 0 {
			return nil, Invalid("empty AND/OR")
		}
		preds := make([]func(T) bool, 0, len(conds))
		for _, sub := range conds {
			p, err := compile(fields, sub)
			if err != nil {
				return nil, err
			}
			preds = append(preds, p)
		}
		return func(r T) bool {
			for _, p := range preds {
				if p(r) != and {
					return !and
				}
			}
			return and
		}, nil
	case *astv1.Expr_Not:
		if c.Not.GetConditions() == nil {
			return nil, Invalid("NOT without a condition")
		}
		p, err := compile(fields, c.Not.GetConditions())
		if err != nil {
			return nil, err
		}
		return func(r T) bool { return !p(r) }, nil
	case *astv1.Expr_Has:
		return nil, Invalid("has() is not supported here")
	case *astv1.Expr_Comp:
		return compileComparison(fields, c.Comp)
	}
	return nil, Invalid("empty expression")
}

func compileComparison[T any](fields map[string]Field[T], c *astv1.Comparison) (func(T) bool, error) {
	f, ok := fields[c.GetField()]
	if !ok {
		return nil, Invalid("unknown field %q", c.GetField())
	}
	if err := CheckOperator(c.GetField(), f.Kind, c.GetOperator()); err != nil {
		return nil, err
	}
	var test func(have any) bool
	switch op := c.GetOperator(); op {
	case "in":
		want, err := ListValues(c.GetField(), f.Kind, c.GetValue())
		if err != nil {
			return nil, err
		}
		test = func(have any) bool {
			return slices.ContainsFunc(want, func(w any) bool { return compare(have, w) == 0 })
		}
	case "contains":
		want, err := Value(c.GetField(), f.Kind, c.GetValue())
		if err != nil {
			return nil, err
		}
		sub := strings.ToLower(want.(string))
		test = func(have any) bool { return strings.Contains(strings.ToLower(have.(string)), sub) }
	default:
		want, err := Value(c.GetField(), f.Kind, c.GetValue())
		if err != nil {
			return nil, err
		}
		test = func(have any) bool {
			d := compare(have, want)
			switch op {
			case "eq":
				return d == 0
			case "neq":
				return d != 0
			case "gt":
				return d > 0
			case "gte":
				return d >= 0
			case "lt":
				return d < 0
			default: // lte
				return d <= 0
			}
		}
	}
	return func(r T) bool { return slices.ContainsFunc(f.Get(r), test) }, nil
}

// compare orders two values of the same kind; Get must return the Go types
// value produces (string, int64, bool, time.Time).
func compare(a, b any) int {
	switch x := a.(type) {
	case string:
		return strings.Compare(x, b.(string))
	case int64:
		return cmp.Compare(x, b.(int64))
	case time.Time:
		return x.Compare(b.(time.Time))
	case bool:
		y := b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	}
	panic("filter: unsupported value type")
}
