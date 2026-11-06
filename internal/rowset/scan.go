// Package rowset owns bounded scan cleanup for framework persistence owners.
// Query planning, row shape, limits and error classification stay with callers.
package rowset

import (
	"context"
	"errors"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// Scan closes even rows returned alongside a query failure or a scanner panic.
// A short successful scan still checks iteration, cancellation and close errors.
func Scan(ctx context.Context, reader db.Queryer, plan query.Plan, failure func(error) error, scan func(db.Rows) (bool, error)) (resultErr error) {
	if nilValue(ctx) || nilValue(reader) || failure == nil || scan == nil {
		return errors.New("rowset: context, reader and callbacks are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := reader.Query(ctx, plan)
	if !nilValue(rows) {
		defer func() {
			if err := rows.Err(); err != nil {
				resultErr = errors.Join(resultErr, failure(err))
			}
			if err := rows.Close(); err != nil {
				resultErr = errors.Join(resultErr, failure(err))
			}
		}()
	}
	if err != nil {
		return failure(err)
	}
	if nilValue(rows) {
		return failure(errors.New("backend returned nil rows"))
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		more, err := scan(rows)
		if err != nil {
			return err
		}
		if !more {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
func nilValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
