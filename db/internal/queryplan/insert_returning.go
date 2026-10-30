package queryplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type InsertReturningExecutor interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ExecuteInsertReturning drains and closes the actual statement's RETURNING
// rows. It never infers a key by reading an existing row or LastInsertId after
// a no-op. No key survives scan, cardinality, transport, close or context errors.
func ExecuteInsertReturning(ctx context.Context, executor InsertReturningExecutor, statement string, arguments []any, minimum, maximum int, classify func(error) error) ([]int64, error) {
	if ctx == nil || minimum < 0 || maximum < minimum || maximum < 1 {
		return nil, invalidPlan("invalid returning insert context or row bounds")
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
		return nil, invalidPlan("insert returned nil rows without an error")
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
		Detail: fmt.Sprintf("insert returned %d rows, expected %d through %d", actual, minimum, maximum)}
}

// ExecuteConflictInsert keeps no-op distinct from an inserted zero key.
func ExecuteConflictInsert(ctx context.Context, executor InsertReturningExecutor, statement string, arguments []any, classify func(error) error) (db.ConflictInsertResult, error) {
	keys, err := ExecuteInsertReturning(ctx, executor, statement, arguments, 0, 1, classify)
	if err != nil {
		return db.ConflictInsertResult{}, err
	}
	if len(keys) == 0 {
		return db.ConflictInsertResult{}, nil
	}
	return db.ConflictInsertResult{Inserted: true, Key: keys[0]}, nil
}
