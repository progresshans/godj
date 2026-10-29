package postgres

import (
	"context"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
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
	parts, err := queryplan.PrepareConflictInsert(plan, validateWriteValue, quoteIdentifier, nil, postgresValue)
	if err != nil {
		return "", nil, err
	}
	placeholders := make([]string, len(parts.Columns))
	for index := range placeholders {
		placeholders[index] = placeholder(index + 1)
	}
	return "INSERT INTO " + table + " (" + strings.Join(parts.Columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (" + strings.Join(parts.Target, ", ") + ") DO NOTHING RETURNING " + parts.Key, parts.Arguments, nil
}

func executeConflictInsert(ctx context.Context, executor queryplan.InsertReturningExecutor, schema string, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	statement, arguments, err := compileConflictInsert(schema, plan)
	if err != nil {
		return db.ConflictInsertResult{}, err
	}
	return queryplan.ExecuteConflictInsert(ctx, executor, statement, arguments, func(err error) error { return classifyDatabaseError(ctx, "insert", schema, plan.Table(), err) })
}

func (b *Backend) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if err := b.validateContext(ctx); err != nil {
		return db.ConflictInsertResult{}, err
	}
	return executeConflictInsert(ctx, b.database, b.schema, plan)
}

func (session *transactionSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if err := session.validate(ctx); err != nil {
		return db.ConflictInsertResult{}, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (db.ConflictInsertResult, error) {
		return executeConflictInsert(ctx, session.transaction, session.backend.schema, plan)
	})
}
