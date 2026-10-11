package postgres

import (
	"context"
	"errors"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var (
	_ db.BulkUpdater = (*Backend)(nil)
	_ db.BulkUpdater = (*transactionSession)(nil)
	_ db.BulkUpdater = (*rootBatchScope)(nil)
)

func prepareBulkUpdate(schema string, spec query.BulkUpdateSpec) (queryplan.BulkUpdateParts, error) {
	return queryplan.PrepareBulkUpdate(spec, postgresBulkParameters,
		func(plan query.Plan) (queryplan.UpdateSource, error) {
			return compileUpdateSource(schema, plan)
		},
		func(table string) (string, error) { return quoteTable(schema, table) }, quoteIdentifier, nil)
}

func compileUpdateSource(schema string, plan query.Plan) (queryplan.UpdateSource, error) {
	var zero queryplan.UpdateSource
	fields, err := validateReadSourceFields(plan.SourceFields())
	if err != nil {
		return zero, err
	}
	where, err := analyzeWhere(plan, fields)
	if err != nil {
		return zero, err
	}
	prepared, err := queryplan.PrepareJoins(plan, "PostgreSQL")
	if err != nil {
		return zero, err
	}
	if len(prepared.Keys) != 0 {
		statement, arguments, err := compileRelation(schema, plan, fields, where)
		return queryplan.UpdateSource{Selection: statement, Arguments: arguments}, err
	}
	// Keep single-table predicates directly on UPDATE. Materializing those
	// keys first would update rows that no longer match after a lock wait.
	// Trimmed FK predicates and correlated NOT EXISTS have no outer joins.
	alias := ""
	resolve, resolveRHS := scalarWhereField, scalarWhereRHSField
	if where.hasRelations {
		alias = `"t0"`
		for index, exists := range prepared.Exists {
			prefix, err := queryplan.CollectionExistsPrefix(exists, func(table string) (string, error) { return quoteTable(schema, table) }, quoteIdentifier, quoteQualified)
			if err != nil {
				return zero, err
			}
			field, err := quoteQualified(exists.TerminalAlias, plan.Conditions()[index].Field().Column())
			if err != nil {
				return zero, err
			}
			where.leaves[index].existsPrefix, where.leaves[index].existsField = prefix, field
		}
		resolve = func(condition query.Condition) (string, error) {
			return quoteQualified("t0", condition.Field().Column())
		}
		resolveRHS = func(field query.FieldRef) (string, error) { return quoteQualified("t0", field.Column()) }
	}
	var statement strings.Builder
	arguments, err := appendWhere(&statement, where, resolve, resolveRHS, nil)
	return queryplan.UpdateSource{Direct: true, Where: statement.String(), Alias: alias, Arguments: arguments}, err
}

func compileBulkUpdate(schema string, plan query.BulkUpdatePlan) (string, []any, error) {
	parts, err := prepareBulkUpdate(schema, plan.Spec())
	if err != nil {
		return "", nil, err
	}
	return queryplan.CompileBulkUpdate(plan, parts, validateWriteValue, postgresValue, placeholder)
}

func executeBulkUpdate(ctx context.Context, executor queryplan.BulkUpdateExecutor, schema string, plan query.BulkUpdatePlan) (int64, error) {
	statement, arguments, err := compileBulkUpdate(schema, plan)
	if err != nil {
		return 0, err
	}
	return queryplan.ExecuteBulkUpdate(ctx, executor, plan, statement, arguments, func(err error) error {
		return classifyDatabaseError(ctx, "update", schema, plan.Spec().Selection().Table(), err)
	})
}

func (b *Backend) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := b.validateContext(ctx); err != nil {
		return 0, err
	}
	parts, err := prepareBulkUpdate(b.schema, spec)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return parts.BatchSize, nil
}

func (b *Backend) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	if err := b.validateContext(ctx); err != nil {
		return 0, err
	}
	return executeBulkUpdate(ctx, b.database, b.schema, plan)
}

func (session *transactionSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	if session.readOnly {
		return 0, unsupportedInsert("read-only snapshots do not support bulk writes")
	}
	parts, err := prepareBulkUpdate(session.backend.schema, spec)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return parts.BatchSize, nil
}

func (session *transactionSession) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	if session.readOnly {
		return 0, unsupportedInsert("read-only snapshots do not support bulk writes")
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		return executeBulkUpdate(ctx, session.transaction, session.backend.schema, plan)
	})
}

func (scope *rootBatchScope) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (limit int, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if lease != nil {
		defer func() {
			if closeErr := lease.Close(); closeErr != nil {
				err, limit = errors.Join(err, closeErr), 0
			}
		}()
	}
	return scope.backend.BulkUpdateBatchSize(ctx, spec)
}

func (scope *rootBatchScope) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (count int64, err error) {
	lease, err := scope.acquire(ctx)
	if err != nil {
		return 0, err
	}
	if lease == nil {
		return scope.backend.BulkUpdate(ctx, plan)
	}
	defer func() {
		if closeErr := lease.Close(); closeErr != nil {
			err, count = errors.Join(err, closeErr), 0
		}
	}()
	return executeBulkUpdate(ctx, lease, scope.backend.schema, plan)
}
