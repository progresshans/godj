package orm

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// PrefetchSelection is a closed, owner-typed batch loading capability.
// Bound ManyToMany factories implement it in either traversal direction.
type PrefetchSelection[S any] interface {
	preparePrefetch(int, *int) (preparedPrefetch[S], error)
}
type preparedPrefetch[S any] interface {
	owner() BoundModel[S]
	name() string
	nodeBudget() int
	validate() error
	merge(preparedPrefetch[S]) (preparedPrefetch[S], error)
	load(context.Context, db.Queryer, []S) ([]cachedPrefetch, error)
}
type cachedPrefetch interface {
	clone() (cachedPrefetch, error)
}

// ManyPrefetch describes an immutable collection loading tree. Children are
// typed to the target model and loaded across the complete parent batch.
type ManyPrefetch[O, T, L any] struct {
	relation         ManyToMany[O, T, L]
	children         []PrefetchSelection[T]
	targetPlan       query.Plan
	custom           bool
	configurationErr error
}

func (r ManyToMany[O, T, L]) WithChildren(children ...PrefetchSelection[T]) ManyPrefetch[O, T, L] {
	return (ManyPrefetch[O, T, L]{relation: r}).WithChildren(children...)
}
func (p ManyPrefetch[O, T, L]) WithChildren(children ...PrefetchSelection[T]) ManyPrefetch[O, T, L] {
	p.children = append(append([]PrefetchSelection[T](nil), p.children...), children...)
	return p
}
func (p ManyPrefetch[O, T, L]) WithConfigurationError(err error) ManyPrefetch[O, T, L] {
	if p.configurationErr == nil {
		p.configurationErr = err
	}
	return p
}

func (p ManyPrefetch[O, T, L]) targetQuery() QuerySet[T] {
	plan := p.relation.plan
	if p.custom {
		plan = p.targetPlan
	}
	q := newQuerySet[T](nil, p.relation.target, plan)
	q.configurationErr = p.configurationErr
	return q
}
func (p ManyPrefetch[O, T, L]) withTarget(q QuerySet[T]) ManyPrefetch[O, T, L] {
	p.custom, p.targetPlan = true, q.plan
	return p.WithConfigurationError(q.configurationErr)
}

// Filter configures the target query, preserving the scope of each Filter call.
// An explicit target query cannot redefine a previously selected lookup.
func (p ManyPrefetch[O, T, L]) Filter(values ...Predicate[T]) ManyPrefetch[O, T, L] {
	return p.withTarget(p.targetQuery().Filter(values...))
}
func (p ManyPrefetch[O, T, L]) OrderBy(values ...Ordering[T]) ManyPrefetch[O, T, L] {
	return p.withTarget(p.targetQuery().OrderBy(values...))
}
func (p ManyPrefetch[O, T, L]) Distinct() ManyPrefetch[O, T, L] {
	return p.withTarget(p.targetQuery().Distinct())
}

type preparedManyPrefetch[S, T, L any] struct {
	relation      ManyToMany[S, T, L]
	children      []preparedPrefetch[T]
	queryChildren []preparedPrefetch[T]
	targetPlan    query.Plan
	custom        bool
	nodes         int
}
type cachedManyPrefetch[T, L any] struct{ collection *ManyCollection[T, L] }

func (r ManyToMany[O, T, L]) preparePrefetch(depth int, remaining *int) (preparedPrefetch[O], error) {
	return r.WithChildren().preparePrefetch(depth, remaining)
}
func (p ManyPrefetch[O, T, L]) preparePrefetch(depth int, remaining *int) (preparedPrefetch[O], error) {
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
	prepared := preparedManyPrefetch[O, T, L]{relation: p.relation, children: children, targetPlan: p.targetPlan, custom: p.custom, nodes: before - *remaining}
	if p.custom {
		prepared.queryChildren = children
	}
	if err := prepared.validate(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func preparePrefetchSet[S any](selections []PrefetchSelection[S], depth int, remaining *int) ([]preparedPrefetch[S], error) {
	if len(selections) > *remaining {
		return nil, relationInvalidPlan("prefetch selection exceeds its node bound")
	}
	prepared := make([]preparedPrefetch[S], 0, len(selections))
	for _, selection := range selections {
		if interfaceIsNil(selection) {
			return nil, relationInvalidPlan("prefetch selection is nil")
		}
		value, err := selection.preparePrefetch(depth, remaining)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, value)
	}
	return mergePrefetchSet(prepared)
}
func mergePrefetchSet[S any](selections []preparedPrefetch[S]) ([]preparedPrefetch[S], error) {
	result := make([]preparedPrefetch[S], 0, len(selections))
	seen := make(map[string]int, len(selections))
	for _, selection := range selections {
		if index, exists := seen[selection.name()]; exists {
			merged, err := result[index].merge(selection)
			if err != nil {
				return nil, err
			}
			result[index] = merged
		} else {
			seen[selection.name()] = len(result)
			result = append(result, selection)
		}
	}
	return result, nil
}
func (p preparedManyPrefetch[S, T, L]) owner() BoundModel[S] { return p.relation.prefetchOwner }
func (p preparedManyPrefetch[S, T, L]) name() string         { return p.relation.prefetchName }
func (p preparedManyPrefetch[S, T, L]) nodeBudget() int      { return p.nodes }
func (p preparedManyPrefetch[S, T, L]) validate() error {
	if p.relation.state == nil || p.name() == "" {
		return relationInvalidPlan("prefetch selection is unbound")
	}
	if err := validateObjectBoundModel(p.owner()); err != nil {
		return err
	}
	if _, _, err := p.relation.prefetchBinding(); err != nil {
		return err
	}
	if p.custom {
		if err := p.targetPlan.ValidateOrderings(); err != nil {
			return err
		}
		path, err := p.relation.prefetchOwnerPath()
		if err != nil {
			return err
		}
		if _, err := p.targetPlan.ForPrefetchOwners(path, nil); err != nil {
			return err
		}
		if _, err := projectionDescriptorFor(p.relation.prefetchTarget); err != nil {
			return err
		}
	}
	for _, child := range p.children {
		if err := child.validate(); err != nil {
			return err
		}
		owner := child.owner()
		if owner.snapshot != p.relation.prefetchTarget.snapshot || owner.identity != p.relation.prefetchTarget.identity {
			return relationInvalidPlan("prefetch child belongs to a different target or project binding")
		}
	}
	return nil
}
func (p preparedManyPrefetch[S, T, L]) merge(other preparedPrefetch[S]) (preparedPrefetch[S], error) {
	value, ok := other.(preparedManyPrefetch[S, T, L])
	if !ok || p.owner().snapshot != value.owner().snapshot || p.owner().identity != value.owner().identity ||
		p.relation.prefetchTarget.identity != value.relation.prefetchTarget.identity || p.relation.prefetchSource.identity != value.relation.prefetchSource.identity ||
		!p.relation.path.Equal(value.relation.path) {
		return nil, relationInvalidPlan("repeated prefetch has conflicting binding metadata")
	}
	if value.custom {
		return nil, relationInvalidPlan("prefetch target query redefines an already selected lookup")
	}
	children, err := mergePrefetchSet(append(append([]preparedPrefetch[T](nil), p.children...), value.children...))
	if err != nil {
		return nil, err
	}
	p.children = children
	p.nodes += value.nodes
	return p, nil
}
func (p preparedManyPrefetch[S, T, L]) load(ctx context.Context, backend db.Queryer, owners []S) ([]cachedPrefetch, error) {
	_, borrowed := backend.(db.SessionValidator)
	var collections []*ManyCollection[T, L]
	var err error
	if p.custom {
		collections, err = p.loadCustom(ctx, backend, owners, borrowed)
	} else {
		collections, err = p.relation.prefetch(ctx, backend, owners, borrowed)
	}
	if err != nil {
		return nil, err
	}
	if len(p.children) > 0 {
		values := make([]relatedSelectedValue[T], 0)
		ends := make([]int, len(collections))
		for i, collection := range collections {
			raw, ready := collection.querySet.evaluation.cachedValues()
			if !ready {
				return nil, relationInvalidPlan("prefetch target cache is not ready")
			}
			for _, value := range raw {
				values = append(values, relatedSelectedValue[T]{source: value})
			}
			ends[i] = len(values)
		}
		if err := loadPrefetchValues(ctx, backend, values, p.children); err != nil {
			return nil, err
		}
		start := 0
		for i, collection := range collections {
			// These handles are still private. Their immutable graph is completed
			// before the outer query publishes any of the cached collections.
			collection.querySet.evaluation.attachment = &materializedRows[T]{binding: p.relation.prefetchTarget, values: values[start:ends[i]:ends[i]]}
			start = ends[i]
		}
	}
	result := make([]cachedPrefetch, len(collections))
	for i, collection := range collections {
		result[i] = cachedManyPrefetch[T, L]{collection: collection}
	}
	return result, nil
}

func loadPrefetchValues[S any](ctx context.Context, backend db.Queryer, values []relatedSelectedValue[S], selections []preparedPrefetch[S]) error {
	owners := make([]S, len(values))
	for i, value := range values {
		owners[i] = value.source
	}
	for _, selection := range selections {
		caches, err := selection.load(ctx, backend, owners)
		if err != nil {
			return err
		}
		if len(caches) != len(values) {
			return relationInvalidPlan("prefetch batch changed owner cardinality")
		}
		for i, cache := range caches {
			if values[i].collections == nil {
				values[i].collections = make(map[string]cachedPrefetch, len(selections))
			}
			values[i].collections[selection.name()] = cache
		}
	}
	return ctx.Err()
}
func (c cachedManyPrefetch[T, L]) clone() (cachedPrefetch, error) {
	set, err := c.collection.Query()
	if err != nil {
		return nil, err
	}
	original := c.collection
	copy := &ManyCollection[T, L]{querySet: set, basePlan: original.basePlan, target: original.target, through: original.through, state: original.state, backend: original.backend, session: original.session, ownerKey: original.ownerKey}
	copy._self = copy
	// Evaluation values are immutable and terminals clone them. Mutation swaps
	// only this handle's evaluation pointer, preserving the held snapshot.
	return cachedManyPrefetch[T, L]{collection: copy}, nil
}

// PrefetchQuery owns one evaluation of the owner query and all selected
// collection batches. Failed attempts publish neither owners nor partial caches.
type PrefetchQuery[S any] struct {
	source           QuerySet[S]
	binding          BoundModel[S]
	selections       []preparedPrefetch[S]
	evaluation       *evaluationState[relatedSelectedValue[S]]
	configurationErr error
}

func PrefetchRelated[S any](source QuerySet[S], selections ...PrefetchSelection[S]) PrefetchQuery[S] {
	q := PrefetchQuery[S]{source: source, evaluation: newEvaluationState[relatedSelectedValue[S]]()}
	q.source.plan = q.source.plan.WithoutCollectionFilterReuse()
	q.configurationErr = source.configurationErr
	if q.configurationErr != nil {
		return q
	}
	if len(selections) == 0 || len(selections) > MaximumRelatedSelectionNodes {
		return q.WithConfigurationError(relationInvalidPlan("prefetch requires between 1 and 1024 selections"))
	}
	remaining := MaximumRelatedSelectionNodes
	if source.materialization != nil {
		remaining -= source.materialization.nodeBudget()
	}
	var err error
	q.selections, err = preparePrefetchSet(selections, 1, &remaining)
	if err != nil {
		return q.WithConfigurationError(err)
	}
	q.binding = q.selections[0].owner()
	if source.materialization != nil {
		if source.materialization.binding.snapshot != q.binding.snapshot || source.materialization.binding.identity != q.binding.identity {
			return q.WithConfigurationError(relationInvalidPlan("prefetch query configuration belongs to another source binding"))
		}
		combined := append(append([]preparedPrefetch[S](nil), source.materialization.selections...), q.selections...)
		q.selections, err = mergePrefetchSet(combined)
		if err != nil {
			return q.WithConfigurationError(err)
		}
	}
	for _, prepared := range q.selections {
		owner := prepared.owner()
		if owner.snapshot != q.binding.snapshot || owner.identity != q.binding.identity {
			return q.WithConfigurationError(relationInvalidPlan("prefetch selections belong to different project owners"))
		}
	}
	if source.evaluation == nil || descriptorIsNil(source.descriptor) || reflect.TypeOf(source.descriptor) != reflect.TypeOf(q.binding.objectDescriptor) || !reflect.DeepEqual(source.descriptor.Metadata(), q.binding.model) || source.plan.Table() != q.binding.model.DBTable || !reflect.DeepEqual(source.plan.SourceFields(), modelFieldReferences(q.binding.model)) || len(source.plan.RelationProjections()) != 0 {
		return q.WithConfigurationError(relationInvalidPlan("prefetch source does not match its owner binding"))
	}
	return q
}
func (q PrefetchQuery[S]) WithConfigurationError(err error) PrefetchQuery[S] {
	if q.configurationErr == nil {
		q.configurationErr = err
	}
	return q
}
func (q PrefetchQuery[S]) ConfigurationError() error { return q.configurationErr }
func (q PrefetchQuery[S]) Plan() query.Plan          { return q.source.plan }
func (q PrefetchQuery[S]) derive(source QuerySet[S]) PrefetchQuery[S] {
	q.source = source
	q.evaluation = newEvaluationState[relatedSelectedValue[S]]()
	return q
}
func (q PrefetchQuery[S]) Filter(values ...Predicate[S]) PrefetchQuery[S] {
	return q.derive(q.source.Filter(values...))
}
func (q PrefetchQuery[S]) OrderBy(values ...Ordering[S]) PrefetchQuery[S] {
	return q.derive(q.source.OrderBy(values...))
}
func (q PrefetchQuery[S]) Distinct() PrefetchQuery[S] { return q.derive(q.source.Distinct()) }
func (q PrefetchQuery[S]) Fresh() PrefetchQuery[S]    { return q.derive(q.source.Fresh()) }
func (q PrefetchQuery[S]) Limit(value int) (PrefetchQuery[S], error) {
	source, err := q.source.Limit(value)
	if err != nil {
		return PrefetchQuery[S]{}, err
	}
	return q.derive(source), nil
}
func (q PrefetchQuery[S]) Offset(value int) (PrefetchQuery[S], error) {
	source, err := q.source.Offset(value)
	if err != nil {
		return PrefetchQuery[S]{}, err
	}
	return q.derive(source), nil
}
func (q PrefetchQuery[S]) validate(ctx context.Context) error {
	if interfaceIsNil(ctx) {
		return relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if q.configurationErr != nil {
		return q.configurationErr
	}
	if q.evaluation == nil || len(q.selections) == 0 {
		return relationInvalidPlan("prefetch query is unbound")
	}
	if err := q.source.validateTerminal(ctx); err != nil {
		return err
	}
	if err := validateObjectBoundModel(q.binding); err != nil {
		return err
	}
	for _, selection := range q.selections {
		if err := selection.validate(); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (q PrefetchQuery[S]) load(ctx context.Context, plan query.Plan) ([]relatedSelectedValue[S], error) {
	owners, err := newQuerySet(q.source.backend, q.source.descriptor, plan).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]relatedSelectedValue[S], len(owners))
	for i, owner := range owners {
		result[i] = relatedSelectedValue[S]{source: owner, collections: make(map[string]cachedPrefetch, len(q.selections))}
	}
	if err := loadPrefetchValues(ctx, q.source.backend, result, q.selections); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, q.source.backend, result, nil)
}

func (q PrefetchQuery[S]) clone(value relatedSelectedValue[S]) (*RelatedSelected[S], error) {
	return cloneRelatedSelection(q.source.backend, q.binding, q.source.descriptor, value)
}

func (r ManyToMany[O, T, L]) FromPrefetched(owner *RelatedSelected[O]) (*ManyCollection[T, L], bool, error) {
	if err := owner.validate(); err != nil {
		return nil, false, err
	}
	if r.state == nil || r.prefetchOwner.snapshot != owner.binding.snapshot || r.prefetchOwner.identity != owner.binding.identity {
		return nil, false, relationInvalidPlan("prefetched owner belongs to another collection binding")
	}
	cache, found := owner.collections[r.prefetchName]
	if !found {
		return nil, false, nil
	}
	cloned, err := cache.clone()
	if err != nil {
		return nil, false, err
	}
	typed, ok := cloned.(cachedManyPrefetch[T, L])
	if !ok {
		return nil, false, relationInvalidPlan("prefetched collection has a different target or intermediary type")
	}
	return typed.collection, true, nil
}
func (q PrefetchQuery[S]) All(ctx context.Context) ([]*RelatedSelected[S], error) {
	if err := q.validate(ctx); err != nil {
		return nil, err
	}
	values, err := q.evaluation.evaluate(ctx, func(ctx context.Context) ([]relatedSelectedValue[S], error) { return q.load(ctx, q.source.plan) })
	if err != nil {
		return nil, err
	}
	result := make([]*RelatedSelected[S], len(values))
	for i, value := range values {
		result[i], err = q.clone(value)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, q.source.backend, result, nil)
}
func (q PrefetchQuery[S]) First(ctx context.Context) (*RelatedSelected[S], bool, error) {
	if err := q.validate(ctx); err != nil {
		return nil, false, err
	}
	if len(q.source.plan.Orderings()) == 0 {
		return nil, false, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnorderedQuery, Detail: "First requires an explicit ordering"}
	}
	values, ready := q.evaluation.cachedValues()
	if !ready {
		var err error
		values, err = q.load(ctx, planWithMaximumRows(q.source.plan, 1))
		if err != nil {
			return nil, false, err
		}
	}
	if len(values) == 0 {
		_, err := sessionReadResult(ctx, q.source.backend, struct{}{}, ctx.Err())
		return nil, false, err
	}
	result, err := q.clone(values[0])
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	result, err = sessionReadResult(ctx, q.source.backend, result, nil)
	return result, err == nil, err
}
func (q PrefetchQuery[S]) Count(ctx context.Context) (int64, error) {
	if err := q.validate(ctx); err != nil {
		return 0, err
	}
	if values, ready := q.evaluation.cachedValues(); ready {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return sessionReadResult(ctx, q.source.backend, int64(len(values)), nil)
	}
	return newQuerySet(q.source.backend, q.source.descriptor, q.source.plan).Count(ctx)
}
