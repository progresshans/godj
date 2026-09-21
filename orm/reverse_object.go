package orm

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ReverseObject is the owner-instance capability for one named reverse
// ForeignKey relation. Query-only reverse predicates use ReverseRelation and
// do not require this primary-key-aware handle.
type ReverseObject[Owner, Source any] struct {
	state        reverseObjectState[Owner, Source]
	ownerMarker  [0]func(Owner)
	sourceMarker [0]func(Source)
}

type reverseObjectState[Owner, Source any] struct {
	owner            BoundModel[Owner]
	source           BoundModel[Source]
	ownerDescriptor  PrimaryKeyObjectDescriptor[Owner]
	sourceDescriptor RelationObjectDescriptor[Source]
	sourcePlan       query.Plan
	ownerPrimaryKey  ir.Field
	sourceForeignKey ir.Field
	sourcePrimaryKey ir.Field
	valid            bool
	ownerMarker      [0]func(Owner)
	sourceMarker     [0]func(Source)
}

// RelatedSet owns one immutable source QuerySet and its evaluation state.
// Pointer identity is part of the cache ownership contract.
type RelatedSet[M any] struct {
	querySet QuerySet[M]
	_self    *RelatedSet[M]
	marker   [0]func(M)
}

// ReverseOneToOneObject binds a single reverse object independently of an
// ordinary unique ForeignKey's collection API. Every From call owns its cache.
type ReverseOneToOneObject[Owner, Source any] struct {
	state reverseObjectState[Owner, Source]
}

func BindReverseObject[Owner, Source any](owner BoundModel[Owner], reverseName string, source BoundModel[Source]) (ReverseObject[Owner, Source], error) {
	state, err := bindReverseObjectState(owner, reverseName, source, ir.RelationManyToOne)
	return ReverseObject[Owner, Source]{state: state}, err
}

func BindReverseOneToOneObject[Owner, Source any](owner BoundModel[Owner], reverseName string, source BoundModel[Source]) (ReverseOneToOneObject[Owner, Source], error) {
	state, err := bindReverseObjectState(owner, reverseName, source, ir.RelationOneToOne)
	return ReverseOneToOneObject[Owner, Source]{state: state}, err
}

func bindReverseObjectState[Owner, Source any](owner BoundModel[Owner], reverseName string, source BoundModel[Source], cardinality ir.RelationCardinality) (reverseObjectState[Owner, Source], error) {
	if err := validateObjectBoundModel(owner); err != nil {
		return reverseObjectState[Owner, Source]{}, err
	}
	if err := validateObjectBoundModel(source); err != nil {
		return reverseObjectState[Owner, Source]{}, err
	}
	relation, err := bindReverseRelationState(owner, reverseName, source)
	if err != nil {
		return reverseObjectState[Owner, Source]{}, err
	}
	ownerDescriptor, ok := owner.objectDescriptor.(PrimaryKeyObjectDescriptor[Owner])
	if !ok || interfaceIsNil(ownerDescriptor) || !immutableZeroStateValue(ownerDescriptor) {
		return reverseObjectState[Owner, Source]{}, relationInvalidPlan("reverse relation owner does not provide a sealed primary-key object descriptor")
	}

	sourceForeignKey, ok := findField(relation.forward.sourceModel.Fields, relation.forward.metadata.Field)
	if !ok || sourceForeignKey.Kind != ir.FieldForeignKey ||
		sourceForeignKey.Column != relation.forward.metadata.Column ||
		sourceForeignKey.Nullable != relation.forward.metadata.Nullable ||
		sourceForeignKey.Relation == nil ||
		sourceForeignKey.Relation.Target != owner.identity ||
		sourceForeignKey.Relation.Cardinality != cardinality ||
		sourceForeignKey.Relation.Reverse.Disabled ||
		sourceForeignKey.Relation.Reverse.Name != reverseName {
		return reverseObjectState[Owner, Source]{}, relationInvalidPlan("reverse relation source ForeignKey is not canonical")
	}
	sourcePrimaryKey, ok := relationAutoPrimaryKey(relation.forward.sourceModel)
	if !ok {
		return reverseObjectState[Owner, Source]{}, relationInvalidPlan("reverse relation source does not have one AutoField primary key")
	}
	ownerPrimaryKey, ok := relationAutoPrimaryKey(owner.model)
	if !ok || !reflect.DeepEqual(relation.forward.targetPrimaryKey, ownerPrimaryKey) {
		return reverseObjectState[Owner, Source]{}, relationInvalidPlan("reverse relation owner primary key is not canonical")
	}

	state := reverseObjectState[Owner, Source]{
		owner:            owner,
		source:           source,
		ownerDescriptor:  ownerDescriptor,
		sourceDescriptor: source.objectDescriptor,
		sourcePlan:       source.objectPlan,
		ownerPrimaryKey:  ownerPrimaryKey.Clone(),
		sourceForeignKey: sourceForeignKey.Clone(),
		sourcePrimaryKey: sourcePrimaryKey.Clone(),
		valid:            true,
	}
	return state, nil
}

func (r ReverseObject[Owner, Source]) From(backend db.Queryer, owner Owner) (*RelatedSet[Source], error) {
	querySet, err := r.state.from(backend, owner)
	if err != nil {
		return nil, err
	}
	return newRelatedSet(querySet), nil
}

func (r ReverseOneToOneObject[Owner, Source]) From(backend db.Queryer, owner Owner) (*RelatedObject[Source], error) {
	querySet, err := r.state.from(backend, owner)
	if err != nil {
		return nil, err
	}
	limited, err := querySet.Limit(2)
	if err != nil {
		return nil, err
	}
	result := newRelatedObject(limited)
	result.allowMissing = true
	return result, nil
}

func (state reverseObjectState[Owner, Source]) from(backend db.Queryer, owner Owner) (QuerySet[Source], error) {
	if interfaceIsNil(backend) {
		return QuerySet[Source]{}, relationBackendInvalidPlan("backend is nil")
	}
	if err := state.validate(); err != nil {
		return QuerySet[Source]{}, err
	}

	ownerSnapshot := state.ownerDescriptor.CloneModel(owner)
	primaryKey, present := state.ownerDescriptor.PrimaryKey(ownerSnapshot)
	if !present {
		return QuerySet[Source]{}, &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeMissingPrimaryKey,
			Field:    state.ownerPrimaryKey.Name,
			Detail:   "reverse relation owner has no explicit primary key state",
		}
	}
	identifier, ok := primaryKey.Integer()
	if !ok || primaryKey.IsNull() {
		return QuerySet[Source]{}, relationInvalidPlan("reverse relation owner descriptor returned an invalid primary key value")
	}

	predicate := predicateFromCondition[Source](query.NewCondition(
		fieldReference(state.sourceForeignKey),
		query.LookupExact,
		query.Integer(identifier),
	), nil)
	ordering := NewAutoField[Source](state.sourcePrimaryKey).Asc()
	querySet := newQuerySet(backend, state.sourceDescriptor, state.sourcePlan).
		Filter(predicate).
		OrderBy(ordering)
	if querySet.configurationErr != nil {
		return QuerySet[Source]{}, querySet.configurationErr
	}
	return querySet, nil
}

func (state reverseObjectState[Owner, Source]) validate() error {
	if !state.valid || interfaceIsNil(state.ownerDescriptor) || interfaceIsNil(state.sourceDescriptor) {
		return relationInvalidPlan("reverse object relation is unbound")
	}
	// BindReverseObject publishes only canonical private field snapshots and
	// immutable descriptor values. Owner and callback results remain per-call.
	return nil
}

func newRelatedSet[M any](querySet QuerySet[M]) *RelatedSet[M] {
	result := &RelatedSet[M]{querySet: querySet}
	result._self = result
	return result
}

func (s *RelatedSet[M]) OrderBy(orderings ...Ordering[M]) (*RelatedSet[M], error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	querySet := s.querySet.OrderBy(orderings...)
	if querySet.configurationErr != nil {
		return nil, querySet.configurationErr
	}
	return newRelatedSet(querySet), nil
}

func (s *RelatedSet[M]) All(ctx context.Context) ([]M, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if interfaceIsNil(ctx) {
		return nil, relationInvalidPlan("context is nil")
	}
	return s.querySet.All(ctx)
}

func (s *RelatedSet[M]) Fresh() (*RelatedSet[M], error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return newRelatedSet(s.querySet.Fresh()), nil
}

func (s *RelatedSet[M]) validate() error {
	if s == nil || s._self != s {
		return relationInvalidPlan("related set is nil, zero, or copied")
	}
	return nil
}
