package roqql

import (
	"fmt"
	"strings"

	astv1 "github.com/roderm/filtrify/pkg/generated/filtrify/ast/v1"
)

func toString(v any) (string, error) {
	if v == nil {
		return "", fmt.Errorf("value is nil")
	}
	switch val := v.(type) {
	case string:
		return val, nil
	case []byte:
		return string(val), nil
	case *astv1.Value:
		if s, ok := val.Value.(*astv1.Value_String_); ok {
			return s.String_, nil
		}
		return "", fmt.Errorf("expected astv1.Value_String_, got %T", val.Value)
	case []any:
		var sb strings.Builder
		for _, item := range val {
			if s, ok := item.(string); ok {
				sb.WriteString(s)
			} else if b, ok := item.([]byte); ok {
				sb.Write(b)
			} else {
				return "", fmt.Errorf("unexpected type in sequence: %T", item)
			}
		}
		return sb.String(), nil
	default:
		return "", fmt.Errorf("unexpected type: %T", v)
	}
}

func buildComparison(field, op, value any) (*astv1.Expr, error) {
	f, err := toString(field)
	if err != nil {
		return nil, fmt.Errorf("rule Comparison: %w", err)
	}
	o, err := toString(op)
	if err != nil {
		return nil, fmt.Errorf("rule Comparison: %w", err)
	}

	if _, ok := value.(*astv1.Value); !ok {
		return nil, fmt.Errorf("rule Comparison: value is '%T' not *astv1.Value", value)
	}

	node := &ComparisonNode{
		Field: f,
		Op:    o,
		Value: value,
	}
	return node.Expr()
}

func buildList(first any, rest any) ([]*astv1.Value, error) {
	values := []*astv1.Value{
		first.(*astv1.Value),
	}

	for _, r := range rest.([]any) {
		pair := r.(*astv1.Value)
		values = append(values, pair)

	}

	return values, nil
}

func buildIn(field, values any) (*astv1.Expr, error) {
	f, err := toString(field)
	if err != nil {
		return nil, fmt.Errorf("rule In: %w", err)
	}

	valuesList, ok := values.([]*astv1.Value)
	if !ok {
		return nil, fmt.Errorf("buildIn: must be []*astv1.Value not %T", values)
	}

	return &astv1.Expr{
		Condition: &astv1.Expr_Comp{
			Comp: &astv1.Comparison{
				Field:    f,
				Operator: "in",
				Value: &astv1.Value{
					Value: &astv1.Value_List{
						List: &astv1.List{
							Values: valuesList,
						},
					},
				},
			},
		},
	}, nil
}

func buildContains(field, value any) (*astv1.Expr, error) {
	f, err := toString(field)
	if err != nil {
		return nil, fmt.Errorf("rule Contains: %w", err)
	}
	val, ok := value.(*astv1.Value)
	if !ok {
		return nil, fmt.Errorf("rule Contains: value is '%T' not *astv1.Value", value)
	}
	return &astv1.Expr{
		Condition: &astv1.Expr_Comp{
			Comp: &astv1.Comparison{
				Field:    f,
				Operator: "contains",
				Value:    val,
			},
		},
	}, nil
}

func buildHas(field, value any) (*astv1.Expr, error) {
	f, err := toString(field)
	if err != nil {
		return nil, fmt.Errorf("rule Has: %w", err)
	}
	val, ok := value.(*astv1.Expr)
	if !ok {
		return nil, fmt.Errorf("rule Has: value is '%T' not *astv1.Expr", value)
	}
	return &astv1.Expr{
		Condition: &astv1.Expr_Has{
			Has: &astv1.HasChildren{
				Entity: f,
				With:   val,
			},
		},
	}, nil
}

func buildAnd(head, tail any) (*astv1.Expr, error) {
	h, ok1 := head.(*astv1.Expr)
	t, ok2 := tail.([]*astv1.Expr)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("invalid head or tail for And: %T, %T", head, tail)
	}
	if len(t) == 0 {
		return h, nil
	}

	conditions := append([]*astv1.Expr{h}, t...)
	return &astv1.Expr{
		Condition: &astv1.Expr_And{
			And: &astv1.AndExpr{
				Conditions: conditions,
			},
		},
	}, nil
}

func buildOr(head, tail any) (*astv1.Expr, error) {
	h, ok1 := head.(*astv1.Expr)
	t, ok2 := tail.([]*astv1.Expr)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("invalid head or tail for Or: %T, %T", head, tail)
	}
	if len(t) == 0 {
		return h, nil
	}

	conditions := append([]*astv1.Expr{h}, t...)
	return &astv1.Expr{
		Condition: &astv1.Expr_Or{
			Or: &astv1.OrExpr{
				Conditions: conditions,
			},
		},
	}, nil
}

func buildAndTail(tail any) ([]*astv1.Expr, error) {
	if tail == nil {
		return nil, nil
	}
	slice, ok := tail.([]any)
	if !ok {
		return nil, fmt.Errorf("expected []any for AndTail, got %T", tail)
	}
	out := make([]*astv1.Expr, 0, len(slice))
	for _, v := range slice {
		pair, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("expected []any for AndTail element, got %T", v)
		}
		if len(pair) < 4 {
			return nil, fmt.Errorf("expected at least 4 elements in AndTail pair, got %d", len(pair))
		}
		expr, ok := pair[3].(*astv1.Expr)
		if !ok {
			return nil, fmt.Errorf("expected *astv1.Expr in AndTail pair, got %T", pair[3])
		}
		out = append(out, expr)
	}
	return out, nil
}

func buildOrTail(tail any) ([]*astv1.Expr, error) {
	if tail == nil {
		return nil, nil
	}
	slice, ok := tail.([]any)
	if !ok {
		return nil, fmt.Errorf("expected []any for OrTail, got %T", tail)
	}
	out := make([]*astv1.Expr, 0, len(slice))
	for _, v := range slice {
		pair, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("expected []any for OrTail element, got %T", v)
		}
		if len(pair) < 4 {
			return nil, fmt.Errorf("expected at least 4 elements in OrTail pair, got %d", len(pair))
		}
		expr, ok := pair[3].(*astv1.Expr)
		if !ok {
			return nil, fmt.Errorf("expected *astv1.Expr in OrTail pair, got %T", pair[3])
		}
		out = append(out, expr)
	}
	return out, nil
}

func buildNot(x any) (*astv1.Expr, error) {
	expr, ok := x.(*astv1.Expr)
	if !ok {
		return nil, fmt.Errorf("expected *astv1.Expr for Not, got %T", x)
	}
	return &astv1.Expr{
		Condition: &astv1.Expr_Not{
			Not: &astv1.NotExpr{
				Conditions: expr,
			},
		},
	}, nil
}
