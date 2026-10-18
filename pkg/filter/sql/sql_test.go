package sql

import (
	"context"
	"errors"
	filter "github.com/snaerverk/filtrify/pkg/filter"
	"reflect"
	"testing"
	"time"
)

var testSchema = &Schema{
	Columns: map[string]Column{
		"tenant":      {SQL: "p.name", Kind: filter.String},
		"size":        {SQL: "p.size", Kind: filter.Int},
		"active":      {SQL: "p.active", Kind: filter.Bool},
		"create_time": {SQL: "p.created", Kind: filter.Time},
	},
	Relations: map[string]Relation{
		"modules": {
			From:   "m",
			Join:   "m.pid = p.id",
			Schema: &Schema{Columns: map[string]Column{"name": {SQL: "m.name", Kind: filter.String}}},
		},
	},
}

func TestSchemaCond(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		filter string
		sql    string
		args   []any
	}{
		{`tenant = "a"`, `p.name = ?`, []any{"a"}},
		{`size >= 3 AND NOT active = true`, `(p.size >= ? AND NOT (p.active = ?))`, []any{int64(3), true}},
		{`tenant != "a" OR size < -1`, `(p.name <> ? OR p.size < ?)`, []any{"a", int64(-1)}},
		{`in(tenant, ["a", "b"])`, `p.name IN (?, ?)`, []any{"a", "b"}},
		{`contains(tenant, "50%_Off!")`, `LOWER(p.name) LIKE ? ESCAPE '!'`, []any{"%50!%!_off!!%"}},
		{`create_time > "2026-01-02"`, `p.created > ?`, []any{time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}},
		{`create_time <= "2026-01-02T03:04:05+02:00"`, `p.created <= ?`, []any{time.Date(2026, 1, 2, 1, 4, 5, 0, time.UTC)}},
		{`has(modules, name = "crm" OR name = "pms")`, `EXISTS (SELECT 1 FROM m WHERE m.pid = p.id AND ((m.name = ? OR m.name = ?)))`, []any{"crm", "pms"}},
	}
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			expr, err := filter.Parse(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			cond, err := testSchema.Cond(expr)
			if err != nil {
				t.Fatal(err)
			}
			if cond.SQL != tt.sql {
				t.Errorf("sql\ngot  %s\nwant %s", cond.SQL, tt.sql)
			}
			// time.Time values compare by instant, not location.
			for i := range cond.Args {
				if tm, ok := cond.Args[i].(time.Time); ok {
					cond.Args[i] = tm.UTC()
				}
			}
			if !reflect.DeepEqual(cond.Args, tt.args) {
				t.Errorf("args\ngot  %#v\nwant %#v", cond.Args, tt.args)
			}
		})
	}
}

func TestSchemaCondInvalid(t *testing.T) {
	ctx := context.Background()
	for _, f := range []string{
		`a = 1 b`,                         // parse error
		`unknown = 1`,                     // unknown field
		`name = "x"`,                      // a relation's field at the top
		`has(unknown, name = "x")`,        // unknown relation
		`has(modules, tenant = "x")`,      // parent field inside a relation
		`size = "3"`,                      // wrong value type
		`tenant = 1`,                      // wrong value type
		`active > true`,                   // ordering a bool
		`contains(size, 3)`,               // contains on a number
		`create_time > "yesterday"`,       // unparsable time
		`in(size, [1, "2"])`,              // mixed list
		`contains("p.name; DROP x", "a")`, // a quoted field is still just a name
	} {
		t.Run(f, func(t *testing.T) {
			expr, err := filter.Parse(ctx, f)
			if err == nil {
				_, err = testSchema.Cond(expr)
			}
			if !errors.Is(err, filter.ErrInvalid) {
				t.Fatalf("err = %v, want filter.ErrInvalid", err)
			}
		})
	}
}
