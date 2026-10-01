package sqlite

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/batchread"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/streamconn"
	"github.com/progresshans/godj/query"
)

var _ db.BatchQueryer = (*Backend)(nil)

// rootBatchScope is a root executor, not a borrowed transaction. Retained
// models use the original backend after the iterator releases its connection.
type rootBatchScope struct {
	backend    *Backend
	connection *streamconn.Owner
	admission  *relationTransactionAdmission
}

func (b *Backend) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) (err error) {
	if err := b.validateBackendContext(ctx); err != nil {
		return err
	}
	if err := batchread.Validate(size, scan, yield); err != nil {
		return err
	}
	statement, arguments, err := Compile(plan)
	if err != nil {
		return err
	}
	if plan.EmptyResult() {
		rows, err := queryplan.EmptyRows(ctx, plan.ResultShape())
		if err != nil {
			return err
		}
		return batchread.Stream(ctx, b, rows, size, scan, yield, b.validateBackendContext)
	}
	// Reserve raw-transaction admission before pinning the connection. Taking
	// it from yield can deadlock a single-connection pool behind a writer that
	// already owns admission and is waiting for this stream's connection.
	admission, err := b.relationRetention.acquire(ctx)
	if err != nil {
		return err
	}
	defer admission.release()
	if err := b.validateBackendContext(ctx); err != nil {
		return err
	}
	connection, err := b.database.Conn(ctx)
	if err != nil {
		return err
	}
	owner := streamconn.New(connection)
	defer func() { err = errors.Join(err, owner.Finish(), ctx.Err()) }()
	scope := &rootBatchScope{backend: b, connection: owner, admission: admission}
	rows, err := scope.queryCompiled(ctx, statement, arguments, plan.Table())
	if err != nil {
		return err
	}
	return batchread.Stream(ctx, scope, rows, size, scan, scope.withReadCleanup(yield), scope.validateStream)
}

func (scope *rootBatchScope) validateStream(ctx context.Context) error {
	if err := scope.backend.validateBackendContext(ctx); err != nil {
		return err
	}
	return scope.connection.Validate(ctx)
}

func (scope *rootBatchScope) acquire(ctx context.Context) (*streamconn.Lease, error) {
	if err := scope.backend.validateBackendContext(ctx); err != nil {
		return nil, err
	}
	return scope.connection.Acquire()
}

func (scope *rootBatchScope) queryCompiled(ctx context.Context, statement string, arguments []any, table string) (db.Rows, error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return nil, err
	}
	// Only active stream calls use this helper; ordinary Query handles the
	// post-stream fallback before compiling.
	if lease == nil {
		return nil, invalidPlan("stream source connection has finished")
	}
	lease.SourceRows()
	scope.backend.queryCount.Add(1)
	rows, err := lease.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, errors.Join(classifyQueryError(err, table), lease.Close())
	}
	return lease.Rows(rows), nil
}

func (scope *rootBatchScope) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return nil, err
	}
	if lease == nil {
		return scope.backend.Query(ctx, plan)
	}
	statement, arguments, err := Compile(plan)
	if err != nil {
		return nil, errors.Join(err, lease.Close())
	}
	if plan.EmptyResult() {
		if err := lease.Close(); err != nil {
			return nil, err
		}
		return queryplan.EmptyRows(ctx, plan.ResultShape())
	}
	scope.backend.queryCount.Add(1)
	rows, err := lease.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, errors.Join(classifyQueryError(err, plan.Table()), lease.Close())
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
	return executeInsert(ctx, lease, plan)
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
	return executeUpdate(ctx, lease, plan)
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
	return executeDelete(ctx, lease, plan)
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
	statement, arguments, err := CompileConflictInsert(plan)
	if err != nil {
		return false, err
	}
	return executeConflictInsert(ctx, lease, statement, arguments)
}

func (scope *rootBatchScope) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return scope.atomic(ctx, callback, false, false)
}
func (scope *rootBatchScope) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	return scope.atomic(ctx, callback, true, false)
}
func (scope *rootBatchScope) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if callback == nil {
		return scope.atomic(ctx, nil, false, true)
	}
	return scope.atomic(ctx, func(session db.Session) error { return callback(session.(db.RelationSession)) }, false, true)
}
func (scope *rootBatchScope) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if callback == nil {
		return scope.atomic(ctx, nil, true, true)
	}
	return scope.atomic(ctx, func(session db.Session) error { return callback(session.(db.RelationSession)) }, true, true)
}

func (scope *rootBatchScope) atomic(ctx context.Context, callback func(db.Session) error, coordinated, relations bool) error {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return err
	}
	if lease == nil {
		if relations {
			var relationCallback func(db.RelationSession) error
			if callback != nil {
				relationCallback = func(session db.RelationSession) error { return callback(session) }
			}
			if coordinated {
				return scope.backend.CoordinatedAtomicRelation(ctx, relationCallback)
			}
			return scope.backend.AtomicRelation(ctx, relationCallback)
		}
		if coordinated {
			return scope.backend.CoordinatedAtomic(ctx, callback)
		}
		return scope.backend.Atomic(ctx, callback)
	}
	if callback == nil {
		return errors.Join(invalidPlan("atomic callback is nil"), lease.Close())
	}
	consume := callback
	callback = func(session db.Session) (err error) {
		defer func() { err = errors.Join(err, lease.CloseRows()) }()
		return consume(session)
	}
	if relations {
		if err := verifyRelationForeignKeys(ctx, lease.Native()); err != nil {
			return errors.Join(err, lease.Close())
		}
		if !coordinated {
			return executeAdmittedAtomicRelation(ctx, func(session db.RelationSession) error { return callback(session) }, lease, scope.admission, &scope.backend.queryCount)
		}
		return executeAdmittedWriteAtomic(ctx, func(session db.Session) error { return callback(session.(*writeSession).session) }, lease, scope.admission, &scope.backend.queryCount, true)
	}
	return executeAdmittedWriteAtomic(ctx, callback, lease, scope.admission, &scope.backend.queryCount, coordinated)
}

func (scope *rootBatchScope) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return err
	}
	if lease == nil {
		return scope.backend.QueryBatches(ctx, plan, size, scan, yield)
	}
	if err := lease.Close(); err != nil {
		return err
	}
	if err := batchread.Validate(size, scan, yield); err != nil {
		return err
	}
	statement, arguments, err := Compile(plan)
	if err != nil {
		return err
	}
	var rows db.Rows
	if plan.EmptyResult() {
		rows, err = queryplan.EmptyRows(ctx, plan.ResultShape())
	} else {
		rows, err = scope.queryCompiled(ctx, statement, arguments, plan.Table())
	}
	if err != nil {
		return err
	}
	return batchread.Stream(ctx, scope, rows, size, scan, scope.withReadCleanup(yield), scope.validateStream)
}

func (scope *rootBatchScope) withReadCleanup(yield func(db.Queryer) (bool, error)) func(db.Queryer) (bool, error) {
	return func(affinity db.Queryer) (more bool, err error) {
		defer func() { err = errors.Join(err, scope.connection.FinishReads()) }()
		return yield(affinity)
	}
}
