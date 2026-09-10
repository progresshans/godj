package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestAtomicCommitRollbackCancellationAndExpiredSession(t *testing.T) {
	t.Parallel()

	state := &transactionTestState{}
	backend := newTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	ctx := context.Background()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	deletePlan := query.NewDeletePlan("article", id, query.Integer(1))

	var retained db.Session
	if err := backend.Atomic(ctx, func(session db.Session) error {
		retained = session
		rows, err := session.Delete(ctx, deletePlan)
		if err != nil || rows != 1 {
			t.Fatalf("Delete() = (%d, %v), want (1, nil)", rows, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("Atomic(commit) error = %v", err)
	}
	committed := state.snapshot()
	if committed.commits != 1 || committed.rollbacks != 0 {
		t.Fatalf("committed state = %#v", committed)
	}
	if committed.lastOptions.Isolation != driver.IsolationLevel(sql.LevelReadCommitted) || committed.lastOptions.ReadOnly {
		t.Fatalf("Atomic transaction options = %#v, want READ COMMITTED READ WRITE", committed.lastOptions)
	}
	if _, err := retained.Delete(ctx, deletePlan); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("expired session error = %v", err)
	}

	rollbackSignal := errors.New("rollback requested")
	err := backend.Atomic(ctx, func(session db.Session) error {
		if _, err := session.Delete(ctx, deletePlan); err != nil {
			return err
		}
		return rollbackSignal
	})
	if !errors.Is(err, rollbackSignal) {
		t.Fatalf("Atomic(rollback) error = %v", err)
	}
	if got := state.snapshot(); got.commits != 1 || got.rollbacks != 1 {
		t.Fatalf("rollback state = %#v", got)
	}

	canceled, cancel := context.WithCancel(ctx)
	err = backend.Atomic(canceled, func(db.Session) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Atomic(canceled) error = %v", err)
	}

	panicValue := "transaction panic"
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("recovered = %#v, want %#v", recovered, panicValue)
			}
		}()
		_ = backend.Atomic(ctx, func(db.Session) error {
			panic(panicValue)
		})
	}()
	if got := state.snapshot(); got.commits != 1 || got.rollbacks < 3 {
		t.Fatalf("cancellation/panic state = %#v", got)
	}
}

func TestAtomicRejectsNilCallbackAndClassifiesUncertainOutcomes(t *testing.T) {
	t.Parallel()

	state := &transactionTestState{}
	backend := newTransactionTestBackend(state)
	t.Cleanup(func() { _ = backend.Close() })
	ctx := context.Background()
	if err := backend.Atomic(ctx, nil); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatalf("Atomic(nil) error = %v", err)
	}
	if state.snapshot().begins != 0 {
		t.Fatalf("nil callback began transaction: %#v", state.snapshot())
	}

	commitFailure := errors.New("commit transport failure")
	state.setCommitError(commitFailure)
	err := backend.Atomic(ctx, func(db.Session) error { return nil })
	if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}) ||
		!errors.Is(err, commitFailure) {
		t.Fatalf("commit failure = %v", err)
	}
	if got := state.snapshot(); got.commits != 1 || got.rollbacks != 0 {
		t.Fatalf("literal COMMIT error must remain outcome-unknown without rollback or retry: %#v", got)
	}
	state.setCommitError(nil)

	rollbackFailure := errors.New("rollback transport failure")
	callbackFailure := errors.New("callback failure")
	state.setRollbackError(rollbackFailure)
	err = backend.Atomic(ctx, func(db.Session) error { return callbackFailure })
	if !errors.Is(err, callbackFailure) || !errors.Is(err, rollbackFailure) ||
		!errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown}) {
		t.Fatalf("rollback failure = %v", err)
	}
}

func TestBackendCloseIsConcurrentAndIdempotent(t *testing.T) {
	t.Parallel()

	state := &transactionTestState{}
	backend := newTransactionTestBackend(state)
	const callers = 32
	var wait sync.WaitGroup
	errorsSeen := make(chan error, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- backend.Close()
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	if _, err := backend.Delete(context.Background(), query.NewDeletePlan("article", id, query.Integer(1))); !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan}) {
		t.Fatalf("post-close Delete() error = %v", err)
	}
}

type transactionTestSnapshot struct {
	begins      int
	commits     int
	rollbacks   int
	execs       int
	lastOptions driver.TxOptions
}

type transactionTestState struct {
	mu          sync.Mutex
	begins      int
	commits     int
	rollbacks   int
	execs       int
	commitErr   error
	rollbackErr error
	lastOptions driver.TxOptions
}

func (state *transactionTestState) snapshot() transactionTestSnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return transactionTestSnapshot{
		begins: state.begins, commits: state.commits, rollbacks: state.rollbacks, execs: state.execs,
		lastOptions: state.lastOptions,
	}
}

func (state *transactionTestState) setCommitError(err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.commitErr = err
}

func (state *transactionTestState) setRollbackError(err error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.rollbackErr = err
}

func newTransactionTestBackend(state *transactionTestState) *Backend {
	database := sql.OpenDB(transactionTestConnector{state: state})
	database.SetMaxOpenConns(1)
	return &Backend{database: database, schema: "godj_test"}
}

type transactionTestConnector struct{ state *transactionTestState }

func (connector transactionTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &transactionTestConnection{state: connector.state}, nil
}
func (connector transactionTestConnector) Driver() driver.Driver {
	return transactionTestDriver{state: connector.state}
}

type transactionTestDriver struct{ state *transactionTestState }

func (testDriver transactionTestDriver) Open(string) (driver.Conn, error) {
	return &transactionTestConnection{state: testDriver.state}, nil
}

type transactionTestConnection struct{ state *transactionTestState }

func (*transactionTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by transaction test driver")
}
func (*transactionTestConnection) Close() error { return nil }
func (connection *transactionTestConnection) Begin() (driver.Tx, error) {
	return connection.BeginTx(context.Background(), driver.TxOptions{})
}
func (connection *transactionTestConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	connection.state.mu.Lock()
	connection.state.begins++
	connection.state.lastOptions = options
	connection.state.mu.Unlock()
	return &transactionTestTransaction{state: connection.state}, nil
}
func (connection *transactionTestConnection) ExecContext(
	ctx context.Context,
	_ string,
	_ []driver.NamedValue,
) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	connection.state.mu.Lock()
	connection.state.execs++
	connection.state.mu.Unlock()
	return driver.RowsAffected(1), nil
}
func (connection *transactionTestConnection) QueryContext(
	ctx context.Context,
	_ string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &transactionTestRows{values: [][]driver.Value{{int64(1)}}}, nil
}

type transactionTestTransaction struct{ state *transactionTestState }

func (transaction *transactionTestTransaction) Commit() error {
	transaction.state.mu.Lock()
	defer transaction.state.mu.Unlock()
	transaction.state.commits++
	return transaction.state.commitErr
}
func (transaction *transactionTestTransaction) Rollback() error {
	transaction.state.mu.Lock()
	defer transaction.state.mu.Unlock()
	transaction.state.rollbacks++
	return transaction.state.rollbackErr
}

type transactionTestRows struct {
	values [][]driver.Value
	index  int
}

func (*transactionTestRows) Columns() []string { return []string{"id"} }
func (*transactionTestRows) Close() error      { return nil }
func (rows *transactionTestRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}
