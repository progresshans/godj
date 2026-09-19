package orm

import (
	"reflect"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Query routes own an immutable slice of declaration snapshots. Object and
// mutation handles continue to own single declarations, not query traversals.
type forwardQueryRoute struct {
	steps []forwardRelationState
	err   error
}

func (route forwardQueryRoute) last() forwardRelationState { return route.steps[len(route.steps)-1] }

func (route forwardQueryRoute) validate() error {
	if route.err != nil {
		return route.err
	}
	if len(route.steps) == 0 || len(route.steps) > query.MaximumRelationHops {
		return relationInvalidPlan("forward query route requires between 1 and 64 declarations")
	}
	for index, step := range route.steps {
		if err := validateForwardState(step); err != nil {
			return err
		}
		canonical, err := resolveForwardRelationState(step.snapshot, step.sourceIdentity, step.sourceModel, step.metadata.Field)
		if err != nil {
			return err
		}
		if canonical.metadata != step.metadata || !reflect.DeepEqual(canonical.targetPrimaryKey, step.targetPrimaryKey) {
			return relationInvalidPlan("forward route declaration disagrees with its project snapshot")
		}
		if index > 0 {
			previous := route.steps[index-1]
			if previous.snapshot != step.snapshot || previous.metadata.Target != step.sourceIdentity || !reflect.DeepEqual(previous.targetModel, step.sourceModel) {
				return relationInvalidPlan("forward route has disconnected project models")
			}
		}
	}
	return nil
}

func (route forwardQueryRoute) path(terminal query.FieldRef, scope query.RelationTerminalScope) (query.RelationPath, error) {
	if err := route.validate(); err != nil {
		return query.RelationPath{}, err
	}
	hops := make([]query.RelationHop, len(route.steps))
	for index, step := range route.steps {
		path, err := step.path(fieldReference(step.targetPrimaryKey))
		if err != nil {
			return query.RelationPath{}, err
		}
		hops[index] = path.Hops()[0]
	}
	return query.NewForwardRelationChain(hops, terminal, scope)
}

// ChainForward composes typed routes. The intermediate Go type and project
// model must both agree. Composition errors survive to field binding and query
// evaluation, so generated lazy groups do not panic or lose structured causes.
func ChainForward[S, T, U any](prefix ForwardRelation[S, T], suffix ForwardRelation[T, U]) ForwardRelation[S, U] {
	fail := func(err error) ForwardRelation[S, U] {
		return ForwardRelation[S, U]{route: forwardQueryRoute{err: err}}
	}
	if err := prefix.route.validate(); err != nil {
		return fail(err)
	}
	if err := suffix.route.validate(); err != nil {
		return fail(err)
	}
	if len(prefix.route.steps)+len(suffix.route.steps) > query.MaximumRelationHops {
		return fail(relationInvalidPlan("forward query route exceeds 64 declarations"))
	}
	left, right := prefix.route.last(), suffix.route.steps[0]
	if left.snapshot != right.snapshot || left.metadata.Target != right.sourceIdentity || !reflect.DeepEqual(left.targetModel, right.sourceModel) {
		return fail(relationInvalidPlan("composed forward routes do not share the same intermediate project model"))
	}
	steps := make([]forwardRelationState, 0, len(prefix.route.steps)+len(suffix.route.steps))
	steps = append(steps, prefix.route.steps...)
	steps = append(steps, suffix.route.steps...)
	return ForwardRelation[S, U]{route: forwardQueryRoute{steps: steps}}
}

// WithConfigurationError carries a generated group binding failure through
// further lazy composition without replacing an earlier route failure.
func (relation ForwardRelation[S, T]) WithConfigurationError(err error) ForwardRelation[S, T] {
	if relation.route.err == nil {
		relation.route.err = err
	}
	return relation
}

func (relation ForwardRelation[S, T]) IsNull(value bool) Predicate[S] {
	if err := relation.route.validate(); err != nil {
		return Predicate[S]{err: err}
	}
	last := relation.route.last()
	field, found := findField(last.sourceModel.Fields, last.metadata.Field)
	if !found || field.Kind != ir.FieldForeignKey {
		return Predicate[S]{err: relationInvalidPlan("forward route source key is unavailable")}
	}
	path, err := relation.route.path(fieldReference(field), query.RelationTerminalSourceKey)
	if err != nil {
		return Predicate[S]{err: err}
	}
	return predicateFromCondition[S](query.NewRelatedCondition(path, query.LookupIsNull, query.Boolean(value)), nil)
}

// Generated groups bind each concrete field through the typed route and keep
// any failure on that field. Scalar comparison and membership share this cause.
func (field RelatedIntegerField[M]) WithConfigurationError(err error) RelatedIntegerField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
func (field RelatedStringField[M]) WithConfigurationError(err error) RelatedStringField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
func (field RelatedBooleanField[M]) WithConfigurationError(err error) RelatedBooleanField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
func (field RelatedDateTimeField[M]) WithConfigurationError(err error) RelatedDateTimeField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
