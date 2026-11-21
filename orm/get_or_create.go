package orm

import (
	"context"
	"errors"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// GetOrCreate performs a fresh Get and builds input only after confirmed
// absence, inside one owned transaction or a borrowed session's savepoint.
// Query predicates are not copied into input. Only a real database unique
// constraint can arbitrate concurrent creators; arbitrary predicates do not
// imply uniqueness. A successful borrowed result is provisional until its
// parent commits. Unknown outcomes and cleanup failures are never retried.
func (qs QuerySet[M]) GetOrCreate(ctx context.Context, input CreateInput[M]) (M, bool, error) {
	return getOrCreate(ctx, qs, input, qs.lookupGet, func(value M) (M, error) {
		return qs.descriptor.CloneModel(value), nil
	})
}

// MaterializeGetOrCreate preserves an existing object's privately owned graph.
// A new model starts with ordinary lazy relation handles bound to the original
// parent/root backend, never to the expired creation scope.
func MaterializeGetOrCreate[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M], input CreateInput[M]) (*RelatedSelected[M], bool, error) {
	return getOrCreate(ctx, source, input, func(ctx context.Context) (*RelatedSelected[M], bool, error) {
		return materializeLookupGet(ctx, source, binding)
	}, func(value M) (*RelatedSelected[M], error) {
		return cloneRelatedSelection(source.backend, binding, source.descriptor, relatedSelectedValue[M]{source: value})
	})
}

func (q RelatedSelectQuery[M]) GetOrCreate(ctx context.Context, input CreateInput[M]) (*RelatedSelected[M], bool, error) {
	source := newQuerySet(q.backend, q.sourceDescriptor, q.plan.WithoutRelationProjections(), q.prepared)
	return getOrCreate(ctx, source, input, q.lookupGet, func(value M) (*RelatedSelected[M], error) {
		return cloneRelatedSelection(q.backend, q.binding, q.sourceDescriptor, relatedSelectedValue[M]{source: value})
	})
}

func (q PrefetchQuery[M]) GetOrCreate(ctx context.Context, input CreateInput[M]) (*RelatedSelected[M], bool, error) {
	return getOrCreate(ctx, q.source, input, q.lookupGet, func(value M) (*RelatedSelected[M], error) {
		return cloneRelatedSelection(q.source.backend, q.binding, q.source.descriptor, relatedSelectedValue[M]{source: value})
	})
}

// lookup distinguishes authoritative absence from any error produced by the
// backend, scanner, relation loader, or result materializer. construct runs
// before scope success so a failure cannot follow an already-committed create.
func getOrCreate[M, R any](
	ctx context.Context,
	source QuerySet[M],
	input CreateInput[M],
	lookup func(context.Context) (R, bool, error),
	construct func(M) (R, error),
) (R, bool, error) {
	var zero R
	value, found, err := lookup(ctx)
	if err != nil || found {
		return value, false, err
	}
	effective, err := executionBackend(ctx, source.backend)
	if err != nil {
		return zero, false, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	if _, _, err := manager.writeConfiguration(ctx, effective); err != nil {
		return zero, false, err
	}
	run, borrowed, err := modelWriteScope(effective, "GetOrCreate")
	if err != nil {
		return zero, false, err
	}

	// In addition to the native owner's lifetime, this local context prevents
	// late work by an adapter which violates its synchronous callback contract.
	workContext, finishWork := context.WithCancelCause(ctx)
	defer finishWork(relationBackendInvalidPlan("creation callback lifetime has ended"))
	guard := &transactionCallbackGuard{}
	var pending R
	var insertAttempted bool
	var insertErr error
	ownerErr := run(ctx, func(session db.Session) error {
		return guard.invoke(func() error {
			if interfaceIsNil(session) {
				return relationBackendInvalidPlan("creation scope supplied a nil session")
			}
			write, err := manager.prepareCreate(workContext, session, input)
			if err != nil {
				return err
			}
			var model M
			model, insertAttempted, insertErr = executePreparedCreate(workContext, session, write)
			if insertErr != nil {
				return insertErr
			}
			pending, err = construct(model)
			if err != nil {
				return err
			}
			return validateQuerySession(workContext, session)
		})
	})
	snapshot := guard.seal()
	finishWork(relationBackendInvalidPlan("creation callback lifetime has ended"))
	if snapshot.entries == 0 && snapshot.completed == 0 && ownerErr != nil {
		return zero, false, ownerErr
	}
	if snapshot.entries != 1 || snapshot.completed != 1 {
		return zero, false, errors.Join(relationBackendInvalidPlan("creation owner violated the single synchronous callback contract"), ownerErr, snapshot.result)
	}
	if snapshot.result != nil && (ownerErr == nil || !errors.Is(ownerErr, snapshot.result)) {
		return zero, false, errors.Join(relationBackendInvalidPlan("creation owner did not preserve its callback error"), ownerErr, snapshot.result)
	}
	if ownerErr == nil {
		if borrowed {
			// RELEASE already succeeded. The parent must still be usable, but
			// cancellation arriving after that boundary cannot undo RELEASE.
			if err := validateQuerySession(context.WithoutCancel(ctx), source.backend); err != nil {
				return zero, false, err
			}
		}
		// A confirmed root commit is authoritative, including a cancellation
		// observed just after it. Do not turn it into a retryable failure.
		return pending, true, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, false, errors.Join(ownerErr, err)
	}
	if !insertAttempted || insertErr == nil || !recoverableUniqueInsert(insertErr) ||
		unsafeCreationOutcome(ownerErr) || !onlyErrorCause(ownerErr, insertErr) {
		return zero, false, ownerErr
	}
	value, found, err = lookup(ctx)
	if err != nil {
		return zero, false, err
	}
	if !found {
		return zero, false, ownerErr
	}
	return value, false, nil
}

func modelWriteScope(source db.Queryer, operation string) (func(context.Context, func(db.Session) error) error, bool, error) {
	if _, borrowed := source.(db.SessionValidator); borrowed {
		session, writable := source.(db.Session)
		savepoint, supported := source.(db.Savepointer)
		if !writable || interfaceIsNil(session) || !supported || interfaceIsNil(savepoint) {
			return nil, true, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: operation + " requires a writable borrowed session with savepoints"}
		}
		return func(ctx context.Context, callback func(db.Session) error) error {
			return db.WithSavepoint(ctx, session, callback)
		}, true, nil
	}
	owner, supported := source.(db.Atomic)
	if !supported || interfaceIsNil(owner) {
		return nil, false, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: operation + " requires an atomic backend"}
	}
	return owner.Atomic, false, nil
}

func unsafeCreationOutcome(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, &query.Error{Category: query.CategoryBackend})
}

func recoverableUniqueInsert(err error) bool {
	if err == nil || unsafeCreationOutcome(err) {
		return false
	}
	budget := 64
	return uniqueErrorBranches(err, &budget)
}

func uniqueErrorBranches(err error, budget *int) bool {
	if err == nil || *budget == 0 {
		return false
	}
	*budget--
	if classified, ok := err.(*query.Error); ok {
		return classified != nil && classified.Category == query.CategoryIntegrity &&
			(classified.Code == query.CodeUniqueConstraint || classified.Code == query.CodeUniquePrimaryKey)
	}
	switch wrapper := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapper.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !uniqueErrorBranches(child, budget) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return uniqueErrorBranches(wrapper.Unwrap(), budget)
	default:
		return false
	}
}

// Transparent wrapping is allowed, but an independent cleanup failure cannot
// be hidden by a successful retry read. Equality is identity/value equality,
// not Error.Is, which may intentionally match only a broad category/code.
func onlyErrorCause(err, expected error) bool {
	budget := 64
	return onlyErrorCauseBranch(err, expected, &budget)
}

func onlyErrorCauseBranch(err, expected error, budget *int) bool {
	if err == nil || expected == nil || *budget == 0 {
		return false
	}
	*budget--
	if reflect.TypeOf(err).Comparable() && reflect.TypeOf(expected).Comparable() && err == expected {
		return true
	}
	if _, classified := err.(*query.Error); classified {
		return false
	}
	switch wrapper := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapper.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyErrorCauseBranch(child, expected, budget) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return onlyErrorCauseBranch(wrapper.Unwrap(), expected, budget)
	default:
		return false
	}
}
