package sql

import (
	"strconv"
	"strings"
)

// Dialect is a SQL dialect.
type Dialect string

const (
	MySQL    Dialect = "mysql" // also MariaDB
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

// Rebind rewrites the "?" placeholders of a statement for d: $1, $2, ... for
// PostgreSQL, unchanged otherwise. The statement must not contain "?"
// elsewhere, which holds for everything this package generates.
func Rebind(d Dialect, stmt string) string {
	if d != Postgres {
		return stmt
	}
	var b strings.Builder
	n := 0
	for _, r := range stmt {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Where joins conds with AND into a WHERE clause (with a leading space), or
// "" for none, and returns their arguments in order.
func Where(conds ...Cond) (string, []any) {
	if len(conds) == 0 {
		return "", nil
	}
	parts := make([]string, len(conds))
	var args []any
	for i, c := range conds {
		parts[i] = "(" + c.SQL + ")"
		args = append(args, c.Args...)
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}
