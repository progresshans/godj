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
	reverse          reverseObjectState[Owner, Source]
	storage          RelationStorage[Source]
	ownerPrimaryKey  ir.Field
	sourceForeignKey ir.Field
	sourcePrimaryKey ir.Field
	valid            bool
	ownerMarker      [0]func(Owner)
	sourceMarker     [0]func(Source)
}

// BindReversePrefetch adds the source ForeignKey storage capability required
// to group one batch result to the owners of an already bound ReverseObject.
func BindReversePrefetch[Owner, Source any](
	reverse ReverseObject[Owner, Source],
) (ReversePrefetch[Owner, Source], error) {
	if err := reverse.state.validate(); err != nil {
		return ReversePrefetch[Owner, Source]{}, err
	}
	storage, ok := reverse.state.sourceDescriptor.BindRelationStorage(reverse.state.sourceForeignKey.Clone())
	if !ok || interfaceIsNil(storage) {
		return ReversePrefetch[Owner, Source]{}, relationInvalidPlan("reverse prefetch source ForeignKey storage is unavailable")
	}
	if !immutableZeroStateValue(storage) {
		return ReversePrefetch[Owner, Source]{}, relationInvalidPlan("reverse prefetch source ForeignKey storage must be a named non-pointer zero-size struct")
	}
	if !reflect.DeepEqual(storage.Field(), reverse.state.sourceForeignKey) {
		return ReversePrefetch[Owner, Source]{}, relationInvalidPlan("reverse prefetch source ForeignKey storage is not canonical")
	}

	reverseState := reverse.state
	reverseState.ownerPrimaryKey = reverse.state.ownerPrimaryKey.Clone()
	reverseState.sourceForeignKey = reverse.state.sourceForeignKey.Clone()
	reverseState.sourcePrimaryKey = reverse.state.sourcePrimaryKey.Clone()
	state := reversePrefetchState[Owner, Source]{
		reverse:          reverseState,
		storage:          storage,
		ownerPrimaryKey:  reverse.state.ownerPrimaryKey.Clone(),
		sourceForeignKey: reverse.state.sourceForeignKey.Clone(),
		sourcePrimaryKey: reverse.state.sourcePrimaryKey.Clone(),
		valid:            true,
	}
	return ReversePrefetch[Owner, Source]{state: state}, nil
}

// Load evaluates exactly one source batch query, validates every returned
// source membership, and returns ready RelatedSet values only after the entire
// operation succeeds.
func (p ReversePrefetch[Owner, Source]) Load(
	ctx context.Context,
	backend db.Queryer,
	owners []Owner,
) ([]*RelatedSet[Source], error) {
	if interfaceIsNil(ctx) {
		return nil, relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if interfaceIsNil(backend) {
		return nil, relationBackendInvalidPlan("backend is nil")
	}
	if err := p.state.validate(); err != nil {
		return nil, err
	}
	if len(owners) == 0 {
		return make([]*RelatedSet[Source], 0), nil
	}

	ownerSnapshots := make([]Owner, len(owners))
	for index := range owners {
		ownerSnapshots[index] = p.state.reverse.ownerDescriptor.CloneModel(owners[index])
	}
	ownerKeys := make([]int64, len(ownerSnapshots))
	for index := range ownerSnapshots {
		primaryKey, present := p.state.reverse.ownerDescriptor.PrimaryKey(ownerSnapshots[index])
		if !present {
			return nil, &query.Error{
				Category: query.CategoryQuery,
				Code:     query.CodeMissingPrimaryKey,
				Field:    p.state.ownerPrimaryKey.Name,
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
	inCondition, err := query.NewInCondition(fieldReference(p.state.sourceForeignKey), values)
	if err != nil {
		return nil, err
	}
	ordering := NewIntegerField[Source](p.state.sourcePrimaryKey).Asc()
	base := NewManager[Source](p.state.reverse.sourceDescriptor).Using(backend)
	batch := base.
		Filter(Predicate[Source]{condition: inCondition}).
		OrderBy(ordering)
	if batch.configurationErr != nil {
		return nil, batch.configurationErr
	}

	coldSets := make([]*RelatedSet[Source], len(ownerKeys))
	for index, identifier := range ownerKeys {
		exact := Predicate[Source]{condition: query.NewCondition(
			fieldReference(p.state.sourceForeignKey),
			query.LookupExact,
			query.Integer(identifier),
		)}
		querySet := base.Filter(exact).OrderBy(ordering)
		if querySet.configurationErr != nil {
			return nil, querySet.configurationErr
		}
		coldSets[index] = newRelatedSet(querySet)
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
		storageInput := p.state.reverse.sourceDescriptor.CloneModel(source)
		foreignKey, present := p.state.storage.Value(storageInput)
		if !present {
			return nil, relationInvalidPlan("reverse prefetch source storage could not read the bound ForeignKey")
		}
		identifier, ok := foreignKey.Integer()
		if !ok || foreignKey.IsNull() {
			return nil, relatedSetMembershipError(
				p.state.sourceForeignKey,
				"reverse prefetch source returned a NULL or non-integer ForeignKey",
			)
		}
		if _, exists := requested[identifier]; !exists {
			return nil, relatedSetMembershipError(
				p.state.sourceForeignKey,
				"reverse prefetch source ForeignKey is outside the requested owner set",
			)
		}
		groups[identifier] = append(
			groups[identifier],
			p.state.reverse.sourceDescriptor.CloneModel(source),
		)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	result := make([]*RelatedSet[Source], len(coldSets))
	for index, cold := range coldSets {
		state := newEvaluationState[Source]()
		state.values = cold.querySet.cloneModels(groups[ownerKeys[index]])
		state.ready = true
		cold.querySet.evaluation = state
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
	if !immutableZeroStateValue(state.storage) {
		return relationInvalidPlan("reverse prefetch storage is not an immutable zero-state value")
	}
	if !reflect.DeepEqual(state.ownerPrimaryKey, state.reverse.ownerPrimaryKey) ||
		!reflect.DeepEqual(state.sourceForeignKey, state.reverse.sourceForeignKey) ||
		!reflect.DeepEqual(state.sourcePrimaryKey, state.reverse.sourcePrimaryKey) {
		return relationInvalidPlan("reverse prefetch relation fields changed")
	}
	sourceForeignKey, ok := findField(
		state.reverse.relation.forward.sourceModel.Fields,
		state.reverse.relation.forward.metadata.Field,
	)
	if !ok || !reflect.DeepEqual(sourceForeignKey, state.sourceForeignKey) {
		return relationInvalidPlan("reverse prefetch source ForeignKey changed")
	}
	sourcePrimaryKey, ok := relationAutoPrimaryKey(state.reverse.relation.forward.sourceModel)
	if !ok || !reflect.DeepEqual(sourcePrimaryKey, state.sourcePrimaryKey) {
		return relationInvalidPlan("reverse prefetch source primary key changed")
	}
	if !reflect.DeepEqual(state.storage.Field(), state.sourceForeignKey) {
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
