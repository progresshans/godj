package orm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestQuerySetPanickingLoaderReleasesRowsAndWaitersWithoutCachingPartialValues(t *testing.T) {
	for _, stage := range []string{"scan", "clone"} {
		t.Run(stage, func(t *testing.T) {
			marker := errors.New(stage + " panic")
			started, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			var calls atomic.Int32
			hook := func() {
				// Leave one fully scanned value before the panic to detect
				// accidental publication of a partially loaded cache.
				if calls.Add(1) == 2 {
					close(started)
					<-release
					panic(marker)
				}
			}
			descriptor := panicCacheTestDescriptor{}
			if stage == "scan" {
				descriptor.scan = hook
			} else {
				descriptor.clone = hook
			}
			firstRows := rowsForIDs(1, 2)
			backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
				if call == 0 {
					return firstRows, nil
				}
				if firstRows.closeCalls.Load() != 1 {
					return nil, errors.New("previous evaluation still owns its rows")
				}
				return rowsForIDs(9), nil
			}}
			qs := NewManager[cacheTestModel](descriptor).Using(backend)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			owner := make(chan any, 1)
			go func() {
				owner <- recoverTestPanic(func() { _, _ = qs.All(ctx) })
			}()
			awaitSignal(t, started, "descriptor panic boundary")
			waiterContext, entered := newEnteredContext(ctx)
			type result struct {
				values []cacheTestModel
				err    error
			}
			waiter := make(chan result, 1)
			go func() {
				values, err := qs.All(waiterContext)
				waiter <- result{values: values, err: err}
			}()
			awaitEntered(t, entered)
			releaseOnce.Do(func() { close(release) })
			if got := awaitValue(t, owner, "recovered owner panic"); got != marker {
				t.Fatalf("panic = %v, want original marker %v", got, marker)
			}
			got := awaitValue(t, waiter, "waiter retry after owner panic")
			if got.err != nil || len(got.values) != 1 || got.values[0].ID != 9 {
				t.Fatalf("waiter retry = (%#v, %v), want complete fresh result", got.values, got.err)
			}
			assertCacheTestID(t, qs, 9)
			if backend.callCount() != 2 || firstRows.closeCalls.Load() != 1 {
				t.Fatalf("backend/old rows close calls = %d/%d, want 2/1", backend.callCount(), firstRows.closeCalls.Load())
			}
		})
	}
}

func TestModelTerminalsCloseRowsAfterDescriptorOrCallbackPanic(t *testing.T) {
	for _, terminal := range []string{"at", "iterate"} {
		for _, stage := range []string{"scan", "clone", "callback"} {
			if terminal == "at" && stage == "callback" {
				continue
			}
			t.Run(terminal+"/"+stage, func(t *testing.T) {
				marker := errors.New(stage + " panic")
				var once sync.Once
				hook := func() { once.Do(func() { panic(marker) }) }
				descriptor := panicCacheTestDescriptor{}
				if stage == "scan" {
					descriptor.scan = hook
				} else if stage == "clone" {
					descriptor.clone = hook
				}
				rows := rowsForIDs(1)
				backend := &cacheTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
					if call == 0 {
						return rows, nil
					}
					if rows.closeCalls.Load() != 1 {
						return nil, errors.New("previous terminal still owns its rows")
					}
					return rowsForIDs(9), nil
				}}
				id := NewIntegerField[cacheTestModel](descriptor.Metadata().Fields[0])
				qs := NewManager[cacheTestModel](descriptor).Using(backend).OrderBy(id.Asc())
				got := recoverTestPanic(func() {
					if terminal == "at" {
						_, _, _ = qs.At(t.Context(), 0)
					} else {
						_ = qs.Iterate(t.Context(), func(cacheTestModel) error {
							if stage == "callback" {
								hook()
							}
							return nil
						})
					}
				})
				if got != marker {
					t.Fatalf("panic = %v, want original marker %v", got, marker)
				}
				assertCacheTestID(t, qs, 9)
				if rows.closeCalls.Load() != 1 {
					t.Fatalf("Close calls = %d, want 1", rows.closeCalls.Load())
				}
			})
		}
	}
}

func TestScalarTerminalsCloseRowsAfterBuilderOrIterationPanic(t *testing.T) {
	for _, terminal := range []string{"projection", "aggregate"} {
		for _, stage := range []string{"builder", "next"} {
			t.Run(terminal+"/"+stage, func(t *testing.T) {
				marker := errors.New(stage + " panic")
				rows := &resultTestRows{values: [][]any{{int64(7)}}}
				if stage == "next" {
					rows.onNext = func(int) { panic(marker) }
				}
				backend := &resultTestBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
					if call == 0 {
						return rows, nil
					}
					if rows.closeCalls != 1 {
						return nil, errors.New("previous scalar terminal still owns its rows")
					}
					return &resultTestRows{values: [][]any{{int64(9)}}}, nil
				}}
				qs := newResultTestQuerySet(backend)
				build := func(value int64) int64 {
					if stage == "builder" {
						panic(marker)
					}
					return value
				}
				got := recoverTestPanic(func() {
					if terminal == "projection" {
						_, _ = SelectInto(t.Context(), qs, Project1(newResultTestFields().ID, build))
					} else {
						_, _ = AggregateInto(t.Context(), qs, Aggregate1(CountRows[resultTestModel](), build))
					}
				})
				if got != marker {
					t.Fatalf("panic = %v, want original marker %v", got, marker)
				}
				values, err := SelectInto(t.Context(), qs, Project1(newResultTestFields().ID, func(value int64) int64 { return value }))
				if err != nil || len(values) != 1 || values[0] != 9 || rows.closeCalls != 1 {
					t.Fatalf("retry = (%v, %v), old rows Close calls = %d", values, err, rows.closeCalls)
				}
			})
		}
	}
}

func TestRelationDeleteClosesProtectedRowsOnPanicBeforeMutation(t *testing.T) {
	marker := errors.New("protected rows panic")
	rows := &relationDeleteTestRows{onNext: func(int) { panic(marker) }}
	session := &relationDeleteTestSession{queryRows: []db.Rows{rows}, deleteCount: 1}
	deleter := relationDeleteTestDeleterWithDescriptor(t, relationDeleteTestAuthorDescriptor{})
	target := relationDeleteTestAuthorValue(1)
	before := target
	got := recoverTestPanic(func() {
		_, _ = deleter.Delete(t.Context(), relationDeleteConformingBackend(session), &target)
	})
	if got != marker || rows.closeCalls != 1 {
		t.Fatalf("panic = %v, Close calls = %d, want original panic and one Close", got, rows.closeCalls)
	}
	if target != before || len(session.setNullPlans) != 0 || len(session.deletePlans) != 0 {
		t.Fatal("protected rows panic allowed caller or database mutation")
	}
}

func TestForwardSelectPanickingScanClosesRowsAndAllowsSameQueryRetry(t *testing.T) {
	post, _, required, _ := bindRelationObjectTestFixture(t)
	path, err := ResolveForwardSelectPath(post, "author")
	if err != nil {
		t.Fatal(err)
	}
	selection, err := BindRequiredForwardSelect(path, required)
	if err != nil {
		t.Fatal(err)
	}
	joined := []selectRelatedJoinedValue{{
		source: relationObjectTestPost{ID: 10, Title: "Alpha", AuthorID: 1},
		target: &relationObjectTestAuthor{ID: 1, Name: "Ada"},
	}}
	marker := errors.New("joined scan panic")
	rows := &selectRelatedRows{values: joined, afterScan: func() { panic(marker) }}
	backend := &selectRelatedBackend{query: func(call int, _ context.Context, _ query.Plan) (db.Rows, error) {
		if call == 0 {
			return rows, nil
		}
		if rows.closeCalls.Load() != 1 {
			return nil, errors.New("previous joined scan still owns its rows")
		}
		return &selectRelatedRows{values: joined}, nil
	}}
	qs := selection.Select(NewManager[relationObjectTestPost](relationObjectTestPostDescriptor{}).Using(backend))
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if got := recoverTestPanic(func() { _, _ = qs.All(ctx) }); got != marker {
		t.Fatalf("panic = %v, want original marker %v", got, marker)
	}
	for range 2 {
		values, err := qs.All(ctx)
		if err != nil || len(values) != 1 {
			t.Fatalf("same joined query retry = (%#v, %v)", values, err)
		}
	}
	if backend.callCount() != 2 || rows.closeCalls.Load() != 1 {
		t.Fatalf("backend/old rows close calls = %d/%d, want 2/1", backend.callCount(), rows.closeCalls.Load())
	}
}

type panicCacheTestDescriptor struct {
	cacheTestDescriptor
	scan  func()
	clone func()
}

func (descriptor panicCacheTestDescriptor) Scan(row db.Row) (cacheTestModel, error) {
	if descriptor.scan != nil {
		descriptor.scan()
	}
	return descriptor.cacheTestDescriptor.Scan(row)
}

func (descriptor panicCacheTestDescriptor) CloneModel(value cacheTestModel) cacheTestModel {
	if descriptor.clone != nil {
		descriptor.clone()
	}
	return descriptor.cacheTestDescriptor.CloneModel(value)
}

func recoverTestPanic(run func()) (value any) {
	defer func() { value = recover() }()
	run()
	return nil
}
