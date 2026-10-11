package queryplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// BulkInsertParts validates and encodes the entire matrix before execution.
// Dialects own identifier equivalence, limits and the statement syntax.
type BulkInsertParts struct {
	Columns, Target, Update []string
	Key                     string
	Arguments               []any
}

func PrepareBulkInsert(plan query.BulkInsertPlan, parameterLimit int, validateValue func(query.FieldRef, query.Value) error, quoteIdentifier func(string) (string, error), columnKey func(string) string, encodeValue func(query.Value) (any, error)) (BulkInsertParts, error) {
	var zero BulkInsertParts
	if err := plan.Validate(); err != nil {
		return zero, err
	}
	if parameterLimit <= 0 || plan.ValueCount() > parameterLimit {
		return zero, invalidPlan("bulk insert exceeds the backend parameter limit")
	}
	if columnKey == nil {
		columnKey = func(value string) string { return value }
	}
	key := plan.Key()
	if err := validateValue(key, query.Integer(0)); err != nil {
		return zero, err
	}
	keyColumn, err := quoteIdentifier(key.Column())
	if err != nil {
		return zero, err
	}
	fields := plan.Fields()
	result := BulkInsertParts{Key: keyColumn, Columns: make([]string, len(fields)), Arguments: make([]any, 0, plan.ValueCount())}
	columns := make(map[string]query.FieldRef, len(fields))
	for index, field := range fields {
		column := columnKey(field.Column())
		if _, repeated := columns[column]; repeated || column == columnKey(key.Column()) && field != key {
			return zero, invalidPlan("bulk insert repeats a physical column or aliases its primary key")
		}
		quoted, err := quoteIdentifier(field.Column())
		if err != nil {
			return zero, err
		}
		columns[column], result.Columns[index] = field, quoted
	}
	for _, row := range plan.Rows() {
		for index, value := range row {
			if err := validateValue(fields[index], value); err != nil {
				return zero, err
			}
			argument, err := encodeValue(value)
			if err != nil {
				return zero, err
			}
			result.Arguments = append(result.Arguments, argument)
		}
	}
	preparePolicy := func(fields []query.FieldRef, update bool) ([]string, error) {
		quoted := make([]string, len(fields))
		seen := make(map[string]bool, len(fields))
		for index, field := range fields {
			column := columnKey(field.Column())
			if seen[column] || update && column == columnKey(key.Column()) || columns[column] != field && !(field == key && !update) {
				return nil, invalidPlan("bulk conflict contains a repeated, foreign or primary update column")
			}
			var err error
			quoted[index], err = quoteIdentifier(field.Column())
			if err != nil {
				return nil, err
			}
			seen[column] = true
		}
		return quoted, nil
	}
	result.Target, err = preparePolicy(plan.Conflict().Target(), false)
	if err != nil {
		return zero, err
	}
	result.Update, err = preparePolicy(plan.Conflict().Update(), true)
	if err != nil {
		return zero, err
	}
	return result, nil
}

type BulkInsertExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ExecuteBulkInsert drains and closes native RETURNING rows before publishing
// keys. An error discards the entire result; the caller still owns rollback.
func ExecuteBulkInsert(ctx context.Context, executor BulkInsertExecutor, plan query.BulkInsertPlan, statement string, arguments []any, classify func(error) error) (db.BulkInsertResult, error) {
	var zero db.BulkInsertResult
	if !plan.ReturnsKeys() {
		result, err := executor.ExecContext(ctx, statement, arguments...)
		if err != nil {
			return zero, classify(err)
		}
		if result == nil {
			return zero, invalidPlan("bulk insert returned no result metadata")
		}
		count, err := result.RowsAffected()
		if err != nil {
			return zero, fmt.Errorf("read bulk insert affected rows: %w", err)
		}
		if count < 0 || count > int64(plan.RowCount()) {
			return zero, unexpectedBulkRows(plan.RowCount(), count)
		}
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return db.BulkInsertResult{RowsAffected: count}, nil
	}
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Close())
		}
		return zero, classify(err)
	}
	if rows == nil {
		return zero, invalidPlan("bulk insert returned nil rows without an error")
	}
	defer rows.Close()
	keys := make([]int64, 0, plan.RowCount())
	for rows.Next() {
		if len(keys) == plan.RowCount() {
			err = unexpectedBulkRows(plan.RowCount(), int64(len(keys)+1))
			break
		}
		var key int64
		if err = rows.Scan(&key); err != nil {
			break
		}
		keys = append(keys, key)
	}
	if err = errors.Join(err, rows.Err(), rows.Close(), ctx.Err()); err != nil {
		return zero, classify(err)
	}
	if len(keys) != plan.RowCount() {
		return zero, unexpectedBulkRows(plan.RowCount(), int64(len(keys)))
	}
	return db.BulkInsertResult{Keys: keys, RowsAffected: int64(len(keys))}, nil
}

func unexpectedBulkRows(want int, got int64) error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows,
		Detail: fmt.Sprintf("bulk insert returned %d rows for %d input rows", got, want)}
}
