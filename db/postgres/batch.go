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
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return err
	}
	name := "godj_batch_" + hex.EncodeToString(identity[:])
	if _, err := session.transaction.ExecContext(ctx, "DECLARE "+name+" NO SCROLL CURSOR WITHOUT HOLD FOR "+statement, arguments...); err != nil {
		return classifyDatabaseError(ctx, "declare batch cursor", session.backend.schema, plan.Table(), err)
	}
	defer func() {
		// Cursor cleanup is independent of the per-call cancellation. The
		// surrounding transaction still owns rollback/commit and its lifetime.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, closeErr := session.transaction.ExecContext(cleanup, "CLOSE "+name)
		if errors.Is(closeErr, sql.ErrTxDone) {
			closeErr = nil // Transaction termination already closes its cursors.
		}
		if closeErr != nil {
			closeErr = classifyDatabaseError(cleanup, "close batch cursor", session.backend.schema, plan.Table(), closeErr)
		}
		err = errors.Join(err, closeErr, ctx.Err())
	}()
	for {
		if err := session.validate(ctx); err != nil {
			return err
		}
		count, err := session.scanBatch(ctx, plan, name, size, scan)
		if err != nil || count == 0 {
			return err
		}
		more, err := batchread.Yield(ctx, session, yield, session.validate)
		if err != nil || !more || count < size {
			return err
		}
	}
}

func (session *transactionSession) scanBatch(ctx context.Context, plan query.Plan, name string, size int, scan func(db.Row) error) (count int, err error) {
	native, err := session.transaction.QueryContext(ctx, "FETCH FORWARD "+strconv.Itoa(size)+" FROM "+name)
	if err != nil {
		return 0, classifyDatabaseError(ctx, "fetch batch cursor", session.backend.schema, plan.Table(), err)
	}
	rows, err := adaptScalarRows(native, plan)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err()) }()
	return batchread.Scan(ctx, rows, size, scan, session.validate)
}
