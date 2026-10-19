// Package paginate serves the PaginateService over entities read through
// sqlmap's generated loaders, filtered and paged with their generated
// filtrify schemas.
//
// A page takes two queries. The first selects only the sort columns of the
// next rows, in order and LIMITed, which yields their keys and cursors. The
// second loads those rows by key through the entity's Load, which applies
// the field mask and eager loading, and the results are put back in page
// order. Load itself can't order or limit, hence the split.
package paginate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/snaerverk/filtrify/pkg/filter"
	"github.com/snaerverk/filtrify/pkg/filter/sql"
	astv1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/ast/v1"
	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
	"github.com/snaerverk/protoc-gen-go-sqlmap/pkg/query"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Querier is the read surface of an entity. A small adapter over the
// generated code implements it, e.g.
//
//	type quotaQuerier struct{}
//
//	func (quotaQuerier) Load(ctx context.Context, c query.Conn, mask *fieldmaskpb.FieldMask, conds ...query.Cond) ([]*quotav1.Quota, error) {
//		return quotav1.LoadQuota(ctx, c, mask, conds...)
//	}
//	func (quotaQuerier) Filter() *sql.Schema { return quotav1.QuotaFilter }
//	func (quotaQuerier) Table() string       { return "quota" }
type Querier[E proto.Message] interface {
	// Load is the entity's generated Load<Msg>.
	Load(ctx context.Context, c query.Conn, mask *fieldmaskpb.FieldMask, conds ...query.Cond) ([]E, error)
	// Filter is the entity's generated <Msg>Filter. Entities whose schema
	// has no Key (e.g. join tables) can be listed but not ordered or paged.
	Filter() *sql.Schema
	// Table is the FROM clause the Filter's columns are qualified against.
	Table() string
}

// Repo implements paginatev1.PaginateServiceServer over the entities
// registered with Register.
type Repo struct {
	paginatev1.UnimplementedPaginateServiceServer
	conn query.Conn
	ents map[string]*entity
}

// NewRepo returns a Repo that queries through c.
func NewRepo(c query.Conn) *Repo {
	return &Repo{conn: c, ents: map[string]*entity{}}
}

// entity is a Querier with its element type erased.
type entity struct {
	load   func(ctx context.Context, c query.Conn, mask *fieldmaskpb.FieldMask, conds ...query.Cond) ([]proto.Message, error)
	schema *sql.Schema
	table  string
	// key is the message field holding schema.Key, "" for keyless entities.
	key protoreflect.Name
}

// Register serves q as entity name, the value requests put in their entity
// field. It panics if name is taken or if the schema's Key is not one of its
// Columns, as the Key's field is how loaded rows are put in page order.
func Register[E proto.Message](r *Repo, name string, q Querier[E]) {
	if _, ok := r.ents[name]; ok {
		panic(fmt.Sprintf("paginate: entity %q registered twice", name))
	}
	e := &entity{
		load: func(ctx context.Context, c query.Conn, mask *fieldmaskpb.FieldMask, conds ...query.Cond) ([]proto.Message, error) {
			rows, err := q.Load(ctx, c, mask, conds...)
			if err != nil {
				return nil, err
			}
			out := make([]proto.Message, len(rows))
			for i, row := range rows {
				out[i] = row
			}
			return out, nil
		},
		schema: q.Filter(),
		table:  q.Table(),
	}
	if e.schema.Key.SQL != "" {
		for field, col := range e.schema.Columns {
			if col.SQL == e.schema.Key.SQL {
				e.key = protoreflect.Name(field)
			}
		}
		if e.key == "" {
			panic(fmt.Sprintf("paginate: entity %q: no column of the schema is its Key %s", name, e.schema.Key.SQL))
		}
	}
	r.ents[name] = e
}

// Query returns one page of the entity's rows matching the search.
func (r *Repo) Query(ctx context.Context, req *paginatev1.QueryRequest) (*paginatev1.QueryResponse, error) {
	e, err := r.entity(req.GetEntity())
	if err != nil {
		return nil, err
	}
	if e.key == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "entity %q has no key, so it can't be paged: use List", req.GetEntity())
	}
	search, err := e.search(req.GetSearch())
	if err != nil {
		return nil, err
	}
	page, err := e.schema.Page(req.GetOrder(), req.GetPaging(), sql.KeyField)
	if err != nil {
		return nil, statusErr(err)
	}
	rows, info, err := r.page(ctx, e, req.GetFieldmask(), search, page)
	if err != nil {
		return nil, err
	}
	total, err := r.count(ctx, e, search)
	if err != nil {
		return nil, err
	}
	resp := &paginatev1.QueryResponse{Total: total, Page: info.Proto()}
	for _, row := range rows {
		a, err := anypb.New(row)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "%s: %v", req.GetEntity(), err)
		}
		resp.Data = append(resp.Data, a)
	}
	return resp, nil
}

// List streams the entity's rows matching the search, in order, starting
// after paging.after. paging.first caps the number of rows; 0 streams all.
func (r *Repo) List(req *paginatev1.ListRequest, stream grpc.ServerStreamingServer[anypb.Any]) error {
	ctx := stream.Context()
	e, err := r.entity(req.GetEntity())
	if err != nil {
		return err
	}
	search, err := e.search(req.GetSearch())
	if err != nil {
		return err
	}
	send := func(rows []proto.Message) error {
		for _, row := range rows {
			a, err := anypb.New(row)
			if err != nil {
				return status.Errorf(codes.Internal, "%s: %v", req.GetEntity(), err)
			}
			if err := stream.Send(a); err != nil {
				return err
			}
		}
		return nil
	}

	if e.key == "" {
		if len(req.GetOrder()) > 0 || req.GetPaging() != nil {
			return status.Errorf(codes.InvalidArgument, "entity %q has no key, so it can't be ordered or paged", req.GetEntity())
		}
		rows, err := e.load(ctx, r.conn, req.GetFieldmask(), queryConds(search)...)
		if err != nil {
			return status.Errorf(codes.Internal, "load %s: %v", req.GetEntity(), err)
		}
		return send(rows)
	}

	remaining := int(req.GetPaging().GetFirst())
	if remaining < 0 {
		return status.Error(codes.InvalidArgument, "paging.first must not be negative")
	}
	after := req.GetPaging().GetAfter()
	for {
		size := filter.MaxPageSize
		if remaining > 0 {
			size = min(size, remaining)
		}
		page, err := e.schema.Page(req.GetOrder(), &paginatev1.Paging{First: int32(size), After: after}, sql.KeyField)
		if err != nil {
			return statusErr(err)
		}
		rows, info, err := r.page(ctx, e, req.GetFieldmask(), search, page)
		if err != nil {
			return err
		}
		if err := send(rows); err != nil {
			return err
		}
		if !info.HasNext {
			return nil
		}
		// A page with a next one is full, even if rows vanished before Load.
		if remaining > 0 {
			if remaining -= size; remaining == 0 {
				return nil
			}
		}
		after = info.LastCursor
	}
}

func (r *Repo) entity(name string) (*entity, error) {
	e, ok := r.ents[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "entity %q is not served", name)
	}
	return e, nil
}

// search translates the request's search, nil for none.
func (e *entity) search(expr *astv1.Expr) ([]sql.Cond, error) {
	if expr == nil {
		return nil, nil
	}
	c, err := e.schema.Cond(expr)
	if err != nil {
		return nil, statusErr(err)
	}
	return []sql.Cond{c}, nil
}

// page runs the keyset query for p, then loads its rows in order.
func (r *Repo) page(ctx context.Context, e *entity, mask *fieldmaskpb.FieldMask, search []sql.Cond, p *sql.Page) ([]proto.Message, filter.PageInfo, error) {
	info := filter.PageInfo{HasPrevious: p.HasPrevious()}
	conds := slices.Clip(search)
	if after, ok := p.After(); ok {
		conds = append(conds, after)
	}
	cols := p.Columns()
	where, args := sql.Where(conds...)
	stmt := "SELECT " + strings.Join(cols, ", ") + " FROM " + e.table + where +
		" ORDER BY " + p.OrderBy() + " LIMIT " + strconv.Itoa(p.Size()+1)

	keyCol := slices.Index(cols, e.schema.Key.SQL)
	var keys []any
	var cursors []string
	err := r.rows(ctx, stmt, args, func(s query.Scanner) (bool, error) {
		if len(keys) == p.Size() {
			info.HasNext = true
			return false, nil
		}
		dest := p.Dest()
		if err := s.Scan(dest...); err != nil {
			return false, err
		}
		keys = append(keys, query.Key(deref(dest[keyCol])))
		cursors = append(cursors, p.Cursor(dest))
		return true, nil
	})
	if err != nil {
		return nil, info, status.Errorf(codes.Internal, "page %s: %v", e.table, err)
	}
	if len(keys) == 0 {
		return nil, info, nil
	}
	info.FirstCursor, info.LastCursor = cursors[0], cursors[len(cursors)-1]

	byKey, _ := query.In(e.schema.Key.SQL, keys)
	loaded, err := e.load(ctx, r.conn, mask, byKey)
	if err != nil {
		return nil, info, status.Errorf(codes.Internal, "load %s: %v", e.table, err)
	}
	found := make(map[any]proto.Message, len(loaded))
	for _, m := range loaded {
		fd := m.ProtoReflect().Descriptor().Fields().ByName(e.key)
		if fd == nil {
			return nil, info, status.Errorf(codes.Internal, "%s has no key field %q", m.ProtoReflect().Descriptor().FullName(), e.key)
		}
		found[query.Key(m.ProtoReflect().Get(fd).Interface())] = m
	}
	out := make([]proto.Message, 0, len(keys))
	for _, k := range keys {
		// A row deleted between the two queries is skipped.
		if m, ok := found[k]; ok {
			out = append(out, m)
		}
	}
	return out, info, nil
}

// count returns the number of rows matching the search.
func (r *Repo) count(ctx context.Context, e *entity, search []sql.Cond) (int32, error) {
	where, args := sql.Where(search...)
	var n int64
	err := r.rows(ctx, "SELECT COUNT(*) FROM "+e.table+where, args, func(s query.Scanner) (bool, error) {
		return false, s.Scan(&n)
	})
	if err != nil {
		return 0, status.Errorf(codes.Internal, "count %s: %v", e.table, err)
	}
	return int32(n), nil
}

// rows runs stmt and calls scan for each row until it returns false.
func (r *Repo) rows(ctx context.Context, stmt string, args []any, scan func(query.Scanner) (bool, error)) error {
	if r.conn.Dialect == query.Postgres {
		stmt = sql.Rebind(sql.Postgres, stmt)
	}
	rows, err := r.conn.DB.QueryContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		more, err := scan(rows)
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func queryConds(conds []sql.Cond) []query.Cond {
	out := make([]query.Cond, len(conds))
	for i, c := range conds {
		out[i] = query.Cond(c)
	}
	return out
}

// deref returns the value behind a destination of sql.Page.Dest.
func deref(d any) any {
	switch x := d.(type) {
	case *string:
		return *x
	case *int64:
		return *x
	case *bool:
		return *x
	case *time.Time:
		return *x
	}
	return nil
}

// statusErr maps an invalid filter, order or cursor to InvalidArgument.
func statusErr(err error) error {
	if errors.Is(err, filter.ErrInvalid) {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}
