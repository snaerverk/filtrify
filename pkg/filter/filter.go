// Package filter is the storage-agnostic side of evaluating filtrify
// expressions: parsing, field kinds and literal conversion, the operator
// vocabulary, in-memory matching and pagination, and opaque page cursors.
//
// Translators to a query language (e.g. SQL) build on Value, ListValues,
// CheckOperator and Order. Only declared field names are accepted, so a
// client's names and operators are looked up, never passed through.
package filter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
	roqql "github.com/snaerverk/filtrify/pkg/parser/roqql_peg"
)

// ErrInvalid marks a filter that doesn't parse or doesn't fit the schema.
var ErrInvalid = errors.New("invalid filter")

// Kind is the type of a filterable field, which decides the values and
// operators it accepts.
type Kind int

const (
	String Kind = iota
	Int
	Bool
	// Time accepts RFC 3339 strings or dates (YYYY-MM-DD, as UTC midnight).
	Time
)

func (k Kind) String() string {
	return [...]string{"string", "integer", "bool", "time"}[k]
}

// Parse parses a filter. A blank filter returns nil, which filters nothing.
func Parse(ctx context.Context, s string) (*astv1.Expr, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	expr, err := (&roqql.Parser{}).Parse(ctx, strings.NewReader(s))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return expr, nil
}

// Invalid returns an error wrapping ErrInvalid.
func Invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Value converts a literal to the Go value of kind: string, int64, bool or
// time.Time. Times are in UTC, so they also compare correctly where they are
// stored as text (SQLite).
func Value(field string, kind Kind, v *astv1.Value) (any, error) {
	switch kind {
	case String:
		if s, ok := v.GetValue().(*astv1.Value_String_); ok {
			return s.String_, nil
		}
	case Int:
		if i, ok := v.GetValue().(*astv1.Value_Integer); ok {
			return i.Integer, nil
		}
	case Bool:
		if b, ok := v.GetValue().(*astv1.Value_Bool); ok {
			return b.Bool, nil
		}
	case Time:
		if s, ok := v.GetValue().(*astv1.Value_String_); ok {
			if t, err := time.Parse(time.RFC3339Nano, s.String_); err == nil {
				return t.UTC(), nil
			}
			if t, err := time.Parse(time.DateOnly, s.String_); err == nil {
				return t.UTC(), nil
			}
			return nil, Invalid("%s: %q is neither an RFC 3339 time nor a date", field, s.String_)
		}
	}
	return nil, Invalid("%s takes a %s value", field, kind)
}

// ListValues converts the literal list of an "in" comparison.
func ListValues(field string, kind Kind, v *astv1.Value) ([]any, error) {
	l, ok := v.GetValue().(*astv1.Value_List)
	if !ok || len(l.List.GetValues()) == 0 {
		return nil, Invalid("in(%s, ...) takes a non-empty list", field)
	}
	out := make([]any, 0, len(l.List.GetValues()))
	for _, e := range l.List.GetValues() {
		x, err := Value(field, kind, e)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// CheckOperator rejects unknown operators and those that make no sense for
// kind. The vocabulary is the parser's: eq, neq, gt, gte, lt, lte, in and
// contains (strings only, meant case-insensitively).
func CheckOperator(field string, kind Kind, op string) error {
	switch op {
	case "eq", "neq", "in":
		return nil
	case "gt", "gte", "lt", "lte":
		if kind != Bool {
			return nil
		}
	case "contains":
		if kind == String {
			return nil
		}
	default:
		return Invalid("unknown operator %q", op)
	}
	return Invalid("operator %q doesn't apply to %s field %s", op, kind, field)
}
