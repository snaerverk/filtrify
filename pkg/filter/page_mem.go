package filter

import (
	"slices"

	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
)

// Paginate sorts records and returns one page of them. The sortable fields
// are those of fields with a single value; key must be unique per record.
// def is the field to order by when order is empty. Records whose sort field
// has no value sort after all others.
func Paginate[T any](fields map[string]Field[T], records []T, order []*paginatev1.OrderField, paging *paginatev1.Paging, def, key string) ([]T, PageInfo, error) {
	kinds := map[string]Kind{}
	for name, f := range fields {
		if !f.List {
			kinds[name] = f.Kind
		}
	}
	sorts, err := ResolveOrder(order, kinds, def, key)
	if err != nil {
		return nil, PageInfo{}, err
	}
	size, err := PageSize(paging)
	if err != nil {
		return nil, PageInfo{}, err
	}
	after, err := sorts.ParseCursor(paging.GetAfter())
	if err != nil {
		return nil, PageInfo{}, err
	}

	vals := func(r T) []any {
		out := make([]any, len(sorts))
		for i, s := range sorts {
			if v := fields[s.Name].Get(r); len(v) > 0 {
				out[i] = v[0]
			}
		}
		return out
	}
	// cmpVals orders two value tuples; nil (no value) sorts last.
	cmpVals := func(a, b []any) int {
		for i, s := range sorts {
			var d int
			switch {
			case a[i] == nil && b[i] == nil:
			case a[i] == nil:
				d = 1
			case b[i] == nil:
				d = -1
			default:
				d = compare(a[i], b[i])
				if s.Desc {
					d = -d
				}
			}
			if d != 0 {
				return d
			}
		}
		return 0
	}

	sorted := slices.Clone(records)
	slices.SortStableFunc(sorted, func(a, b T) int { return cmpVals(vals(a), vals(b)) })
	start := 0
	if after != nil {
		start, _ = slices.BinarySearchFunc(sorted, after, func(r T, c []any) int {
			if cmpVals(vals(r), c) <= 0 {
				return -1
			}
			return 1
		})
	}
	end := min(start+size, len(sorted))
	page := sorted[start:end]

	info := PageInfo{HasNext: end < len(sorted), HasPrevious: after != nil}
	if len(page) > 0 {
		info.FirstCursor = sorts.Cursor(vals(page[0]))
		info.LastCursor = sorts.Cursor(vals(page[len(page)-1]))
	}
	return page, info, nil
}
