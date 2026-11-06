package orm

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func (manager Manager[M]) Update(ctx context.Context, backend db.Queryer, assignments ...UpdateAssignment[M]) (int64, error) {
	return manager.Using(backend).Update(ctx, assignments...)
}
func (manager Manager[M]) UpdateDynamic(ctx context.Context, backend db.Queryer, inputs ...DynamicUpdateInput) (int64, error) {
	return manager.Using(backend).UpdateDynamic(ctx, inputs...)
}

// Update performs one uniform native UPDATE with no per-row Save/Clean hooks.
// It owns an atomic transaction, or a savepoint in a borrowed writable session.
// Success reports matched rows and invalidates only this query's shared cache.
// Returned/held models and derived queries remain independent snapshots.
func (source QuerySet[M]) Update(ctx context.Context, assignments ...UpdateAssignment[M]) (int64, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return 0, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	_, model, err := manager.writeConfiguration(ctx, source.backend)
	if err != nil {
		return 0, err
	}
	if len(assignments) > query.MaximumScalarNodes {
		return 0, invalidWritePlan("too many query update assignments")
	}
	values := make([]query.ScalarAssignment, len(assignments))
	for index, input := range assignments {
		if input.err != nil {
			return 0, input.err
		}
		if _, valid := model.mutationField(input.assignment.Field()); !valid {
			return 0, invalidWritePlan("query update assignment is not model metadata")
		}
		values[index] = input.assignment
	}
	plan, err := query.NewQueryUpdatePlan(source.plan, fieldReference(model.primaryKey), values)
	if err != nil {
		return 0, err
	}
	effective, err := executionBackend(ctx, source.backend)
	if err != nil {
		return 0, err
	}
	run, borrowed, err := modelWriteScope(effective, "QuerySet.Update")
	if err != nil {
		return 0, err
	}
	if _, err := queryUpdateCapability(ctx, effective, plan); err != nil {
		return 0, err
	}
	if plan.NoOp() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		source.evaluation.invalidate()
		return 0, nil
	}
	var pending int64
	err = guardedModelWriteScope(ctx, run, func(workContext context.Context, session db.Session) error {
		capability, err := queryUpdateCapability(workContext, session, plan)
		if err != nil {
			return err
		}
		pending, err = capability.QueryUpdate(workContext, plan)
		if err != nil {
			return err
		}
		if pending < 0 {
			return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows, Detail: "query update returned a negative matched count"}
		}
		return nil
	})
	if err != nil {
		if uncertainQueryUpdate(err) {
			source.evaluation.invalidate()
		}
		return 0, errors.Join(err, ctx.Err())
	}
	if borrowed {
		if err := validateQuerySession(context.WithoutCancel(ctx), source.backend); err != nil {
			source.evaluation.invalidate()
			return 0, err
		}
	}
	source.evaluation.invalidate()
	return pending, nil
}

func (source QuerySet[M]) UpdateDynamic(ctx context.Context, inputs ...DynamicUpdateInput) (int64, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return 0, err
	}
	assignments, err := prepareDynamicUpdate[M](source.prepared, inputs)
	if err != nil {
		return 0, err
	}
	return source.Update(ctx, assignments...)
}

func queryUpdateCapability(ctx context.Context, backend db.Queryer, plan query.QueryUpdatePlan) (db.QueryUpdater, error) {
	capability, valid := backend.(db.QueryUpdater)
	if !valid || interfaceIsNil(capability) {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session has no native query update capability"}
	}
	if err := capability.CheckQueryUpdate(ctx, plan); err != nil {
		return nil, err
	}
	return capability, nil
}
func uncertainQueryUpdate(err error) bool {
	return errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
}

func (source RelatedSelectQuery[M]) Update(ctx context.Context, assignments ...UpdateAssignment[M]) (int64, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return 0, err
	}
	plain := newQuerySet(source.backend, source.sourceDescriptor, source.plan.WithoutRelationProjections(), source.prepared)
	count, err := plain.Update(ctx, assignments...)
	if err == nil || uncertainQueryUpdate(err) {
		source.evaluation.invalidate()
	}
	return count, err
}
func (source RelatedSelectQuery[M]) UpdateDynamic(ctx context.Context, inputs ...DynamicUpdateInput) (int64, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return 0, err
	}
	assignments, err := prepareDynamicUpdate[M](source.prepared, inputs)
	if err != nil {
		return 0, err
	}
	return source.Update(ctx, assignments...)
}
func (source PrefetchQuery[M]) Update(ctx context.Context, assignments ...UpdateAssignment[M]) (int64, error) {
	if err := source.validate(ctx); err != nil {
		return 0, err
	}
	plain := newQuerySet(source.source.backend, source.source.descriptor, source.source.plan, source.source.prepared)
	count, err := plain.Update(ctx, assignments...)
	if err == nil || uncertainQueryUpdate(err) {
		source.evaluation.invalidate()
	}
	return count, err
}
func (source PrefetchQuery[M]) UpdateDynamic(ctx context.Context, inputs ...DynamicUpdateInput) (int64, error) {
	if err := source.validate(ctx); err != nil {
		return 0, err
	}
	assignments, err := prepareDynamicUpdate[M](source.source.prepared, inputs)
	if err != nil {
		return 0, err
	}
	return source.Update(ctx, assignments...)
}
