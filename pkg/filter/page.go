package filter

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
)

const (
	DefaultPageSize = 50
	MaxPageSize     = 500
)

// PageSize reads paging.first: 0 means DefaultPageSize.
func PageSize(p *paginatev1.Paging) (int, error) {
	switch n := int(p.GetFirst()); {
	case n == 0:
		return DefaultPageSize, nil
	case n < 0 || n > MaxPageSize:
		return 0, Invalid("paging.first must be between 1 and %d", MaxPageSize)
	default:
		return n, nil
	}
}

// SortField is one resolved entry of an Order.
type SortField struct {
	Name string
	Kind Kind
	Desc bool
}

// Order is a resolved sort, ending with a unique key so positions in it are
// exact.
type Order []SortField

// ResolveOrder checks order against the sortable fields, defaulting to def
// ascending, and appends key as the tiebreaker that makes cursors exact.
func ResolveOrder(order []*paginatev1.OrderField, kinds map[string]Kind, def, key string) (Order, error) {
	var out Order
	seen := map[string]bool{}
	for _, o := range order {
		kind, ok := kinds[o.GetField()]
		if !ok {
			return nil, Invalid("can't order by %q", o.GetField())
		}
		if seen[o.GetField()] {
			return nil, Invalid("%q ordered twice", o.GetField())
		}
		seen[o.GetField()] = true
		out = append(out, SortField{Name: o.GetField(), Kind: kind, Desc: o.GetDirection() == paginatev1.Direction_DIRECTION_DESC})
	}
	if len(out) == 0 && def != key {
		out = append(out, SortField{Name: def, Kind: kinds[def]})
		seen[def] = true
	}
	if !seen[key] {
		// The key follows the direction of the last field, so an all-
		// descending order stays a single index scan.
		desc := len(out) > 0 && out[len(out)-1].Desc
		out = append(out, SortField{Name: key, Kind: kinds[key], Desc: desc})
	}
	return out, nil
}

// Key identifies the order, so a cursor can't be replayed under another,
// e.g. "tenant,-create_time,id".
func (o Order) Key() string {
	parts := make([]string, len(o))
	for i, f := range o {
		parts[i] = f.Name
		if f.Desc {
			parts[i] = "-" + f.Name
		}
	}
	return strings.Join(parts, ",")
}

// cursor is the position after a row: the order it was taken under and the
// row's values of the sort fields, as strings (JSON numbers would lose int64
// precision), null for a missing value.
type cursor struct {
	Order  string    `json:"o"`
	Values []*string `json:"v"`
}

// Cursor encodes the position after a row with the given values of the sort
// fields (nil for a missing value) as an opaque, URL-safe string.
func (o Order) Cursor(vals []any) string {
	c := cursor{Order: o.Key(), Values: make([]*string, len(vals))}
	for i, v := range vals {
		var s string
		switch x := v.(type) {
		case nil:
			continue
		case string:
			s = x
		case int64:
			s = strconv.FormatInt(x, 10)
		case bool:
			s = strconv.FormatBool(x)
		case time.Time:
			s = x.UTC().Format(time.RFC3339Nano)
		}
		c.Values[i] = &s
	}
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ParseCursor returns the sort values of a Cursor, or nil for no cursor. A
// cursor of another order is rejected.
func (o Order) ParseCursor(after string) ([]any, error) {
	if after == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(after)
	var c cursor
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	if err != nil {
		return nil, Invalid("malformed cursor")
	}
	if c.Order != o.Key() || len(c.Values) != len(o) {
		return nil, Invalid("cursor belongs to a different order")
	}
	vals := make([]any, len(o))
	for i, f := range o {
		if c.Values[i] == nil {
			continue
		}
		var v any
		switch raw := *c.Values[i]; f.Kind {
		case String:
			v = raw
		case Int:
			v, err = strconv.ParseInt(raw, 10, 64)
		case Bool:
			v, err = strconv.ParseBool(raw)
		case Time:
			v, err = time.Parse(time.RFC3339Nano, raw)
		}
		if err != nil {
			return nil, Invalid("malformed cursor")
		}
		vals[i] = v
	}
	return vals, nil
}

// PageInfo describes the returned slice of a result.
type PageInfo struct {
	FirstCursor string
	LastCursor  string
	HasNext     bool
	// HasPrevious is true when the page started after a cursor.
	HasPrevious bool
}

// Proto converts the info for a response.
func (p PageInfo) Proto() *paginatev1.Page {
	return &paginatev1.Page{
		FirstCursor: p.FirstCursor,
		LastCursor:  p.LastCursor,
		HasNext:     p.HasNext,
		HasPrevious: p.HasPrevious,
	}
}
