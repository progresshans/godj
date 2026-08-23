package postgres

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

// Atomic executes callback once in a transaction-bound Session. Callback
// errors and cancellation are rolled back; a rollback failure makes the
// transaction outcome unknown. Any literal COMMIT error is deliberately
// classified as outcome-unknown and requires reconciliation rather than an
// automatic retry.
func (b *Backend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	if err := b.validateContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "atomic callback is nil"}
	}
	transaction, err := b.database.BeginTx(ctx, nil)
	if err != nil {
		return classifyDatabaseError(ctx, "begin transaction", b.schema, "", err)
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
		rollbackErr := normalizeRollbackError(transaction.Rollback())
		finished = true
		if rollbackErr != nil {
			return errors.Join(
				callbackErr,
				transactionUnknown("PostgreSQL callback failed and rollback outcome is unknown", rollbackErr),
			)
		}
		return callbackErr
	}

	session.active.Store(false)
	if contextErr := ctx.Err(); contextErr != nil {
		rollbackErr := normalizeRollbackError(transaction.Rollback())
		finished = true
		if rollbackErr != nil {
			return errors.Join(
				contextErr,
				transactionUnknown("PostgreSQL context was canceled and rollback outcome is unknown", rollbackErr),
			)
		}
		return contextErr
	}
	if err := transaction.Commit(); err != nil {
		finished = true
		return commitUnknown(err)
	}
	finished = true
	return nil
}

func normalizeRollbackError(err error) error {
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return fmt.Errorf("rollback PostgreSQL transaction: %w", err)
}

func (session *transactionSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	statement, arguments, err := compilePlan(session.backend.schema, plan)
	if err != nil {
		return nil, err
	}
	rows, err := session.transaction.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, classifyDatabaseError(ctx, "transaction query", session.backend.schema, plan.Table(), err)
	}
	return rows, nil
}

func (session *transactionSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return executeInsert(ctx, session.transaction, session.backend.schema, plan)
}

func (session *transactionSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return executeUpdate(ctx, session.transaction, session.backend.schema, plan)
}

func (session *transactionSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return executeDelete(ctx, session.transaction, session.backend.schema, plan)
}

func (session *transactionSession) validate(ctx context.Context) error {
	if session == nil || session.transaction == nil || session.backend == nil || !session.active.Load() {
		return backendInvalid("PostgreSQL transaction session is nil or no longer active")
	}
	if ctx == nil {
		return backendInvalid("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
