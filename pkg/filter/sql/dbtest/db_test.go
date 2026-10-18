package dbtest_test

// Runs the generated SQL against real databases: SQLite always (pure Go
// driver), MySQL/MariaDB and PostgreSQL when their DSN is set:
//
//	FILTRIFY_TEST_MYSQL_DSN='user:pw@tcp(127.0.0.1:3306)/db'
//	FILTRIFY_TEST_POSTGRES_DSN='postgres://user:pw@127.0.0.1:5432/db?sslmode=disable'

import (
	"context"
	dbsql "database/sql"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/snaerverk/filtrify/pkg/filter"
	fsql "github.com/snaerverk/filtrify/pkg/filter/sql"
	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
	_ "modernc.org/sqlite"
)

type testDB struct {
	name    string
	dialect fsql.Dialect
	db      *dbsql.DB
}

// DDL per dialect: same columns, the closest native types. Strings compare
// case-sensitively everywhere (utf8mb4_bin on MySQL), so equality agrees.
var ddl = map[fsql.Dialect][]string{
	fsql.SQLite: {
		`CREATE TABLE parent (id INTEGER PRIMARY KEY, name VARCHAR(100) NOT NULL, size BIGINT NOT NULL, active BOOLEAN NOT NULL, created TIMESTAMP NOT NULL)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, pid BIGINT NOT NULL, name VARCHAR(100) NOT NULL)`,
	},
	fsql.MySQL: {
		`CREATE TABLE parent (id BIGINT PRIMARY KEY, name VARCHAR(100) COLLATE utf8mb4_bin NOT NULL, size BIGINT NOT NULL, active BOOLEAN NOT NULL, created DATETIME(6) NOT NULL) DEFAULT CHARSET utf8mb4`,
		`CREATE TABLE child (id BIGINT PRIMARY KEY, pid BIGINT NOT NULL, name VARCHAR(100) COLLATE utf8mb4_bin NOT NULL) DEFAULT CHARSET utf8mb4`,
	},
	fsql.Postgres: {
		`CREATE TABLE parent (id BIGINT PRIMARY KEY, name VARCHAR(100) NOT NULL, size BIGINT NOT NULL, active BOOLEAN NOT NULL, created TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE child (id BIGINT PRIMARY KEY, pid BIGINT NOT NULL, name VARCHAR(100) NOT NULL)`,
	},
}

func day(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }

// Rows 1 and 4 tie on size and created; names hold LIKE wildcards and the
// escape character.
var parents = []struct {
	id      int64
	name    string
	size    int64
	active  bool
	created time.Time
}{
	{1, "Acme", 10, true, day(1)},
	{2, "acme_50%", 20, false, day(2)},
	{3, "beta", 30, true, day(3)},
	{4, "Gamma!x", 10, false, day(1)},
	{5, "delta", 20, true, day(5)},
}

var children = []struct {
	id, pid int64
	name    string
}{{1, 1, "crm"}, {2, 1, "pms"}, {3, 3, "crm"}, {4, 5, "hr"}}

var schema = &fsql.Schema{
	Columns: map[string]fsql.Column{
		"name":    {SQL: "p.name", Kind: filter.String},
		"size":    {SQL: "p.size", Kind: filter.Int},
		"active":  {SQL: "p.active", Kind: filter.Bool},
		"created": {SQL: "p.created", Kind: filter.Time},
	},
	Relations: map[string]fsql.Relation{
		"children": {
			From: "child c",
			Join: "c.pid = p.id",
			Schema: &fsql.Schema{Columns: map[string]fsql.Column{
				"name": {SQL: "c.name", Kind: filter.String},
			}},
		},
	},
	Sortable: []string{"name", "size", "active", "created"},
	Key:      fsql.Column{SQL: "p.id", Kind: filter.Int},
}

func openDBs(t *testing.T) []testDB {
	t.Helper()
	sqlite, err := dbsql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Every connection of :memory: is its own database.
	sqlite.SetMaxOpenConns(1)
	dbs := []testDB{{"sqlite", fsql.SQLite, sqlite}}
	if dsn := os.Getenv("FILTRIFY_TEST_MYSQL_DSN"); dsn != "" {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		db, err := dbsql.Open("mysql", dsn+sep+"parseTime=true&loc=UTC")
		if err != nil {
			t.Fatal(err)
		}
		dbs = append(dbs, testDB{"mysql", fsql.MySQL, db})
	}
	if dsn := os.Getenv("FILTRIFY_TEST_POSTGRES_DSN"); dsn != "" {
		db, err := dbsql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		dbs = append(dbs, testDB{"postgres", fsql.Postgres, db})
	}
	ctx := context.Background()
	for _, d := range dbs {
		t.Cleanup(func() { d.db.Close() })
		for _, stmt := range []string{"DROP TABLE IF EXISTS child", "DROP TABLE IF EXISTS parent"} {
			if _, err := d.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
		}
		for _, stmt := range ddl[d.dialect] {
			if _, err := d.db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
		}
		for _, p := range parents {
			if _, err := d.db.ExecContext(ctx, fsql.Rebind(d.dialect, "INSERT INTO parent VALUES (?, ?, ?, ?, ?)"),
				p.id, p.name, p.size, p.active, p.created); err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
		}
		for _, c := range children {
			if _, err := d.db.ExecContext(ctx, fsql.Rebind(d.dialect, "INSERT INTO child VALUES (?, ?, ?)"),
				c.id, c.pid, c.name); err != nil {
				t.Fatalf("%s: %v", d.name, err)
			}
		}
	}
	return dbs
}

// ids runs SELECT p.id with the conditions, in id order.
func ids(t *testing.T, d testDB, conds ...fsql.Cond) []int64 {
	t.Helper()
	where, args := fsql.Where(conds...)
	rows, err := d.db.Query(fsql.Rebind(d.dialect, "SELECT p.id FROM parent p"+where+" ORDER BY p.id"), args...)
	if err != nil {
		t.Fatalf("%s: %v", d.name, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

func TestDatabases(t *testing.T) {
	ctx := context.Background()
	dbs := openDBs(t)
	t.Logf("databases: %v", func() (n []string) {
		for _, d := range dbs {
			n = append(n, d.name)
		}
		return
	}())

	filters := []struct {
		filter string
		want   []int64
	}{
		{`name = "Acme"`, []int64{1}},
		{`name != "Acme"`, []int64{2, 3, 4, 5}},
		{`contains(name, "ACME")`, []int64{1, 2}},
		{`contains(name, "_50%")`, []int64{2}},
		{`contains(name, "e_")`, []int64{2}},
		{`contains(name, "a!x")`, []int64{4}},
		{`contains(name, "%")`, []int64{2}},
		{`size > 10 AND active = true`, []int64{3, 5}},
		{`in(size, [10, 30])`, []int64{1, 3, 4}},
		{`NOT active = true`, []int64{2, 4}},
		{`active = false OR size >= 30`, []int64{2, 3, 4}},
		{`created >= "2026-01-02" AND created < "2026-01-05T00:00:00Z"`, []int64{2, 3}},
		{`created = "2026-01-01T00:00:00Z"`, []int64{1, 4}},
		// Offsets: 2025-12-31T23:00Z and 2026-01-01T23:00Z. SQLite compares
		// times as text, which only works once the value is in UTC.
		{`created > "2026-01-01T01:00:00+02:00"`, []int64{1, 2, 3, 4, 5}},
		{`created < "2026-01-02T01:00:00+02:00"`, []int64{1, 4}},
		{`has(children, name = "crm")`, []int64{1, 3}},
		{`NOT has(children, name = "crm" OR name = "hr")`, []int64{2, 4}},
		{`has(children, name = "pms") OR size = 30`, []int64{1, 3}},
		{`in(name, ["beta", "delta", "nope"]) AND NOT has(children, name = "hr")`, []int64{3}},
	}

	type walk struct {
		order []*paginatev1.OrderField
		want  []int64
	}
	asc := func(f string) *paginatev1.OrderField { return &paginatev1.OrderField{Field: f} }
	desc := func(f string) *paginatev1.OrderField {
		return &paginatev1.OrderField{Field: f, Direction: paginatev1.Direction_DIRECTION_DESC}
	}
	walks := []walk{
		{[]*paginatev1.OrderField{asc("size")}, []int64{1, 4, 2, 5, 3}},
		{[]*paginatev1.OrderField{desc("size")}, []int64{3, 5, 2, 4, 1}},
		{[]*paginatev1.OrderField{desc("created"), asc("size")}, []int64{5, 3, 2, 1, 4}},
		{[]*paginatev1.OrderField{asc("created"), desc("size")}, []int64{4, 1, 2, 3, 5}},
		{[]*paginatev1.OrderField{asc("active"), desc("created")}, []int64{2, 4, 5, 3, 1}},
		{nil, nil}, // by name: collation-dependent, checked below
	}

	for _, d := range dbs {
		t.Run(d.name, func(t *testing.T) {
			for _, tt := range filters {
				t.Run(tt.filter, func(t *testing.T) {
					expr, err := filter.Parse(ctx, tt.filter)
					if err != nil {
						t.Fatal(err)
					}
					cond, err := schema.Cond(expr)
					if err != nil {
						t.Fatal(err)
					}
					if got := ids(t, d, cond); !slices.Equal(got, tt.want) {
						t.Errorf("got %v, want %v\nSQL: %s %v", got, tt.want, cond.SQL, cond.Args)
					}
				})
			}

			pageWalk := func(t *testing.T, order []*paginatev1.OrderField, size int32, conds ...fsql.Cond) []int64 {
				t.Helper()
				var out []int64
				after := ""
				for range 20 {
					page, err := schema.Page(order, &paginatev1.Paging{First: size, After: after}, "name")
					if err != nil {
						t.Fatal(err)
					}
					all := slices.Clone(conds)
					if c, ok := page.After(); ok {
						all = append(all, c)
					}
					where, args := fsql.Where(all...)
					stmt := fmt.Sprintf("SELECT p.id, %s FROM parent p%s ORDER BY %s LIMIT %d",
						strings.Join(page.Columns(), ", "), where, page.OrderBy(), page.Size()+1)
					rows, err := d.db.Query(fsql.Rebind(d.dialect, stmt), args...)
					if err != nil {
						t.Fatalf("%v\nSQL: %s", err, stmt)
					}
					n, last, more := 0, "", false
					for rows.Next() {
						if n == page.Size() {
							more = true // the extra row: another page follows
							break
						}
						var id int64
						dest := page.Dest()
						if err := rows.Scan(append([]any{&id}, dest...)...); err != nil {
							t.Fatal(err)
						}
						out = append(out, id)
						last = page.Cursor(dest)
						n++
					}
					rows.Close()
					if !more {
						return out
					}
					after = last
				}
				t.Fatal("no last page")
				return nil
			}

			for _, w := range walks {
				for _, size := range []int32{1, 2, 3, 10} {
					got := pageWalk(t, w.order, size)
					if w.want == nil {
						// Name order follows the collation: check that every
						// row comes once and descending reverses ascending.
						rev := pageWalk(t, []*paginatev1.OrderField{desc("name")}, size)
						sorted := slices.Sorted(slices.Values(got))
						slices.Reverse(rev)
						if !slices.Equal(sorted, []int64{1, 2, 3, 4, 5}) || !slices.Equal(got, rev) {
							t.Errorf("by name, size %d: asc %v, reversed desc %v", size, got, rev)
						}
						continue
					}
					if !slices.Equal(got, w.want) {
						t.Errorf("order %v, size %d: got %v, want %v", w.order, size, got, w.want)
					}
				}
			}

			// Filter, paging and count together.
			expr, _ := filter.Parse(ctx, `has(children, name = "crm" OR name = "hr")`)
			cond, err := schema.Cond(expr)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := pageWalk(t, []*paginatev1.OrderField{desc("size")}, 1, cond), []int64{3, 5, 1}; !slices.Equal(got, want) {
				t.Errorf("filtered walk: got %v, want %v", got, want)
			}
			where, args := fsql.Where(cond)
			var count int
			if err := d.db.QueryRow(fsql.Rebind(d.dialect, "SELECT COUNT(*) FROM parent p"+where), args...).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 3 {
				t.Errorf("count %d, want 3", count)
			}
		})
	}
}

func TestRebind(t *testing.T) {
	stmt := "SELECT 1 WHERE a = ? AND b IN (?, ?)"
	if got := fsql.Rebind(fsql.Postgres, stmt); got != "SELECT 1 WHERE a = $1 AND b IN ($2, $3)" {
		t.Errorf("postgres: %s", got)
	}
	for _, d := range []fsql.Dialect{fsql.MySQL, fsql.SQLite} {
		if got := fsql.Rebind(d, stmt); got != stmt {
			t.Errorf("%s: %s", d, got)
		}
	}
	if w, a := fsql.Where(); w != "" || a != nil {
		t.Errorf("empty where: %q %v", w, a)
	}
}
