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
	"github.com/progresshans/godj/db/internal/queryplan"
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
	statement, arguments, err := compilePlan(session.backend.schema, plan)
	if err != nil {
		return err
	}
	if plan.EmptyResult() {
		rows, err := queryplan.EmptyRowsInSession(ctx, session.lifetime, plan.ResultShape())
		if err != nil {
			return err
		}
		return batchread.Stream(ctx, session, rows, size, scan, yield, session.validate)
	}
	return queryCursorBatches(ctx, session.transaction, session.backend.schema, session, session.validate, plan, statement, arguments, size, scan, yield, false, nil)
}

type cursorExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func queryCursorBatches(ctx context.Context, executor cursorExecutor, schema string, affinity db.Queryer, validate func(context.Context) error, plan query.Plan, statement string, arguments []any, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error), hold bool, closeFailure func(error) error) (err error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return err
	}
	name := "godj_batch_" + hex.EncodeToString(identity[:])
	lifetime := "WITHOUT HOLD"
	if hold {
		lifetime = "WITH HOLD"
	}
	if _, err := executor.ExecContext(ctx, "DECLARE "+name+" NO SCROLL CURSOR "+lifetime+" FOR "+statement, arguments...); err != nil {
		failure := classifyDatabaseError(ctx, "declare batch cursor", schema, plan.Table(), err)
		if closeFailure != nil {
			return errors.Join(failure, closeFailure(failure))
		}
		return failure
	}
	defer func() {
		// Cursor cleanup is independent of the per-call cancellation. The
		// surrounding transaction still owns rollback/commit and its lifetime.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, closeErr := executor.ExecContext(cleanup, "CLOSE "+name)
		if errors.Is(closeErr, sql.ErrTxDone) || errors.Is(closeErr, sql.ErrConnDone) {
			closeErr = nil // Transaction termination already closes its cursors.
		}
		if closeErr != nil {
			closeErr = classifyDatabaseError(cleanup, "close batch cursor", schema, plan.Table(), closeErr)
			if closeFailure != nil {
				closeErr = errors.Join(closeErr, closeFailure(closeErr))
			}
		}
		err = errors.Join(err, closeErr, ctx.Err())
	}()
	for {
		if err := validate(ctx); err != nil {
			return err
		}
		count, err := scanCursorBatch(ctx, executor, schema, validate, plan, name, size, scan)
		if err != nil || count == 0 {
			return err
		}
		more, err := batchread.Yield(ctx, affinity, yield, validate)
		if err != nil || !more || count < size {
			return err
		}
	}
}

func scanCursorBatch(ctx context.Context, executor cursorExecutor, schema string, validate func(context.Context) error, plan query.Plan, name string, size int, scan func(db.Row) error) (count int, err error) {
	native, err := executor.QueryContext(ctx, "FETCH FORWARD "+strconv.Itoa(size)+" FROM "+name)
	if err != nil {
		return 0, classifyDatabaseError(ctx, "fetch batch cursor", schema, plan.Table(), err)
	}
	if owner, ok := executor.(interface{ ForgetRows(*sql.Rows) }); ok {
		defer owner.ForgetRows(native)
	}
	rows, err := adaptScalarRows(native, plan)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err()) }()
	return batchread.Scan(ctx, rows, size, scan, validate)
}
