package orm

import (
	"context"
	"errors"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type materializedBatchBackend struct {
	db.Queryer
	rows     *selectRelatedRows
	size     int
	protocol string
}

func (b *materializedBatchBackend) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	b.size = size
	defer b.rows.Close()
	if b.protocol == "empty" {
		_, err := yield(b)
		return err
	}
	if b.protocol == "nil_row" {
		return scan(nil)
	}
	if b.protocol == "typed_nil_row" {
		return scan((*selectRelatedRows)(nil))
	}
	n := 0
	for b.rows.Next() {
		if err := scan(b.rows); err != nil {
			return err
		}
		n++
		if n == size {
			if b.protocol == "drop" {
				return nil
			}
			if b.protocol == "overflow" {
				continue
			}
			more, err := yield(b)
			if err != nil {
				if b.protocol == "swallow" {
					return nil
				}
				return err
			}
			if !more {
				return nil
			}
			n = 0
		}
	}
	if n > 0 {
		_, err := yield(b)
		return err
	}
	return nil
}
func TestMaterializedStreamRejectsProtocolViolationsAndPreservesCallbackFailure(t *testing.T) {
	for _, mode := range []string{"empty", "drop", "overflow", "swallow", "nil_row", "typed_nil_row"} {
		t.Run(mode, func(t *testing.T) {
			rows := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue(), selectRelatedRequiredValue()}}
			b := &materializedBatchBackend{rows: rows, protocol: mode}
			q := requiredSelectQuery(t, b)
			calls := 0
			boom := errors.New("callback")
			err := q.IterateBatches(t.Context(), 1, func(context.Context, []*RelatedSelected[relationObjectTestPost]) (bool, error) {
				calls++
				return false, boom
			})
			if err == nil || rows.closeCalls.Load() != 1 {
				t.Fatal(err, rows.closeCalls.Load())
			}
			if mode == "swallow" {
				if calls != 1 || !errors.Is(err, boom) {
					t.Fatal(calls, err)
				}
			} else if calls != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal(calls, err)
			}
		})
	}
}
func TestMaterializedStreamValidatesCompleteBatchBeforePublication(t *testing.T) {
	for _, size := range []int{1, 2} {
		t.Run(string(rune('0'+size)), func(t *testing.T) {
			invalid := selectRelatedRequiredValue()
			invalid.target = &relationObjectTestAuthor{ID: 999, Name: "foreign"}
			rows := &selectRelatedRows{values: []selectRelatedJoinedValue{selectRelatedRequiredValue(), invalid}}
			b := &materializedBatchBackend{rows: rows}
			q := requiredSelectQuery(t, b)
			calls := 0
			err := q.IterateBatches(t.Context(), size, func(context.Context, []*RelatedSelected[relationObjectTestPost]) (bool, error) {
				calls++
				return true, nil
			})
			if err == nil || calls != 2-size || rows.closeCalls.Load() != 1 {
				t.Fatal(calls, err, rows.closeCalls.Load())
			}
			if _, _, ready := q.evaluation.cachedResult(); ready {
				t.Fatal("stream populated full cache")
			}
		})
	}
}

type scopeQueryer struct{ calls int }

func (b *scopeQueryer) Query(context.Context, query.Plan) (db.Rows, error) {
	b.calls++
	return &sessionCacheRows{}, nil
}

type scopeNonComparable []int

func (scopeNonComparable) Query(context.Context, query.Plan) (db.Rows, error) { return nil, nil }

type scopeSession struct {
	*scopeQueryer
	closed error
}

func (s *scopeSession) ValidateSession(context.Context) error { return s.closed }

func TestExecutionScopeKeepsIdentityCapabilitiesAndSessionOwnership(t *testing.T) {
	original, first, second, other := &scopeQueryer{}, &scopeQueryer{}, &scopeQueryer{}, &scopeQueryer{}
	ctx, err := withExecutionScope(t.Context(), original, first)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := withExecutionScope(ctx, original, second)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		ctx         context.Context
		input, want db.Queryer
	}{{ctx, original, first}, {nested, original, second}, {nested, first, first}, {nested, other, other}, {t.Context(), original, original}} {
		got, err := executionBackend(test.ctx, test.input)
		if err != nil || got != test.want {
			t.Fatal(got, test.want, err)
		}
	}
	if _, err := withExecutionScope(ctx, scopeNonComparable{}, first); err == nil {
		t.Fatal("uncomparable identity accepted")
	}
	if _, err := withExecutionScope(ctx, original, (*scopeQueryer)(nil)); err == nil {
		t.Fatal("nil executor accepted")
	}
	borrowed := &scopeSession{scopeQueryer: original}
	if _, err := withExecutionScope(ctx, borrowed, first); err == nil {
		t.Fatal("borrowed session became root")
	}
	if _, err := withExecutionScope(ctx, original, borrowed); err == nil {
		t.Fatal("root became borrowed session")
	}
	affine := &scopeSession{scopeQueryer: first}
	scoped, err := withExecutionScope(ctx, borrowed, affine)
	if err != nil {
		t.Fatal(err)
	}
	expired := errors.New("session closed")
	borrowed.closed = expired
	if err := validateQuerySession(scoped, borrowed); !errors.Is(err, expired) {
		t.Fatal(err)
	}
	borrowed.closed = nil
	affine.closed = expired
	if err := validateQuerySession(scoped, borrowed); !errors.Is(err, expired) {
		t.Fatal(err)
	}
}

type scopeWriter struct {
	scopeQueryer
	writes int
}

func (s *scopeWriter) Insert(context.Context, query.InsertPlan) (int64, error) {
	s.writes++
	return 1, nil
}
func (s *scopeWriter) Update(context.Context, query.UpdatePlan) (int64, error) {
	s.writes++
	return 1, nil
}
func (s *scopeWriter) Delete(context.Context, query.DeletePlan) (int64, error) {
	s.writes++
	return 1, nil
}

func TestExecutionScopeNeverFallsBackToRootWritesWhenExecutorCannotWrite(t *testing.T) {
	original := &scopeWriter{}
	ctx, err := withExecutionScope(t.Context(), original, &scopeQueryer{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executionBackend[db.Mutator](ctx, original); err == nil {
		t.Fatal("read executor fell back to root writer")
	}
	if original.writes != 0 {
		t.Fatal(original.writes)
	}
}
