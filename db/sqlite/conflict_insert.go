package sqlite

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
	_ db.ConflictInserter = (*relationSession)(nil)
	_ db.ConflictInserter = (*writeSession)(nil)
)

// CompileConflictInsert suppresses only the explicit unique tuple. SQLite's
// broader INSERT OR IGNORE would also suppress unrelated integrity failures.
func CompileConflictInsert(plan query.ConflictInsertPlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	parts, err := queryplan.PrepareConflictInsert(plan, queryplan.WriteValue, quoteIdentifier, sqliteIdentifierKey, sqliteValue)
	if err != nil {
		return "", nil, err
	}
	placeholders := make([]string, len(parts.Columns))
	for index := range placeholders {
		placeholders[index] = "?"
	}
	return "INSERT INTO " + table + " (" + strings.Join(parts.Columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (" + strings.Join(parts.Target, ", ") + ") DO NOTHING RETURNING " + parts.Key, parts.Arguments, nil
}

func executeConflictInsert(ctx context.Context, executor queryplan.InsertReturningExecutor, statement string, arguments []any) (db.ConflictInsertResult, error) {
	return queryplan.ExecuteConflictInsert(ctx, executor, statement, arguments, func(err error) error { return classifySQLiteWriteError(ctx, "insert", err) })
}

func (b *Backend) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return db.ConflictInsertResult{}, err
	}
	statement, arguments, err := CompileConflictInsert(plan)
	if err != nil {
		return db.ConflictInsertResult{}, err
	}
	return executeConflictInsert(ctx, b.database, statement, arguments)
}

func (session *transactionSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if err := session.validate(ctx); err != nil {
		return db.ConflictInsertResult{}, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (db.ConflictInsertResult, error) {
		statement, arguments, err := CompileConflictInsert(plan)
		if err != nil {
			return db.ConflictInsertResult{}, err
		}
		return executeConflictInsert(ctx, session.transaction, statement, arguments)
	})
}

func (session *relationSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if err := session.validate(ctx); err != nil {
		return db.ConflictInsertResult{}, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (db.ConflictInsertResult, error) {
		statement, arguments, err := CompileConflictInsert(plan)
		if err != nil {
			return db.ConflictInsertResult{}, err
		}
		session.state.mutationPossible.Store(true)
		return executeConflictInsert(ctx, session.state.connection, statement, arguments)
	})
}

func (session *writeSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if session == nil || session.session == nil {
		return db.ConflictInsertResult{}, inactiveWriteSessionError()
	}
	return session.session.InsertOnConflict(ctx, plan)
}
