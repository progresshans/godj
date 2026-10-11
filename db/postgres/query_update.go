package postgres

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var (
	_ db.QueryUpdater = (*Backend)(nil)
	_ db.QueryUpdater = (*transactionSession)(nil)
	_ db.QueryUpdater = (*rootBatchScope)(nil)
)

func compileQueryUpdate(schema string, plan query.QueryUpdatePlan) (string, []any, error) {
	return queryplan.CompileQueryUpdate(plan, queryplan.UpdateDialect{
		ParameterLimit: postgresBulkParameters,
		Source: func(selection query.Plan) (queryplan.UpdateSource, error) {
			return compileUpdateSource(schema, selection)
		},
		QuoteTable: func(table string) (string, error) { return quoteTable(schema, table) }, QuoteIdentifier: quoteIdentifier,
		Value: postgresValue, Placeholder: placeholder,
		Predicate: compileScalarPredicate,
		Literal:   readScalarLiteral,
	})
}

func executeQueryUpdate(ctx context.Context, executor queryplan.BulkUpdateExecutor, schema string, plan query.QueryUpdatePlan) (int64, error) {
	statement, arguments, err := compileQueryUpdate(schema, plan)
	if err != nil {
		return 0, err
	}
	return queryplan.ExecuteQueryUpdate(ctx, executor, plan, statement, arguments, func(err error) error {
		return classifyDatabaseError(ctx, "update", schema, plan.Selection().Table(), err)
	})
}
func (backend *Backend) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := backend.validateContext(ctx); err != nil {
		return err
	}
	_, _, err := compileQueryUpdate(backend.schema, plan)
	return errors.Join(err, ctx.Err())
}
func (backend *Backend) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := backend.validateContext(ctx); err != nil {
		return 0, err
	}
	return executeQueryUpdate(ctx, backend.database, backend.schema, plan)
}
func (session *transactionSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	if err := session.validate(ctx); err != nil {
		return err
	}
	if session.readOnly {
		return unsupportedInsert("read-only snapshots do not support query updates")
	}
	_, _, err := compileQueryUpdate(session.backend.schema, plan)
	return errors.Join(err, ctx.Err())
}
func (session *transactionSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	if session.readOnly {
		return 0, unsupportedInsert("read-only snapshots do not support query updates")
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		return executeQueryUpdate(ctx, session.transaction, session.backend.schema, plan)
	})
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
	return executeQueryUpdate(ctx, lease, scope.backend.schema, plan)
}
