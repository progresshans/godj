package orm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestGetFreshReadPreservesWarmCacheAndOwnsNullableValues(t *testing.T) {
	backend := &cacheTestBackend{query: func(call int, _ context.Context, plan query.Plan) (db.Rows, error) {
		note := fmt.Sprintf("stored-%d", call)
		return &cacheTestRows{values: []cacheTestModel{{ID: int64(call + 1), Note: &note}}}, nil
	}}
	source := newCacheTestManager().Using(backend)
	cached, err := source.All(t.Context())
	if err != nil || len(cached) != 1 {
		t.Fatalf("prime cache: %v, %v", cached, err)
	}
	for expected := int64(2); expected <= 3; expected++ {
		value, err := source.Get(t.Context())
		if err != nil || value.ID != expected || value.Note == nil || value.Note == cached[0].Note {
			t.Fatalf("Get = %#v, %v", value, err)
		}
		*value.Note = "caller mutation"
		warm, err := source.All(t.Context())
		if err != nil || len(warm) != 1 || warm[0].ID != 1 || *warm[0].Note != "stored-0" {
			t.Fatalf("Get changed the base cache: %#v, %v", warm, err)
		}
	}
	if backend.callCount() != 3 {
		t.Fatalf("fresh reads made %d calls, want 3", backend.callCount())
	}
}

func TestGetCardinalityAndIndependentCalls(t *testing.T) {
	for _, count := range []int{0, 1, 2, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ids := make([]int64, count)
			for index := range ids {
				ids[index] = int64(index + 1)
			}
			backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rowsForIDs(ids...), nil }}
			source := newCacheTestManager().Using(backend)
			for range 2 {
				value, err := source.Get(t.Context())
				switch count {
				case 0:
					if !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) || value.ID != 0 {
						t.Fatalf("missing result = %#v, %v", value, err)
					}
				case 1:
					if err != nil || value.ID != 1 {
						t.Fatalf("single result = %#v, %v", value, err)
					}
				default:
					if !errors.Is(err, &query.Error{Code: query.CodeMultipleObjectsReturned}) || value.ID != 0 {
						t.Fatalf("multiple result = %#v, %v", value, err)
					}
				}
			}
			if backend.callCount() != 2 {
				t.Fatalf("Get reused a previous result: %d calls", backend.callCount())
			}
			if _, ready := source.evaluation.cachedValues(); ready {
				t.Fatal("Get populated the original cache")
			}
		})
	}
	t.Run("concurrent calls have separate evaluations", func(t *testing.T) {
		backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
			return rowsForIDs(int64(call + 1)), nil
		}}
		source := newCacheTestManager().Using(backend)
		const workers = 12
		results := make(chan int64, workers)
		var group sync.WaitGroup
		for range workers {
			group.Go(func() {
				value, err := source.Get(t.Context())
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				results <- value.ID
			})
		}
		group.Wait()
		close(results)
		seen := map[int64]bool{}
		for value := range results {
			seen[value] = true
		}
		if len(seen) != workers || backend.callCount() != workers {
			t.Fatalf("separate Get calls shared a flight: ids=%v calls=%d", seen, backend.callCount())
		}
	})
}

func TestGetPreservesExplicitSlicesAndValidatesDiscardedOrdering(t *testing.T) {
	metadata := (cacheTestDescriptor{}).Metadata()
	ordering := NewAutoField[cacheTestModel](metadata.Fields[0]).Desc()
	for _, test := range []struct {
		name          string
		limit, offset int
		sliced        bool
		expectedLimit int
		ordered       bool
	}{
		{name: "unsliced", limit: -1, expectedLimit: 21},
		{name: "small limit", limit: 1, sliced: true, expectedLimit: 1, ordered: true},
		{name: "empty limit", limit: 0, sliced: true, expectedLimit: 0, ordered: true},
		{name: "large limit", limit: 50, sliced: true, expectedLimit: 21, ordered: true},
		{name: "offset", limit: -1, offset: 3, expectedLimit: 21, ordered: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &cacheTestBackend{query: func(_ int, _ context.Context, plan query.Plan) (db.Rows, error) {
				if plan.EmptyResult() {
					return rowsForIDs(), nil
				}
				return rowsForIDs(7), nil
			}}
			source := newCacheTestManager().Using(backend).OrderBy(ordering)
			var err error
			if test.sliced {
				source, err = source.Limit(test.limit)
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.offset != 0 {
				source, err = source.Offset(test.offset)
				if err != nil {
					t.Fatal(err)
				}
			}
			original := source.Plan()
			_, err = source.Get(t.Context())
			if test.expectedLimit == 0 {
				if !errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
					t.Fatalf("empty slice = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			plan := backend.plan(0)
			limit, limited := plan.Limit()
			offset, _ := plan.Offset()
			if !limited || limit != test.expectedLimit || offset != test.offset || (len(plan.Orderings()) != 0) != test.ordered || !source.Plan().Equal(original) {
				t.Fatalf("Get changed slice/source semantics: limit=%d offset=%d ordering=%v", limit, offset, plan.Orderings())
			}
		})
	}
	backend := &cacheTestBackend{}
	foreign := metadata.Fields[0]
	foreign.Column = "foreign_id"
	source := newCacheTestManager().Using(backend).OrderBy(NewAutoField[cacheTestModel](foreign).Asc())
	if _, err := source.Get(t.Context()); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) || backend.callCount() != 0 {
		t.Fatalf("discarded foreign ordering escaped validation: %v, calls=%d", err, backend.callCount())
	}
}

func TestGetDoesNotTurnReadCleanupOrExpiredSessionIntoCardinality(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			readErr, closeErr := errors.New("read failed"), errors.New("close failed")
			rows := &cacheTestRows{values: make([]cacheTestModel, count), rowsErr: readErr, closeErr: closeErr}
			backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
			_, err := newCacheTestManager().Using(backend).Get(t.Context())
			if !errors.Is(err, readErr) || !errors.Is(err, closeErr) || errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) || errors.Is(err, &query.Error{Code: query.CodeMultipleObjectsReturned}) {
				t.Fatalf("Get masked query/cleanup failure: %v", err)
			}
			if rows.closeCalls.Load() != 1 {
				t.Fatalf("rows closed %d times", rows.closeCalls.Load())
			}
		})
	}
	t.Run("session expires when its rows close", func(t *testing.T) {
		expired := errors.New("borrowed session expired")
		backend := &getSessionBackend{expired: expired}
		backend.active.Store(true)
		backend.rows = &getClosingRows{cacheTestRows: rowsForIDs(), close: func() { backend.active.Store(false) }}
		_, err := newCacheTestManager().Using(backend).Get(t.Context())
		if !errors.Is(err, expired) || errors.Is(err, &query.Error{Code: query.CodeDoesNotExist}) {
			t.Fatalf("Get masked expired session: %v", err)
		}
	})
	t.Run("context cancels during close", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		backend := &cacheTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) {
			return &getClosingRows{cacheTestRows: rowsForIDs(1), close: cancel}, nil
		}}
		if _, err := newCacheTestManager().Using(backend).Get(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("Get after cancellation = %v", err)
		}
	})
}

type getClosingRows struct {
	*cacheTestRows
	close func()
}

func (rows *getClosingRows) Close() error { rows.close(); return rows.cacheTestRows.Close() }

type getSessionBackend struct {
	active  atomic.Bool
	expired error
	rows    db.Rows
}

func (backend *getSessionBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return backend.rows, nil
}
func (backend *getSessionBackend) ValidateSession(context.Context) error {
	if !backend.active.Load() {
		return backend.expired
	}
	return nil
}
