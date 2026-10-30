package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestPostgresCoordinatedAtomicAdvisoryLockKeyIsStableSchemaScopedAndMigrationDistinct(t *testing.T) {
	t.Parallel()

	const want int64 = -4673781987282205094
	if got := postgresCoordinatedAtomicAdvisoryLockKey("godj_phase_d"); got != want {
		t.Fatalf("coordinated lock key = %d, want %d", got, want)
	}
	if got := postgresCoordinatedAtomicAdvisoryLockKey("godj_phase_d_other"); got == want {
		t.Fatalf("different schema reused coordinated lock key %d", got)
	}
	if migration := postgresMigrationAdvisoryLockKey("godj_phase_d"); migration == want {
		t.Fatalf("coordinated lock reused migration lock key %d", migration)
	}
}

func TestPostgresCoordinatedAtomicAcquiresBeforeCallbackAndExpiresSession(t *testing.T) {
	t.Parallel()

	state := &coordinatedTransactionTestState{}
	backend := newCoordinatedTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	ctx := context.Background()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	deletePlan := query.NewDeletePlan("article", id, query.Integer(1))

	var retained db.Session
	callbackCalls := 0
	err := backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		callbackCalls++
		state.record("callback")
		retained = session
		rows, deleteErr := session.Delete(ctx, deletePlan)
		if deleteErr != nil || rows != 1 {
			t.Fatalf("Delete() = (%d, %v), want (1, nil)", rows, deleteErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CoordinatedAtomic() error = %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls = %d, want 1", callbackCalls)
	}

	snapshot := state.snapshot()
	wantEvents := []string{"begin", "acquire", "callback", "session-exec", "commit"}
	if !reflect.DeepEqual(snapshot.events, wantEvents) {
		t.Fatalf("events = %v, want %v", snapshot.events, wantEvents)
	}
	if snapshot.acquireStatement != postgresCoordinatedAtomicLock {
		t.Fatalf("acquire statement = %q, want %q", snapshot.acquireStatement, postgresCoordinatedAtomicLock)
	}
	if len(snapshot.acquireArguments) != 1 || snapshot.acquireArguments[0].Value != postgresCoordinatedAtomicAdvisoryLockKey("godj_test") {
		t.Fatalf("acquire arguments = %#v", snapshot.acquireArguments)
	}
	if snapshot.options.Isolation != driver.IsolationLevel(sql.LevelReadCommitted) || snapshot.options.ReadOnly {
		t.Fatalf("transaction options = %#v, want READ COMMITTED READ WRITE", snapshot.options)
	}
	if _, err := retained.Delete(ctx, deletePlan); !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeInvalidPlan,
	}) {
		t.Fatalf("expired coordinated session error = %v", err)
	}
	if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("expired session performed driver work: events = %v", got)
	}
}

func TestPostgresCoordinatedAtomicAcquireFailureAndCancellationSkipCallback(t *testing.T) {
	t.Parallel()

	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		acquireFailure := &pgconn.PgError{Code: "XX000", Message: "lock failed"}
		state := &coordinatedTransactionTestState{acquireErr: acquireFailure}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })
		var callbackCalls atomic.Int32

		err := backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
			callbackCalls.Add(1)
			return nil
		})
		if !errors.Is(err, acquireFailure) {
			t.Fatalf("acquire failure = %v, want structured cause", err)
		}
		if callbackCalls.Load() != 0 {
			t.Fatalf("callback calls = %d, want 0", callbackCalls.Load())
		}
		wantEvents := []string{"begin", "acquire", "rollback"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})

	t.Run("blocked cancellation", func(t *testing.T) {
		t.Parallel()
		started := make(chan struct{})
		rollbackDone := make(chan struct{})
		state := &coordinatedTransactionTestState{acquireHook: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}, rollbackDone: rollbackDone}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var callbackCalls atomic.Int32
		result := make(chan error, 1)
		go func() {
			result <- backend.CoordinatedAtomic(ctx, func(db.Session) error {
				callbackCalls.Add(1)
				return nil
			})
		}()
		<-started
		cancel()
		err := <-result
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled acquisition error = %v", err)
		}
		if callbackCalls.Load() != 0 {
			t.Fatalf("callback calls = %d, want 0", callbackCalls.Load())
		}
		waitForCoordinatedTransactionRollback(t, rollbackDone)
		wantEvents := []string{"begin", "acquire", "rollback"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})
}

func TestPostgresCoordinatedAtomicInvokesCallbackAfterAcquireBoundaryCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rollbackDone := make(chan struct{})
	state := &coordinatedTransactionTestState{acquireHook: func(context.Context) error {
		cancel()
		return nil
	}, rollbackDone: rollbackDone}
	backend := newCoordinatedTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	callbackCalls := 0
	err := backend.CoordinatedAtomic(ctx, func(db.Session) error {
		callbackCalls++
		state.record("callback")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("boundary cancellation error = %v", err)
	}
	if callbackCalls != 1 {
		t.Fatalf("callback calls = %d, want 1", callbackCalls)
	}
	waitForCoordinatedTransactionRollback(t, rollbackDone)
	gotEvents := state.snapshot().events
	wantCallbackFirst := []string{"begin", "acquire", "callback", "rollback"}
	wantRollbackFirst := []string{"begin", "acquire", "rollback", "callback"}
	// database/sql observes the canceled transaction context independently.
	// Once ExecContext releases the transaction read lock, its rollback goroutine
	// and this callback can record in either order. The public contract fixes the
	// successful acquire prefix, one callback invocation, and rollback—not that
	// scheduler-dependent tail order.
	if !reflect.DeepEqual(gotEvents, wantCallbackFirst) && !reflect.DeepEqual(gotEvents, wantRollbackFirst) {
		t.Fatalf("events = %v, want %v or %v", gotEvents, wantCallbackFirst, wantRollbackFirst)
	}
}

func TestPostgresCoordinatedAtomicPreservesAtomicFailureSemanticsWithoutRetry(t *testing.T) {
	t.Parallel()

	t.Run("callback error", func(t *testing.T) {
		t.Parallel()
		callbackFailure := errors.New("callback failure")
		state := &coordinatedTransactionTestState{}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })
		callbackCalls := 0

		err := backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
			callbackCalls++
			state.record("callback")
			return callbackFailure
		})
		if !errors.Is(err, callbackFailure) {
			t.Fatalf("callback failure = %v", err)
		}
		if callbackCalls != 1 {
			t.Fatalf("callback calls = %d, want 1", callbackCalls)
		}
		wantEvents := []string{"begin", "acquire", "callback", "rollback"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})

	t.Run("callback rollback unknown", func(t *testing.T) {
		t.Parallel()
		callbackFailure := errors.New("callback failure")
		rollbackFailure := errors.New("rollback transport failure")
		state := &coordinatedTransactionTestState{rollbackErr: rollbackFailure}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })

		err := backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
			state.record("callback")
			return callbackFailure
		})
		if !errors.Is(err, callbackFailure) || !errors.Is(err, rollbackFailure) || !errors.Is(err, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeTransactionOutcomeUnknown,
		}) {
			t.Fatalf("callback rollback failure = %v", err)
		}
		wantEvents := []string{"begin", "acquire", "callback", "rollback"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})

	t.Run("acquire rollback unknown", func(t *testing.T) {
		t.Parallel()
		acquireFailure := errors.New("acquire failure")
		rollbackFailure := errors.New("rollback transport failure")
		state := &coordinatedTransactionTestState{
			acquireErr:  acquireFailure,
			rollbackErr: rollbackFailure,
		}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })
		var callbackCalls atomic.Int32

		err := backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
			callbackCalls.Add(1)
			return nil
		})
		if !errors.Is(err, acquireFailure) || !errors.Is(err, rollbackFailure) || !errors.Is(err, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeTransactionOutcomeUnknown,
		}) {
			t.Fatalf("acquire rollback failure = %v", err)
		}
		if callbackCalls.Load() != 0 {
			t.Fatalf("callback calls = %d, want 0", callbackCalls.Load())
		}
		wantEvents := []string{"begin", "acquire", "rollback"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})

	t.Run("panic", func(t *testing.T) {
		t.Parallel()
		state := &coordinatedTransactionTestState{}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })
		const panicValue = "coordinated callback panic"
		callbackCalls := 0
		func() {
			defer func() {
				if recovered := recover(); recovered != panicValue {
					t.Fatalf("recovered = %#v, want %#v", recovered, panicValue)
				}
			}()
			_ = backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
				callbackCalls++
				state.record("callback")
				panic(panicValue)
			})
		}()
		if callbackCalls != 1 {
			t.Fatalf("callback calls = %d, want 1", callbackCalls)
		}
		wantEvents := []string{"begin", "acquire", "callback", "rollback"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})

	t.Run("commit unknown", func(t *testing.T) {
		t.Parallel()
		commitFailure := errors.New("commit transport failure")
		state := &coordinatedTransactionTestState{commitErr: commitFailure}
		backend := newCoordinatedTransactionTestBackend(state)
		t.Cleanup(func() { _ = backend.Close() })
		callbackCalls := 0

		err := backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
			callbackCalls++
			state.record("callback")
			return nil
		})
		if !errors.Is(err, commitFailure) || !errors.Is(err, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeCommitOutcomeUnknown,
		}) {
			t.Fatalf("commit failure = %v", err)
		}
		if callbackCalls != 1 {
			t.Fatalf("callback calls = %d, want 1", callbackCalls)
		}
		wantEvents := []string{"begin", "acquire", "callback", "commit"}
		if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
			t.Fatalf("events = %v, want %v", got, wantEvents)
		}
	})
}

func TestPostgresCoordinatedAtomicRejectsInvalidInputsBeforeTransaction(t *testing.T) {
	t.Parallel()

	state := &coordinatedTransactionTestState{}
	backend := newCoordinatedTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.CoordinatedAtomic(context.Background(), nil); !errors.Is(err, &query.Error{
		Category: query.CategoryQuery,
		Code:     query.CodeInvalidPlan,
	}) {
		t.Fatalf("nil callback error = %v", err)
	}
	if err := backend.CoordinatedAtomic(nil, func(db.Session) error { return nil }); !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeInvalidPlan,
	}) {
		t.Fatalf("nil context error = %v", err)
	}
	var nilBackend *Backend
	if err := nilBackend.CoordinatedAtomic(context.Background(), func(db.Session) error { return nil }); !errors.Is(err, &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeInvalidPlan,
	}) {
		t.Fatalf("nil backend error = %v", err)
	}
	if got := state.snapshot().events; len(got) != 0 {
		t.Fatalf("invalid input began transaction: events = %v", got)
	}
}

func TestPostgresCoordinatedAtomicRedactsAcquireConnectFailure(t *testing.T) {
	t.Parallel()

	cause := &coordinatedTransactionConnectErrorCarrier{connect: &pgconn.ConnectError{Config: &pgconn.Config{
		User:     "secret_user",
		Password: "secret_password",
		Database: "secret_database",
	}}}
	state := &coordinatedTransactionTestState{acquireErr: cause}
	backend := newCoordinatedTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	var callbackCalls atomic.Int32
	err := backend.CoordinatedAtomic(context.Background(), func(db.Session) error {
		callbackCalls.Add(1)
		return nil
	})
	for _, secret := range []string{"secret_user", "secret_password", "secret_database"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("acquire error leaked %q: %v", secret, err)
		}
	}
	var retained *pgconn.ConnectError
	if errors.As(err, &retained) {
		t.Fatalf("acquire error retained credential-bearing ConnectError: %#v", retained.Config)
	}
	if callbackCalls.Load() != 0 {
		t.Fatalf("callback calls = %d, want 0", callbackCalls.Load())
	}
	wantEvents := []string{"begin", "acquire", "rollback"}
	if got := state.snapshot().events; !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("events = %v, want %v", got, wantEvents)
	}
}

type coordinatedTransactionTestSnapshot struct {
	events           []string
	acquireStatement string
	acquireArguments []driver.NamedValue
	options          driver.TxOptions
}

type coordinatedTransactionTestState struct {
	mu               sync.Mutex
	events           []string
	acquireStatement string
	acquireArguments []driver.NamedValue
	options          driver.TxOptions
	acquireHook      func(context.Context) error
	acquireErr       error
	commitErr        error
	rollbackErr      error
	rollbackDone     chan struct{}
	rollbackOnce     sync.Once
}

func (state *coordinatedTransactionTestState) record(event string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.events = append(state.events, event)
}

func (state *coordinatedTransactionTestState) snapshot() coordinatedTransactionTestSnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return coordinatedTransactionTestSnapshot{
		events:           append([]string(nil), state.events...),
		acquireStatement: state.acquireStatement,
		acquireArguments: append([]driver.NamedValue(nil), state.acquireArguments...),
		options:          state.options,
	}
}

func newCoordinatedTransactionTestBackend(state *coordinatedTransactionTestState) *Backend {
	database := sql.OpenDB(coordinatedTransactionTestConnector{state: state})
	database.SetMaxOpenConns(1)
	return &Backend{database: database, schema: "godj_test"}
}

type coordinatedTransactionTestConnector struct {
	state *coordinatedTransactionTestState
}

func (connector coordinatedTransactionTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &coordinatedTransactionTestConnection{state: connector.state}, nil
}

func (connector coordinatedTransactionTestConnector) Driver() driver.Driver {
	return coordinatedTransactionTestDriver{state: connector.state}
}

type coordinatedTransactionTestDriver struct {
	state *coordinatedTransactionTestState
}

func (testDriver coordinatedTransactionTestDriver) Open(string) (driver.Conn, error) {
	return &coordinatedTransactionTestConnection{state: testDriver.state}, nil
}

type coordinatedTransactionTestConnection struct {
	state *coordinatedTransactionTestState
}

func (*coordinatedTransactionTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by coordinated transaction test driver")
}

func (*coordinatedTransactionTestConnection) Close() error { return nil }

func (connection *coordinatedTransactionTestConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}

func (connection *coordinatedTransactionTestConnection) BeginTx(
	_ context.Context,
	options driver.TxOptions,
) (driver.Tx, error) {
	connection.state.mu.Lock()
	connection.state.options = options
	connection.state.events = append(connection.state.events, "begin")
	connection.state.mu.Unlock()
	return &coordinatedTransactionTestTransaction{state: connection.state}, nil
}

func (connection *coordinatedTransactionTestConnection) ExecContext(
	ctx context.Context,
	statement string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	if statement != postgresCoordinatedAtomicLock {
		connection.state.record("session-exec")
		return driver.RowsAffected(1), nil
	}
	connection.state.mu.Lock()
	connection.state.events = append(connection.state.events, "acquire")
	connection.state.acquireStatement = statement
	connection.state.acquireArguments = append([]driver.NamedValue(nil), arguments...)
	hook := connection.state.acquireHook
	acquireErr := connection.state.acquireErr
	connection.state.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return nil, err
		}
	}
	if acquireErr != nil {
		return nil, acquireErr
	}
	return driver.RowsAffected(1), nil
}

type coordinatedTransactionTestTransaction struct {
	state *coordinatedTransactionTestState
}

func (transaction *coordinatedTransactionTestTransaction) Commit() error {
	transaction.state.mu.Lock()
	defer transaction.state.mu.Unlock()
	transaction.state.events = append(transaction.state.events, "commit")
	return transaction.state.commitErr
}

func (transaction *coordinatedTransactionTestTransaction) Rollback() error {
	transaction.state.mu.Lock()
	transaction.state.events = append(transaction.state.events, "rollback")
	rollbackErr := transaction.state.rollbackErr
	rollbackDone := transaction.state.rollbackDone
	transaction.state.mu.Unlock()
	if rollbackDone != nil {
		transaction.state.rollbackOnce.Do(func() { close(rollbackDone) })
	}
	return rollbackErr
}

func waitForCoordinatedTransactionRollback(t *testing.T, rollbackDone <-chan struct{}) {
	t.Helper()
	select {
	case <-rollbackDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for coordinated transaction rollback")
	}
}

type coordinatedTransactionConnectErrorCarrier struct {
	connect *pgconn.ConnectError
}

func (*coordinatedTransactionConnectErrorCarrier) Error() string {
	return "connect user=secret_user database=secret_database password=secret_password"
}

func (carrier *coordinatedTransactionConnectErrorCarrier) As(target any) bool {
	connect, ok := target.(**pgconn.ConnectError)
	if !ok {
		return false
	}
	*connect = carrier.connect
	return true
}
