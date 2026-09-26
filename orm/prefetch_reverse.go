package orm

import (
	"context"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// ReverseCollectionPrefetch configures one reverse FK collection and its
// target's eager and prefetch descendants in the common materialized graph.
type ReverseCollectionPrefetch[O, T any] struct {
	state            reversePrefetchState[O, T]
	children         []PrefetchSelection[T]
	eager            []RelatedSelection[T]
	plan             query.Plan
	custom           bool
	configurationErr error
}

func (r ReverseObject[O, T]) WithChildren(children ...PrefetchSelection[T]) ReverseCollectionPrefetch[O, T] {
	state, err := bindReversePrefetchState(r.state)
	p := ReverseCollectionPrefetch[O, T]{state: state, configurationErr: err}
	if err == nil {
		q := newQuerySet[T](nil, state.reverse.sourceDescriptor, state.reverse.sourcePlan).OrderBy(NewAutoField[T](state.reverse.sourcePrimaryKey).Asc())
		p.plan, p.configurationErr = q.plan, q.configurationErr
	}
	return p.WithChildren(children...)
}
func (r ReverseObject[O, T]) preparePrefetch(depth int, remaining *int) (preparedPrefetch[O], error) {
	return r.WithChildren().preparePrefetch(depth, remaining)
}
func (p ReverseCollectionPrefetch[O, T]) WithChildren(children ...PrefetchSelection[T]) ReverseCollectionPrefetch[O, T] {
	p.children = append(append([]PrefetchSelection[T](nil), p.children...), children...)
	return p
}
func (p ReverseCollectionPrefetch[O, T]) WithConfigurationError(err error) ReverseCollectionPrefetch[O, T] {
	if p.configurationErr == nil {
		p.configurationErr = err
	}
	return p
}
func (p ReverseCollectionPrefetch[O, T]) targetQuery() QuerySet[T] {
	q := newQuerySet[T](nil, p.state.reverse.sourceDescriptor, p.plan)
	q.configurationErr = p.configurationErr
	return q
}
func (p ReverseCollectionPrefetch[O, T]) withTarget(q QuerySet[T]) ReverseCollectionPrefetch[O, T] {
	p.custom, p.plan = true, q.plan
	return p.WithConfigurationError(q.configurationErr)
}
func (p ReverseCollectionPrefetch[O, T]) Filter(values ...Predicate[T]) ReverseCollectionPrefetch[O, T] {
	return p.withTarget(p.targetQuery().Filter(values...))
}
func (p ReverseCollectionPrefetch[O, T]) OrderBy(values ...Ordering[T]) ReverseCollectionPrefetch[O, T] {
	return p.withTarget(p.targetQuery().OrderBy(values...))
}
func (p ReverseCollectionPrefetch[O, T]) Distinct() ReverseCollectionPrefetch[O, T] {
	return p.withTarget(p.targetQuery().Distinct())
}
func (p ReverseCollectionPrefetch[O, T]) SelectRelated(values ...RelatedSelection[T]) ReverseCollectionPrefetch[O, T] {
	if len(values) == 0 {
		return p.WithConfigurationError(relationInvalidPlan("target eager selection is empty"))
	}
	p.eager = append(append([]RelatedSelection[T](nil), p.eager...), values...)
	return p.withTarget(p.targetQuery())
}

type preparedReverseCollection[O, T any] struct {
	state         reversePrefetchState[O, T]
	children      []preparedPrefetch[T]
	configuration *queryMaterialization[T]
	plan          query.Plan
	custom        bool
	nodes         int
}

func (p ReverseCollectionPrefetch[O, T]) preparePrefetch(depth int, remaining *int) (preparedPrefetch[O], error) {
	if p.configurationErr != nil {
		return nil, p.configurationErr
	}
	if depth > query.MaximumRelationHops || *remaining <= 0 {
		return nil, relationInvalidPlan("prefetch selection exceeds its depth or node bound")
	}
	before := *remaining
	*remaining--
	children, err := preparePrefetchSet(p.children, depth+1, remaining)
	if err != nil {
		return nil, err
	}
	targets, eagerNodes, err := preparePrefetchEager(p.eager, p.state.reverse.source, depth+1, remaining)
	if err != nil {
		return nil, err
	}
	config := &queryMaterialization[T]{binding: p.state.reverse.source, targets: targets, eagerNodes: eagerNodes}
	if p.custom {
		config.selections = children
	}
	result := preparedReverseCollection[O, T]{state: p.state, children: children, configuration: config, plan: p.plan, custom: p.custom, nodes: before - *remaining}
	if err := result.validate(); err != nil {
		return nil, err
	}
	return result, nil
}
func (p preparedReverseCollection[O, T]) owner() BoundModel[O] { return p.state.reverse.owner }
func (p preparedReverseCollection[O, T]) name() string {
	return p.state.reverse.sourceForeignKey.Relation.Reverse.Name
}
func (p preparedReverseCollection[O, T]) nodeBudget() int { return p.nodes }
func (p preparedReverseCollection[O, T]) validate() error {
	if err := p.state.validate(); err != nil {
		return err
	}
	if _, ok := p.state.reverse.sourceDescriptor.(PrimaryKeyObjectDescriptor[T]); !ok {
		return relationInvalidPlan("reverse prefetch target has no primary-key descriptor")
	}
	if _, err := projectionDescriptorFor(p.state.reverse.source); err != nil {
		return err
	}
	if err := p.plan.ValidateOrderings(); err != nil {
		return err
	}
	if err := validatePrefetchEager(p.configuration.targets, p.state.reverse.source); err != nil {
		return err
	}
	for _, child := range p.children {
		if err := child.validate(); err != nil {
			return err
		}
		owner := child.owner()
		if owner.snapshot != p.state.reverse.source.snapshot || owner.identity != p.state.reverse.source.identity {
			return relationInvalidPlan("reverse prefetch child belongs to another target binding")
		}
	}
	return nil
}
func (p preparedReverseCollection[O, T]) merge(other preparedPrefetch[O]) (preparedPrefetch[O], error) {
	v, ok := other.(preparedReverseCollection[O, T])
	if !ok || p.owner().snapshot != v.owner().snapshot || p.owner().identity != v.owner().identity || p.state.reverse.source.identity != v.state.reverse.source.identity || p.name() != v.name() {
		return nil, relationInvalidPlan("repeated reverse prefetch has conflicting binding metadata")
	}
	if v.custom {
		return nil, relationInvalidPlan("prefetch target query redefines an already selected lookup")
	}
	children, err := mergePrefetchSet(append(append([]preparedPrefetch[T](nil), p.children...), v.children...))
	if err != nil {
		return nil, err
	}
	p.children, p.nodes = children, p.nodes+v.nodes
	return p, nil
}
func (p preparedReverseCollection[O, T]) apply(ctx context.Context, backend db.Queryer, owners []relatedSelectedValue[O]) error {
	if err := p.validate(); err != nil {
		return err
	}
	if err := validateQuerySession(ctx, backend); err != nil {
		return err
	}
	keys := make([]int64, len(owners))
	requested := map[int64]struct{}{}
	for i, value := range owners {
		key, err := manyObjectKey(p.state.reverse.ownerDescriptor, value.source)
		if err != nil {
			return err
		}
		keys[i] = key
		requested[key] = struct{}{}
	}
	ordered := make([]int64, 0, len(requested))
	for key := range requested {
		ordered = append(ordered, key)
	}
	slices.Sort(ordered)
	groups := make(map[int64][]relatedSelectedValue[T], len(ordered))
	descriptor := p.state.reverse.sourceDescriptor.(PrimaryKeyObjectDescriptor[T])
	for start := 0; start < len(ordered); start += manyPrefetchBatchSize {
		batch := ordered[start:min(start+manyPrefetchBatchSize, len(ordered))]
		args := make([]query.Value, len(batch))
		for i, key := range batch {
			args[i] = query.Integer(key)
		}
		condition, err := query.NewInCondition(fieldReference(p.state.reverse.sourceForeignKey), args)
		if err != nil {
			return err
		}
		q := newQuerySet(backend, p.state.reverse.sourceDescriptor, p.plan).Filter(predicateFromCondition[T](condition, nil))
		q.materialization = &queryMaterialization[T]{binding: p.state.reverse.source, targets: p.configuration.targets, eagerNodes: p.configuration.eagerNodes}
		if err := q.validateTerminal(ctx); err != nil {
			return err
		}
		raw, attachment, err := q.evaluateModels(ctx)
		if err != nil {
			return err
		}
		values, err := materializationValues(p.state.reverse.source, raw, attachment)
		if err != nil {
			return err
		}
		for _, value := range values {
			if _, err := manyObjectKey(descriptor, value.source); err != nil {
				return err
			}
			key, present := p.state.storage.Value(value.source)
			cloned, clonePresent := p.state.storage.Value(descriptor.CloneModel(value.source))
			owner, integer := key.Integer()
			_, inBatch := slices.BinarySearch(batch, owner)
			if !present || !clonePresent || !key.Equal(cloned) || !integer || key.IsNull() || !inBatch {
				return relatedSetMembershipError(p.state.reverse.sourceForeignKey, "reverse prefetch target is outside its requested batch or changed by cloning")
			}
			groups[owner] = append(groups[owner], value)
		}
	}
	// Apply child batches across all owners, after every target rowset is closed.
	flattened := make([]relatedSelectedValue[T], 0)
	ends := make([]int, len(owners))
	for i, key := range keys {
		flattened = append(flattened, copyRelatedValues(groups[key])...)
		ends[i] = len(flattened)
	}
	if err := loadPrefetchValues(ctx, backend, flattened, p.children); err != nil {
		return err
	}
	start := 0
	for i, owner := range owners {
		defaults, err := p.state.reverse.from(backend, owner.source)
		if err != nil {
			return err
		}
		condition := query.NewCondition(fieldReference(p.state.reverse.sourceForeignKey), query.LookupExact, query.Integer(keys[i]))
		q := newQuerySet(backend, p.state.reverse.sourceDescriptor, p.plan).Filter(predicateFromCondition[T](condition, nil))
		if q.configurationErr != nil {
			return q.configurationErr
		}
		if p.custom {
			q.materialization = p.configuration
		}
		graph := flattened[start:ends[i]:ends[i]]
		start = ends[i]
		q.evaluation.values = make([]T, len(graph))
		for j, value := range graph {
			q.evaluation.values[j] = descriptor.CloneModel(value.source)
		}
		q.evaluation.attachment = &materializedRows[T]{binding: p.state.reverse.source, values: graph}
		q.evaluation.ready = true
		related := newRelatedSet(q)
		related.basePlan = defaults.plan
		if owners[i].collections == nil {
			owners[i].collections = map[string]cachedPrefetch{}
		}
		owners[i].collections[p.name()] = cachedReversePrefetch[T]{set: related}
	}
	_, err := sessionReadResult(ctx, backend, struct{}{}, ctx.Err())
	return err
}

type cachedReversePrefetch[T any] struct{ set *RelatedSet[T] }

func (c cachedReversePrefetch[T]) clone() (cachedPrefetch, error) {
	q, err := c.set.Query()
	if err != nil {
		return nil, err
	}
	copy := newRelatedSet(q)
	copy.basePlan = c.set.basePlan
	return cachedReversePrefetch[T]{set: copy}, nil
}
func (r ReverseObject[O, T]) FromPrefetched(owner *RelatedSelected[O]) (*RelatedSet[T], bool, error) {
	if err := r.state.validate(); err != nil {
		return nil, false, err
	}
	if err := owner.validate(); err != nil {
		return nil, false, err
	}
	if r.state.owner.snapshot != owner.binding.snapshot || r.state.owner.identity != owner.binding.identity {
		return nil, false, relationInvalidPlan("prefetched owner belongs to another reverse binding")
	}
	cache, present := owner.collections[r.state.sourceForeignKey.Relation.Reverse.Name]
	if !present {
		return nil, false, nil
	}
	cloned, err := cache.clone()
	if err != nil {
		return nil, false, err
	}
	typed, ok := cloned.(cachedReversePrefetch[T])
	if !ok {
		return nil, false, relationInvalidPlan("prefetched reverse collection has another target type")
	}
	return typed.set, true, nil
}
