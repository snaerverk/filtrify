package filter

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestParseBlank(t *testing.T) {
	expr, err := Parse(context.Background(), "  \n")
	if expr != nil || err != nil {
		t.Fatalf("got %v, %v; want nil, nil", expr, err)
	}
}

type rec struct {
	tags []string
	n    int64
	exp  *time.Time
}

func TestMatcher(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	recs := map[string]rec{
		"a":     {tags: []string{"root"}, n: 1},
		"b":     {tags: []string{"t1", "t2"}, n: 2, exp: &past},
		"c":     {tags: []string{"T1"}, n: 3},
		"empty": {n: 4},
	}
	fields := map[string]Field[rec]{
		"tag": {Kind: String, Get: func(r rec) []any {
			out := make([]any, len(r.tags))
			for i, t := range r.tags {
				out[i] = t
			}
			return out
		}},
		"n": {Kind: Int, Get: func(r rec) []any { return []any{r.n} }},
		"expire_time": {Kind: Time, Get: func(r rec) []any {
			if r.exp == nil {
				return nil
			}
			return []any{*r.exp}
		}},
	}
	tests := []struct {
		filter string
		want   []string
	}{
		{``, []string{"a", "b", "c", "empty"}},
		{`tag = "t2"`, []string{"b"}},                   // any element
		{`NOT tag = "t1"`, []string{"a", "c", "empty"}}, // no element
		{`tag != "t1"`, []string{"a", "b", "c"}},        // some element differs
		{`contains(tag, "t1")`, []string{"b", "c"}},     // case-insensitive
		{`in(tag, ["root", "t2"])`, []string{"a", "b"}}, //
		{`n > 1 AND n <= 3`, []string{"b", "c"}},        //
		{`n = 1 OR tag = "T1"`, []string{"a", "c"}},     //
		{`expire_time < "2999-01-01"`, []string{"b"}},   // no expiry matches no comparison
		{`NOT expire_time < "2999-01-01"`, []string{"a", "c", "empty"}},
	}
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			expr, err := Parse(context.Background(), tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			match, err := Matcher(fields, expr)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, k := range []string{"a", "b", "c", "empty"} {
				if match(recs[k]) {
					got = append(got, k)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	for _, f := range []string{`has(x, n = 1)`, `unknown = 1`, `n = "1"`, `contains(n, 1)`} {
		expr, _ := Parse(context.Background(), f)
		if _, err := Matcher(fields, expr); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", f, err)
		}
	}
}
