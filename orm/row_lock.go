package orm

import (
	"strings"

	"github.com/progresshans/godj/query"
)

// RowLockOptions configures SelectForUpdate. The zero value waits for an UPDATE
// lock. NoWait and SkipLocked are mutually exclusive; NoKey requests the weaker
// lock that permits concurrent foreign-key references on supporting backends.
type RowLockOptions struct {
	NoWait     bool
	SkipLocked bool
	NoKey      bool
}

// RowLockTarget retains the query's source model type. Its canonical relation
// metadata comes from the same sealed IR route as typed and dynamic predicates.
// A zero target is invalid; no targets means the whole SELECT scope.
type RowLockTarget[M any] struct {
	target query.RowLockTarget
	err    error
	marker [0]func(M)
}

func LockSelf[M any]() RowLockTarget[M] { return RowLockTarget[M]{target: query.LockSelf()} }

func (relation QueryRelation[S, T]) LockTarget() RowLockTarget[S] {
	return rowLockTargetForRoute[S](relation.route)
}

func rowLockTargetForRoute[M any](route relationQueryRoute) RowLockTarget[M] {
	if err := route.validate(); err != nil {
		return RowLockTarget[M]{err: err}
	}
	key, present := relationAutoPrimaryKey(route.last().targetModel)
	if !present {
		return RowLockTarget[M]{err: relationInvalidPlan("row lock target has no canonical primary key")}
	}
	path, err := route.path(fieldReference(key), query.RelationTerminalRelatedField)
	if err != nil {
		return RowLockTarget[M]{err: err}
	}
	target, err := query.LockRelated(path)
	return RowLockTarget[M]{target: target, err: err}
}

// ParseRowLockTargets resolves case-sensitive paths and "self" without SQL
// aliases. Collections, empty segments and foreign metadata fail before I/O.
func ParseRowLockTargets[M any](source BoundModel[M], paths ...string) ([]RowLockTarget[M], error) {
	if err := validateBoundModel(source); err != nil {
		return nil, err
	}
	if len(paths) > query.MaximumRelatedLockTargets {
		return nil, relationInvalidPlan("row lock exceeds its target bound")
	}
	targets := make([]RowLockTarget[M], len(paths))
	for index, path := range paths {
		if path == "self" {
			targets[index] = LockSelf[M]()
			continue
		}
		parts := strings.Split(path, "__")
		if len(parts) > query.MaximumRelationHops {
			return nil, invalidRelatedPath(path)
		}
		route := relationQueryRoute{}
		identity, model := source.identity, source.model
		for _, part := range parts {
			if part == "" {
				return nil, invalidRelatedPath(path)
			}
			step, err := resolveQueryRelationStep(source.snapshot, identity, model, part)
			if err != nil {
				return nil, err
			}
			route.steps = append(route.steps, step)
			identity, model = step.targetIdentity, step.targetModel
		}
		target := rowLockTargetForRoute[M](route)
		if target.err != nil {
			return nil, target.err
		}
		targets[index] = target
	}
	return targets, nil
}

func withRowLock[M any](plan query.Plan, options RowLockOptions, targets []RowLockTarget[M]) (query.Plan, error) {
	if options.NoWait && options.SkipLocked {
		return query.Plan{}, &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidValue, Detail: "NoWait and SkipLocked are mutually exclusive"}
	}
	if len(targets) > query.MaximumRelatedLockTargets {
		return query.Plan{}, relationInvalidPlan("row lock exceeds its target bound")
	}
	strength, wait := query.LockForUpdate, query.LockWait
	if options.NoKey {
		strength = query.LockForNoKeyUpdate
	}
	if options.NoWait {
		wait = query.LockNoWait
	}
	if options.SkipLocked {
		wait = query.LockSkipLocked
	}
	values := make([]query.RowLockTarget, len(targets))
	for index, target := range targets {
		if target.err != nil {
			return query.Plan{}, target.err
		}
		values[index] = target.target
	}
	lock, err := query.NewRowLock(strength, wait, values...)
	if err != nil {
		return query.Plan{}, err
	}
	return plan.WithRowLock(lock)
}

// SelectForUpdate derives a fresh evaluation. Reading an earlier ordinary
// cache never acquires a lock, and lock lifetime belongs to the native session.
func (qs QuerySet[M]) SelectForUpdate(options RowLockOptions, targets ...RowLockTarget[M]) QuerySet[M] {
	qs.evaluation = newEvaluationState[M]()
	if qs.configurationErr == nil {
		qs.plan, qs.configurationErr = withRowLock(qs.plan, options, targets)
	}
	return qs
}

func (qs QuerySet[M]) LockTarget() RowLockTarget[M] {
	if qs.configurationErr != nil {
		return RowLockTarget[M]{err: qs.configurationErr}
	}
	if qs.prepared == nil || qs.evaluation == nil {
		return RowLockTarget[M]{err: relationInvalidPlan("row lock source query is unbound")}
	}
	return LockSelf[M]()
}

// ConfigurationError reports a deferred query-construction error without I/O.
func (qs QuerySet[M]) ConfigurationError() error { return qs.configurationErr }

func (q RelatedSelectQuery[M]) SelectForUpdate(options RowLockOptions, targets ...RowLockTarget[M]) RelatedSelectQuery[M] {
	q.evaluation = newEvaluationState[relatedSelectedValue[M]]()
	if q.configurationErr == nil {
		q.plan, q.configurationErr = withRowLock(q.plan, options, targets)
	}
	return q
}

func (q PrefetchQuery[M]) SelectForUpdate(options RowLockOptions, targets ...RowLockTarget[M]) PrefetchQuery[M] {
	source := q.source.SelectForUpdate(options, targets...)
	return q.derive(source).WithConfigurationError(source.configurationErr)
}

func (p SinglePrefetch[S, T]) SelectForUpdate(options RowLockOptions, targets ...RowLockTarget[T]) SinglePrefetch[S, T] {
	return p.withTarget(p.targetQuery().SelectForUpdate(options, targets...))
}
func (p ManyPrefetch[S, T, L]) SelectForUpdate(options RowLockOptions, targets ...RowLockTarget[T]) ManyPrefetch[S, T, L] {
	return p.withTarget(p.targetQuery().SelectForUpdate(options, targets...))
}
func (p ReverseCollectionPrefetch[S, T]) SelectForUpdate(options RowLockOptions, targets ...RowLockTarget[T]) ReverseCollectionPrefetch[S, T] {
	return p.withTarget(p.targetQuery().SelectForUpdate(options, targets...))
}
