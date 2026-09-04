package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func TestAtomicLiteralCommitErrorIsUnknownAndIsNotRetried(t *testing.T) {
	t.Parallel()

	commitErr := errors.New("literal commit failure")
	state := &atomicCommitFaultState{commitErr: commitErr}
	database := sql.OpenDB(atomicCommitFaultConnector{state: state})
	database.SetMaxOpenConns(1)
	backend := &Backend{database: database}
	t.Cleanup(func() { _ = backend.Close() })

	var callbackCalls atomic.Int32
	err := backend.Atomic(context.Background(), func(db.Session) error {
		callbackCalls.Add(1)
		return nil
	})
	var classified *query.Error
	if !errors.As(err, &classified) || classified.Category != query.CategoryBackend ||
		classified.Code != query.CodeCommitOutcomeUnknown ||
		classified.Detail != "SQLite commit outcome is unknown; do not retry automatically" ||
		classified.Cause != commitErr || !errors.Is(err, commitErr) {
		t.Fatalf("Atomic() error = %v", err)
	}
	if callbackCalls.Load() != 1 || state.commitCalls.Load() != 1 {
		t.Fatalf("callback/COMMIT calls = %d/%d, want 1/1", callbackCalls.Load(), state.commitCalls.Load())
	}
}

func TestSQLiteCommitUnknownPreservesCommitAndRollbackErrors(t *testing.T) {
	t.Parallel()

	commitErr := errors.New("commit transport failure")
	rollbackErr := errors.New("rollback cleanup failure")
	err := sqliteCommitUnknown(commitErr, rollbackErr)
	var classified *query.Error
	if !errors.As(err, &classified) || classified.Category != query.CategoryBackend ||
		classified.Code != query.CodeCommitOutcomeUnknown ||
		!errors.Is(err, commitErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("sqliteCommitUnknown() error = %v", err)
	}

	err = sqliteCommitUnknown(commitErr, sql.ErrTxDone)
	if !errors.Is(err, commitErr) || errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("sqliteCommitUnknown(ErrTxDone) error = %v", err)
	}
}

type atomicCommitFaultState struct {
	commitErr   error
	commitCalls atomic.Int32
}

type atomicCommitFaultConnector struct {
	state *atomicCommitFaultState
}

func (connector atomicCommitFaultConnector) Connect(context.Context) (driver.Conn, error) {
	return &atomicCommitFaultConnection{state: connector.state}, nil
}

func (connector atomicCommitFaultConnector) Driver() driver.Driver {
	return atomicCommitFaultDriver{state: connector.state}
}

type atomicCommitFaultDriver struct {
	state *atomicCommitFaultState
}

func (testDriver atomicCommitFaultDriver) Open(string) (driver.Conn, error) {
	return &atomicCommitFaultConnection{state: testDriver.state}, nil
}

type atomicCommitFaultConnection struct {
	state *atomicCommitFaultState
}

func (*atomicCommitFaultConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by atomic commit fault driver")
}

func (*atomicCommitFaultConnection) Close() error { return nil }

func (connection *atomicCommitFaultConnection) Begin() (driver.Tx, error) {
	return &atomicCommitFaultTransaction{state: connection.state}, nil
}

type atomicCommitFaultTransaction struct {
	state *atomicCommitFaultState
}

func (transaction *atomicCommitFaultTransaction) Commit() error {
	transaction.state.commitCalls.Add(1)
	return transaction.state.commitErr
}

func (*atomicCommitFaultTransaction) Rollback() error { return nil }
