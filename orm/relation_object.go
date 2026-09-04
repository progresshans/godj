package orm

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// RequiredForwardObject is a sealed required one-hop relation object loader.
// The handle itself is immutable and copyable; each successful From call owns
// a distinct RelatedObject evaluation cache.
type RequiredForwardObject[S, T any] struct {
	state        forwardObjectState[S, T]
	sourceMarker [0]func(S)
	targetMarker [0]func(T)
}

// NullableForwardObject is a sealed nullable one-hop relation object loader.
// In addition to loading it owns the relation-level source-key isnull path.
type NullableForwardObject[S, T any] struct {
	state        forwardObjectState[S, T]
	sourceMarker [0]func(S)
	targetMarker [0]func(T)
}

type forwardObjectState[S, T any] struct {
	source       BoundModel[S]
	target       BoundModel[T]
	storage      RelationStorage[S]
	targetKey    ir.Field
	nullablePath query.RelationPath
	nullable     bool
	valid        bool
	sourceMarker [0]func(S)
	targetMarker [0]func(T)
}

// RelatedObject owns one lazy target QuerySet and its successful full-result
// cache. Pointer identity is part of the ownership contract; dereference-copy
// and zero values fail with a structured invalid-plan error.
type RelatedObject[T any] struct {
	querySet QuerySet[T]
	absent   bool
	_self    *RelatedObject[T]
	marker   [0]func(T)
}

func BindRequiredForwardObject[S, T any](
	source BoundModel[S],
	field string,
	target BoundModel[T],
) (RequiredForwardObject[S, T], error) {
	state, err := bindForwardObject(source, field, target, false)
	if err != nil {
		return RequiredForwardObject[S, T]{}, err
	}
	return RequiredForwardObject[S, T]{state: state}, nil
}

func BindNullableForwardObject[S, T any](
	source BoundModel[S],
	field string,
	target BoundModel[T],
) (NullableForwardObject[S, T], error) {
	state, err := bindForwardObject(source, field, target, true)
	if err != nil {
		return NullableForwardObject[S, T]{}, err
	}
	return NullableForwardObject[S, T]{state: state}, nil
}

func bindForwardObject[S, T any](
	source BoundModel[S],
	field string,
	target BoundModel[T],
	wantNullable bool,
) (forwardObjectState[S, T], error) {
	if err := validateObjectBoundModel(source); err != nil {
		return forwardObjectState[S, T]{}, err
	}
	if err := validateObjectBoundModel(target); err != nil {
		return forwardObjectState[S, T]{}, err
	}
	if source.snapshot != target.snapshot {
		return forwardObjectState[S, T]{}, relationInvalidPlan("source and target models belong to different project snapshots")
	}

	relation, err := resolveForwardRelationState(source.snapshot, source.identity, source.model, field)
	if err != nil {
		return forwardObjectState[S, T]{}, err
	}
	if relation.metadata.Nullable != wantNullable {
		return forwardObjectState[S, T]{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeUnsupportedLookup,
			Field:    field,
			Detail:   "forward relation nullability does not match object loader",
		}
	}
	if relation.metadata.Target != target.identity || !reflect.DeepEqual(relation.targetModel, target.model) {
		return forwardObjectState[S, T]{}, relationInvalidPlan("forward relation target does not match bound target model")
	}

	sourceField, ok := findField(relation.sourceModel.Fields, relation.metadata.Field)
	if !ok || sourceField.Kind != ir.FieldForeignKey || sourceField.Nullable != wantNullable ||
		sourceField.Relation == nil || sourceField.Relation.Target != target.identity ||
		sourceField.Relation.Cardinality != ir.RelationManyToOne {
		return forwardObjectState[S, T]{}, relationInvalidPlan("forward relation source field is not canonical")
	}
	storage, ok := source.objectDescriptor.BindRelationStorage(sourceField.Clone())
	if !ok || interfaceIsNil(storage) {
		return forwardObjectState[S, T]{}, relationInvalidPlan("relation storage is not available for the canonical source field")
	}
	if !immutableZeroStateValue(storage) {
		return forwardObjectState[S, T]{}, relationInvalidPlan("relation storage must be a named non-pointer zero-size struct")
	}
	if storageField := storage.Field().Clone(); !reflect.DeepEqual(storageField, sourceField) {
		return forwardObjectState[S, T]{}, relationInvalidPlan("relation storage field does not match the canonical source field")
	}

	state := forwardObjectState[S, T]{
		source:    source,
		target:    target,
		storage:   storage,
		targetKey: relation.targetPrimaryKey.Clone(),
		nullable:  wantNullable,
		valid:     true,
	}
	if wantNullable {
		state.nullablePath, err = query.NewNullableForwardRelationIsNullPath(
			relation.sourceIdentity,
			relation.sourceModel.DBTable,
			fieldReference(sourceField),
			relation.metadata.Target,
			relation.targetModel.DBTable,
			relation.targetPrimaryKey.Column,
		)
		if err != nil {
			return forwardObjectState[S, T]{}, err
		}
	}
	return state, nil
}

func validateObjectBoundModel[M any](model BoundModel[M]) error {
	if err := validateBoundModel(model); err != nil {
		return err
	}
	if interfaceIsNil(model.objectDescriptor) {
		return relationInvalidPlan("bound model does not provide a sealed relation object descriptor")
	}
	if !immutableZeroStateValue(model.objectDescriptor) {
		return relationInvalidPlan("bound relation object descriptor is not an immutable zero-state value")
	}
	return nil
}

func (r RequiredForwardObject[S, T]) From(backend db.Queryer, source S) (*RelatedObject[T], error) {
	if interfaceIsNil(backend) {
		return nil, relationBackendInvalidPlan("backend is nil")
	}
	return r.state.from(backend, source)
}

func (r NullableForwardObject[S, T]) From(backend db.Queryer, source S) (*RelatedObject[T], error) {
	if interfaceIsNil(backend) {
		return nil, relationBackendInvalidPlan("backend is nil")
	}
	return r.state.from(backend, source)
}

func (r NullableForwardObject[S, T]) IsNull(value bool) Predicate[S] {
	if !r.state.valid || !r.state.nullable || r.state.nullablePath.TerminalScope() != query.RelationTerminalSourceKey {
		return Predicate[S]{err: relationInvalidPlan("nullable forward object relation is unbound")}
	}
	return predicateFromCondition[S](
		query.NewRelatedCondition(r.state.nullablePath, query.LookupIsNull, query.Boolean(value)),
		nil,
	)
}

func (state forwardObjectState[S, T]) from(backend db.Queryer, source S) (*RelatedObject[T], error) {
	if !state.valid {
		return nil, relationInvalidPlan("forward object relation is unbound")
	}

	snapshot := state.source.objectDescriptor.CloneModel(source)
	value, ok := state.storage.Value(snapshot)
	if !ok {
		return nil, relationInvalidPlan("relation storage could not read the bound source field")
	}
	if value.IsNull() {
		if !state.nullable {
			return nil, relationInvalidPlan("required relation storage returned NULL")
		}
		return newAbsentRelatedObject[T](), nil
	}
	identifier, ok := value.Integer()
	if !ok {
		return nil, relationInvalidPlan("relation storage returned a non-integer key")
	}

	primaryKey := NewIntegerField[T](state.targetKey)
	querySet := NewManager[T](state.target.objectDescriptor).
		Using(backend).
		Filter(primaryKey.Exact(identifier))
	limited, err := querySet.Limit(2)
	if err != nil {
		return nil, err
	}
	return newRelatedObject(limited), nil
}

func newRelatedObject[T any](querySet QuerySet[T]) *RelatedObject[T] {
	result := &RelatedObject[T]{querySet: querySet}
	result._self = result
	return result
}

func newAbsentRelatedObject[T any]() *RelatedObject[T] {
	result := &RelatedObject[T]{absent: true}
	result._self = result
	return result
}

func (r *RelatedObject[T]) Get(ctx context.Context) (T, bool, error) {
	var zero T
	if err := r.validate(); err != nil {
		return zero, false, err
	}
	if interfaceIsNil(ctx) {
		return zero, false, relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return zero, false, err
	}
	if r.absent {
		return zero, false, nil
	}

	values, err := r.querySet.All(ctx)
	if err != nil {
		return zero, false, err
	}
	switch len(values) {
	case 0:
		return zero, false, &query.Error{
			Category: query.CategoryModelState,
			Code:     query.CodeRelatedObjectMissing,
			Detail:   "related object does not exist",
		}
	case 1:
		return values[0], true, nil
	default:
		return zero, false, &query.Error{
			Category: query.CategoryIntegrity,
			Code:     query.CodeRelatedObjectCardinality,
			Detail:   "forward relation resolved to more than one target row",
		}
	}
}

func (r *RelatedObject[T]) Fresh() (*RelatedObject[T], error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	if r.absent {
		return newAbsentRelatedObject[T](), nil
	}
	return newRelatedObject(r.querySet.Fresh()), nil
}

func (r *RelatedObject[T]) validate() error {
	if r == nil || r._self != r {
		return relationInvalidPlan("related object is nil, zero, or copied")
	}
	return nil
}

func relationBackendInvalidPlan(detail string) *query.Error {
	return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: detail}
}
