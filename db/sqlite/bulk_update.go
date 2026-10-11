package sqlite

import (
	"context"
	"errors"
	"strconv"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/db/internal/txscope"
	"github.com/progresshans/godj/query"
)

var (
	_ db.BulkUpdater = (*Backend)(nil)
	_ db.BulkUpdater = (*transactionSession)(nil)
	_ db.BulkUpdater = (*rootBatchScope)(nil)
	_ db.BulkUpdater = (*relationSession)(nil)
	_ db.BulkUpdater = (*writeSession)(nil)
)

func prepareBulkUpdate(spec query.BulkUpdateSpec) (queryplan.BulkUpdateParts, error) {
	return queryplan.PrepareBulkUpdate(spec, sqliteBulkParameters, func(plan query.Plan) (queryplan.UpdateSource, error) {
		statement, arguments, err := Compile(plan)
		return queryplan.UpdateSource{Selection: statement, Arguments: arguments}, err
	}, quoteIdentifier, quoteIdentifier, sqliteIdentifierKey)
}

func CompileBulkUpdate(plan query.BulkUpdatePlan) (string, []any, error) {
	parts, err := prepareBulkUpdate(plan.Spec())
	if err != nil {
		return "", nil, err
	}
	return queryplan.CompileBulkUpdate(plan, parts, queryplan.WriteValue, sqliteValue, func(index int) string { return "?" + strconv.Itoa(index) })
}

func executeBulkUpdate(ctx context.Context, executor queryplan.BulkUpdateExecutor, plan query.BulkUpdatePlan) (int64, error) {
	statement, arguments, err := CompileBulkUpdate(plan)
	if err != nil {
		return 0, err
	}
	return queryplan.ExecuteBulkUpdate(ctx, executor, plan, statement, arguments, func(err error) error {
		return classifySQLiteWriteError(ctx, "update", err)
	})
}

func (b *Backend) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return 0, err
	}
	parts, err := prepareBulkUpdate(spec)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return parts.BatchSize, nil
}

func (b *Backend) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	if err := b.validateWriteContext(ctx); err != nil {
		return 0, err
	}
	return executeBulkUpdate(ctx, b.database, plan)
}

func (session *transactionSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	parts, err := prepareBulkUpdate(spec)
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
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		return executeBulkUpdate(ctx, session.transaction, plan)
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
	return executeBulkUpdate(ctx, lease, plan)
}

func (session *relationSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	parts, err := prepareBulkUpdate(spec)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return parts.BatchSize, nil
}

func (session *relationSession) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	if err := session.validate(ctx); err != nil {
		return 0, err
	}
	return txscope.Do(session.scope, ctx, func(ctx context.Context) (int64, error) {
		statement, arguments, err := CompileBulkUpdate(plan)
		if err != nil {
			return 0, err
		}
		if !plan.Spec().Selection().EmptyResult() {
			session.state.mutationPossible.Store(true)
		}
		return queryplan.ExecuteBulkUpdate(ctx, session.state.connection, plan, statement, arguments, func(err error) error {
			return classifySQLiteWriteError(ctx, "update", err)
		})
	})
}

func (session *writeSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.session.BulkUpdateBatchSize(ctx, spec)
}

func (session *writeSession) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return 0, err
	}
	return session.session.BulkUpdate(ctx, plan)
}
