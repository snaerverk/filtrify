package dbtest_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/snaerverk/filtrify/pkg/filter"
	fsql "github.com/snaerverk/filtrify/pkg/filter/sql"
	paginatev1 "github.com/snaerverk/filtrify/pkg/generated/filtrify/paginate/v1"
	"github.com/snaerverk/filtrify/pkg/paginate"
	"github.com/snaerverk/protoc-gen-go-sqlmap/pkg/query"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// parentDesc is a message for the parent table, built at runtime so the
// test needs no generated code.
var parentDesc = func() protoreflect.MessageDescriptor {
	field := func(name string, n int32, t descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{Name: proto.String(name), Number: proto.Int32(n), Type: t.Enum()}
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("dbtest/parent.proto"),
		Package: proto.String("dbtest"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Parent"),
			Field: []*descriptorpb.FieldDescriptorProto{
				field("id", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				field("name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				field("size", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
			},
		}},
	}, nil)
	if err != nil {
		panic(err)
	}
	return fd.Messages().Get(0)
}()

// parentQuerier stands in for sqlmap's generated LoadParent and filter
// schema; the key is filterable, as generated schemas declare it.
type parentQuerier struct{}

func (parentQuerier) Load(ctx context.Context, c query.Conn, _ *fieldmaskpb.FieldMask, conds ...query.Cond) ([]*dynamicpb.Message, error) {
	rows, err := c.Select(ctx, "parent p", []string{"p.id", "p.name", "p.size"}, conds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*dynamicpb.Message
	for rows.Next() {
		var id, size int64
		var name string
		if err := rows.Scan(&id, &name, &size); err != nil {
			return nil, err
		}
		m := dynamicpb.NewMessage(parentDesc)
		m.Set(parentDesc.Fields().ByName("id"), protoreflect.ValueOfInt64(id))
		m.Set(parentDesc.Fields().ByName("name"), protoreflect.ValueOfString(name))
		m.Set(parentDesc.Fields().ByName("size"), protoreflect.ValueOfInt64(size))
		out = append(out, m)
	}
	return out, rows.Err()
}

func (parentQuerier) Filter() *fsql.Schema {
	s := *schema
	s.Columns = maps.Clone(schema.Columns)
	s.Columns["id"] = fsql.Column{SQL: "p.id", Kind: filter.Int}
	return &s
}

func (parentQuerier) Table() string { return "parent p" }

// keylessQuerier is parent without a key, like a join table.
type keylessQuerier struct{ parentQuerier }

func (keylessQuerier) Filter() *fsql.Schema {
	s := *schema
	s.Key = fsql.Column{}
	return &s
}

var queryDialects = map[fsql.Dialect]query.Dialect{
	fsql.SQLite: query.SQLite, fsql.MySQL: query.MySQL, fsql.Postgres: query.Postgres,
}

func anyIDs(t *testing.T, as []*anypb.Any) []int64 {
	t.Helper()
	out := []int64{}
	for _, a := range as {
		m := dynamicpb.NewMessage(parentDesc)
		if err := a.UnmarshalTo(m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m.Get(parentDesc.Fields().ByName("id")).Int())
	}
	return out
}

// listStream collects what List sends.
type listStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent []*anypb.Any
}

func (s *listStream) Context() context.Context { return s.ctx }
func (s *listStream) Send(a *anypb.Any) error  { s.sent = append(s.sent, a); return nil }

func TestRepo(t *testing.T) {
	ctx := context.Background()
	for _, d := range openDBs(t) {
		t.Run(d.name, func(t *testing.T) {
			r := paginate.NewRepo(query.Conn{DB: d.db, Dialect: queryDialects[d.dialect]})
			paginate.Register(r, "parent", parentQuerier{})
			paginate.Register(r, "keyless", keylessQuerier{})

			search, err := filter.Parse(ctx, `size >= 20`)
			if err != nil {
				t.Fatal(err)
			}
			bySizeDesc := []*paginatev1.OrderField{{Field: "size", Direction: paginatev1.Direction_DIRECTION_DESC}}

			// Size desc, ties by id desc: 3 (30), 5, 2 (20), 4, 1 (10).
			var got []int64
			var after string
			for i := 0; ; i++ {
				resp, err := r.Query(ctx, &paginatev1.QueryRequest{
					Entity: "parent", Order: bySizeDesc,
					Paging: &paginatev1.Paging{First: 2, After: after},
				})
				if err != nil {
					t.Fatal(err)
				}
				if resp.GetTotal() != 5 {
					t.Errorf("total = %d, want 5", resp.GetTotal())
				}
				if resp.GetPage().GetHasPrevious() != (i > 0) {
					t.Errorf("page %d: has_previous = %v", i, resp.GetPage().GetHasPrevious())
				}
				got = append(got, anyIDs(t, resp.GetData())...)
				if !resp.GetPage().GetHasNext() {
					break
				}
				after = resp.GetPage().GetLastCursor()
			}
			if want := []int64{3, 5, 2, 4, 1}; !slices.Equal(got, want) {
				t.Errorf("pages = %v, want %v", got, want)
			}

			resp, err := r.Query(ctx, &paginatev1.QueryRequest{Entity: "parent", Search: search})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := anyIDs(t, resp.GetData()), []int64{2, 3, 5}; !slices.Equal(got, want) || resp.GetTotal() != 3 {
				t.Errorf("search: got %v total %d, want %v total 3", got, resp.GetTotal(), want)
			}

			s := &listStream{ctx: ctx}
			if err := r.List(&paginatev1.ListRequest{Entity: "parent", Order: bySizeDesc, Paging: &paginatev1.Paging{First: 4}}, s); err != nil {
				t.Fatal(err)
			}
			if got, want := anyIDs(t, s.sent), []int64{3, 5, 2, 4}; !slices.Equal(got, want) {
				t.Errorf("list: got %v, want %v", got, want)
			}

			s = &listStream{ctx: ctx}
			if err := r.List(&paginatev1.ListRequest{Entity: "keyless", Search: search}, s); err != nil {
				t.Fatal(err)
			}
			got = anyIDs(t, s.sent)
			slices.Sort(got)
			if want := []int64{2, 3, 5}; !slices.Equal(got, want) {
				t.Errorf("keyless list: got %v, want %v", got, want)
			}

			bad, _ := filter.Parse(ctx, `nope = 1`)
			for name, tc := range map[string]struct {
				err  error
				want codes.Code
			}{
				"unknown entity": {r.List(&paginatev1.ListRequest{Entity: "nope"}, &listStream{ctx: ctx}), codes.NotFound},
				"bad search": {func() error {
					_, err := r.Query(ctx, &paginatev1.QueryRequest{Entity: "parent", Search: bad})
					return err
				}(), codes.InvalidArgument},
				"bad cursor": {func() error {
					_, err := r.Query(ctx, &paginatev1.QueryRequest{Entity: "parent", Paging: &paginatev1.Paging{After: "x"}})
					return err
				}(), codes.InvalidArgument},
				"keyless query": {func() error { _, err := r.Query(ctx, &paginatev1.QueryRequest{Entity: "keyless"}); return err }(), codes.FailedPrecondition},
				"keyless order": {r.List(&paginatev1.ListRequest{Entity: "keyless", Order: bySizeDesc}, &listStream{ctx: ctx}), codes.InvalidArgument},
			} {
				if got := status.Code(tc.err); got != tc.want {
					t.Errorf("%s: code %v, want %v (%v)", name, got, tc.want, tc.err)
				}
			}
		})
	}
}
