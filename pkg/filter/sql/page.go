package sql

import (
	"errors"
	filter "github.com/snaerverk/filtrify/pkg/filter"
	"strings"
	"time"

	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
)

// Page is a resolved order and position for a keyset-paginated query.
type Page struct {
	schema *Schema
	fields filter.Order
	after  []any
	size   int
}

// Page resolves order and paging against the schema's Sortable columns plus
// its Key. def is the field to order by when order is empty.
func (s *Schema) Page(order []*paginatev1.OrderField, paging *paginatev1.Paging, def string) (*Page, error) {
	if s.Key.SQL == "" {
		return nil, errors.New("sql: schema has no Key, so it can't be paginated")
	}
	kinds := map[string]filter.Kind{keyName: s.Key.Kind}
	for _, name := range s.Sortable {
		kinds[name] = s.Columns[name].Kind
	}
	fields, err := filter.ResolveOrder(order, kinds, def, keyName)
	if err != nil {
		return nil, err
	}
	size, err := filter.PageSize(paging)
	if err != nil {
		return nil, err
	}
	after, err := fields.ParseCursor(paging.GetAfter())
	if err != nil {
		return nil, err
	}
	return &Page{schema: s, fields: fields, after: after, size: size}, nil
}

// keyName is how orders and cursors refer to Schema.Key.
const keyName = "id"

func (p *Page) col(name string) string {
	if name == keyName {
		return p.schema.Key.SQL
	}
	return p.schema.Columns[name].SQL
}

// Size is the page size; query Size()+1 rows to learn whether more follow.
func (p *Page) Size() int { return p.size }

// Columns are the sort columns' SQL, to select and pass to Dest and Cursor.
func (p *Page) Columns() []string {
	cols := make([]string, len(p.fields))
	for i, f := range p.fields {
		cols[i] = p.col(f.Name)
	}
	return cols
}

// Dest returns typed scan destinations for Columns.
func (p *Page) Dest() []any {
	dest := make([]any, len(p.fields))
	for i, f := range p.fields {
		switch f.Kind {
		case filter.String:
			dest[i] = new(string)
		case filter.Int:
			dest[i] = new(int64)
		case filter.Bool:
			dest[i] = new(bool)
		case filter.Time:
			dest[i] = new(time.Time)
		}
	}
	return dest
}

// Cursor encodes the position after a row scanned into dest.
func (p *Page) Cursor(dest []any) string {
	vals := make([]any, len(dest))
	for i, d := range dest {
		switch x := d.(type) {
		case *string:
			vals[i] = *x
		case *int64:
			vals[i] = *x
		case *bool:
			vals[i] = *x
		case *time.Time:
			vals[i] = *x
		}
	}
	return p.fields.Cursor(vals)
}

// HasPrevious reports whether the page starts after a cursor.
func (p *Page) HasPrevious() bool { return p.after != nil }

// OrderBy is the ORDER BY clause, without the keywords.
func (p *Page) OrderBy() string {
	parts := make([]string, len(p.fields))
	for i, f := range p.fields {
		parts[i] = p.col(f.Name) + " ASC"
		if f.Desc {
			parts[i] = p.col(f.Name) + " DESC"
		}
	}
	return strings.Join(parts, ", ")
}

// After returns the condition selecting rows past the cursor, reporting
// false without one. For fields a, b it is
//
//	a > ? OR (a = ? AND b > ?)
//
// with < for descending fields.
func (p *Page) After() (Cond, bool) {
	if p.after == nil {
		return Cond{}, false
	}
	var ors []string
	var args []any
	for i := range p.fields {
		var ands []string
		for j := 0; j < i; j++ {
			ands = append(ands, p.col(p.fields[j].Name)+" = ?")
			args = append(args, p.after[j])
		}
		op := " > ?"
		if p.fields[i].Desc {
			op = " < ?"
		}
		ands = append(ands, p.col(p.fields[i].Name)+op)
		args = append(args, p.after[i])
		ors = append(ors, "("+strings.Join(ands, " AND ")+")")
	}
	return Cond{SQL: "(" + strings.Join(ors, " OR ") + ")", Args: args}, true
}
