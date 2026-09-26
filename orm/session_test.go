package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type sessionCacheModel struct{ ID int64 }
type sessionCacheDescriptor struct{ clone, scan func() }

func (sessionCacheDescriptor) Metadata() ir.Model {
	return ir.Model{Name: "record", GoName: "Record", DBTable: "records", Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}}
}
func (d sessionCacheDescriptor) Scan(row db.Row) (sessionCacheModel, error) {
	var v sessionCacheModel
	err := row.Scan(&v.ID)
	if d.scan != nil {
		d.scan()
	}
	return v, err
}
func (d sessionCacheDescriptor) CloneModel(v sessionCacheModel) sessionCacheModel {
	if d.clone != nil {
		d.clone()
	}
	return v
}

type sessionCacheRows struct{ step int }

func (r *sessionCacheRows) Next() bool { r.step++; return r.step == 1 }
func (*sessionCacheRows) Scan(dest ...any) error {
	switch target := dest[0].(type) {
	case *int64:
		*target = 7
	case *any:
		*target = int64(7)
	case sql.Scanner:
		return target.Scan(int64(7))
	default:
		return errors.New("unexpected scalar destination")
	}
	return nil
}
func (*sessionCacheRows) Close() error { return nil }
func (*sessionCacheRows) Err() error   { return nil }

type sessionCacheBackend struct {
	allowed int
	queries int
	closed  error
}

func (b *sessionCacheBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	b.queries++
	return &sessionCacheRows{}, nil
}
func (b *sessionCacheBackend) ValidateSession(context.Context) error {
	if b.allowed == 0 {
		return b.closed
	}
	if b.allowed > 0 {
		b.allowed--
	}
	return nil
}
func TestBorrowedQueryCacheRechecksSessionBeforePublishing(t *testing.T) {
	for _, method := range []string{"All", "Count", "Exists", "First", "At", "AbsentAt", "Clone"} {
		t.Run(method, func(t *testing.T) {
			closed := errors.New("borrowed session ended")
			backend := &sessionCacheBackend{allowed: -1, closed: closed}
			descriptor := sessionCacheDescriptor{}
			closeDuringClone := false
			descriptor.clone = func() {
				if closeDuringClone {
					backend.allowed = 0
				}
			}
			qs := NewManager[sessionCacheModel](descriptor).Using(backend).OrderBy(NewAutoField[sessionCacheModel](descriptor.Metadata().Fields[0]).Asc())
			if values, err := qs.All(t.Context()); err != nil || len(values) != 1 {
				t.Fatal(values, err)
			}
			if method == "Clone" {
				closeDuringClone = true
			} else {
				backend.allowed = 1
			}
			var err error
			switch method {
			case "All", "Clone":
				var values []sessionCacheModel
				values, err = qs.All(t.Context())
				if values != nil {
					t.Fatal("expired values published", values)
				}
			case "Count":
				var count int64
				count, err = qs.Count(t.Context())
				if count != 0 {
					t.Fatal(count)
				}
			case "Exists":
				var found bool
				found, err = qs.Exists(t.Context())
				if found {
					t.Fatal("expired existence published")
				}
			case "First", "At", "AbsentAt":
				var value sessionCacheModel
				var found bool
				if method == "First" {
					value, found, err = qs.First(t.Context())
				} else {
					index := 0
					if method == "AbsentAt" {
						index = 9
					}
					value, found, err = qs.At(t.Context(), index)
				}
				if found || value != (sessionCacheModel{}) {
					t.Fatal("expired row published", value, found)
				}
			}
			if !errors.Is(err, closed) {
				t.Fatal("session cause lost", err)
			}
			if backend.queries != 1 {
				t.Fatal("warm cache unexpectedly performed I/O", backend.queries)
			}
		})
	}
}

func TestBorrowedProjectionAndAggregateRejectExpiredDecoderResult(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprint(aggregate), func(t *testing.T) {
			closed := errors.New("session ended during decoder")
			backend := &sessionCacheBackend{allowed: -1, closed: closed}
			descriptor := sessionCacheDescriptor{}
			source := NewManager[sessionCacheModel](descriptor).Using(backend)
			build := func(value int64) int64 { backend.allowed = 0; return value }
			if aggregate {
				value, err := AggregateInto(t.Context(), source, Aggregate1(CountRows[sessionCacheModel](), build))
				if value != 0 || !errors.Is(err, closed) {
					t.Fatal("expired aggregate published", value, err)
				}
			} else {
				field := NewAutoField[sessionCacheModel](descriptor.Metadata().Fields[0])
				values, err := SelectInto(t.Context(), source, Project1(field, build))
				if values != nil || !errors.Is(err, closed) {
					t.Fatal("expired projection published", values, err)
				}
			}
		})
	}
}

func TestBorrowedIterationRejectsExpiredDecodedRow(t *testing.T) {
	for _, during := range []string{"scan", "clone"} {
		t.Run(during, func(t *testing.T) {
			closed := errors.New("session ended during iterator decoding")
			backend := &sessionCacheBackend{allowed: -1, closed: closed}
			descriptor := sessionCacheDescriptor{}
			end := func() { backend.allowed = 0 }
			if during == "scan" {
				descriptor.scan = end
			} else {
				descriptor.clone = end
			}
			source := NewManager[sessionCacheModel](descriptor).Using(backend)
			calls := 0
			err := source.Iterate(t.Context(), func(sessionCacheModel) error { calls++; return nil })
			if calls != 0 || !errors.Is(err, closed) {
				t.Fatalf("expired iteration row reached callback: calls=%d err=%v", calls, err)
			}
		})
	}
}

type reversePrefetchSessionBackend struct {
	db.Queryer
	lifetime *sessionCacheBackend
}

func (b reversePrefetchSessionBackend) ValidateSession(ctx context.Context) error {
	return b.lifetime.ValidateSession(ctx)
}
func TestReversePrefetchRejectsEmptyExpiredSession(t *testing.T) {
	prefetch := bindReversePrefetchTestRelation(t, "posts")
	closed := errors.New("borrowed session ended")
	backend := &sessionCacheBackend{allowed: -1, closed: closed}
	if values, err := prefetch.Load(t.Context(), backend, nil); err != nil || len(values) != 0 {
		t.Fatal(values, err)
	}
	backend.allowed = 0
	if values, err := prefetch.Load(t.Context(), backend, nil); values != nil || !errors.Is(err, closed) {
		t.Fatal("expired empty prefetch published", values, err)
	}
	if backend.queries != 0 {
		t.Fatal("empty prefetch queried")
	}
}
func TestReversePrefetchRejectsSessionEndingWhileGrouping(t *testing.T) {
	prefetch := bindReversePrefetchWithDescriptors(t, relationObjectTestAuthorDescriptor{}, reversePrefetchCancelStoragePostDescriptor{})
	closed := errors.New("borrowed session ended while grouping")
	lifetime := &sessionCacheBackend{allowed: -1, closed: closed}
	backend := reversePrefetchSessionBackend{lifetime: lifetime, Queryer: &reversePostBackend{query: func(_ int, _ context.Context, _ query.Plan) (db.Rows, error) {
		return &reversePostRows{values: []relationObjectTestPost{{ID: 10, AuthorID: 1}}}, nil
	}}}
	reversePrefetchCancelStorageHook = func() { lifetime.allowed = 0 }
	defer func() { reversePrefetchCancelStorageHook = nil }()
	if values, err := prefetch.Load(t.Context(), backend, []relationObjectTestAuthor{{ID: 1}}); values != nil || !errors.Is(err, closed) {
		t.Fatal("expired grouped prefetch published", values, err)
	}
}
