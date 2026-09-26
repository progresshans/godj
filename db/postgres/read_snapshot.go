package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/readscope"
	"github.com/progresshans/godj/db/internal/streamconn"
	"github.com/progresshans/godj/query"
)

var _ db.SnapshotReader = (*Backend)(nil)

// ReadSnapshot uses one REPEATABLE READ, READ ONLY transaction. Ending it
// rolls back the read scope; it does not request or claim a durable write.
func (b *Backend) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) (err error) {
	if err := b.validateContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "read snapshot callback is nil"}
	}
	connection, err := b.database.Conn(ctx)
	if err != nil {
		return classifyDatabaseError(ctx, "acquire read snapshot connection", b.schema, "", err)
	}
	owner := streamconn.New(connection)
	defer func() { err = errors.Join(err, owner.Finish()) }()
	lease, err := owner.Acquire()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	scope := &rootBatchScope{backend: b, connection: owner}
	if _, err := lease.ExecContext(ctx, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY NOT DEFERRABLE"); err != nil {
		return errors.Join(classifyDatabaseError(ctx, "begin read snapshot", b.schema, "", err), scope.discard(lease, err))
	}
	transaction := &pinnedTransaction{Lease: lease, scope: scope, ctx: ctx, readOnly: true}
	lifetime, finish := context.WithCancelCause(ctx)
	session := &transactionSession{transaction: transaction, backend: b, lifetime: lifetime}
	session.active.Store(true)
	return readscope.Run(ctx, session, callback, func() {
		session.active.Store(false)
		finish(sql.ErrTxDone)
	}, func() error { return errors.Join(lease.CloseRows(), transaction.Rollback()) })
}
