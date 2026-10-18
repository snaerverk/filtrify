package sql

import (
	"errors"
	filter "github.com/snaerverk/filtrify/pkg/filter"
	"reflect"
	"testing"
	"time"

	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
)

func asc(f string) *paginatev1.OrderField { return &paginatev1.OrderField{Field: f} }

func desc(f string) *paginatev1.OrderField {
	return &paginatev1.OrderField{Field: f, Direction: paginatev1.Direction_DIRECTION_DESC}
}

var pageSchema = &Schema{
	Columns: map[string]Column{
		"tenant":      {SQL: "p.name", Kind: filter.String},
		"create_time": {SQL: "p.created", Kind: filter.Time},
	},
	Sortable: []string{"tenant", "create_time"},
	Key:      Column{SQL: "p.id", Kind: filter.Int},
}

func TestPage(t *testing.T) {
	p, err := pageSchema.Page([]*paginatev1.OrderField{desc("create_time"), asc("tenant")}, nil, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.OrderBy(), "p.created DESC, p.name ASC, p.id ASC"; got != want {
		t.Errorf("order by %q, want %q", got, want)
	}
	if p.Size() != filter.DefaultPageSize || p.HasPrevious() {
		t.Errorf("size %d, has previous %v", p.Size(), p.HasPrevious())
	}
	if _, ok := p.After(); ok {
		t.Error("condition without cursor")
	}

	// A cursor taken from a row continues after it.
	created := time.Date(2026, 10, 18, 10, 0, 0, 123000, time.UTC)
	dest := p.Dest()
	*dest[0].(*time.Time), *dest[1].(*string), *dest[2].(*int64) = created, "acme", 7
	cur := p.Cursor(dest)

	p2, err := pageSchema.Page([]*paginatev1.OrderField{desc("create_time"), asc("tenant")}, &paginatev1.Paging{After: cur, First: 10}, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	cond, ok := p2.After()
	if !ok || !p2.HasPrevious() || p2.Size() != 10 {
		t.Fatalf("after cursor: ok %v, has previous %v, size %d", ok, p2.HasPrevious(), p2.Size())
	}
	wantSQL := "((p.created < ?) OR (p.created = ? AND p.name > ?) OR (p.created = ? AND p.name = ? AND p.id > ?))"
	if cond.SQL != wantSQL {
		t.Errorf("after\ngot  %s\nwant %s", cond.SQL, wantSQL)
	}
	wantArgs := []any{created, created, "acme", created, "acme", int64(7)}
	if !reflect.DeepEqual(cond.Args, wantArgs) {
		t.Errorf("args\ngot  %#v\nwant %#v", cond.Args, wantArgs)
	}

	// The default order, all descending, keeps the key descending too.
	p3, _ := pageSchema.Page([]*paginatev1.OrderField{desc("tenant")}, nil, "tenant")
	if got, want := p3.OrderBy(), "p.name DESC, p.id DESC"; got != want {
		t.Errorf("order by %q, want %q", got, want)
	}
}

func TestPageInvalid(t *testing.T) {
	cur := func(order ...*paginatev1.OrderField) string {
		p, _ := pageSchema.Page(order, nil, "tenant")
		dest := p.Dest()
		return p.Cursor(dest)
	}
	for name, tt := range map[string]struct {
		order  []*paginatev1.OrderField
		paging *paginatev1.Paging
	}{
		"unknown field":      {order: []*paginatev1.OrderField{asc("nope")}},
		"filter-only field":  {order: []*paginatev1.OrderField{asc("p.name")}},
		"twice":              {order: []*paginatev1.OrderField{asc("tenant"), desc("tenant")}},
		"negative size":      {paging: &paginatev1.Paging{First: -1}},
		"size too large":     {paging: &paginatev1.Paging{First: filter.MaxPageSize + 1}},
		"garbage cursor":     {paging: &paginatev1.Paging{After: "!!"}},
		"cursor other order": {order: []*paginatev1.OrderField{desc("tenant")}, paging: &paginatev1.Paging{After: cur(asc("tenant"))}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := pageSchema.Page(tt.order, tt.paging, "tenant"); !errors.Is(err, filter.ErrInvalid) {
				t.Fatalf("err = %v, want filter.ErrInvalid", err)
			}
		})
	}
}

func TestPageWithoutKey(t *testing.T) {
	noKey := &Schema{Columns: pageSchema.Columns, Sortable: pageSchema.Sortable}
	if _, err := noKey.Page(nil, nil, "tenant"); err == nil || errors.Is(err, filter.ErrInvalid) {
		t.Fatalf("err = %v, want a schema error (not the client's fault)", err)
	}
}
