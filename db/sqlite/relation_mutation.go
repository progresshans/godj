package sqlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
)

// compileRelationSetNull turns the deliberately narrow relation mutation into
// one parameterized bulk UPDATE. All plan validation happens before an
// executor can observe the statement.
func compileRelationSetNull(plan query.RelationSetNullPlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	foreignKey := plan.ForeignKey()
	if foreignKey.Name() == "" || strings.ContainsRune(foreignKey.Name(), '\x00') {
		return "", nil, invalidPlan("relation SET_NULL foreign key name is empty or contains NUL")
	}
	if foreignKey.Kind() != query.FieldInteger {
		return "", nil, invalidPlan("relation SET_NULL foreign key must be an integer field")
	}
	if !foreignKey.Nullable() {
		return "", nil, invalidPlan("relation SET_NULL foreign key must be nullable")
	}
	column, err := quoteIdentifier(foreignKey.Column())
	if err != nil {
		return "", nil, err
	}
	targetKey := plan.TargetKey()
	if targetKey.IsNull() {
		return "", nil, invalidPlan("relation SET_NULL target key cannot be NULL")
	}
	if targetKey.Kind() != query.ValueInteger {
		return "", nil, invalidPlan("relation SET_NULL target key must be an integer")
	}
	argument, err := targetKey.DatabaseValue()
	if err != nil {
		return "", nil, err
	}
	return "UPDATE " + table + " SET " + column + " = NULL WHERE " + column + " = ?", []any{argument}, nil
}

func executeCompiledRelationSetNull(
	ctx context.Context,
	executor writeExecutor,
	statement string,
	arguments []any,
) (int64, error) {
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, fmt.Errorf("execute SQLite relation SET_NULL: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read SQLite relation SET_NULL rows affected: %w", err)
	}
	if rowsAffected < 0 {
		return 0, &query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeUnexpectedRows,
			Detail:   fmt.Sprintf("relation SET_NULL affected %d rows, want a non-negative count", rowsAffected),
		}
	}
	return rowsAffected, nil
}
