package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var _ db.Atomic = (*Backend)(nil)
var _ db.RelationAtomic = (*Backend)(nil)
var _ db.Session = (*transactionSession)(nil)
var _ db.RelationSession = (*transactionSession)(nil)
var _ db.SessionValidator = (*transactionSession)(nil)
var _ db.Savepointer = (*transactionSession)(nil)

type transactionSession struct {
	transaction transactionHandle
	backend     *Backend
	readOnly    bool
	scope       *txscope.Scope
}

func (session *transactionSession) ValidateSession(ctx context.Context) error {
	return session.validate(ctx)
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
		return b.atomic(ctx, nil, nil)
	}
	return b.atomic(ctx, nil, func(session *transactionSession) error { return callback(session) })
}

// AtomicRelation shares the same transaction owner, session lifetime and
// uncertain-outcome handling as ordinary writes. It adds bulk SET_NULL to the
// transaction-bound callback without introducing a second transaction.
func (b *Backend) AtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	if err := b.validateContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return b.atomic(ctx, nil, nil)
	}
	return b.atomic(ctx, nil, func(session *transactionSession) error { return callback(session) })
}

type transactionHandle interface {
	cursorExecutor
	QueryRowContext(context.Context, string, ...any) *sql.Row
	Commit() error
	Rollback() error
}

func (b *Backend) atomic(ctx context.Context, begin func(context.Context) (transactionHandle, error), callback func(*transactionSession) error) error {
	if err := b.validateContext(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "atomic callback is nil"}
	}
	if begin == nil {
		begin = func(ctx context.Context) (transactionHandle, error) {
			return b.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
		}
	}
	transaction, err := begin(ctx)
	if err != nil {
		return classifyDatabaseError(ctx, "begin transaction", b.schema, "", err)
	}
	session := newTransactionSession(ctx, transaction, b)
	finished := false
	defer func() {
		if !finished {
			_ = session.scope.Finish()
			_ = transaction.Rollback()
		}
	}()

	if callbackErr := session.scope.FinishCallback(callback(session)); callbackErr != nil {
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

func newTransactionSession(ctx context.Context, transaction transactionHandle, backend *Backend) *transactionSession {
	return &transactionSession{transaction: transaction, backend: backend, scope: txscope.New(ctx, func(ctx context.Context, statement string) error {
		_, err := transaction.ExecContext(ctx, statement)
		return classifyDatabaseError(ctx, "savepoint control", backend.schema, "", err)
	})}
}

func (session *transactionSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if err := session.validate(ctx); err != nil {
		return nil, err
	}
	return txscope.Query(session.scope, ctx, func(ctx context.Context) (db.Rows, error) {
		statement, arguments, err := compilePlan(session.backend.schema, plan)
		if err != nil {
			return nil, err
		}
		if err := validateRowLockTransaction(plan, true, session.readOnly); err != nil {
			return nil, err
		}
		if plan.EmptyResult() {
			return queryplan.EmptyRows(ctx, plan.ResultShape())
		}
		rows, err := session.transaction.QueryContext(ctx, statement, arguments...)
		if err != nil {
			return nil, classifyDatabaseError(ctx, "transaction query", session.backend.schema, plan.Table(), err)
		}
		adapted, err := adaptScalarRows(rows, plan)
		if err != nil {
			return nil, err
		}
		if owner, ok := session.transaction.(interface {
			WrapRows(db.Rows, *sql.Rows) db.Rows
		}); ok {
			return owner.WrapRows(adapted, rows), nil
		}
		return adapted, nil
	})
}

func (session *transactionSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		return executeInsert(ctx, session.transaction, session.backend.schema, plan)
	})
}

func (session *transactionSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		return executeUpdate(ctx, session.transaction, session.backend.schema, plan)
	})
}

func (session *transactionSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		return executeDelete(ctx, session.transaction, session.backend.schema, plan)
	})
}

func (session *transactionSession) validate(ctx context.Context) error {
	if session == nil || session.transaction == nil || session.backend == nil || session.scope == nil {
		return backendInvalid("PostgreSQL transaction session is nil or no longer active")
	}
	return session.scope.Validate(ctx)
}

func (session *transactionSession) Savepoint(ctx context.Context, callback func(db.Session) error) error {
	if err := session.validate(ctx); err != nil {
		return err
	}
	if callback == nil {
		return session.scope.Savepoint(ctx, nil)
	}
	return session.scope.Savepoint(ctx, func(child *txscope.Scope) error {
		return callback(&transactionSession{transaction: session.transaction, backend: session.backend, readOnly: session.readOnly, scope: child})
	})
}
