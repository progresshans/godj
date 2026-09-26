package sqlite

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/batchread"
	"github.com/progresshans/godj/query"
)

var _ db.BatchQueryer = (*transactionSession)(nil)
var _ db.BatchQueryer = (*relationSession)(nil)
var _ db.BatchQueryer = (*writeSession)(nil)

func (session *transactionSession) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	return querySessionBatches(ctx, session, plan, size, scan, yield)
}

func (session *relationSession) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	return querySessionBatches(ctx, session, plan, size, scan, yield)
}

func (session *writeSession) QueryBatches(ctx context.Context, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	return querySessionBatches(ctx, session, plan, size, scan, yield)
}

func querySessionBatches(ctx context.Context, session interface {
	db.Queryer
	db.SessionValidator
}, plan query.Plan, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	if err := session.ValidateSession(ctx); err != nil {
		return err
	}
	if err := batchread.Validate(size, scan, yield); err != nil {
		return err
	}
	rows, err := session.Query(ctx, plan)
	if err != nil {
		return err
	}
	return batchread.Stream(ctx, session, rows, size, scan, yield, session.ValidateSession)
}
