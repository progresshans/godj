package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var _ db.Mutator = (*Backend)(nil)

type writeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (b *Backend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return 0, err
	}
	return executeInsert(ctx, b.database, plan)
}

func (b *Backend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return 0, err
	}
	return executeUpdate(ctx, b.database, plan)
}

func (b *Backend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return 0, err
	}
	return executeDelete(ctx, b.database, plan)
}

// CompileInsert turns an immutable insert plan into parameterized SQLite SQL.
func CompileInsert(plan query.InsertPlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	assignments := plan.Assignments()
	if len(assignments) == 0 {
		return "INSERT INTO " + table + " DEFAULT VALUES", []any{}, nil
	}
	columns, arguments, err := queryplan.Assignments(assignments, queryplan.WriteValue, quoteIdentifier, sqliteIdentifierKey)
	if err != nil {
		return "", nil, err
	}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = "?"
	}
	return "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")", arguments, nil
}

// CompileUpdate turns an immutable explicit-field update into parameterized
// SQLite SQL. The primary-key predicate is always bound last.
func CompileUpdate(plan query.UpdatePlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	assignments := plan.Assignments()
	if len(assignments) == 0 {
		return "", nil, invalidPlan("update assignments are empty")
	}
	for _, assignment := range assignments {
		if assignment.Field().Equal(plan.KeyField()) || sqliteIdentifierKey(assignment.Field().Column()) == sqliteIdentifierKey(plan.KeyField().Column()) {
			return "", nil, invalidPlan("update cannot assign its key field")
		}
	}
	columns, arguments, err := queryplan.Assignments(assignments, queryplan.WriteValue, quoteIdentifier, sqliteIdentifierKey)
	if err != nil {
		return "", nil, err
	}
	setClauses := make([]string, len(columns))
	for index, column := range columns {
		setClauses[index] = column + " = ?"
	}
	keyColumn, keyArgument, err := queryplan.Key(plan.KeyField(), plan.KeyValue(), queryplan.WriteValue, quoteIdentifier)
	if err != nil {
		return "", nil, err
	}
	arguments = append(arguments, keyArgument)
	return "UPDATE " + table + " SET " + strings.Join(setClauses, ", ") + " WHERE " + keyColumn + " = ?", arguments, nil
}

// CompileDelete turns an immutable key delete into parameterized SQLite SQL.
func CompileDelete(plan query.DeletePlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	keyColumn, keyArgument, err := queryplan.Key(plan.KeyField(), plan.KeyValue(), queryplan.WriteValue, quoteIdentifier)
	if err != nil {
		return "", nil, err
	}
	return "DELETE FROM " + table + " WHERE " + keyColumn + " = ?", []any{keyArgument}, nil
}

func executeInsert(ctx context.Context, executor writeExecutor, plan query.InsertPlan) (int64, error) {
	statement, arguments, err := CompileInsert(plan)
	if err != nil {
		return 0, err
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classifyInsertError(err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite insert rows affected: %w", err)
	}
	if rowsAffected != 1 {
		return 0, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeUnexpectedRows,
			Detail:   fmt.Sprintf("insert affected %d rows, want 1", rowsAffected),
		}
	}
	lastInsertID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read SQLite last insert id: %w", err)
	}
	return lastInsertID, nil
}

func classifyInsertError(err error) error {
	var sqliteError *modernsqlite.Error
	if errors.As(err, &sqliteError) && sqliteError.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY {
		return &query.Error{
			Category: query.CategoryIntegrity,
			Code:     query.CodeUniquePrimaryKey,
			Detail:   "SQLite primary-key constraint rejected the insert",
			Cause:    err,
		}
	}
	return fmt.Errorf("execute SQLite insert: %w", err)
}

func executeUpdate(ctx context.Context, executor writeExecutor, plan query.UpdatePlan) (int64, error) {
	statement, arguments, err := CompileUpdate(plan)
	if err != nil {
		return 0, err
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, fmt.Errorf("execute SQLite update: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite update rows affected: %w", err)
	}
	return rowsAffected, nil
}

func executeDelete(ctx context.Context, executor writeExecutor, plan query.DeletePlan) (int64, error) {
	statement, arguments, err := CompileDelete(plan)
	if err != nil {
		return 0, err
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, fmt.Errorf("execute SQLite delete: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite delete rows affected: %w", err)
	}
	return rowsAffected, nil
}

// SQLite folds only ASCII case in identifiers, including quoted identifiers.
// Preserve all other bytes so validation mirrors SQLite instead of applying
// Unicode case folding that the database itself does not perform.
func sqliteIdentifierKey(identifier string) string {
	for index := 0; index < len(identifier); index++ {
		value := identifier[index]
		if value >= 'A' && value <= 'Z' {
			key := []byte(identifier)
			for offset := index; offset < len(key); offset++ {
				if key[offset] >= 'A' && key[offset] <= 'Z' {
					key[offset] += 'a' - 'A'
				}
			}
			return string(key)
		}
	}
	return identifier
}

func (b *Backend) validateWriteContext(ctx context.Context) error {
	return b.validateBackendContext(ctx)
}
