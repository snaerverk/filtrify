package roqql

import (
	"fmt"

	astv1 "github.com/roderm/filtrify/pkg/generated/filtrify/ast/v1"
)

func buildComparison(field, op, value any) (*astv1.Expr, error) {
	node := &ComparisonNode{
		Field: field,
		Op:    op,
		Value: value,
	}
	return node.Expr()
}

func buildHas(field, value any) (*astv1.Expr, error) {
	f := field.(string)
	val, ok := value.(*astv1.Expr)
	if !ok {
		return nil, fmt.Errorf("value is '%T' not *astv1.Expr", val)
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

func buildAnd() (*astv1.Expr, error) {}
func buildOr() (*astv1.Expr, error)  {}
