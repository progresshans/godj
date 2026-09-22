package postgres

import (
	"context"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

var (
	_ db.ConflictInserter = (*Backend)(nil)
	_ db.ConflictInserter = (*transactionSession)(nil)
)

func compileConflictInsert(schema string, plan query.ConflictInsertPlan) (string, []any, error) {
	table, err := quoteTable(schema, plan.Table())
	if err != nil {
		return "", nil, err
	}
	columns, arguments, target, err := queryplan.ConflictInsert(plan, validateWriteValue, quoteIdentifier, nil, postgresValue)
	if err != nil {
		return "", nil, err
	}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = placeholder(index + 1)
	}
	return "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (" + strings.Join(target, ", ") + ") DO NOTHING", arguments, nil
}

func executeConflictInsert(ctx context.Context, executor writeExecutor, schema string, plan query.ConflictInsertPlan) (bool, error) {
	statement, arguments, err := compileConflictInsert(schema, plan)
	if err != nil {
		return false, err
	}
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, classifyDatabaseError(ctx, "insert", schema, plan.Table(), err)
	}
	return queryplan.ConflictInsertResult(result)
}

func (b *Backend) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	if err := b.validateContext(ctx); err != nil {
		return false, err
	}
	return executeConflictInsert(ctx, b.database, b.schema, plan)
}

func (session *transactionSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	if err := session.validate(ctx); err != nil {
		return false, err
	}
	return executeConflictInsert(ctx, session.transaction, session.backend.schema, plan)
}
