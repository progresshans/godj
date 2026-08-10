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
	relation         reverseRelationState
	ownerDescriptor  PrimaryKeyObjectDescriptor[Owner]
	sourceDescriptor RelationObjectDescriptor[Source]
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

func BindReverseObject[Owner, Source any](
	owner BoundModel[Owner],
	reverseName string,
	source BoundModel[Source],
) (ReverseObject[Owner, Source], error) {
	if err := validateObjectBoundModel(owner); err != nil {
		return ReverseObject[Owner, Source]{}, err
	}
	if err := validateObjectBoundModel(source); err != nil {
		return ReverseObject[Owner, Source]{}, err
	}
	relation, err := bindReverseRelationState(owner, reverseName, source)
	if err != nil {
		return ReverseObject[Owner, Source]{}, err
	}
	ownerDescriptor, ok := owner.objectDescriptor.(PrimaryKeyObjectDescriptor[Owner])
	if !ok || interfaceIsNil(ownerDescriptor) || !immutableZeroStateValue(ownerDescriptor) {
		return ReverseObject[Owner, Source]{}, relationInvalidPlan("reverse relation owner does not provide a sealed primary-key object descriptor")
	}

	sourceForeignKey, ok := findField(relation.forward.sourceModel.Fields, relation.forward.metadata.Field)
	if !ok || sourceForeignKey.Kind != ir.FieldForeignKey ||
		sourceForeignKey.Column != relation.forward.metadata.Column ||
		sourceForeignKey.Nullable != relation.forward.metadata.Nullable ||
		sourceForeignKey.Relation == nil ||
		sourceForeignKey.Relation.Target != owner.identity ||
		sourceForeignKey.Relation.Cardinality != ir.RelationManyToOne ||
		sourceForeignKey.Relation.Reverse.Disabled ||
		sourceForeignKey.Relation.Reverse.Name != reverseName {
		return ReverseObject[Owner, Source]{}, relationInvalidPlan("reverse relation source ForeignKey is not canonical")
	}
	sourcePrimaryKey, ok := relationAutoPrimaryKey(relation.forward.sourceModel)
	if !ok {
		return ReverseObject[Owner, Source]{}, relationInvalidPlan("reverse relation source does not have one AutoField primary key")
	}
	ownerPrimaryKey, ok := relationAutoPrimaryKey(owner.model)
	if !ok || !reflect.DeepEqual(relation.forward.targetPrimaryKey, ownerPrimaryKey) {
		return ReverseObject[Owner, Source]{}, relationInvalidPlan("reverse relation owner primary key is not canonical")
	}

	state := reverseObjectState[Owner, Source]{
		relation:         relation,
		ownerDescriptor:  ownerDescriptor,
		sourceDescriptor: source.objectDescriptor,
		ownerPrimaryKey:  ownerPrimaryKey.Clone(),
		sourceForeignKey: sourceForeignKey.Clone(),
		sourcePrimaryKey: sourcePrimaryKey.Clone(),
		valid:            true,
	}
	return ReverseObject[Owner, Source]{state: state}, nil
}

func (r ReverseObject[Owner, Source]) From(
	backend db.Queryer,
	owner Owner,
) (*RelatedSet[Source], error) {
	if interfaceIsNil(backend) {
		return nil, relationBackendInvalidPlan("backend is nil")
	}
	if err := r.state.validate(); err != nil {
		return nil, err
	}

	ownerSnapshot := r.state.ownerDescriptor.CloneModel(owner)
	primaryKey, present := r.state.ownerDescriptor.PrimaryKey(ownerSnapshot)
	if !present {
		return nil, &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeMissingPrimaryKey,
			Field:    r.state.ownerPrimaryKey.Name,
			Detail:   "reverse relation owner has no explicit primary key state",
		}
	}
	identifier, ok := primaryKey.Integer()
	if !ok || primaryKey.IsNull() {
		return nil, relationInvalidPlan("reverse relation owner descriptor returned an invalid primary key value")
	}

	predicate := Predicate[Source]{condition: query.NewCondition(
		fieldReference(r.state.sourceForeignKey),
		query.LookupExact,
		query.Integer(identifier),
	)}
	ordering := NewIntegerField[Source](r.state.sourcePrimaryKey).Asc()
	querySet := NewManager[Source](r.state.sourceDescriptor).
		Using(backend).
		Filter(predicate).
		OrderBy(ordering)
	if querySet.configurationErr != nil {
		return nil, querySet.configurationErr
	}
	return newRelatedSet(querySet), nil
}

func (state reverseObjectState[Owner, Source]) validate() error {
	if !state.valid || interfaceIsNil(state.ownerDescriptor) || interfaceIsNil(state.sourceDescriptor) {
		return relationInvalidPlan("reverse object relation is unbound")
	}
	if err := validateReverseRelationState(state.relation); err != nil {
		return err
	}
	if !immutableZeroStateValue(state.ownerDescriptor) || !immutableZeroStateValue(state.sourceDescriptor) {
		return relationInvalidPlan("reverse object descriptors are not immutable zero-state values")
	}
	if !reflect.DeepEqual(state.ownerPrimaryKey, state.relation.forward.targetPrimaryKey) {
		return relationInvalidPlan("reverse object owner primary key changed")
	}
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
