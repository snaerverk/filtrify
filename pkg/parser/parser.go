package parser

import (
	"context"
	"io"

	astv1 "github.com/roderm/filtrify/pkg/generated/filtrify/ast/v1"
)

type Parser interface {
	Parse(context.Context, io.Reader) (*astv1.Expr, error)
}
