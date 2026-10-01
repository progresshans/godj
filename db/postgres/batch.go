package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/batchread"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var _ db.BatchQueryer = (*transactionSession)(nil)

func (session *transactionSession) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) (err error) {
	if err := session.validate(ctx); err != nil {
		return err
	}
	if err := batchread.Validate(size, scan, yield); err != nil {
		return err
	}
	finish, err := session.scope.Hold(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, finish()) }()
	statement, arguments, err := compilePlan(session.backend.schema, plan)
	if err != nil {
		return err
	}
	if err := validateRowLockTransaction(plan, true, session.readOnly); err != nil {
		return err
	}
	if plan.EmptyResult() {
		rows, err := session.Query(ctx, plan)
		if err != nil {
			return err
		}
		return batchread.Stream(ctx, session, rows, size, scan, yield, session.validate)
	}
	return queryCursorBatches(ctx, session.transaction, session.backend.schema, session, session.validate, plan, statement, arguments, size, scan, yield, false, nil, session.scope)
}

type cursorExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryCursorBatches(ctx context.Context, executor cursorExecutor, schema string, affinity db.Queryer, validate func(context.Context) error, plan query.Plan, statement string, arguments []any, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error), hold bool, closeFailure func(error) error, scope *txscope.Scope) (err error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return err
	}
	name := "godj_batch_" + hex.EncodeToString(identity[:])
	lifetime := "WITHOUT HOLD"
	if hold {
		lifetime = "WITH HOLD"
	}
	closeCursor := func(cleanup context.Context) error {
		_, closeErr := executor.ExecContext(cleanup, "CLOSE "+name)
		if errors.Is(closeErr, sql.ErrTxDone) || errors.Is(closeErr, sql.ErrConnDone) {
			return nil
		}
		if closeErr != nil {
			closeErr = classifyDatabaseError(cleanup, "close batch cursor", schema, plan.Table(), closeErr)
			if closeFailure != nil {
				closeErr = errors.Join(closeErr, closeFailure(closeErr))
			}
		}
		return closeErr
	}
	declare := "DECLARE " + name + " NO SCROLL CURSOR " + lifetime + " FOR " + statement
	if scope != nil {
		op, beginErr := scope.Begin(ctx)
		if beginErr != nil {
			return beginErr
		}
		defer op.Release()
		if _, err := executor.ExecContext(op.Context(), declare, arguments...); err != nil {
			return op.End(classifyDatabaseError(op.Context(), "declare batch cursor", schema, plan.Table(), err))
		}
		resource, resourceErr := op.Resource(closeCursor)
		if resourceErr != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return op.End(errors.Join(resourceErr, closeCursor(cleanup)))
		}
		defer func() { err = errors.Join(err, resource.Close(), ctx.Err()) }()
		if err := op.End(nil); err != nil {
			return err
		}
	} else {
		if _, err := executor.ExecContext(ctx, declare, arguments...); err != nil {
			failure := classifyDatabaseError(ctx, "declare batch cursor", schema, plan.Table(), err)
			if closeFailure != nil {
				failure = errors.Join(failure, closeFailure(failure))
			}
			return failure
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, closeCursor(cleanup), ctx.Err())
		}()
	}
	for {
		if err := validate(ctx); err != nil {
			return err
		}
		count, err := scanCursorBatch(ctx, executor, schema, validate, plan, name, size, scan, scope)
		if err != nil || count == 0 {
			return err
		}
		more, err := batchread.Yield(ctx, affinity, yield, validate)
		if err != nil || !more || count < size {
			return err
		}
	}
}

func scanCursorBatch(ctx context.Context, executor cursorExecutor, schema string, validate func(context.Context) error, plan query.Plan, name string, size int, scan func(db.Row) error, scope *txscope.Scope) (count int, err error) {
	var op *txscope.Operation
	executionContext := ctx
	if scope != nil {
		op, err = scope.Begin(ctx)
		if err != nil {
			return 0, err
		}
		defer op.Release()
		executionContext = op.Context()
	}
	native, err := executor.QueryContext(executionContext, "FETCH FORWARD "+strconv.Itoa(size)+" FROM "+name)
	if err != nil {
		failure := classifyDatabaseError(executionContext, "fetch batch cursor", schema, plan.Table(), err)
		if op != nil {
			failure = op.End(failure)
		}
		return 0, failure
	}
	if owner, ok := executor.(interface{ ForgetRows(*sql.Rows) }); ok {
		defer owner.ForgetRows(native)
	}
	rows, err := adaptScalarRows(native, plan)
	if err != nil {
		return 0, err
	}
	if op != nil {
		rows, err = op.Rows(rows, nil)
		if err != nil {
			return 0, err
		}
	}
	defer func() { err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err()) }()
	return batchread.Scan(ctx, rows, size, scan, validate)
}
