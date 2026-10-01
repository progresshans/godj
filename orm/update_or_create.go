package orm

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// UpdateOrCreate reads and writes in one owned transaction or borrowed
// savepoint. It evaluates create only on absence and patch only on an existing
// row, using the current locked model. A validated empty patch performs no
// UPDATE. Predicates are not copied into create input or authorization rules.
// A successful borrowed result remains provisional until its parent commits.
//
// A concurrent unique creation is recovered only after confirmed rollback of
// the inner create savepoint, by one fresh locked read and the patch branch.
// Other conflicts, unknown outcomes and cleanup failures are never retried.
func (source QuerySet[M]) UpdateOrCreate(ctx context.Context, create CreateInput[M], patch PatchInput[M]) (M, bool, error) {
	return updateOrCreate(ctx, source, create, patch, func(value relatedSelectedValue[M]) (M, error) {
		return source.descriptor.CloneModel(value.source), nil
	})
}

// MaterializeUpdateOrCreate prepares selected relations from the saved model
// before the scope can succeed. Returned caches and lazy edges use the original
// caller's backend lifetime, not the operation's expiring internal scope.
func MaterializeUpdateOrCreate[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M], create CreateInput[M], patch PatchInput[M]) (*RelatedSelected[M], bool, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return nil, false, err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return nil, false, err
	}
	return updateOrCreate(ctx, source, create, patch, func(value relatedSelectedValue[M]) (*RelatedSelected[M], error) {
		return cloneWrittenSelection(source.backend, binding, source.descriptor, value)
	})
}

func (q RelatedSelectQuery[M]) UpdateOrCreate(ctx context.Context, create CreateInput[M], patch PatchInput[M]) (*RelatedSelected[M], bool, error) {
	if err := q.validateTerminal(ctx); err != nil {
		return nil, false, err
	}
	source := newQuerySet(q.backend, q.sourceDescriptor, q.plan.WithoutRelationProjections(), q.prepared)
	source.materialization = &queryMaterialization[M]{binding: q.binding, targets: q.targets, eagerNodes: q.nodes}
	if q.materialization != nil {
		source.materialization.selections = q.materialization.selections
	}
	return MaterializeUpdateOrCreate(ctx, source, q.binding, create, patch)
}

func (q PrefetchQuery[M]) UpdateOrCreate(ctx context.Context, create CreateInput[M], patch PatchInput[M]) (*RelatedSelected[M], bool, error) {
	if err := q.validate(ctx); err != nil {
		return nil, false, err
	}
	source := q.source
	source.materialization = &queryMaterialization[M]{binding: q.binding, selections: q.selections}
	if q.related != nil {
		source.materialization.targets = q.related.targets
		source.materialization.eagerNodes = q.related.nodes
	}
	return MaterializeUpdateOrCreate(ctx, source, q.binding, create, patch)
}

func updateOrCreate[M, R any](ctx context.Context, source QuerySet[M], create CreateInput[M], patch PatchInput[M], construct func(relatedSelectedValue[M]) (R, error)) (R, bool, error) {
	var zero R
	if err := source.validateTerminal(ctx); err != nil {
		return zero, false, err
	}
	effective, err := executionBackend(ctx, source.backend)
	if err != nil {
		return zero, false, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	if _, _, err := manager.writeConfiguration(ctx, effective); err != nil {
		return zero, false, err
	}
	run, borrowed, err := modelWriteScope(effective, "UpdateOrCreate")
	if err != nil {
		return zero, false, err
	}
	var pending R
	var created bool
	err = guardedModelWriteScope(ctx, run, func(workContext context.Context, session db.Session) error {
		plan, err := readModifyWritePlan(workContext, session, source.plan)
		if err != nil {
			return err
		}
		lookup := source
		lookup.backend, lookup.plan = session, plan
		// Keep eager joins for explicit OF targets and their projection checks.
		// Prefetch batches belong to the returned saved model, whose FK values
		// may differ from those of the row found here.
		if source.materialization != nil {
			lookup.materialization = &queryMaterialization[M]{binding: source.materialization.binding, targets: source.materialization.targets, eagerNodes: source.materialization.eagerNodes}
		}
		value, found, err := lookup.lookupGet(workContext)
		if err != nil {
			return err
		}
		if !found {
			value, created, err = createOrRecoverForUpdate(workContext, session, manager, create, lookup.lookupGet)
			if err != nil {
				return err
			}
		}
		if !created {
			write, err := manager.prepareUpdate(workContext, session, value, patch)
			if err != nil {
				return err
			}
			value = write.mutation.value
			if len(write.mutation.assignments) > 0 {
				value, err = executePreparedUpdate(workContext, session, write)
				if err != nil {
					return err
				}
			}
		}
		graph, err := materializeSavedModel(workContext, session, source, value)
		if err != nil {
			return err
		}
		pending, err = construct(graph)
		return err
	})
	if err != nil {
		return zero, false, errors.Join(err, ctx.Err())
	}
	if borrowed {
		if err := validateQuerySession(context.WithoutCancel(ctx), source.backend); err != nil {
			return zero, false, err
		}
	}
	// Scope success is authoritative even if cancellation follows COMMIT or
	// RELEASE. It is never converted to a retryable cancellation here.
	return pending, created, nil
}

func readModifyWritePlan(ctx context.Context, session db.Session, plan query.Plan) (query.Plan, error) {
	capability, ok := session.(db.ReadModifyWriteSession)
	if !ok || interfaceIsNil(capability) {
		return query.Plan{}, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session has no native read-modify-write policy"}
	}
	policy, err := capability.ReadModifyWritePolicy(ctx)
	if err != nil {
		return query.Plan{}, err
	}
	if err := plan.ValidateRowLock(); err != nil {
		return query.Plan{}, err
	}
	lock, explicit := plan.RowLock()
	switch policy {
	case db.ReadModifyWriteRowLock:
		if explicit {
			if lock.WaitPolicy() == query.LockSkipLocked {
				return query.Plan{}, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "UpdateOrCreate cannot treat a skipped locked row as absent"}
			}
			targets := lock.Targets()
			root := len(targets) == 0
			for _, target := range targets {
				root = root || target.Self()
			}
			if !root {
				return query.Plan{}, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "UpdateOrCreate requires its source row in the explicit lock targets"}
			}
			return plan, nil
		}
		lock, err = query.NewRowLock(query.LockForUpdate, query.LockWait, query.LockSelf())
		if err != nil {
			return query.Plan{}, err
		}
		return plan.WithRowLock(lock)
	case db.ReadModifyWriteConflict:
		if explicit {
			return query.Plan{}, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "transaction conflict detection does not implement explicit row locks"}
		}
		return plan, nil
	default:
		return query.Plan{}, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session returned an unknown read-modify-write policy"}
	}
}

func createOrRecoverForUpdate[M any](ctx context.Context, parent db.Session, manager Manager[M], create CreateInput[M], lookup func(context.Context) (M, bool, error)) (M, bool, error) {
	var zero M
	var pending M
	var insertAttempted bool
	var insertErr error
	run := func(ctx context.Context, callback func(db.Session) error) error {
		return db.WithSavepoint(ctx, parent, callback)
	}
	err := guardedModelWriteScope(ctx, run, func(workContext context.Context, session db.Session) error {
		write, err := manager.prepareCreate(workContext, session, create)
		if err != nil {
			return err
		}
		pending, insertAttempted, insertErr = executePreparedCreate(workContext, session, write)
		return insertErr
	})
	if err == nil {
		return pending, true, nil
	}
	if !insertAttempted || insertErr == nil || ctx.Err() != nil || !recoverableUniqueInsert(insertErr) || unsafeCreationOutcome(err) || !onlyErrorCause(err, insertErr) {
		return zero, false, err
	}
	// The child must have ended and the original parent must be usable before
	// reading the winner. A poisoned parent cannot manufacture recovery.
	if scopeErr := validateQuerySession(ctx, parent); scopeErr != nil {
		return zero, false, errors.Join(err, scopeErr)
	}
	value, found, lookupErr := lookup(ctx)
	if lookupErr != nil {
		return zero, false, lookupErr
	}
	if !found {
		return zero, false, err
	}
	return value, false, nil
}

// Both the operation and its inner create scope require exactly one completed
// callback. A broken adapter cannot publish partial output, swallow failures or
// retain a usable work context after returning to its caller.
func guardedModelWriteScope(ctx context.Context, run func(context.Context, func(db.Session) error) error, callback func(context.Context, db.Session) error) error {
	workContext, finishWork := context.WithCancelCause(ctx)
	defer finishWork(relationBackendInvalidPlan("model write callback lifetime has ended"))
	guard := &transactionCallbackGuard{}
	ownerErr := run(ctx, func(session db.Session) error {
		return guard.invoke(func() error {
			if interfaceIsNil(session) {
				return relationBackendInvalidPlan("model write scope supplied a nil session")
			}
			if err := callback(workContext, session); err != nil {
				return err
			}
			return validateQuerySession(workContext, session)
		})
	})
	snapshot := guard.seal()
	finishWork(relationBackendInvalidPlan("model write callback lifetime has ended"))
	if snapshot.entries == 0 && snapshot.completed == 0 && ownerErr != nil {
		return ownerErr
	}
	if snapshot.entries != 1 || snapshot.completed != 1 {
		return errors.Join(relationBackendInvalidPlan("model write owner violated the single synchronous callback contract"), ownerErr, snapshot.result)
	}
	if snapshot.result != nil && (ownerErr == nil || !errors.Is(ownerErr, snapshot.result)) {
		return errors.Join(relationBackendInvalidPlan("model write owner did not preserve its callback error"), ownerErr, snapshot.result)
	}
	return ownerErr
}

func materializeSavedModel[M any](ctx context.Context, session db.Session, source QuerySet[M], value M) (relatedSelectedValue[M], error) {
	values := []relatedSelectedValue[M]{{source: source.descriptor.CloneModel(value)}}
	if source.materialization != nil {
		for _, target := range source.materialization.targets {
			if err := target.prefetch().apply(ctx, session, values); err != nil {
				return relatedSelectedValue[M]{}, err
			}
		}
		if err := loadPrefetchValues(ctx, session, values, source.materialization.selections); err != nil {
			return relatedSelectedValue[M]{}, err
		}
	}
	return sessionReadResult(ctx, session, values[0], nil)
}
