package roqql

import (
	"fmt"

	astv1 "github.com/roderm/filtrify/pkg/generated/filtrify/ast/v1"
)

type ComparisonNode struct {
	Field any
	Op    any
	Value any
}

func (cn *ComparisonNode) GetField() string {
	f, ok := cn.Field.(string)
	if !ok {
		panic("ComparisonNode \"field\" no a string")
	}
	return f
}

func (cn *ComparisonNode) GetOperator() string {
	f, ok := cn.Op.(string)
	if !ok {
		panic("ComparisonNode \"operator\" not a string")
	}
	return f
}

func (cn *ComparisonNode) GetValue() (*astv1.Value, error) {
	switch v := cn.Value.(type) {
	case *astv1.Value:
		return v, nil
	case bool:
		return &astv1.Value{
			Value: &astv1.Value_Bool{
				Bool: v,
			},
		}, nil
	case int64:
		return &astv1.Value{
			Value: &astv1.Value_Integer{
				Integer: v,
			},
		}, nil
	}

	return nil, fmt.Errorf("unknown type '%T'", cn.Value)
}

func (cn *ComparisonNode) Expr() (*astv1.Expr, error) {
	op := "eq"
	switch cn.GetOperator() {
	case "=", "==", ":", "eq", "equal":
		op = "eq"
	case "!=", "neq", "not_equal":
		op = "neq"
	case ">=", "gte":
		op = "gte"
	case "<=", "lte":
		op = "lte"
	case ">", "gt":
		op = "gt"
	case "<", "lt":
		op = "lt"
	}
	val, err := cn.GetValue()
	return &astv1.Expr{
		Condition: &astv1.Expr_Comp{
			Comp: &astv1.Comparison{
				Field:    cn.GetField(),
				Operator: op,
				Value:    val,
			},
		},
	}, err
}
