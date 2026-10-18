package roqql

import (
	"context"
	"fmt"
	"io"

	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
)

type Parser struct{}

func (p *Parser) Parse(ctx context.Context, r io.Reader) (*astv1.Expr, error) {
	res, err := ParseReader("filter", r)
	if err != nil {
		return nil, err
	}
	expr, ok := res.(*astv1.Expr)
	if !ok {
		return nil, fmt.Errorf("unexpected result type: %T", res)
	}
	return expr, nil
}
