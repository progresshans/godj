package systemstate

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// scanRows owns the query result from acquisition through close. A scanner
// consumes the current row and returns false when its bounded inspection is
// complete. Row shape, capacity and domain validation remain with the caller.
// Successful partial results are never published on iteration/close failure;
// a scanner panic still closes rows and propagates unchanged.
func scanRows(
	ctx context.Context,
	queryer db.Queryer,
	plan query.Plan,
	failure func(error) error,
	scan func(db.Rows) (bool, error),
) (resultErr error) {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if isNilInterface(queryer) {
		return &Error{Code: CodeInvalidConfig, Field: "backend", Detail: "query backend is nil"}
	}
	rows, err := queryer.Query(ctx, plan)
	if !isNilInterface(rows) {
		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				resultErr = errors.Join(resultErr, failure(closeErr))
			}
		}()
	}
	if err != nil {
		return failure(err)
	}
	if isNilInterface(rows) {
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
	if err := rows.Err(); err != nil {
		return failure(err)
	}
	return nil
}

func schemaRowsFailure(table string) func(error) error {
	return func(err error) error {
		return &Error{Code: CodeSchemaUnavailable, Field: table, Detail: "required system-state table is unavailable", Cause: err}
	}
}

func persistenceRowsFailure(operation string) func(error) error {
	return func(err error) error { return persistenceFailure(operation, err) }
}
