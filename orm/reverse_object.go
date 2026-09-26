package orm

import (
	"context"
	"reflect"
	"sync"

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
	mu       *sync.Mutex
	querySet QuerySet[M]
	basePlan query.Plan
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
	result := &RelatedSet[M]{querySet: querySet, basePlan: querySet.plan, mu: &sync.Mutex{}}
	result._self = result
	return result
}

func (s *RelatedSet[M]) OrderBy(orderings ...Ordering[M]) (*RelatedSet[M], error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	snapshot, err := s.Query()
	if err != nil {
		return nil, err
	}
	querySet := snapshot.OrderBy(orderings...)
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	querySet, err := s.Query()
	if err != nil {
		return nil, err
	}
	return querySet.All(ctx)
}

func (s *RelatedSet[M]) Fresh() (*RelatedSet[M], error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	snapshot, err := s.Query()
	if err != nil {
		return nil, err
	}
	return newRelatedSet(newQuerySet(snapshot.backend, snapshot.descriptor, s.basePlan)), nil
}

// Query holds the current immutable query configuration and cache snapshot.
func (s *RelatedSet[M]) Query() (QuerySet[M], error) {
	if err := s.validate(); err != nil {
		return QuerySet[M]{}, err
	}
	s.mu.Lock()
	snapshot := s.querySet
	s.mu.Unlock()
	if err := validateQuerySession(context.Background(), snapshot.backend); err != nil {
		return QuerySet[M]{}, err
	}
	return snapshot, nil
}

// Invalidate restores the relation's default owner scope for future reads.
// Queries already obtained from Query keep their configuration and snapshot.
func (s *RelatedSet[M]) Invalidate() error {
	if err := s.validate(); err != nil {
		return err
	}
	snapshot, err := s.Query()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.querySet = newQuerySet(snapshot.backend, snapshot.descriptor, s.basePlan)
	s.mu.Unlock()
	return nil
}

func (s *RelatedSet[M]) validate() error {
	if s == nil || s._self != s || s.mu == nil {
		return relationInvalidPlan("related set is nil, zero, or copied")
	}
	return nil
}

// RelatedSetCache owns one generated reverse collection handle. Binding is
// serialized and performs no I/O; failed binding leaves the cell empty.
type RelatedSetCache[T any] struct {
	mu    sync.Mutex
	value *RelatedSet[T]
}

func (c *RelatedSetCache[T]) Get(bind func() (*RelatedSet[T], error)) (*RelatedSet[T], error) {
	if c == nil || bind == nil {
		return nil, relationInvalidPlan("related set cache or binder is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil {
		return c.value, nil
	}
	value, err := bind()
	if err != nil {
		return nil, err
	}
	if err := value.validate(); err != nil {
		return nil, err
	}
	c.value = value
	return value, nil
}
