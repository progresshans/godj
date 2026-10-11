// Package batchread owns callback and row-lifecycle rules shared by native
// streaming executors. It does not compile plans or own transactions.
package batchread

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func Validate(size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error)) error {
	if size <= 0 || scan == nil || yield == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "batch query requires a positive size and non-nil scan and yield callbacks"}
	}
	return nil
}

// Scan never reads ahead into the next batch. The caller owns rows.Close.
func Scan(ctx context.Context, rows db.Rows, size int, scan func(db.Row) error, validate func(context.Context) error) (int, error) {
	count := 0
	for count < size {
		if err := validate(ctx); err != nil {
			return count, err
		}
		if !rows.Next() {
			break
		}
		if err := validate(ctx); err != nil {
			return count, err
		}
		if err := scan(rows); err != nil {
			return count, errors.Join(err, ctx.Err())
		}
		count++
	}
	return count, errors.Join(rows.Err(), validate(ctx))
}

func Yield(ctx context.Context, backend db.Queryer, yield func(db.Queryer) (bool, error), validate func(context.Context) error) (bool, error) {
	if err := validate(ctx); err != nil {
		return false, err
	}
	more, err := yield(backend)
	return more, errors.Join(err, validate(ctx))
}

// Stream uses a rowset whose connection supports other statements between
// batches (SQLite and synthetic empty results). PostgreSQL fetches and closes
// each native rowset instead. Validation and SQL compilation precede this call.
func Stream(ctx context.Context, backend db.Queryer, rows db.Rows, size int, scan func(db.Row) error, yield func(db.Queryer) (bool, error), validate func(context.Context) error) (err error) {
	closed := false
	closeRows := func() error {
		if closed {
			return nil
		}
		closed = true
		return errors.Join(rows.Err(), rows.Close())
	}
	defer func() { err = errors.Join(err, closeRows(), ctx.Err()) }()
	for {
		count, scanErr := Scan(ctx, rows, size, scan, validate)
		if scanErr != nil {
			return scanErr
		}
		if count < size {
			if err := closeRows(); err != nil {
				return err
			}
		}
		if count == 0 {
			return nil
		}
		more, yieldErr := Yield(ctx, backend, yield, validate)
		if yieldErr != nil || !more || count < size {
			return yieldErr
		}
	}
}
