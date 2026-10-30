package queryplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type IntegerReturningExecutor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ExecuteIntegerReturning drains and closes one integer column from a native
// statement. Callers own whether it returns inserted or reserved keys. No key
// survives scan, cardinality, transport, close or context errors.
func ExecuteIntegerReturning(ctx context.Context, executor IntegerReturningExecutor, statement string, arguments []any, minimum, maximum int, classify func(error) error) ([]int64, error) {
	if ctx == nil || minimum < 0 || maximum < minimum || maximum < 1 {
		return nil, invalidPlan("invalid integer result context or row bounds")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
		return nil, classify(err)
	}
	if rows == nil {
		return nil, invalidPlan("integer statement returned nil rows without an error")
	}
	defer rows.Close()
	keys := make([]int64, 0, maximum)
	for rows.Next() {
		if len(keys) == maximum {
			err = returningRowCount(minimum, maximum, len(keys)+1)
			break
		}
		var key int64
		if err = rows.Scan(&key); err != nil {
			break
		}
		keys = append(keys, key)
	}
	if err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err()); err != nil {
		return nil, classify(err)
	}
	if len(keys) < minimum {
		return nil, returningRowCount(minimum, maximum, len(keys))
	}
	return keys, nil
}

func returningRowCount(minimum, maximum, actual int) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows,
		Detail: fmt.Sprintf("integer statement returned %d rows, expected %d through %d", actual, minimum, maximum)}
}

// ExecuteConflictInsert keeps no-op distinct from an inserted zero key.
func ExecuteConflictInsert(ctx context.Context, executor IntegerReturningExecutor, statement string, arguments []any, classify func(error) error) (db.ConflictInsertResult, error) {
	keys, err := ExecuteIntegerReturning(ctx, executor, statement, arguments, 0, 1, classify)
	if err != nil {
		return db.ConflictInsertResult{}, err
	}
	if len(keys) == 0 {
		return db.ConflictInsertResult{}, nil
	}
	return db.ConflictInsertResult{Inserted: true, Key: keys[0]}, nil
}
