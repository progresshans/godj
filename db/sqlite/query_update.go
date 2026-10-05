package sqlite

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	_ db.QueryUpdater = (*Backend)(nil)
	_ db.QueryUpdater = (*transactionSession)(nil)
	_ db.QueryUpdater = (*rootBatchScope)(nil)
	_ db.QueryUpdater = (*relationSession)(nil)
	_ db.QueryUpdater = (*writeSession)(nil)
)

func CompileQueryUpdate(plan query.QueryUpdatePlan) (string, []any, error) {
	return queryplan.CompileQueryUpdate(plan, queryplan.UpdateDialect{
		ParameterLimit: sqliteBulkParameters,
		Source: func(selection query.Plan) (queryplan.UpdateSource, error) {
			statement, arguments, err := Compile(selection)
			return queryplan.UpdateSource{Selection: statement, Arguments: arguments}, err
		},
		QuoteTable: quoteIdentifier, QuoteIdentifier: quoteIdentifier, ColumnKey: sqliteIdentifierKey,
		Value: sqliteValue, Placeholder: func(index int) string { return "?" + strconv.Itoa(index) }, Expression: wrapSQLiteScalar,
	})
}

func classifySQLiteQueryUpdate(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var native *modernsqlite.Error
	if errors.As(err, &native) && native.Code() == sqlite3.SQLITE_ERROR && strings.Contains(native.Error(), invalidIntegerExpression) {
		return &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Detail: "SQLite integer expression is outside the exact int64 domain", Cause: err}
	}
	return classifySQLiteWriteError(ctx, "update", err)
}

func executeQueryUpdate(ctx context.Context, executor queryplan.BulkUpdateExecutor, plan query.QueryUpdatePlan) (int64, error) {
	statement, arguments, err := CompileQueryUpdate(plan)
	if err != nil {
		return 0, err
	}
	return queryplan.ExecuteQueryUpdate(ctx, executor, plan, statement, arguments, func(err error) error { return classifySQLiteQueryUpdate(ctx, err) })
}

func (backend *Backend) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := backend.validateWriteContext(ctx); err != nil {
		return err
	}
	_, _, err := CompileQueryUpdate(plan)
	return errors.Join(err, ctx.Err())
}
func (backend *Backend) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := backend.validateWriteContext(ctx); err != nil {
		return 0, err
	}
	return executeQueryUpdate(ctx, backend.database, plan)
}
func (session *transactionSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := session.validate(ctx); err != nil {
		return err
	}
	_, _, err := CompileQueryUpdate(plan)
	return errors.Join(err, ctx.Err())
}
func (session *transactionSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) { return executeQueryUpdate(ctx, session.transaction, plan) })
}
func (scope *rootBatchScope) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return err
	}
	if lease != nil {
		defer func() { err = errors.Join(err, lease.Close()) }()
	}
	return scope.backend.CheckQueryUpdate(ctx, plan)
}
func (scope *rootBatchScope) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (count int64, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if lease == nil {
		return scope.backend.QueryUpdate(ctx, plan)
	}
	defer func() {
		if closeErr := lease.Close(); closeErr != nil {
			err, count = errors.Join(err, closeErr), 0
		}
	}()
	return executeQueryUpdate(ctx, lease, plan)
}
func (session *relationSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := session.validate(ctx); err != nil {
		return err
	}
	_, _, err := CompileQueryUpdate(plan)
	return errors.Join(err, ctx.Err())
}
func (session *relationSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		statement, arguments, err := CompileQueryUpdate(plan)
		if err != nil {
			return 0, err
		}
		if !plan.NoOp() {
			session.state.mutationPossible.Store(true)
		}
		return queryplan.ExecuteQueryUpdate(ctx, session.state.connection, plan, statement, arguments, func(err error) error { return classifySQLiteQueryUpdate(ctx, err) })
	})
}
func (session *writeSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := session.ValidateSession(ctx); err != nil {
		return err
	}
	return session.session.CheckQueryUpdate(ctx, plan)
}
func (session *writeSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.session.QueryUpdate(ctx, plan)
}
