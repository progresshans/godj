package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/batchread"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/streamconn"
	"github.com/progresshans/godj/query"
)

var _ db.BatchQueryer = (*Backend)(nil)

type rootBatchScope struct {
	backend    *Backend
	connection *streamconn.Owner
}

func (b *Backend) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) (err error) {
	if err := b.validateContext(ctx); err != nil {
		return err
	}
	if err := batchread.Validate(size, scan, yield); err != nil {
		return err
	}
	statement, arguments, err := compilePlan(b.schema, plan)
	if err != nil {
		return err
	}
	if plan.EmptyResult() {
		rows, err := queryplan.EmptyRows(ctx, plan.ResultShape())
		if err != nil {
			return err
		}
		return batchread.Stream(ctx, b, rows, size, scan, yield, b.validateContext)
	}
	connection, err := b.database.Conn(ctx)
	if err != nil {
		return err
	}
	owner := streamconn.New(connection)
	defer func() { err = errors.Join(err, owner.Finish(), ctx.Err()) }()
	scope := &rootBatchScope{backend: b, connection: owner}
	return scope.queryBatches(ctx, plan, statement, arguments, size, scan, yield)
}

func (scope *rootBatchScope) acquire(ctx context.Context) (*streamconn.Lease, error) {
	if err := scope.backend.validateContext(ctx); err != nil {
		return nil, err
	}
	return scope.connection.Acquire()
}
func (scope *rootBatchScope) validateStream(ctx context.Context) error {
	if err := scope.backend.validateContext(ctx); err != nil {
		return err
	}
	return scope.connection.Validate(ctx)
}

func (scope *rootBatchScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return nil, err
	}
	if lease == nil {
		return scope.backend.Query(ctx, plan)
	}
	statement, arguments, err := compilePlan(scope.backend.schema, plan)
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	if plan.EmptyResult() {
		if err := lease.Close(); err != nil {
			return nil, err
		}
		return queryplan.EmptyRows(ctx, plan.ResultShape())
	}
	native, err := lease.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, errors.Join(classifyDatabaseError(ctx, "stream query", scope.backend.schema, plan.Table(), err), lease.Close())
	}
	rows, err := adaptScalarRows(native, plan)
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	return lease.Rows(rows), nil
}
func (scope *rootBatchScope) Insert(ctx context.Context, plan query.InsertPlan) (value int64, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if lease == nil {
		return scope.backend.Insert(ctx, plan)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	return executeInsert(ctx, lease, scope.backend.schema, plan)
}
func (scope *rootBatchScope) Update(ctx context.Context, plan query.UpdatePlan) (value int64, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if lease == nil {
		return scope.backend.Update(ctx, plan)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	return executeUpdate(ctx, lease, scope.backend.schema, plan)
}
func (scope *rootBatchScope) Delete(ctx context.Context, plan query.DeletePlan) (value int64, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if lease == nil {
		return scope.backend.Delete(ctx, plan)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	return executeDelete(ctx, lease, scope.backend.schema, plan)
}
func (scope *rootBatchScope) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (inserted bool, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return false, err
	}
	if lease == nil {
		return scope.backend.InsertOnConflict(ctx, plan)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	return executeConflictInsert(ctx, lease, scope.backend.schema, plan)
}

func (scope *rootBatchScope) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return scope.atomic(ctx, callback, false)
}
func (scope *rootBatchScope) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if callback == nil {
		return scope.atomic(ctx, nil, false)
	}
	return scope.atomic(ctx, func(session db.Session) error { return callback(session.(*transactionSession)) }, false)
}
func (scope *rootBatchScope) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return scope.atomic(ctx, callback, true)
}
func (scope *rootBatchScope) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if callback == nil {
		return scope.atomic(ctx, nil, true)
	}
	return scope.atomic(ctx, func(session db.Session) error { return callback(session.(*transactionSession)) }, true)
}

func (scope *rootBatchScope) atomic(ctx context.Context, callback func(db.Session) error, coordinated bool) (err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return err
	}
	if lease == nil {
		if coordinated {
			return scope.backend.CoordinatedAtomic(ctx, callback)
		}
		return scope.backend.Atomic(ctx, callback)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "atomic callback is nil"}
	}
	err = scope.backend.atomic(ctx, func(ctx context.Context) (transactionHandle, error) {
		if _, err := lease.ExecContext(ctx, "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE"); err != nil {
			return nil, errors.Join(err, scope.discard(lease, err))
		}
		return &pinnedTransaction{Lease: lease, scope: scope, ctx: ctx}, nil
	}, func(session *transactionSession) (err error) {
		defer func() { err = errors.Join(err, lease.CloseRows()) }()
		if coordinated {
			if _, err := session.transaction.ExecContext(ctx, postgresCoordinatedAtomicLock, postgresCoordinatedAtomicAdvisoryLockKey(scope.backend.schema)); err != nil {
				return classifyDatabaseError(ctx, "acquire coordinated transaction fence", scope.backend.schema, "", err)
			}
		}
		return callback(session)
	})
	return err
}

// A pinned connection cannot be reused while database/sql's asynchronous
// cancellation rollback is still running. The stream owns terminal SQL here;
// session lifetime checks still observe the original caller context.
type pinnedTransaction struct {
	*streamconn.Lease
	scope    *rootBatchScope
	ctx      context.Context
	done     bool
	readOnly bool
}

func (transaction *pinnedTransaction) Commit() error {
	if transaction.done {
		return sql.ErrTxDone
	}
	transaction.done = true
	_, err := transaction.ExecContext(transaction.ctx, "COMMIT")
	if err != nil {
		return errors.Join(err, transaction.scope.discard(transaction.Lease, commitUnknown(err)))
	}
	return nil
}

func (transaction *pinnedTransaction) Rollback() error {
	if transaction.done {
		return sql.ErrTxDone
	}
	transaction.done = true
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(transaction.ctx), 5*time.Second)
	defer cancel()
	_, err := transaction.ExecContext(cleanup, "ROLLBACK")
	if err != nil {
		cause := err
		if !transaction.readOnly {
			cause = transactionUnknown("pinned PostgreSQL rollback outcome is unknown", err)
		}
		return errors.Join(err, transaction.scope.discard(transaction.Lease, cause))
	}
	return nil
}

func (scope *rootBatchScope) discard(lease *streamconn.Lease, cause error) error {
	cleanup := scope.connection.Revoke(cause)
	err := lease.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		return cleanup
	}
	return errors.Join(cleanup, err)
}

func (scope *rootBatchScope) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	if err := scope.backend.validateContext(ctx); err != nil {
		return err
	}
	if err := batchread.Validate(size, scan, yield); err != nil {
		return err
	}
	statement, arguments, err := compilePlan(scope.backend.schema, plan)
	if err != nil {
		return err
	}
	return scope.queryBatches(ctx, plan, statement, arguments, size, scan, yield)
}

func (scope *rootBatchScope) queryBatches(ctx context.Context, plan query.Plan, statement string, arguments []any, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) (err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return err
	}
	if lease == nil {
		return scope.backend.QueryBatches(ctx, plan, size, scan, yield)
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	consume := func(affinity db.Queryer) (more bool, err error) {
		defer func() { err = errors.Join(err, scope.connection.FinishReads()) }()
		return yield(affinity)
	}

	if plan.EmptyResult() {
		rows, err := queryplan.EmptyRows(ctx, plan.ResultShape())
		if err != nil {
			return err
		}
		return batchread.Stream(ctx, scope, rows, size, scan, consume, scope.validateStream)
	}
	return queryCursorBatches(ctx, lease, scope.backend.schema, scope, scope.validateStream, plan, statement, arguments, size, scan, consume, true, func(err error) error { return scope.discard(lease, err) })
}
