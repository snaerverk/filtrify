package filter

import (
	"errors"
	"slices"
	"testing"
	"time"

	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
)

func asc(f string) *paginatev1.OrderField { return &paginatev1.OrderField{Field: f} }

func desc(f string) *paginatev1.OrderField {
	return &paginatev1.OrderField{Field: f, Direction: paginatev1.Direction_DIRECTION_DESC}
}

type tok struct {
	id   int64
	tags []string
	exp  *time.Time
}

func TestPaginate(t *testing.T) {
	day := func(d int) *time.Time {
		tm := time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC)
		return &tm
	}
	// Ties on expiry and tokens without one must neither repeat nor vanish.
	recs := []tok{
		{id: 1, exp: day(5)}, {id: 2}, {id: 3, exp: day(1)}, {id: 4, exp: day(5)},
		{id: 5}, {id: 6, exp: day(3)}, {id: 7, exp: day(5)},
	}
	fields := map[string]Field[tok]{
		"id": {Kind: Int, Get: func(r tok) []any { return []any{r.id} }},
		"tag": {Kind: String, List: true, Get: func(r tok) []any {
			return []any{}
		}},
		"expire_time": {Kind: Time, Get: func(r tok) []any {
			if r.exp == nil {
				return nil
			}
			return []any{*r.exp}
		}},
	}
	walk := func(order []*paginatev1.OrderField, size int32) []int64 {
		t.Helper()
		var ids []int64
		after := ""
		for range 20 {
			page, info, err := Paginate(fields, recs, order, &paginatev1.Paging{First: size, After: after}, "id", "id")
			if err != nil {
				t.Fatal(err)
			}
			if info.HasPrevious != (after != "") {
				t.Errorf("has previous %v after %q", info.HasPrevious, after)
			}
			for _, r := range page {
				ids = append(ids, r.id)
			}
			if !info.HasNext {
				return ids
			}
			after = info.LastCursor
		}
		t.Fatal("no last page")
		return nil
	}

	for _, size := range []int32{1, 2, 3, 50} {
		if got, want := walk(nil, size), []int64{1, 2, 3, 4, 5, 6, 7}; !slices.Equal(got, want) {
			t.Errorf("default order, size %d: %v, want %v", size, got, want)
		}
		if got, want := walk([]*paginatev1.OrderField{asc("expire_time")}, size), []int64{3, 6, 1, 4, 7, 2, 5}; !slices.Equal(got, want) {
			t.Errorf("by expiry, size %d: %v, want %v", size, got, want)
		}
		// Descending values, missing ones still last, ties by id descending.
		if got, want := walk([]*paginatev1.OrderField{desc("expire_time")}, size), []int64{7, 4, 1, 6, 3, 5, 2}; !slices.Equal(got, want) {
			t.Errorf("by expiry desc, size %d: %v, want %v", size, got, want)
		}
	}

	if _, _, err := Paginate(fields, recs, []*paginatev1.OrderField{asc("tag")}, nil, "id", "id"); !errors.Is(err, ErrInvalid) {
		t.Errorf("sorting a list field: err = %v, want ErrInvalid", err)
	}
	page, info, err := Paginate(fields, nil, nil, nil, "id", "id")
	if err != nil || len(page) != 0 || info.HasNext || info.FirstCursor != "" {
		t.Errorf("empty: %v %+v %v", page, info, err)
	}
}

func TestOrderCursor(t *testing.T) {
	kinds := map[string]Kind{"name": String, "at": Time, "ok": Bool, "id": Int}
	o, err := ResolveOrder([]*paginatev1.OrderField{desc("at"), asc("name"), asc("ok")}, kinds, "name", "id")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := o.Key(), "-at,name,ok,id"; got != want {
		t.Errorf("key %q, want %q", got, want)
	}
	at := time.Date(2026, 10, 18, 10, 0, 0, 123456789, time.FixedZone("x", 3600))
	// int64 beyond float64 precision and a missing value survive.
	vals := []any{at, "ä,\"x\"", nil, int64(1<<62 + 1)}
	got, err := o.ParseCursor(o.Cursor(vals))
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].(time.Time).Equal(at) || got[1] != vals[1] || got[2] != nil || got[3] != vals[3] {
		t.Errorf("round trip: got %#v, want %#v", got, vals)
	}
	if v, err := o.ParseCursor(""); v != nil || err != nil {
		t.Errorf("empty cursor: %v, %v", v, err)
	}
	other, _ := ResolveOrder([]*paginatev1.OrderField{asc("at")}, kinds, "name", "id")
	for name, c := range map[string]string{"other order": other.Cursor([]any{at, int64(1)}), "garbage": "!!", "not json": "bm9wZQ"} {
		if _, err := o.ParseCursor(c); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	// By default the key follows the last field's direction.
	o2, _ := ResolveOrder([]*paginatev1.OrderField{desc("name")}, kinds, "name", "id")
	if got, want := o2.Key(), "-name,-id"; got != want {
		t.Errorf("key %q, want %q", got, want)
	}
	for name, order := range map[string][]*paginatev1.OrderField{
		"unknown": {asc("nope")}, "twice": {asc("name"), desc("name")},
	} {
		if _, err := ResolveOrder(order, kinds, "name", "id"); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	for _, n := range []int32{-1, MaxPageSize + 1} {
		if _, err := PageSize(&paginatev1.Paging{First: n}); !errors.Is(err, ErrInvalid) {
			t.Errorf("page size %d: err = %v", n, err)
		}
	}
}
