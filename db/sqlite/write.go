package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db"
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
	columns, arguments, err := compileAssignments(assignments)
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
	columns, arguments, err := compileAssignments(assignments)
	if err != nil {
		return "", nil, err
	}
	setClauses := make([]string, len(columns))
	for index, column := range columns {
		setClauses[index] = column + " = ?"
	}
	keyColumn, keyArgument, err := compileKey(plan.KeyField(), plan.KeyValue())
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
	keyColumn, keyArgument, err := compileKey(plan.KeyField(), plan.KeyValue())
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

func compileAssignments(assignments []query.Assignment) ([]string, []any, error) {
	columns := make([]string, len(assignments))
	arguments := make([]any, len(assignments))
	seen := make(map[string]struct{}, len(assignments))
	for index, assignment := range assignments {
		field := assignment.Field()
		column, err := quoteIdentifier(field.Column())
		if err != nil {
			return nil, nil, err
		}
		identifierKey := sqliteIdentifierKey(field.Column())
		if _, duplicate := seen[identifierKey]; duplicate {
			return nil, nil, invalidPlan(fmt.Sprintf("field column %q is assigned more than once", field.Column()))
		}
		seen[identifierKey] = struct{}{}
		if err := validateWriteValue(field, assignment.Value()); err != nil {
			return nil, nil, err
		}
		argument, err := assignment.Value().DatabaseValue()
		if err != nil {
			return nil, nil, err
		}
		columns[index] = column
		arguments[index] = argument
	}
	return columns, arguments, nil
}

// SQLite folds only ASCII case in identifiers, including quoted identifiers.
// Preserve all other bytes so validation mirrors SQLite instead of applying
// Unicode case folding that the database itself does not perform.
func sqliteIdentifierKey(identifier string) string {
	key := []byte(identifier)
	for index, value := range key {
		if value >= 'A' && value <= 'Z' {
			key[index] = value + ('a' - 'A')
		}
	}
	return string(key)
}

func compileKey(field query.FieldRef, value query.Value) (string, any, error) {
	if field.Nullable() || value.IsNull() {
		return "", nil, invalidPlan("mutation key cannot be nullable or NULL")
	}
	if err := validateWriteValue(field, value); err != nil {
		return "", nil, err
	}
	column, err := quoteIdentifier(field.Column())
	if err != nil {
		return "", nil, err
	}
	argument, err := value.DatabaseValue()
	if err != nil {
		return "", nil, err
	}
	return column, argument, nil
}

func validateWriteValue(field query.FieldRef, value query.Value) error {
	if value.IsNull() {
		if !field.Nullable() {
			return invalidPlan(fmt.Sprintf("non-null field %q cannot be assigned NULL", field.Name()))
		}
		return nil
	}
	if !valueMatchesField(value.Kind(), field.Kind()) {
		return invalidPlan(fmt.Sprintf("value kind %q does not match field %q", value.Kind(), field.Name()))
	}
	return nil
}

func (b *Backend) validateWriteContext(ctx context.Context) error {
	if b == nil || b.database == nil || b.closed.Load() {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "SQLite backend is nil or closed"}
	}
	if ctx == nil {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
