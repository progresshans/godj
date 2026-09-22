package sqlite

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
	_ db.ConflictInserter = (*relationSession)(nil)
	_ db.ConflictInserter = (*coordinatedSession)(nil)
)

// CompileConflictInsert suppresses only the explicit unique tuple. SQLite's
// broader INSERT OR IGNORE would also suppress unrelated integrity failures.
func CompileConflictInsert(plan query.ConflictInsertPlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	columns, arguments, target, err := queryplan.ConflictInsert(plan, queryplan.WriteValue, quoteIdentifier, sqliteIdentifierKey, sqliteValue)
	if err != nil {
		return "", nil, err
	}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = "?"
	}
	return "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") +
		") ON CONFLICT (" + strings.Join(target, ", ") + ") DO NOTHING", arguments, nil
}

func executeConflictInsert(ctx context.Context, executor writeExecutor, statement string, arguments []any) (bool, error) {
	result, err := executor.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, classifySQLiteWriteError(ctx, "insert", err)
	}
	return queryplan.ConflictInsertResult(result)
}

func (b *Backend) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return false, err
	}
	statement, arguments, err := CompileConflictInsert(plan)
	if err != nil {
		return false, err
	}
	return executeConflictInsert(ctx, b.database, statement, arguments)
}

func (session *transactionSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	if err := session.validate(ctx); err != nil {
		return false, err
	}
	statement, arguments, err := CompileConflictInsert(plan)
	if err != nil {
		return false, err
	}
	return executeConflictInsert(ctx, session.transaction, statement, arguments)
}

func (session *relationSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	if session == nil {
		return false, inactiveRelationSessionError()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.validateLocked(ctx); err != nil {
		return false, err
	}
	statement, arguments, err := CompileConflictInsert(plan)
	if err != nil {
		return false, err
	}
	session.mutationPossible = true
	return executeConflictInsert(ctx, session.connection, statement, arguments)
}

func (session *coordinatedSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (bool, error) {
	if session == nil || session.session == nil {
		return false, inactiveCoordinatedSessionError()
	}
	return session.session.InsertOnConflict(ctx, plan)
}
