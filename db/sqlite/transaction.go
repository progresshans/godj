package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var _ db.Atomic = (*Backend)(nil)
var _ db.Session = (*transactionSession)(nil)

type transactionSession struct {
	transaction *sql.Tx
	backend     *Backend
	active      atomic.Bool
}

// Atomic rolls back on callback errors and context cancellation observed
// before commit. A literal COMMIT error has an unknown outcome and requires
// reconciliation rather than an automatic retry. A panic is not converted
// into a framework error: the deferred rollback runs and the original panic
// continues to the caller.
func (b *Backend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	if err := b.validateWriteContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "atomic callback is nil"}
	}
	transaction, err := b.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite transaction: %w", err)
	}
	session := &transactionSession{transaction: transaction, backend: b}
	session.active.Store(true)
	finished := false
	defer func() {
		session.active.Store(false)
		if !finished {
			_ = transaction.Rollback()
		}
	}()

	if callbackErr := callback(session); callbackErr != nil {
		session.active.Store(false)
		rollbackErr := transaction.Rollback()
		finished = true
		if errors.Is(rollbackErr, sql.ErrTxDone) {
			rollbackErr = nil
		}
		if rollbackErr != nil {
			return errors.Join(callbackErr, fmt.Errorf("rollback SQLite transaction: %w", rollbackErr))
		}
		return callbackErr
	}
	session.active.Store(false)
	if contextErr := ctx.Err(); contextErr != nil {
		rollbackErr := transaction.Rollback()
		finished = true
		if errors.Is(rollbackErr, sql.ErrTxDone) {
			rollbackErr = nil
		}
		if rollbackErr != nil {
			return errors.Join(contextErr, fmt.Errorf("rollback canceled SQLite transaction: %w", rollbackErr))
		}
		return contextErr
	}
	if commitErr := transaction.Commit(); commitErr != nil {
		rollbackErr := transaction.Rollback()
		finished = true
		return sqliteCommitUnknown(commitErr, rollbackErr)
	}
	finished = true
	return nil
}

func sqliteCommitUnknown(commitErr, rollbackErr error) error {
	if errors.Is(rollbackErr, sql.ErrTxDone) {
		rollbackErr = nil
	}
	cause := commitErr
	if rollbackErr != nil {
		cause = errors.Join(commitErr, wrapTransactionRollback(rollbackErr))
	}
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "SQLite commit outcome is unknown; do not retry automatically",
		Cause:    cause,
	}
}

func wrapTransactionRollback(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("rollback SQLite transaction after commit failure: %w", err)
}

func (session *transactionSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	statement, arguments, err := Compile(plan)
	if err != nil {
		return nil, err
	}
	session.backend.queryCount.Add(1)
	rows, err := session.transaction.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("execute SQLite transaction query: %w", err)
	}
	return rows, nil
}

func (session *transactionSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return executeInsert(ctx, session.transaction, plan)
}

func (session *transactionSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return executeUpdate(ctx, session.transaction, plan)
}

func (session *transactionSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return executeDelete(ctx, session.transaction, plan)
}

func (session *transactionSession) validate(ctx context.Context) error {
	if session == nil || session.transaction == nil || !session.active.Load() {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite transaction session is nil or no longer active"}
	}
	if ctx == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
