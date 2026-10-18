package roqql

import (
	"context"
	"strings"
	"testing"

	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// TestParser_Strict covers input the parser used to accept by quietly
// dropping or reinterpreting part of it, which widens a filter.
func TestParser_Strict(t *testing.T) {
	tests := []struct {
		name  string
		input string
		// want is the expected AST as protojson; empty means a parse error.
		want string
	}{
		// Trailing input used to be ignored.
		{name: "trailing junk", input: `a = 1 xyz`},
		{name: "lowercase and joins", input: `a = "x" and b = 1`,
			want: `{"and":{"conditions":[{"comp":{"field":"a","operator":"eq","value":{"string":"x"}}},{"comp":{"field":"b","operator":"eq","value":{"integer":"1"}}}]}}`},
		{name: "mixed case or", input: `a = 1 Or b = 2`,
			want: `{"or":{"conditions":[{"comp":{"field":"a","operator":"eq","value":{"integer":"1"}}},{"comp":{"field":"b","operator":"eq","value":{"integer":"2"}}}]}}`},
		{name: "unclosed group", input: `(a = 1) )`},
		{name: "surrounding whitespace", input: "  a = 1 \n",
			want: `{"comp":{"field":"a","operator":"eq","value":{"integer":"1"}}}`},

		// A missing operator used to mean equality.
		{name: "missing operator", input: `tenant "t1"`},
		{name: "unknown operator", input: `a ~ 1`},

		// Numbers.
		{name: "float", input: `n = 1.5`,
			want: `{"comp":{"field":"n","operator":"eq","value":{"float":1.5}}}`},
		{name: "negative", input: `n >= -3`,
			want: `{"comp":{"field":"n","operator":"gte","value":{"integer":"-3"}}}`},
		{name: "negative float", input: `n < -0.25`,
			want: `{"comp":{"field":"n","operator":"lt","value":{"float":-0.25}}}`},
		{name: "int overflow", input: `n = 99999999999999999999`},
		{name: "number into ident", input: `n = 12ab`},

		// Strings.
		{name: "other quote inside", input: `a = "it's"`,
			want: `{"comp":{"field":"a","operator":"eq","value":{"string":"it's"}}}`},
		{name: "escaped quote", input: `a = 'it\'s "x" \\ y'`,
			want: `{"comp":{"field":"a","operator":"eq","value":{"string":"it's \"x\" \\ y"}}}`},
		{name: "unknown escape", input: `a = "\n"`},
		{name: "unterminated string", input: `a = "x`},

		// Keywords.
		{name: "NOT without space", input: `NOT(a = 1)`,
			want: `{"not":{"conditions":{"comp":{"field":"a","operator":"eq","value":{"integer":"1"}}}}}`},
		{name: "ident starting with not", input: `notes = 1`,
			want: `{"comp":{"field":"notes","operator":"eq","value":{"integer":"1"}}}`},
		{name: "ident starting with and", input: `a = 1 AND android = 2`,
			want: `{"and":{"conditions":[{"comp":{"field":"a","operator":"eq","value":{"integer":"1"}}},{"comp":{"field":"android","operator":"eq","value":{"integer":"2"}}}]}}`},
		{name: "keyword glued to ident", input: `a = 1 ANDb = 2`},
		{name: "bool prefix", input: `a = trueish`},
		{name: "uppercase functions", input: `CONTAINS(name, "x") AND HAS(modules, name = "crm") AND IN(id, [1, 2])`,
			want: `{"and":{"conditions":[{"comp":{"field":"name","operator":"contains","value":{"string":"x"}}},{"has":{"entity":"modules","with":{"comp":{"field":"name","operator":"eq","value":{"string":"crm"}}}}},{"comp":{"field":"id","operator":"in","value":{"list":{"values":[{"integer":"1"},{"integer":"2"}]}}}}]}}`},
		{name: "uppercase bool", input: `a = TRUE`,
			want: `{"comp":{"field":"a","operator":"eq","value":{"bool":true}}}`},
		{name: "empty", input: ``},
	}

	p := &Parser{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Parse(context.Background(), strings.NewReader(tt.input))
			if tt.want == "" {
				if err == nil {
					t.Fatalf("parsed %q to %v, want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse %q: %v", tt.input, err)
			}
			want := &astv1.Expr{}
			if err := protojson.Unmarshal([]byte(tt.want), want); err != nil {
				t.Fatalf("bad want: %v", err)
			}
			if !proto.Equal(got, want) {
				g, _ := protojson.Marshal(got)
				t.Errorf("parse %q\ngot  %s\nwant %s", tt.input, g, tt.want)
			}
		})
	}
}
