package sqlite

import (
	"context"
	"errors"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

// The bundled modernc SQLite profile uses SQLITE_MAX_VARIABLE_NUMBER=32766.
const sqliteBulkParameters = 32766

var (
	_ db.BulkInserter = (*Backend)(nil)
	_ db.BulkInserter = (*transactionSession)(nil)
	_ db.BulkInserter = (*rootBatchScope)(nil)
	_ db.BulkInserter = (*relationSession)(nil)
	_ db.BulkInserter = (*writeSession)(nil)
)

func CompileBulkInsert(plan query.BulkInsertPlan) (string, []any, error) {
	table, err := quoteIdentifier(plan.Table())
	if err != nil {
		return "", nil, err
	}
	parts, err := queryplan.PrepareBulkInsert(plan, sqliteBulkParameters, queryplan.WriteValue, quoteIdentifier, sqliteIdentifierKey, sqliteValue)
	if err != nil {
		return "", nil, err
	}
	columns := parts.Columns
	if len(columns) == 0 {
		columns = []string{parts.Key}
	}
	var statement strings.Builder
	statement.WriteString("INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES ")
	for row := 0; row < plan.RowCount(); row++ {
		if row > 0 {
			statement.WriteString(", ")
		}
		statement.WriteByte('(')
		if len(parts.Columns) == 0 {
			statement.WriteString("NULL")
		} else {
			for column := range parts.Columns {
				if column > 0 {
					statement.WriteString(", ")
				}
				statement.WriteByte('?')
			}
		}
		statement.WriteByte(')')
	}
	switch plan.Conflict().Mode() {
	case query.BulkConflictIgnore:
		statement.WriteString(" ON CONFLICT DO NOTHING")
	case query.BulkConflictUpdate:
		statement.WriteString(" ON CONFLICT (" + strings.Join(parts.Target, ", ") + ") DO UPDATE SET ")
		for index, column := range parts.Update {
			if index > 0 {
				statement.WriteString(", ")
			}
			statement.WriteString(column + " = EXCLUDED." + column)
		}
	}
	if plan.ReturnsKeys() {
		statement.WriteString(" RETURNING " + parts.Key)
	}
	return statement.String(), parts.Arguments, nil
}

func executeBulkInsert(ctx context.Context, executor queryplan.BulkInsertExecutor, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	statement, arguments, err := CompileBulkInsert(plan)
	if err != nil {
		return db.BulkInsertResult{}, err
	}
	return queryplan.ExecuteBulkInsert(ctx, executor, plan, statement, arguments, func(err error) error {
		return classifySQLiteWriteError(ctx, "insert", err)
	})
}

func (b *Backend) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return db.BulkInsertLimits{}, err
	}
	return db.BulkInsertLimits{Rows: query.MaximumBulkRows, Parameters: sqliteBulkParameters}, nil
}

func (b *Backend) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return db.BulkInsertResult{}, err
	}
	return executeBulkInsert(ctx, b.database, plan)
}

func (session *transactionSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	if err := session.validate(ctx); err != nil {
		return db.BulkInsertLimits{}, err
	}
	return db.BulkInsertLimits{Rows: query.MaximumBulkRows, Parameters: sqliteBulkParameters}, nil
}

func (session *transactionSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	if _, err := session.BulkInsertLimits(ctx); err != nil {
		return db.BulkInsertResult{}, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (db.BulkInsertResult, error) {
		return executeBulkInsert(ctx, session.transaction, plan)
	})
}

func (scope *rootBatchScope) BulkInsertLimits(ctx context.Context) (value db.BulkInsertLimits, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return value, err
	}
	if lease != nil {
		defer func() { err = errors.Join(err, lease.Close()) }()
	}
	return scope.backend.BulkInsertLimits(ctx)
}

func (scope *rootBatchScope) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (value db.BulkInsertResult, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return value, err
	}
	if lease == nil {
		return scope.backend.BulkInsert(ctx, plan)
	}
	defer func() {
		err = errors.Join(err, lease.Close())
		if err != nil {
			value = db.BulkInsertResult{}
		}
	}()
	return executeBulkInsert(ctx, lease, plan)
}

func (session *relationSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	if err := session.validate(ctx); err != nil {
		return db.BulkInsertLimits{}, err
	}
	return db.BulkInsertLimits{Rows: query.MaximumBulkRows, Parameters: sqliteBulkParameters}, nil
}

func (session *relationSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	if err := session.validate(ctx); err != nil {
		return db.BulkInsertResult{}, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (db.BulkInsertResult, error) {
		statement, arguments, err := CompileBulkInsert(plan)
		if err != nil {
			return db.BulkInsertResult{}, err
		}
		session.state.mutationPossible.Store(true)
		return queryplan.ExecuteBulkInsert(ctx, session.state.connection, plan, statement, arguments, func(err error) error {
			return classifySQLiteWriteError(ctx, "insert", err)
		})
	})
}

func (session *writeSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return db.BulkInsertLimits{}, err
	}
	return session.session.BulkInsertLimits(ctx)
}

func (session *writeSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return db.BulkInsertResult{}, err
	}
	return session.session.BulkInsert(ctx, plan)
}
