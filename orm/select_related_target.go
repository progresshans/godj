package orm

import (
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"reflect"
)

// ForwardSelection is a closed, source-typed eager target. Its implementation
// retains the concrete target model type through scanning, cloning and access.
type ForwardSelection[S any] interface {
	prepareForwardSelection() (preparedForwardSelection[S], error)
}

type preparedForwardSelection[S any] interface {
	path() forwardSelectPathState[S]
	validate() error
	sameBinding(preparedForwardSelection[S]) bool
	newScan() forwardTargetScan[S]
}
type forwardTargetScan[S any] interface {
	destinations() []any
	snapshot() projectedForwardTarget[S]
}
type projectedForwardTarget[S any] interface {
	validate(S) (cachedForwardTarget, error)
}
type cachedForwardTarget interface {
	projection() query.RelationProjection
	relatedObject(db.Queryer) any
}

type preparedForwardTarget[S, T any] struct {
	state forwardSelectState[S, T]
	base  query.Plan
}

func (selection ForwardSelect[S, T]) prepareForwardSelection() (preparedForwardSelection[S], error) {
	if err := validateForwardSelectState(selection.state); err != nil {
		return nil, err
	}
	target := selection.state.path.relation.targetModel
	base, err := query.NewPlan(target.DBTable, modelFieldReferences(target)).WithLimit(2)
	if err != nil {
		return nil, err
	}
	return preparedForwardTarget[S, T]{state: selection.state, base: base}, nil
}
func (target preparedForwardTarget[S, T]) path() forwardSelectPathState[S] { return target.state.path }
func (target preparedForwardTarget[S, T]) validate() error {
	if err := validateForwardSelectState(target.state); err != nil {
		return err
	}
	expected, err := query.NewPlan(target.state.path.relation.targetModel.DBTable, modelFieldReferences(target.state.path.relation.targetModel)).WithLimit(2)
	if err != nil || !expected.Equal(target.base) {
		return relationInvalidPlan("selected target source plan changed after binding")
	}
	return nil
}
func (target preparedForwardTarget[S, T]) sameBinding(other preparedForwardSelection[S]) bool {
	value, ok := other.(preparedForwardTarget[S, T])
	return ok && target.state.path.projection.Equal(value.state.path.projection) &&
		target.state.path.source.snapshot == value.state.path.source.snapshot &&
		reflect.TypeOf(target.state.sourceDescriptor) == reflect.TypeOf(value.state.sourceDescriptor) &&
		reflect.TypeOf(target.state.targetDescriptor) == reflect.TypeOf(value.state.targetDescriptor) &&
		reflect.TypeOf(target.state.relation.storage) == reflect.TypeOf(value.state.relation.storage)
}
func (target preparedForwardTarget[S, T]) newScan() forwardTargetScan[S] {
	scan := target.state.targetDescriptor.NewProjectionScan()
	if interfaceIsNil(scan) {
		return nil
	}
	return typedForwardTargetScan[S, T]{target: target, scan: scan}
}

type typedForwardTargetScan[S, T any] struct {
	target preparedForwardTarget[S, T]
	scan   ProjectionScan[T]
}

func (scan typedForwardTargetScan[S, T]) destinations() []any { return scan.scan.Destinations() }
func (scan typedForwardTargetScan[S, T]) snapshot() projectedForwardTarget[S] {
	value, key, presence := scan.scan.Decode()
	return typedProjectedForwardTarget[S, T]{target: scan.target, value: scan.target.state.targetDescriptor.CloneModel(value), key: key, presence: presence}
}

type typedProjectedForwardTarget[S, T any] struct {
	target   preparedForwardTarget[S, T]
	value    T
	key      query.Value
	presence ProjectionPresence
}

func (row typedProjectedForwardTarget[S, T]) validate(source S) (cachedForwardTarget, error) {
	state := row.target.state
	foreignKey, ok := state.relation.storage.Value(state.sourceDescriptor.CloneModel(source))
	if !ok {
		return nil, relationInvalidPlan("relation storage could not read the projected source key")
	}
	if row.presence != ProjectionAbsent && row.presence != ProjectionPresent {
		return nil, relationInvalidPlan("target projection decoded an invalid row shape")
	}
	if row.presence == ProjectionAbsent && !row.key.IsNull() {
		return nil, relationInvalidPlan("absent target projection did not return a NULL key")
	}
	result := typedCachedForwardTarget[T]{selected: state.path.projection, descriptor: state.targetDescriptor}
	if foreignKey.IsNull() {
		if !state.relation.nullable {
			return nil, relatedObjectProjectionError(state.path.sourceKey, "required source key is NULL")
		}
		if row.presence != ProjectionAbsent || !row.key.IsNull() {
			return nil, relatedObjectProjectionError(state.path.sourceKey, "nullable NULL source key has a projected target")
		}
		return result, nil
	}
	identifier, ok := foreignKey.Integer()
	if !ok {
		return nil, relationInvalidPlan("relation storage returned a non-integer projected source key")
	}
	if row.presence != ProjectionPresent {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "non-NULL source key has no projected target")
	}
	targetID, ok := row.key.Integer()
	if !ok || row.key.IsNull() {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "projected target primary key is absent or non-integer")
	}
	if targetID != identifier {
		return nil, relatedObjectProjectionError(state.path.sourceKey, "projected target primary key does not match the source key")
	}
	plan, err := row.target.base.WithConditions(query.NewCondition(fieldReference(state.relation.targetKey), query.LookupExact, query.Integer(targetID)))
	if err != nil {
		return nil, err
	}
	result.value = state.targetDescriptor.CloneModel(row.value)
	result.present = true
	result.plan = plan
	return result, nil
}

type typedCachedForwardTarget[T any] struct {
	selected   query.RelationProjection
	descriptor ProjectionDescriptor[T]
	value      T
	present    bool
	plan       query.Plan
}

func (target typedCachedForwardTarget[T]) projection() query.RelationProjection {
	return target.selected
}
func (target typedCachedForwardTarget[T]) relatedObject(backend db.Queryer) any {
	if !target.present {
		return newAbsentRelatedObject[T]()
	}
	evaluation := newEvaluationState[T]()
	evaluation.values = []T{target.descriptor.CloneModel(target.value)}
	evaluation.ready = true
	return newRelatedObject(QuerySet[T]{backend: backend, descriptor: target.descriptor, plan: target.plan, evaluation: evaluation})
}

// Related retrieves this binding's prepared cache from a selected result. The
// erased storage is private; matching projection, snapshot and concrete T are
// required before any typed cache is returned.
func (selection ForwardSelect[S, T]) Related(selected *ForwardSelected[S]) (*RelatedObject[T], error) {
	if err := selected.validate(); err != nil {
		return nil, err
	}
	if err := validateForwardSelectState(selection.state); err != nil {
		return nil, err
	}
	if selected.binding.snapshot != selection.state.path.source.snapshot || selected.binding.identity != selection.state.path.source.identity || reflect.TypeOf(selected.sourceDescriptor) != reflect.TypeOf(selection.state.sourceDescriptor) {
		return nil, relationInvalidPlan("selected result belongs to a different source binding")
	}
	value, found := selected.targets[selection.state.path.path]
	if !found || !value.projection.Equal(selection.state.path.projection) {
		return nil, relationInvalidPlan("selected result does not contain this bound projection")
	}
	related, ok := value.related.(*RelatedObject[T])
	if !ok || related == nil {
		return nil, relationInvalidPlan("selected target has a different Go model type")
	}
	if err := related.validate(); err != nil {
		return nil, err
	}
	return related, nil
}
