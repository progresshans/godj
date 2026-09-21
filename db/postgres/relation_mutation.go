package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
)

func compileRelationSetNull(schema string, plan query.RelationSetNullPlan) (string, []any, error) {
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	field := plan.ForeignKey()
	if field.Name() == "" || strings.ContainsRune(field.Name(), '\x00') || field.Kind() != query.FieldInteger || !field.Nullable() {
		return "", nil, invalidPlan("relation SET_NULL requires a named nullable integer foreign key")
	}
	column, err := quoteIdentifier(field.Column())
	if err != nil {
		return "", nil, err
	}
	key, ok := plan.TargetKey().Integer()
	if !ok || plan.TargetKey().IsNull() {
		return "", nil, invalidPlan("relation SET_NULL requires a non-null integer target key")
	}
	return "UPDATE " + table + " SET " + column + " = NULL WHERE " + column + " = $1", []any{key}, nil
}

func (session *transactionSession) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	statement, arguments, err := compileRelationSetNull(session.backend.schema, plan)
	if err != nil {
		return 0, err
	}
	result, err := session.transaction.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, classifyDatabaseError(ctx, "relation SET_NULL", session.backend.schema, plan.Table(), err)
	}
	if result == nil {
		return 0, backendInvalid("PostgreSQL relation SET_NULL returned a nil result")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read PostgreSQL relation SET_NULL rows affected: %w", err)
	}
	if count < 0 {
		return 0, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows,
			Detail: fmt.Sprintf("relation SET_NULL affected %d rows, want a non-negative count", count)}
	}
	return count, nil
}
