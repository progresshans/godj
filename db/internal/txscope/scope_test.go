package txscope

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestNestedScopesOwnHandlesAndRollbackOnlyTheirWork(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var statements []string
	root := New(ctx, func(_ context.Context, sql string) error { statements = append(statements, sql); return nil })
	t.Cleanup(func() { _ = root.Finish() })
	var childHandle, grandchildHandle *Scope
	rollback := errors.New("undo grandchild")
	err := root.Savepoint(ctx, func(child *Scope) error {
		childHandle = child
		if err := root.Validate(ctx); !invalidScope(err) {
			t.Fatalf("parent handle inside child: %v", err)
		}
		if err := root.Savepoint(ctx, func(*Scope) error { t.Fatal("concurrent sibling callback ran"); return nil }); !invalidScope(err) {
			t.Fatalf("parent savepoint inside child: %v", err)
		}
		if value, err := Do(child, ctx, func(context.Context) (int, error) { return 7, nil }); err != nil || value != 7 {
			t.Fatalf("child operation: %d, %v", value, err)
		}
		if err := child.Savepoint(ctx, func(grandchild *Scope) error {
			grandchildHandle = grandchild
			if err := child.Validate(ctx); !invalidScope(err) {
				t.Fatalf("child handle inside grandchild: %v", err)
			}
			return rollback
		}); err != rollback {
			t.Fatalf("confirmed rollback changed callback error: %v", err)
		}
		if err := child.Validate(ctx); err != nil {
			t.Fatalf("restored child handle: %v", err)
		}
		if err := grandchildHandle.Validate(ctx); !invalidScope(err) {
			t.Fatalf("retained grandchild: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := childHandle.Validate(ctx); !invalidScope(err) {
		t.Fatalf("retained child: %v", err)
	}
	if err := root.Savepoint(ctx, func(*Scope) error { return nil }); err != nil {
		t.Fatal(err)
	}
	want := []string{"SAVEPOINT godj_savepoint_1", "SAVEPOINT godj_savepoint_2", "ROLLBACK TO SAVEPOINT godj_savepoint_2", "RELEASE SAVEPOINT godj_savepoint_2", "RELEASE SAVEPOINT godj_savepoint_1", "SAVEPOINT godj_savepoint_3", "RELEASE SAVEPOINT godj_savepoint_3"}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("control = %v, want %v", statements, want)
	}
	if err := root.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := root.Validate(ctx); !invalidScope(err) {
		t.Fatalf("retained root: %v", err)
	}
}

func TestControlFailuresRequireRootRollbackEvenWhenCallerIgnoresThem(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"SAVEPOINT ", "ROLLBACK TO ", "RELEASE "} {
		t.Run(strings.TrimSpace(failure), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			transportFailure := errors.New("control transport failed")
			callbackFailure := errors.New("child rejected")
			var statements []string
			root := New(ctx, func(_ context.Context, sql string) error {
				statements = append(statements, sql)
				if strings.HasPrefix(sql, failure) {
					return transportFailure
				}
				return nil
			})
			var calls int
			err := root.Savepoint(ctx, func(*Scope) error {
				calls++
				if failure == "ROLLBACK TO " {
					return callbackFailure
				}
				return nil
			})
			if !rollbackRequired(err) || !errors.Is(err, transportFailure) {
				t.Fatalf("failure = %v", err)
			}
			if failure == "SAVEPOINT " && calls != 0 || failure != "SAVEPOINT " && calls != 1 {
				t.Fatalf("callback calls = %d", calls)
			}
			before := len(statements)
			if err := root.Savepoint(ctx, func(*Scope) error { t.Fatal("poisoned callback ran"); return nil }); !rollbackRequired(err) {
				t.Fatalf("poisoned savepoint: %v", err)
			}
			if _, err := Do(root, ctx, func(context.Context) (int, error) { t.Fatal("poisoned native operation ran"); return 1, nil }); !rollbackRequired(err) {
				t.Fatalf("poisoned operation: %v", err)
			}
			if len(statements) != before {
				t.Fatalf("control retried after failure: %v", statements)
			}
			if err := root.Finish(); !rollbackRequired(err) || !errors.Is(err, transportFailure) {
				t.Fatalf("root finish: %v", err)
			}
			wantCount := 2
			if failure == "SAVEPOINT " {
				wantCount = 1
			}
			if len(statements) != wantCount {
				t.Fatalf("control executions = %v", statements)
			}
		})
	}
}

func TestChildCancellationUsesDetachedBoundedCleanupAndPreservesParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	childContext, cancelChild := context.WithCancel(ctx)
	var statements []string
	root := New(ctx, func(control context.Context, sql string) error {
		statements = append(statements, sql)
		if len(statements) > 1 {
			if control.Err() != nil {
				t.Fatalf("cleanup inherited cancellation: %v", control.Err())
			}
			deadline, ok := control.Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > cleanupTimeout {
				t.Fatalf("cleanup deadline = %v/%v", deadline, ok)
			}
		}
		return nil
	})
	err := root.Savepoint(childContext, func(child *Scope) error {
		cancelChild()
		if _, err := Do(child, ctx, func(context.Context) (int, error) { t.Fatal("canceled child executed"); return 0, nil }); !errors.Is(err, context.Canceled) {
			t.Fatalf("child ignored scope cancellation: %v", err)
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || rollbackRequired(err) {
		t.Fatalf("canceled savepoint = %v", err)
	}
	if _, err := Do(root, ctx, func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatalf("parent was canceled: %v", err)
	}
	if err := root.Finish(); err != nil {
		t.Fatalf("parent finish: %v", err)
	}
	want := []string{"SAVEPOINT godj_savepoint_1", "ROLLBACK TO SAVEPOINT godj_savepoint_1", "RELEASE SAVEPOINT godj_savepoint_1"}
	if !reflect.DeepEqual(statements, want) {
		t.Fatalf("control = %v", statements)
	}
}

func TestScopeResourcesCloseBeforeControlAndRemainExpired(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var events []string
	root := New(ctx, func(_ context.Context, sql string) error { events = append(events, sql); return nil })
	parentRows, err := Query(root, ctx, func(context.Context) (db.Rows, error) { return &scopeRows{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Savepoint(ctx, func(*Scope) error { t.Fatal("open-cursor callback ran"); return nil }); !invalidScope(err) || len(events) != 0 {
		t.Fatalf("savepoint with parent cursor = %v/%v", err, events)
	}
	if err := parentRows.Close(); err != nil {
		t.Fatal(err)
	}
	var retained db.Rows
	transport := &scopeRows{close: func() error { events = append(events, "close child rows"); return nil }}
	if err := root.Savepoint(ctx, func(child *Scope) error {
		var err error
		retained, err = Query(child, ctx, func(context.Context) (db.Rows, error) { return transport, nil })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if retained.Next() || !invalidScope(retained.Err()) || !invalidScope(retained.Scan(new(int))) {
		t.Fatalf("retained rowset did not expire: %v", retained.Err())
	}
	if err := retained.Close(); err != nil || transport.closes.Load() != 1 {
		t.Fatalf("retained Close = %v, count %d", err, transport.closes.Load())
	}
	want := []string{"SAVEPOINT godj_savepoint_1", "close child rows", "RELEASE SAVEPOINT godj_savepoint_1"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("cleanup order = %v", events)
	}
	if len(root.resources) != 0 {
		t.Fatalf("closed root resources retained: %d", len(root.resources))
	}
	if err := root.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestClosedResourceFailureStillPreventsRootCommit(t *testing.T) {
	t.Parallel()
	root := New(context.Background(), nil)
	failure := errors.New("rows close failed")
	transport := &scopeRows{close: func() error { return failure }}
	rows, err := Query(root, context.Background(), func(context.Context) (db.Rows, error) { return transport, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(root.resources) != 0 {
		t.Fatalf("closed resource retained")
	}
	if err := root.Finish(); !errors.Is(err, failure) {
		t.Fatalf("ignored cleanup failure lost: %v", err)
	}
	if transport.closes.Load() != 1 {
		t.Fatalf("close retried: %d", transport.closes.Load())
	}
}

func TestRootFinishCancelsUnjoinedIOAndRejectsOverlappingIO(t *testing.T) {
	t.Parallel()
	root := New(context.Background(), nil)
	entered, result := make(chan struct{}), make(chan error, 1)
	go func() {
		value, err := Do(root, context.Background(), func(ctx context.Context) (int, error) { close(entered); <-ctx.Done(); return 42, nil })
		if value != 0 {
			err = errors.Join(err, fmt.Errorf("expired result escaped: %d", value))
		}
		result <- err
	}()
	awaitSignal(t, entered)
	if _, err := Do(root, context.Background(), func(context.Context) (int, error) { t.Error("overlapping operation executed"); return 0, nil }); !invalidScope(err) {
		t.Fatalf("overlapping I/O = %v", err)
	}
	if err := root.Finish(); !rollbackRequired(err) {
		t.Fatalf("unfinished root = %v", err)
	}
	if err := awaitError(t, result); err == nil {
		t.Fatal("unfinished I/O succeeded")
	}
}

func TestRootFinishRevokesUnjoinedChildWithoutLateControl(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var statements []string
	root := New(context.Background(), func(_ context.Context, sql string) error {
		mu.Lock()
		defer mu.Unlock()
		statements = append(statements, sql)
		return nil
	})
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- root.Savepoint(context.Background(), func(child *Scope) error {
			close(entered)
			<-resume
			_, err := Do(child, context.Background(), func(context.Context) (int, error) { return 99, errors.New("late native I/O ran") })
			return err
		})
	}()
	awaitSignal(t, entered)
	if err := root.Finish(); !rollbackRequired(err) {
		t.Errorf("unjoined child = %v", err)
	}
	close(resume)
	if err := awaitError(t, done); err == nil || strings.Contains(err.Error(), "late native I/O ran") {
		t.Fatalf("late child result = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(statements, []string{"SAVEPOINT godj_savepoint_1"}) {
		t.Fatalf("late control executed: %v", statements)
	}
}

func TestClosingRowsInterruptsNextUsingItsOriginalQueryContext(t *testing.T) {
	t.Parallel()
	root := New(context.Background(), nil)
	t.Cleanup(func() { _ = root.Finish() })
	entered := make(chan struct{})
	var transport *scopeRows
	rows, err := Query(root, context.Background(), func(ctx context.Context) (db.Rows, error) {
		transport = &scopeRows{next: func() bool { close(entered); <-ctx.Done(); return false }}
		return transport, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { rows.Next(); done <- rows.Err() }()
	awaitSignal(t, entered)
	closed := make(chan error, 1)
	go func() { closed <- rows.Close() }()
	if err := awaitError(t, closed); err != nil {
		t.Fatalf("Close during Next: %v", err)
	}
	if err := awaitError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted Next: %v", err)
	}
	if transport.closes.Load() != 1 {
		t.Fatalf("close count = %d", transport.closes.Load())
	}
}

func TestHandledQueryCancellationDoesNotPoisonItsParentScope(t *testing.T) {
	t.Parallel()
	root := New(context.Background(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	rows, err := Query(root, ctx, func(context.Context) (db.Rows, error) { return &scopeRows{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := rows.Close(); err != nil {
		t.Fatalf("confirmed cursor close = %v", err)
	}
	if err := rows.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("query cancellation was lost: %v", err)
	}
	if _, err := Do(root, context.Background(), func(context.Context) (int, error) { return 1, nil }); err != nil {
		t.Fatalf("handled query cancellation poisoned parent: %v", err)
	}
	if err := root.Finish(); err != nil {
		t.Fatalf("query error became a cleanup failure: %v", err)
	}
}

func TestSavepointPanicAndGoexitRollBackBeforeUnwinding(t *testing.T) {
	t.Parallel()
	for _, goexit := range []bool{false, true} {
		t.Run(fmt.Sprint(goexit), func(t *testing.T) {
			t.Parallel()
			var statements []string
			root := New(context.Background(), func(_ context.Context, sql string) error { statements = append(statements, sql); return nil })
			done := make(chan struct{})
			marker := &struct{}{}
			var recovered any
			go func() {
				defer close(done)
				defer func() { recovered = recover() }()
				_ = root.Savepoint(context.Background(), func(*Scope) error {
					if goexit {
						runtime.Goexit()
					}
					panic(marker)
				})
			}()
			awaitSignal(t, done)
			if !goexit && recovered != marker || goexit && recovered != nil {
				t.Fatalf("original panic changed: %#v", recovered)
			}
			want := []string{"SAVEPOINT godj_savepoint_1", "ROLLBACK TO SAVEPOINT godj_savepoint_1", "RELEASE SAVEPOINT godj_savepoint_1"}
			if !reflect.DeepEqual(statements, want) {
				t.Fatalf("unwind control = %v", statements)
			}
			if err := root.Validate(context.Background()); err != nil {
				t.Fatalf("parent after confirmed unwind: %v", err)
			}
			if err := root.Finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLiveStreamCannotCrossSavepointOrRootLifetime(t *testing.T) {
	t.Parallel()
	var calls int
	root := New(context.Background(), func(context.Context, string) error { calls++; return nil })
	finishStream, err := root.Hold(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Savepoint(context.Background(), func(*Scope) error { t.Fatal("stream savepoint callback ran"); return nil }); !invalidScope(err) || calls != 0 {
		t.Fatalf("live-stream savepoint = %v/%d", err, calls)
	}
	if err := root.Finish(); !rollbackRequired(err) {
		t.Fatalf("root with live stream = %v", err)
	}
	if err := finishStream(); !invalidScope(err) {
		t.Fatalf("late stream completion = %v", err)
	}
}

func invalidScope(err error) bool {
	return errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan})
}
func rollbackRequired(err error) bool {
	return errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionRollbackRequired})
}

func awaitSignal(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatal("scope operation did not complete")
	}
}
func awaitError(t *testing.T, channel <-chan error) error {
	t.Helper()
	select {
	case err := <-channel:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("scope operation did not complete")
		return nil
	}
}

type scopeRows struct {
	next   func() bool
	close  func() error
	closes atomic.Int32
}

func (rows *scopeRows) Next() bool {
	if rows.next != nil {
		return rows.next()
	}
	return false
}
func (*scopeRows) Scan(...any) error { return nil }
func (*scopeRows) Err() error        { return nil }
func (rows *scopeRows) Close() error {
	rows.closes.Add(1)
	if rows.close != nil {
		return rows.close()
	}
	return nil
}
