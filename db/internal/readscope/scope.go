// Package readscope narrows a transaction to reads and owns callback cleanup.
// Backends choose and begin their native snapshot isolation before entering.
package readscope

import (
	"context"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type Session interface {
	db.Queryer
	db.SessionValidator
}

// reader deliberately does not embed Session: a callback cannot recover the
// underlying write, transaction or backend capabilities by type assertion.
type reader struct{ source Session }

func (value *reader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return value.source.Query(ctx, plan)
}

func (value *reader) ValidateSession(ctx context.Context) error {
	return value.source.ValidateSession(ctx)
}

// Run owns an already-open read transaction. Even panic and Goexit expire its
// borrowed handle and end it. Ordinary cleanup failures remain observable.
func Run(ctx context.Context, source Session, callback func(db.Queryer) error, expire func(), rollback func() error) error {
	ended := false
	defer func() {
		if !ended {
			expire()
			_ = rollback()
		}
	}()
	callbackErr := callback(&reader{source: source})
	expire()
	ended = true
	cleanupErr := rollback()
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("end database read snapshot: %w", cleanupErr)
	}
	return errors.Join(callbackErr, ctx.Err(), cleanupErr)
}
