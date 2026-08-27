package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

func TestCoordinatedAtomicCommitsExpiresNarrowSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, err := OpenMemory(ctx, "coordinated-commit-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	if _, err := backend.ExecContext(ctx, `CREATE TABLE "widget" ("id" INTEGER NOT NULL PRIMARY KEY, "name" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	var retained db.Session
	var callbackCalls int
	err = backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		callbackCalls++
		retained = session
		if _, leaked := session.(db.RelationSession); leaked {
			return errors.New("coordinated callback leaked db.RelationSession")
		}
		inserted, err := session.Insert(ctx, query.NewInsertPlan("widget", []query.Assignment{
			query.NewAssignment(id, query.Integer(1)),
			query.NewAssignment(name, query.String("first")),
		}))
		if err != nil || inserted != 1 {
			return fmt.Errorf("Insert() = (%d, %v), want (1, nil)", inserted, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CoordinatedAtomic() error = %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls = %d, want 1", callbackCalls)
	}
	var rows int
	if err := backend.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "widget"`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("durable widget rows = (%d, %v), want (1, nil)", rows, err)
	}
	if deleted, err := retained.Delete(ctx, query.NewDeletePlan("widget", id, query.Integer(1))); deleted != 0 ||
		!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("expired session Delete() = (%d, %v), want invalid_plan", deleted, err)
	}
}

func TestExecuteCoordinatedAtomicCallbackAndTerminalContracts(t *testing.T) {
	t.Parallel()

	t.Run("acquire_failure_callback_zero_no_retry", func(t *testing.T) {
		beginErr := errors.New("begin immediate failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return nil, beginErr
				}
				return relationFaultResult(0), nil
			},
		}
		calls := 0
		err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return nil
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, beginErr) {
			t.Fatalf("acquire error = %v", err)
		}
		if calls != 0 || fmt.Sprint(connection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE"}) || connection.rawCalls.Load() != 1 {
			t.Fatalf("callback/statements/discards = %d/%v/%d", calls, connection.statementSnapshot(), connection.rawCalls.Load())
		}
	})

	t.Run("callback_error_rolls_back_once", func(t *testing.T) {
		callbackErr := errors.New("callback failure")
		connection := &relationFaultConnection{}
		calls := 0
		err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return callbackErr
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, callbackErr) || calls != 1 {
			t.Fatalf("callback failure = %v, calls = %d", err, calls)
		}
		if got := fmt.Sprint(connection.statementSnapshot()); got != fmt.Sprint([]string{"BEGIN IMMEDIATE", "ROLLBACK"}) {
			t.Fatalf("statements = %s, want BEGIN/ROLLBACK", got)
		}
	})

	t.Run("cancellation_after_acquire_calls_once_and_rolls_back", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		connection := &relationFaultConnection{}
		calls := 0
		err := executeCoordinatedAtomic(ctx, func(db.Session) error {
			calls++
			cancel()
			return nil
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("canceled result = %v, calls = %d", err, calls)
		}
		if got := fmt.Sprint(connection.statementSnapshot()); got != fmt.Sprint([]string{"BEGIN IMMEDIATE", "ROLLBACK"}) {
			t.Fatalf("statements = %s, want BEGIN/ROLLBACK", got)
		}
	})

	t.Run("cancellation_at_successful_acquire_boundary_still_calls_once", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					cancel()
				}
				return relationFaultResult(0), nil
			},
		}
		calls := 0
		err := executeCoordinatedAtomic(ctx, func(db.Session) error {
			calls++
			return nil
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("boundary cancellation result = %v, calls = %d", err, calls)
		}
		if got := fmt.Sprint(connection.statementSnapshot()); got != fmt.Sprint([]string{"BEGIN IMMEDIATE", "ROLLBACK"}) {
			t.Fatalf("statements = %s, want BEGIN/ROLLBACK", got)
		}
	})

	t.Run("panic_rolls_back_and_repanics_exact_value", func(t *testing.T) {
		connection := &relationFaultConnection{}
		panicValue := &struct{ marker string }{marker: "exact"}
		calls := 0
		func() {
			defer func() {
				if got := recover(); got != panicValue {
					t.Fatalf("recovered panic = %#v, want %#v", got, panicValue)
				}
			}()
			_ = executeCoordinatedAtomic(context.Background(), func(db.Session) error {
				calls++
				panic(panicValue)
			}, connection, newRelationRetentionState(), nil)
		}()
		if calls != 1 || fmt.Sprint(connection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "ROLLBACK"}) {
			t.Fatalf("panic calls/statements = %d/%v", calls, connection.statementSnapshot())
		}
	})

	t.Run("literal_commit_error_is_unknown_and_not_retried", func(t *testing.T) {
		commitErr := errors.New("literal commit failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "COMMIT" {
					return nil, commitErr
				}
				return relationFaultResult(0), nil
			},
		}
		calls := 0
		err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return nil
		}, connection, newRelationRetentionState(), nil)
		if !errors.Is(err, commitErr) || !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}) {
			t.Fatalf("commit error = %v", err)
		}
		if calls != 1 || fmt.Sprint(connection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "COMMIT", "ROLLBACK"}) {
			t.Fatalf("commit calls/statements = %d/%v", calls, connection.statementSnapshot())
		}
	})

	t.Run("successful_commit_ignores_connection_close_error", func(t *testing.T) {
		connection := &relationFaultConnection{closeErr: errors.New("connection return failure")}
		calls := 0
		if err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return nil
		}, connection, newRelationRetentionState(), nil); err != nil {
			t.Fatalf("successful transaction error = %v", err)
		}
		if calls != 1 || connection.closeCalls.Load() != 1 || fmt.Sprint(connection.statementSnapshot()) != fmt.Sprint([]string{"BEGIN IMMEDIATE", "COMMIT"}) {
			t.Fatalf("success calls/close/statements = %d/%d/%v", calls, connection.closeCalls.Load(), connection.statementSnapshot())
		}
	})
}

func TestExecuteCoordinatedAtomicRetainsUncertainConnections(t *testing.T) {
	t.Parallel()

	t.Run("acquire_failure_unconfirmed_discard", func(t *testing.T) {
		beginErr := errors.New("begin failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "BEGIN IMMEDIATE" {
					return nil, beginErr
				}
				return relationFaultResult(0), nil
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		calls := 0
		err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return nil
		}, connection, retention, nil)
		if !errors.Is(err, beginErr) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
			errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
			t.Fatalf("acquire error = %v", err)
		}
		if calls != 0 || connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("callback/close/retained = %d/%d/%d", calls, connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		if err := retention.sealAndDrain(); err != nil {
			t.Fatal(err)
		}
		if connection.closeCalls.Load() != 1 {
			t.Fatalf("terminal close calls = %d, want 1", connection.closeCalls.Load())
		}
	})

	t.Run("callback_error_and_unconfirmed_rollback", func(t *testing.T) {
		callbackErr := errors.New("callback mutation failure")
		rollbackErr := errors.New("rollback failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "ROLLBACK" {
					return nil, rollbackErr
				}
				return relationFaultResult(1), nil
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		calls := 0
		err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return callbackErr
		}, connection, retention, nil)
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown}) ||
			!errors.Is(err, callbackErr) || !errors.Is(err, rollbackErr) {
			t.Fatalf("rollback uncertainty error = %v", err)
		}
		if calls != 1 || connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("callback/close/retained = %d/%d/%d", calls, connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})

	t.Run("commit_unknown_preserves_unconfirmed_cleanup", func(t *testing.T) {
		commitErr := errors.New("commit failure")
		rollbackErr := errors.New("rollback after commit failure")
		discardErr := errors.New("discard confirmation failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				switch statement {
				case "COMMIT":
					return nil, commitErr
				case "ROLLBACK":
					return nil, rollbackErr
				default:
					return relationFaultResult(0), nil
				}
			},
			raw: func(func(any) error) error { return discardErr },
		}
		retention := newRelationRetentionState()
		calls := 0
		err := executeCoordinatedAtomic(context.Background(), func(db.Session) error {
			calls++
			return nil
		}, connection, retention, nil)
		if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}) ||
			!errors.Is(err, commitErr) || !errors.Is(err, rollbackErr) || !errors.Is(err, discardErr) ||
			errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
			t.Fatalf("commit uncertainty error = %v", err)
		}
		if calls != 1 || connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("callback/close/retained = %d/%d/%d", calls, connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})

	t.Run("panic_repanics_after_unconfirmed_cleanup_is_retained", func(t *testing.T) {
		rollbackErr := errors.New("panic rollback failure")
		connection := &relationFaultConnection{
			exec: func(_ context.Context, statement string, _ []any) (sql.Result, error) {
				if statement == "ROLLBACK" {
					return nil, rollbackErr
				}
				return relationFaultResult(1), nil
			},
			raw: func(func(any) error) error { return nil },
		}
		retention := newRelationRetentionState()
		panicValue := &struct{ marker string }{marker: "retained"}
		func() {
			defer func() {
				if got := recover(); got != panicValue {
					t.Fatalf("recovered panic = %#v, want %#v", got, panicValue)
				}
			}()
			_ = executeCoordinatedAtomic(context.Background(), func(session db.Session) error {
				if _, err := session.Delete(context.Background(), relationDeleteTestPlan(1)); err != nil {
					t.Fatal(err)
				}
				panic(panicValue)
			}, connection, retention, nil)
		}()
		if connection.closeCalls.Load() != 0 || retainedConnectionCount(retention) != 1 {
			t.Fatalf("panic close/retained = %d/%d", connection.closeCalls.Load(), retainedConnectionCount(retention))
		}
		_ = retention.sealAndDrain()
	})
}

func TestCoordinatedAtomicSerializesTwoBackendsOnRealFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coordinated.sqlite")
	left := openCoordinatedFileBackend(t, ctx, path, 5000)
	right := openCoordinatedFileBackend(t, ctx, path, 5000)
	if _, err := left.ExecContext(ctx, `CREATE TABLE "events" ("id" INTEGER NOT NULL PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)

	leftEntered := make(chan struct{})
	releaseLeft := make(chan struct{})
	rightEntered := make(chan struct{})
	rightAttempted := make(chan struct{})
	leftResult := make(chan error, 1)
	rightResult := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		leftResult <- left.CoordinatedAtomic(ctx, func(session db.Session) error {
			calls.Add(1)
			if _, err := session.Insert(ctx, query.NewInsertPlan("events", []query.Assignment{query.NewAssignment(id, query.Integer(1))})); err != nil {
				return err
			}
			close(leftEntered)
			<-releaseLeft
			return nil
		})
	}()
	<-leftEntered
	go func() {
		close(rightAttempted)
		rightResult <- right.CoordinatedAtomic(ctx, func(session db.Session) error {
			calls.Add(1)
			close(rightEntered)
			_, err := session.Insert(ctx, query.NewInsertPlan("events", []query.Assignment{query.NewAssignment(id, query.Integer(2))}))
			return err
		})
	}()

	select {
	case <-rightAttempted:
	case <-time.After(5 * time.Second):
		t.Fatal("second coordinated transaction was not attempted")
	}
	select {
	case <-rightEntered:
		t.Fatal("second callback entered before first coordinated transaction committed")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseLeft)
	if err := waitCoordinatedResult(t, leftResult); err != nil {
		t.Fatalf("left CoordinatedAtomic() error = %v", err)
	}
	if err := waitCoordinatedResult(t, rightResult); err != nil {
		t.Fatalf("right CoordinatedAtomic() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("callback calls = %d, want 2", calls.Load())
	}
	select {
	case <-rightEntered:
	default:
		t.Fatal("second callback did not run after first commit")
	}
	var rows int
	if err := left.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "events"`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("event rows = (%d, %v), want (2, nil)", rows, err)
	}
}

func TestCoordinatedAtomicCanceledWaitingAcquireSkipsCallbackAndBackendIsReusable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coordinated-canceled-acquire.sqlite")
	holder := openCoordinatedFileBackend(t, ctx, path, 5000)
	contender := openCoordinatedFileBackend(t, ctx, path, 500)

	holderEntered := make(chan struct{})
	releaseHolder := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHolder) }) }
	t.Cleanup(release)
	holderResult := make(chan error, 1)
	go func() {
		holderResult <- holder.CoordinatedAtomic(ctx, func(db.Session) error {
			close(holderEntered)
			<-releaseHolder
			return nil
		})
	}()
	select {
	case <-holderEntered:
	case err := <-holderResult:
		t.Fatalf("holder returned before barrier release: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("holder did not acquire coordinated fence")
	}

	acquireCtx, cancelAcquire := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelAcquire()
	acquireAttempted := make(chan struct{})
	acquireResult := make(chan error, 1)
	var canceledCallbackCalls atomic.Int32
	go func() {
		close(acquireAttempted)
		acquireResult <- contender.CoordinatedAtomic(acquireCtx, func(db.Session) error {
			canceledCallbackCalls.Add(1)
			return nil
		})
	}()
	select {
	case <-acquireAttempted:
	case <-time.After(5 * time.Second):
		t.Fatal("contending acquire was not attempted")
	}
	var acquireErr error
	select {
	case acquireErr = <-acquireResult:
	case <-time.After(2 * time.Second):
		t.Fatal("contending acquire did not honor its context deadline")
	}
	if !errors.Is(acquireErr, context.DeadlineExceeded) {
		t.Fatalf("contending acquire error = %v, want context deadline exceeded", acquireErr)
	}
	if canceledCallbackCalls.Load() != 0 {
		t.Fatalf("callback calls after canceled acquire = %d, want 0", canceledCallbackCalls.Load())
	}
	if errors.Is(acquireErr, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) ||
		errors.Is(acquireErr, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
		t.Fatalf("canceled acquire used outcome-unknown marker: %v", acquireErr)
	}

	release()
	if err := waitCoordinatedResult(t, holderResult); err != nil {
		t.Fatalf("holder CoordinatedAtomic() error = %v", err)
	}
	reuseCtx, cancelReuse := context.WithTimeout(ctx, 2*time.Second)
	defer cancelReuse()
	var reuseCalls atomic.Int32
	if err := contender.CoordinatedAtomic(reuseCtx, func(db.Session) error {
		reuseCalls.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("contender reuse CoordinatedAtomic() error = %v", err)
	}
	if reuseCalls.Load() != 1 {
		t.Fatalf("reused callback calls = %d, want 1", reuseCalls.Load())
	}
}

func TestCoordinatedAtomicRealMutationErrorRollsBackBeforeOtherBackendAcquires(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coordinated-real-rollback.sqlite")
	first := openCoordinatedFileBackend(t, ctx, path, 5000)
	second := openCoordinatedFileBackend(t, ctx, path, 5000)
	if _, err := first.ExecContext(ctx, `CREATE TABLE "events" ("id" INTEGER NOT NULL PRIMARY KEY, "owner" TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	owner := query.NewFieldRef("owner", "owner", query.FieldString, false)
	insert := func(value string) query.InsertPlan {
		return query.NewInsertPlan("events", []query.Assignment{
			query.NewAssignment(id, query.Integer(1)),
			query.NewAssignment(owner, query.String(value)),
		})
	}

	callbackErr := errors.New("rollback requested after mutation")
	mutationRan := make(chan struct{})
	allowCallbackReturn := make(chan struct{})
	var allowCallbackReturnOnce sync.Once
	releaseCallback := func() {
		allowCallbackReturnOnce.Do(func() { close(allowCallbackReturn) })
	}
	defer releaseCallback()
	firstResult := make(chan error, 1)
	var firstCalls atomic.Int32
	go func() {
		firstResult <- first.CoordinatedAtomic(ctx, func(session db.Session) error {
			firstCalls.Add(1)
			if _, err := session.Insert(ctx, insert("rolled-back")); err != nil {
				return err
			}
			close(mutationRan)
			<-allowCallbackReturn
			return callbackErr
		})
	}()
	select {
	case <-mutationRan:
	case err := <-firstResult:
		t.Fatalf("first transaction returned before mutation barrier: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first coordinated mutation did not run")
	}
	releaseCallback()
	if err := waitCoordinatedResult(t, firstResult); !errors.Is(err, callbackErr) {
		t.Fatalf("first CoordinatedAtomic() error = %v, want callback error", err)
	}
	if firstCalls.Load() != 1 {
		t.Fatalf("first callback calls = %d, want 1", firstCalls.Load())
	}

	secondCtx, cancelSecond := context.WithTimeout(ctx, 2*time.Second)
	defer cancelSecond()
	var secondCalls atomic.Int32
	if err := second.CoordinatedAtomic(secondCtx, func(session db.Session) error {
		secondCalls.Add(1)
		_, err := session.Insert(secondCtx, insert("winner"))
		return err
	}); err != nil {
		t.Fatalf("second CoordinatedAtomic() error = %v", err)
	}
	if secondCalls.Load() != 1 {
		t.Fatalf("second callback calls = %d, want 1", secondCalls.Load())
	}
	var durableOwner string
	if err := second.database.QueryRowContext(ctx, `SELECT "owner" FROM "events" WHERE "id" = 1`).Scan(&durableOwner); err != nil {
		t.Fatal(err)
	}
	if durableOwner != "winner" {
		t.Fatalf("durable owner = %q, want winner", durableOwner)
	}
}

func TestCoordinatedAtomicBusyAcquireDoesNotInvokeOrRetryCallback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coordinated-busy.sqlite")
	locker := openCoordinatedFileBackend(t, ctx, path, 5000)
	contender := openCoordinatedFileBackend(t, ctx, path, 1)

	entered := make(chan struct{})
	release := make(chan struct{})
	lockerResult := make(chan error, 1)
	go func() {
		lockerResult <- locker.CoordinatedAtomic(ctx, func(db.Session) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	calls := 0
	err := contender.CoordinatedAtomic(ctx, func(db.Session) error {
		calls++
		return nil
	})
	var sqliteErr *modernsqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_BUSY {
		t.Fatalf("contended acquire error = %v, want SQLITE_BUSY", err)
	}
	if calls != 0 || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
		t.Fatalf("contended callback calls/error = %d/%v", calls, err)
	}
	close(release)
	if err := waitCoordinatedResult(t, lockerResult); err != nil {
		t.Fatalf("locker CoordinatedAtomic() error = %v", err)
	}
}

func openCoordinatedFileBackend(t *testing.T, ctx context.Context, path string, busyTimeout int) *Backend {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=rwc&_busy_timeout=%d", filepath.ToSlash(path), busyTimeout)
	backend, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	backend.database.SetMaxOpenConns(1)
	backend.database.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return backend
}

func waitCoordinatedResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for coordinated transaction")
		return nil
	}
}
