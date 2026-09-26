package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/batchtest"
)

func TestSQLiteRootBatches(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		"CREATE TABLE batch_source(value INTEGER NOT NULL)", "INSERT INTO batch_source VALUES(3),(1),(1),(4),(2)",
		"CREATE TABLE batch_writes(id INTEGER PRIMARY KEY,value INTEGER NOT NULL UNIQUE)",
	} {
		if _, err := backend.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	batchtest.CheckRoot(t, backend)
}

func TestSQLiteRootBatchAdmissionAllowsScopedWriteAheadOfWaitingRootWriter(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	backend, err := OpenMemory(ctx, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	for _, statement := range []string{"CREATE TABLE batch_source(value INTEGER)", "INSERT INTO batch_source VALUES(1),(2),(3)"} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	waiting := &doneObservedContext{Context: ctx, observed: make(chan struct{})}
	result := make(chan error, 1)
	var calls atomic.Int64
	err = backend.QueryBatches(ctx, batchtest.Plan(), 2, func(row db.Row) error { var value int64; return row.Scan(&value) }, func(affinity db.Queryer) (bool, error) {
		go func() {
			result <- backend.AtomicRelation(waiting, func(db.RelationSession) error { calls.Add(1); return nil })
		}()
		select {
		case <-waiting.observed:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		if calls.Load() != 0 {
			t.Fatal("waiting writer entered active stream admission")
		}
		return false, affinity.(db.RelationAtomic).AtomicRelation(ctx, func(session db.RelationSession) error {
			values, err := batchtest.Read(ctx, session, batchtest.Plan())
			if err != nil || len(values) != 3 {
				t.Fatal(values, err)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil || calls.Load() != 1 {
			t.Fatal(calls.Load(), err)
		}
	case <-ctx.Done():
		t.Fatal("writer remained blocked", ctx.Err())
	}
}

func TestSQLiteRootBatchUnconfirmedCleanupKeepsRetainedConnectionOwned(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "retained-stream.sqlite")
	backend, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	backend.database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = backend.Close() })
	for _, statement := range []string{"CREATE TABLE batch_source(value INTEGER)", "INSERT INTO batch_source VALUES(1),(2),(3)", "CREATE TABLE batch_writes(id INTEGER PRIMARY KEY,value INTEGER)"} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	signal := errors.New("authored transaction failure")
	rollbackFailure := errors.New("unconfirmed rollback")
	var fault *relationFaultConnection
	err = backend.QueryBatches(ctx, batchtest.Plan(), 2, func(row db.Row) error { var value int64; return row.Scan(&value) }, func(affinity db.Queryer) (bool, error) {
		scope := affinity.(*rootBatchScope)
		lease, err := scope.connection.Acquire()
		if err != nil {
			return false, err
		}
		fault = &relationFaultConnection{
			exec: func(ctx context.Context, statement string, args []any) (sql.Result, error) {
				if statement == "ROLLBACK" {
					return nil, rollbackFailure
				}
				return lease.ExecContext(ctx, statement, args...)
			},
			query: func(ctx context.Context, statement string, args []any) (*sql.Rows, error) {
				return lease.QueryContext(ctx, statement, args...)
			},
			raw: func(func(any) error) error { _ = scope.connection.Revoke(rollbackFailure); return nil },
			closeHook: func() {
				if err := lease.Close(); err != nil {
					t.Error(err)
				}
			},
		}
		err = executeAdmittedAtomicRelation(ctx, func(session db.RelationSession) error {
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			if _, err := session.Insert(ctx, query.NewInsertPlanReturningKey("batch_writes", []query.Assignment{query.NewAssignment(batchtest.Value, query.Integer(99))}, id)); err != nil {
				return err
			}
			return signal
		}, fault, scope.admission, &backend.queryCount)
		return false, err
	})
	if !errors.Is(err, signal) || !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) || !errors.Is(err, &query.Error{Code: query.CodeBackendRecoveryRequired}) {
		t.Fatal(err)
	}
	if fault == nil || fault.closeCalls.Load() != 0 || backend.database.Stats().InUse != 1 || retainedConnectionCount(backend.relationRetention) != 1 {
		t.Fatal("stream returned unconfirmed connection to pool")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if fault.closeCalls.Load() != 1 || backend.database.Stats().OpenConnections != 0 {
		t.Fatal("retained lease was not physically closed")
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	values, err := batchtest.Read(ctx, reopened, query.NewPlan("batch_writes", []query.FieldRef{batchtest.Value}))
	if err != nil || len(values) != 0 {
		t.Fatal("uncommitted write survived physical close", values, err)
	}
}
