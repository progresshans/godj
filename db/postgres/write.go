package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var _ db.Mutator = (*Backend)(nil)

type writeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (b *Backend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := b.validateContext(ctx); err != nil {
		return 0, err
	}
	return executeInsert(ctx, b.database, b.schema, plan)
}

func (b *Backend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := b.validateContext(ctx); err != nil {
		return 0, err
	}
	return executeUpdate(ctx, b.database, b.schema, plan)
}

func (b *Backend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := b.validateContext(ctx); err != nil {
		return 0, err
	}
	return executeDelete(ctx, b.database, b.schema, plan)
}

func compileInsert(schema string, plan query.InsertPlan) (string, []any, error) {
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	returningKey, present := plan.ReturningKey()
	if !present {
		return "", nil, unsupportedInsert("PostgreSQL insert requires one generated returning key")
	}
	if returningKey.Kind() != query.FieldInteger || returningKey.Nullable() {
		return "", nil, invalidPlan("PostgreSQL returning key must be a non-null integer field")
	}
	returningColumn, err := quoteIdentifier(returningKey.Column())
	if err != nil {
		return "", nil, err
	}

	assignments := plan.Assignments()
	for _, assignment := range assignments {
		if assignment.Field().Equal(returningKey) || assignment.Field().Column() == returningKey.Column() {
			return "", nil, unsupportedInsert("PostgreSQL explicit generated-key assignment and identity sequence reconciliation are not supported")
		}
	}
	if len(assignments) == 0 {
		return "INSERT INTO " + table + " DEFAULT VALUES RETURNING " + returningColumn, []any{}, nil
	}
	columns, arguments, err := compileAssignments(assignments)
	if err != nil {
		return "", nil, err
	}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = placeholder(index + 1)
	}
	return "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ") RETURNING " + returningColumn, arguments, nil
}

func compileUpdate(schema string, plan query.UpdatePlan) (string, []any, error) {
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	assignments := plan.Assignments()
	if len(assignments) == 0 {
		return "", nil, invalidPlan("update assignments are empty")
	}
	for _, assignment := range assignments {
		if assignment.Field().Equal(plan.KeyField()) || assignment.Field().Column() == plan.KeyField().Column() {
			return "", nil, invalidPlan("update cannot assign its key field")
		}
	}
	columns, arguments, err := compileAssignments(assignments)
	if err != nil {
		return "", nil, err
	}
	setClauses := make([]string, len(columns))
	for index, column := range columns {
		setClauses[index] = column + " = " + placeholder(index+1)
	}
	keyColumn, keyArgument, err := compileKey(plan.KeyField(), plan.KeyValue())
	if err != nil {
		return "", nil, err
	}
	arguments = append(arguments, keyArgument)
	return "UPDATE " + table + " SET " + strings.Join(setClauses, ", ") + " WHERE " +
		keyColumn + " = " + placeholder(len(arguments)), arguments, nil
}

func compileDelete(schema string, plan query.DeletePlan) (string, []any, error) {
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	keyColumn, keyArgument, err := compileKey(plan.KeyField(), plan.KeyValue())
	if err != nil {
		return "", nil, err
	}
	return "DELETE FROM " + table + " WHERE " + keyColumn + " = $1", []any{keyArgument}, nil
}

func executeInsert(ctx context.Context, executor writeExecutor, schema string, plan query.InsertPlan) (int64, error) {
	statement, arguments, err := compileInsert(schema, plan)
	if err != nil {
		return 0, err
	}
	var identifier int64
	if err := executor.QueryRowContext(ctx, statement, arguments...).Scan(&identifier); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, &query.Error{
				Category: query.CategoryBackend,
				Code:     query.CodeUnexpectedRows,
				Detail:   "PostgreSQL insert returned no generated key",
				Cause:    err,
			}
		}
		return 0, classifyDatabaseError(ctx, "insert", schema, plan.Table(), err)
	}
	return identifier, nil
}

func executeUpdate(ctx context.Context, executor writeExecutor, schema string, plan query.UpdatePlan) (int64, error) {
	statement, arguments, err := compileUpdate(schema, plan)
	if err != nil {
		return 0, err
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classifyDatabaseError(ctx, "update", schema, plan.Table(), err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read PostgreSQL update rows affected: %w", err)
	}
	return rowsAffected, nil
}

func executeDelete(ctx context.Context, executor writeExecutor, schema string, plan query.DeletePlan) (int64, error) {
	statement, arguments, err := compileDelete(schema, plan)
	if err != nil {
		return 0, err
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classifyDatabaseError(ctx, "delete", schema, plan.Table(), err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read PostgreSQL delete rows affected: %w", err)
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
		if _, duplicate := seen[field.Column()]; duplicate {
			return nil, nil, invalidPlan(fmt.Sprintf("field column %q is assigned more than once", field.Column()))
		}
		seen[field.Column()] = struct{}{}
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
	if err := validateIdentifier(field.Name()); err != nil {
		return invalidPlan("field name " + err.Error())
	}
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

func unsupportedInsert(detail string) error {
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeUnsupported,
		Detail:   detail,
	}
}
