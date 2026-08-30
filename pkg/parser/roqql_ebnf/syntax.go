package roqql

import (
	"context"
	"io"

	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
)

type Parser struct {
	Grammer any
}

func (p *Parser) Parse(ctx context.Context, r io.Reader) (*astv1.Expr, error) {
	// TODO
}
