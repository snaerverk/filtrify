// Package sql translates filtrify expressions and page requests to SQL
// fragments for MySQL/MariaDB, PostgreSQL and SQLite.
//
// Only names declared in a Schema are accepted and every value becomes a
// bind parameter, so a filter never puts client text into SQL. Fragments use
// "?" placeholders so they compose; run the final statement through Rebind
// for PostgreSQL (or let a library such as sqlmap's query.Conn do it).
//
// Portability notes:
//   - contains() compares LOWER() of both sides; SQLite's LOWER only folds
//     ASCII, and MySQL's doesn't affect binary strings (VARBINARY/BLOB).
//   - Ordering strings (<, >, order) follows the column's collation, which
//     differs between databases.
//   - Sort columns and the Key must be NOT NULL: databases disagree on
//     where NULLs sort, which would break keyset pagination.
//   - SQLite has no time type: Time columns must hold values bound from
//     time.Time in UTC by the same driver, so they compare as text.
package sql

import (
	filter "github.com/snaerverk/filtrify/pkg/filter"
	"strings"

	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
)

// Cond is a SQL boolean expression with "?" placeholders and its arguments.
type Cond struct {
	SQL  string
	Args []any
}

// Column is a filterable field backed by a SQL expression. Qualify it with
// its table, as it may end up inside an EXISTS subquery.
type Column struct {
	SQL  string
	Kind filter.Kind
}

// Relation is a child entity that has(name, ...) tests with a correlated
// EXISTS subquery, which neither multiplies parent rows nor disturbs the
// eager loading that runs on the filtered parents.
type Relation struct {
	// From is the child's FROM clause; it may join further tables.
	From string
	// Join correlates the child with the parent row.
	Join   string
	Schema *Schema
}

// Schema declares the fields and relations a filter may name.
type Schema struct {
	Columns   map[string]Column
	Relations map[string]Relation
	// Sortable names the Columns a page may be ordered by.
	Sortable []string
	// Key is a unique column, the tiebreaker of every order. It is not
	// filterable unless also listed in Columns; pages may order by "id".
	Key Column
}

// Cond translates expr into a condition.
func (s *Schema) Cond(expr *astv1.Expr) (Cond, error) {
	var b sqlBuilder
	if err := b.expr(s, expr); err != nil {
		return Cond{}, err
	}
	return Cond{SQL: b.sql.String(), Args: b.args}, nil
}

type sqlBuilder struct {
	sql  strings.Builder
	args []any
}

func (b *sqlBuilder) expr(s *Schema, e *astv1.Expr) error {
	switch c := e.GetCondition().(type) {
	case *astv1.Expr_And:
		return b.join(s, " AND ", c.And.GetConditions())
	case *astv1.Expr_Or:
		return b.join(s, " OR ", c.Or.GetConditions())
	case *astv1.Expr_Not:
		if c.Not.GetConditions() == nil {
			return filter.Invalid("NOT without a condition")
		}
		b.sql.WriteString("NOT (")
		if err := b.expr(s, c.Not.GetConditions()); err != nil {
			return err
		}
		b.sql.WriteString(")")
		return nil
	case *astv1.Expr_Has:
		rel, ok := s.Relations[c.Has.GetEntity()]
		if !ok {
			return filter.Invalid("unknown relation %q", c.Has.GetEntity())
		}
		b.sql.WriteString("EXISTS (SELECT 1 FROM " + rel.From + " WHERE " + rel.Join)
		if c.Has.GetWith() != nil {
			b.sql.WriteString(" AND (")
			if err := b.expr(rel.Schema, c.Has.GetWith()); err != nil {
				return err
			}
			b.sql.WriteString(")")
		}
		b.sql.WriteString(")")
		return nil
	case *astv1.Expr_Comp:
		return b.comparison(s, c.Comp)
	}
	return filter.Invalid("empty expression")
}

func (b *sqlBuilder) join(s *Schema, sep string, conds []*astv1.Expr) error {
	if len(conds) == 0 {
		return filter.Invalid("empty AND/OR")
	}
	b.sql.WriteString("(")
	for i, c := range conds {
		if i > 0 {
			b.sql.WriteString(sep)
		}
		if err := b.expr(s, c); err != nil {
			return err
		}
	}
	b.sql.WriteString(")")
	return nil
}

var sqlOperators = map[string]string{
	"eq": "=", "neq": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=",
}

func (b *sqlBuilder) comparison(s *Schema, c *astv1.Comparison) error {
	col, ok := s.Columns[c.GetField()]
	if !ok {
		return filter.Invalid("unknown field %q", c.GetField())
	}
	if err := filter.CheckOperator(c.GetField(), col.Kind, c.GetOperator()); err != nil {
		return err
	}
	switch c.GetOperator() {
	case "in":
		vals, err := filter.ListValues(c.GetField(), col.Kind, c.GetValue())
		if err != nil {
			return err
		}
		b.sql.WriteString(col.SQL + " IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(vals)), ", ") + ")")
		b.args = append(b.args, vals...)
	case "contains":
		v, err := filter.Value(c.GetField(), col.Kind, c.GetValue())
		if err != nil {
			return err
		}
		// Case-insensitive, whatever the column's collation. "!" escapes,
		// as backslashes mean different things across dialects.
		b.sql.WriteString("LOWER(" + col.SQL + ") LIKE ? ESCAPE '!'")
		b.args = append(b.args, "%"+escapeLike(strings.ToLower(v.(string)))+"%")
	default:
		v, err := filter.Value(c.GetField(), col.Kind, c.GetValue())
		if err != nil {
			return err
		}
		b.sql.WriteString(col.SQL + " " + sqlOperators[c.GetOperator()] + " ?")
		b.args = append(b.args, v)
	}
	return nil
}

func escapeLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}
