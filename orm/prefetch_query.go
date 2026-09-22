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
	preparePrefetch() (preparedPrefetch[S], error)
}
type preparedPrefetch[S any] interface {
	owner() BoundModel[S]
	name() string
	validate() error
	load(context.Context, db.Queryer, []S) ([]cachedPrefetch, error)
}
type cachedPrefetch interface {
	clone() (cachedPrefetch, error)
}
type preparedManyPrefetch[S, T, L any] struct{ relation ManyToMany[S, T, L] }
type cachedManyPrefetch[T, L any] struct{ collection *ManyCollection[T, L] }

func (r ManyToMany[O, T, L]) preparePrefetch() (preparedPrefetch[O], error) {
	prepared := preparedManyPrefetch[O, T, L]{relation: r}
	if err := prepared.validate(); err != nil {
		return nil, err
	}
	return prepared, nil
}
func (p preparedManyPrefetch[S, T, L]) owner() BoundModel[S] { return p.relation.prefetchOwner }
func (p preparedManyPrefetch[S, T, L]) name() string         { return p.relation.prefetchName }
func (p preparedManyPrefetch[S, T, L]) validate() error {
	if p.relation.state == nil || p.name() == "" {
		return relationInvalidPlan("prefetch selection is unbound")
	}
	if err := validateObjectBoundModel(p.owner()); err != nil {
		return err
	}
	_, _, err := p.relation.prefetchBinding()
	return err
}
func (p preparedManyPrefetch[S, T, L]) load(ctx context.Context, backend db.Queryer, owners []S) ([]cachedPrefetch, error) {
	_, borrowed := backend.(db.SessionValidator)
	collections, err := p.relation.prefetch(ctx, backend, owners, borrowed)
	if err != nil {
		return nil, err
	}
	result := make([]cachedPrefetch, len(collections))
	for i, collection := range collections {
		result[i] = cachedManyPrefetch[T, L]{collection: collection}
	}
	return result, nil
}
func (c cachedManyPrefetch[T, L]) clone() (cachedPrefetch, error) {
	set, err := c.collection.Query()
	if err != nil {
		return nil, err
	}
	original := c.collection
	copy := &ManyCollection[T, L]{querySet: set, target: original.target, through: original.through, state: original.state, backend: original.backend, session: original.session, ownerKey: original.ownerKey}
	copy._self = copy
	// Evaluation values are immutable and terminals clone them. Mutation swaps
	// only this handle's evaluation pointer, preserving the held snapshot.
	return cachedManyPrefetch[T, L]{collection: copy}, nil
}

type prefetchedValue[S any] struct {
	source      S
	collections map[string]cachedPrefetch
}

// PrefetchQuery owns one evaluation of the owner query and all selected
// collection batches. Failed attempts publish neither owners nor partial caches.
type PrefetchQuery[S any] struct {
	source           QuerySet[S]
	binding          BoundModel[S]
	selections       []preparedPrefetch[S]
	evaluation       *evaluationState[prefetchedValue[S]]
	configurationErr error
}

func PrefetchRelated[S any](source QuerySet[S], selections ...PrefetchSelection[S]) PrefetchQuery[S] {
	q := PrefetchQuery[S]{source: source, evaluation: newEvaluationState[prefetchedValue[S]]()}
	q.source.plan = q.source.plan.WithoutCollectionFilterReuse()
	q.configurationErr = source.configurationErr
	if q.configurationErr != nil {
		return q
	}
	if len(selections) == 0 || len(selections) > MaximumRelatedSelectionNodes {
		return q.WithConfigurationError(relationInvalidPlan("prefetch requires between 1 and 1024 selections"))
	}
	seen := make(map[string]bool, len(selections))
	for _, selection := range selections {
		if interfaceIsNil(selection) {
			return q.WithConfigurationError(relationInvalidPlan("prefetch selection is nil"))
		}
		prepared, err := selection.preparePrefetch()
		if err != nil {
			return q.WithConfigurationError(err)
		}
		if len(q.selections) == 0 {
			q.binding = prepared.owner()
		}
		owner := prepared.owner()
		if owner.snapshot != q.binding.snapshot || owner.identity != q.binding.identity {
			return q.WithConfigurationError(relationInvalidPlan("prefetch selections belong to different project owners"))
		}
		if !seen[prepared.name()] {
			q.selections = append(q.selections, prepared)
			seen[prepared.name()] = true
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
	q.evaluation = newEvaluationState[prefetchedValue[S]]()
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
func (q PrefetchQuery[S]) load(ctx context.Context, plan query.Plan) ([]prefetchedValue[S], error) {
	owners, err := newQuerySet(q.source.backend, q.source.descriptor, plan).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]prefetchedValue[S], len(owners))
	for i, owner := range owners {
		result[i] = prefetchedValue[S]{source: owner, collections: make(map[string]cachedPrefetch, len(q.selections))}
	}
	for _, selection := range q.selections {
		caches, err := selection.load(ctx, q.source.backend, owners)
		if err != nil {
			return nil, err
		}
		if len(caches) != len(owners) {
			return nil, relationInvalidPlan("prefetch batch changed owner cardinality")
		}
		for i, cache := range caches {
			result[i].collections[selection.name()] = cache
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, q.source.backend, result, nil)
}

// Prefetched retains an owned source and selected caches. Every query terminal
// and FromPrefetched call materializes independent mutable collection handles.
type Prefetched[S any] struct {
	value      prefetchedValue[S]
	binding    BoundModel[S]
	descriptor ModelDescriptor[S]
	backend    db.Queryer
	_self      *Prefetched[S]
}

func (q PrefetchQuery[S]) clone(value prefetchedValue[S]) (*Prefetched[S], error) {
	copy := &Prefetched[S]{value: prefetchedValue[S]{source: q.source.descriptor.CloneModel(value.source), collections: make(map[string]cachedPrefetch, len(value.collections))}, binding: q.binding, descriptor: q.source.descriptor, backend: q.source.backend}
	copy._self = copy
	for name, cache := range value.collections {
		cloned, err := cache.clone()
		if err != nil {
			return nil, err
		}
		copy.value.collections[name] = cloned
	}
	return copy, nil
}
func (p *Prefetched[S]) validate() error {
	if p == nil || p._self != p || descriptorIsNil(p.descriptor) || interfaceIsNil(p.backend) || len(p.value.collections) == 0 {
		return relationInvalidPlan("prefetched result is nil, zero, or copied")
	}
	return validateQuerySession(context.Background(), p.backend)
}
func (p *Prefetched[S]) Source() (S, error) {
	var zero S
	if err := p.validate(); err != nil {
		return zero, err
	}
	value := p.descriptor.CloneModel(p.value.source)
	return sessionReadResult(context.Background(), p.backend, value, nil)
}
func (r ManyToMany[O, T, L]) FromPrefetched(owner *Prefetched[O]) (*ManyCollection[T, L], bool, error) {
	if err := owner.validate(); err != nil {
		return nil, false, err
	}
	if r.state == nil || r.prefetchOwner.snapshot != owner.binding.snapshot || r.prefetchOwner.identity != owner.binding.identity {
		return nil, false, relationInvalidPlan("prefetched owner belongs to another collection binding")
	}
	cache, found := owner.value.collections[r.prefetchName]
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
func (q PrefetchQuery[S]) All(ctx context.Context) ([]*Prefetched[S], error) {
	if err := q.validate(ctx); err != nil {
		return nil, err
	}
	values, err := q.evaluation.evaluate(ctx, func(ctx context.Context) ([]prefetchedValue[S], error) { return q.load(ctx, q.source.plan) })
	if err != nil {
		return nil, err
	}
	result := make([]*Prefetched[S], len(values))
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
func (q PrefetchQuery[S]) First(ctx context.Context) (*Prefetched[S], bool, error) {
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
