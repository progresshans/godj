package orm

import (
	"context"
	"reflect"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const reversePrefetchKeyLimit = 999

// ReversePrefetch is the sealed one-batch loading capability for one bound
// reverse ForeignKey object relation. Each Load owns independent evaluation
// state and never evaluates the primary owner query.
type ReversePrefetch[Owner, Source any] struct {
	state        reversePrefetchState[Owner, Source]
	ownerMarker  [0]func(Owner)
	sourceMarker [0]func(Source)
}

type reversePrefetchState[Owner, Source any] struct {
	reverse      reverseObjectState[Owner, Source]
	storage      RelationStorage[Source]
	valid        bool
	ownerMarker  [0]func(Owner)
	sourceMarker [0]func(Source)
}

// ReverseOneToOnePrefetch loads single reverse objects with one source batch.
// Ordinary unique ForeignKeys retain the collection-shaped ReversePrefetch API.
type ReverseOneToOnePrefetch[Owner, Source any] struct {
	state reversePrefetchState[Owner, Source]
}

// BindReversePrefetch adds the source ForeignKey storage capability required
// to group one batch result to the owners of an already bound ReverseObject.
func BindReversePrefetch[Owner, Source any](reverse ReverseObject[Owner, Source]) (ReversePrefetch[Owner, Source], error) {
	state, err := bindReversePrefetchState(reverse.state)
	return ReversePrefetch[Owner, Source]{state: state}, err
}

func BindReverseOneToOnePrefetch[Owner, Source any](reverse ReverseOneToOneObject[Owner, Source]) (ReverseOneToOnePrefetch[Owner, Source], error) {
	state, err := bindReversePrefetchState(reverse.state)
	return ReverseOneToOnePrefetch[Owner, Source]{state: state}, err
}

func bindReversePrefetchState[Owner, Source any](reverse reverseObjectState[Owner, Source]) (reversePrefetchState[Owner, Source], error) {
	if err := reverse.validate(); err != nil {
		return reversePrefetchState[Owner, Source]{}, err
	}
	storage, ok := reverse.sourceDescriptor.BindRelationStorage(reverse.sourceForeignKey.Clone())
	if !ok || interfaceIsNil(storage) {
		return reversePrefetchState[Owner, Source]{}, relationInvalidPlan("reverse prefetch source ForeignKey storage is unavailable")
	}
	if !immutableZeroStateValue(storage) {
		return reversePrefetchState[Owner, Source]{}, relationInvalidPlan("reverse prefetch source ForeignKey storage must be a named non-pointer zero-size struct")
	}
	if !reflect.DeepEqual(storage.Field(), reverse.sourceForeignKey) {
		return reversePrefetchState[Owner, Source]{}, relationInvalidPlan("reverse prefetch source ForeignKey storage is not canonical")
	}

	// The reverse handle already owns immutable canonical fields. Only the
	// field passed to the user storage callback above crosses an ownership edge.
	state := reversePrefetchState[Owner, Source]{
		reverse: reverse,
		storage: storage,
		valid:   true,
	}
	return state, nil
}

// Load evaluates exactly one source batch query, validates every returned
// source membership, and returns ready RelatedSet values only after the entire
// operation succeeds.
func (p ReversePrefetch[Owner, Source]) Load(ctx context.Context, backend db.Queryer, owners []Owner) ([]*RelatedSet[Source], error) {
	queries, err := p.state.load(ctx, backend, owners)
	if err != nil {
		return nil, err
	}
	result := make([]*RelatedSet[Source], len(queries))
	for i, querySet := range queries {
		result[i] = newRelatedSet(querySet)
	}
	return result, nil
}

// Load publishes independent ready single-object handles after validating the
// complete batch. A missing child is cached as an ordinary absent result.
func (p ReverseOneToOnePrefetch[Owner, Source]) Load(ctx context.Context, backend db.Queryer, owners []Owner) ([]*RelatedObject[Source], error) {
	queries, err := p.state.load(ctx, backend, owners)
	if err != nil {
		return nil, err
	}
	result := make([]*RelatedObject[Source], len(queries))
	for i, querySet := range queries {
		result[i] = newRelatedObject(querySet)
		result[i].allowMissing = true
	}
	return result, nil
}

func (state reversePrefetchState[Owner, Source]) load(ctx context.Context, backend db.Queryer, owners []Owner) ([]QuerySet[Source], error) {
	if interfaceIsNil(ctx) {
		return nil, relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if interfaceIsNil(backend) {
		return nil, relationBackendInvalidPlan("backend is nil")
	}
	if err := state.validate(); err != nil {
		return nil, err
	}
	if len(owners) == 0 {
		return make([]QuerySet[Source], 0), nil
	}

	ownerSnapshots := make([]Owner, len(owners))
	for index := range owners {
		ownerSnapshots[index] = state.reverse.ownerDescriptor.CloneModel(owners[index])
	}
	ownerKeys := make([]int64, len(ownerSnapshots))
	for index := range ownerSnapshots {
		primaryKey, present := state.reverse.ownerDescriptor.PrimaryKey(ownerSnapshots[index])
		if !present {
			return nil, &query.Error{
				Category: query.CategoryQuery,
				Code:     query.CodeMissingPrimaryKey,
				Field:    state.reverse.ownerPrimaryKey.Name,
				Detail:   "reverse prefetch owner has no explicit primary key state",
			}
		}
		identifier, ok := primaryKey.Integer()
		if !ok || primaryKey.IsNull() {
			return nil, relationInvalidPlan("reverse prefetch owner descriptor returned an invalid primary key value")
		}
		ownerKeys[index] = identifier
	}

	requested := make(map[int64]struct{}, len(ownerKeys))
	batchKeys := make([]int64, 0, len(ownerKeys))
	for _, identifier := range ownerKeys {
		if _, exists := requested[identifier]; exists {
			continue
		}
		requested[identifier] = struct{}{}
		batchKeys = append(batchKeys, identifier)
	}
	slices.Sort(batchKeys)
	if len(batchKeys) > reversePrefetchKeyLimit {
		return nil, &query.Error{
			Category: query.CategoryArgument,
			Code:     query.CodeInvalidValue,
			Detail:   "reverse prefetch supports at most 999 distinct owner keys",
		}
	}

	values := make([]query.Value, len(batchKeys))
	for index, identifier := range batchKeys {
		values[index] = query.Integer(identifier)
	}
	inCondition, err := query.NewInCondition(fieldReference(state.reverse.sourceForeignKey), values)
	if err != nil {
		return nil, err
	}
	ordering := NewAutoField[Source](state.reverse.sourcePrimaryKey).Asc()
	base := newQuerySet(backend, state.reverse.sourceDescriptor, state.reverse.sourcePlan)
	batch := base.
		Filter(predicateFromCondition[Source](inCondition, nil)).
		OrderBy(ordering)
	if batch.configurationErr != nil {
		return nil, batch.configurationErr
	}

	coldSets := make([]QuerySet[Source], len(ownerKeys))
	for index, identifier := range ownerKeys {
		exact := predicateFromCondition[Source](query.NewCondition(
			fieldReference(state.reverse.sourceForeignKey),
			query.LookupExact,
			query.Integer(identifier),
		), nil)
		querySet := base.Filter(exact).OrderBy(ordering)
		if querySet.configurationErr != nil {
			return nil, querySet.configurationErr
		}
		coldSets[index] = querySet
	}

	sources, err := batch.All(ctx)
	if err != nil {
		return nil, err
	}
	groups := make(map[int64][]Source, len(batchKeys))
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		storageInput := state.reverse.sourceDescriptor.CloneModel(source)
		foreignKey, present := state.storage.Value(storageInput)
		if !present {
			return nil, relationInvalidPlan("reverse prefetch source storage could not read the bound ForeignKey")
		}
		identifier, ok := foreignKey.Integer()
		if !ok || foreignKey.IsNull() {
			return nil, relatedSetMembershipError(
				state.reverse.sourceForeignKey,
				"reverse prefetch source returned a NULL or non-integer ForeignKey",
			)
		}
		if _, exists := requested[identifier]; !exists {
			return nil, relatedSetMembershipError(
				state.reverse.sourceForeignKey,
				"reverse prefetch source ForeignKey is outside the requested owner set",
			)
		}
		// All returned owned models, and groups never escape this operation.
		// Clone separately when publishing each owner's independent cache below.
		if state.reverse.sourceForeignKey.Relation.Cardinality == ir.RelationOneToOne && len(groups[identifier]) != 0 {
			return nil, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectCardinality,
				Field: state.reverse.sourceForeignKey.Name, Detail: "one-to-one prefetch returned multiple source rows for one owner"}
		}
		groups[identifier] = append(groups[identifier], source)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := make([]QuerySet[Source], len(coldSets))
	for index, cold := range coldSets {
		state := newEvaluationState[Source]()
		state.values = cold.cloneModels(groups[ownerKeys[index]])
		state.ready = true
		cold.evaluation = state
		result[index] = cold
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (state reversePrefetchState[Owner, Source]) validate() error {
	if !state.valid || interfaceIsNil(state.storage) {
		return relationInvalidPlan("reverse prefetch is unbound")
	}
	if err := state.reverse.validate(); err != nil {
		return err
	}
	// A zero-state storage value cannot change its type, but its methods may
	// consult external state. Keep validating the callback's current result.
	if !reflect.DeepEqual(state.storage.Field(), state.reverse.sourceForeignKey) {
		return relationInvalidPlan("reverse prefetch storage field changed")
	}
	return nil
}

func relatedSetMembershipError(field ir.Field, detail string) *query.Error {
	return &query.Error{
		Category: query.CategoryIntegrity,
		Code:     query.CodeRelatedSetMembership,
		Field:    field.Name,
		Detail:   detail,
	}
}
