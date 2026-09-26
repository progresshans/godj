package orm

import (
	"context"
	"reflect"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// SinglePrefetch loads one forward or reverse OneToOne edge and its target's
// descendants. Existing eager targets are reused without re-reading them.
type SinglePrefetch[S, T any] struct {
	selection        RelatedSelect[S, T]
	children         []PrefetchSelection[T]
	eager            []RelatedSelection[T]
	plan             query.Plan
	custom           bool
	configurationErr error
}

func PrefetchRequiredForward[S, T any](relation RequiredForwardObject[S, T]) SinglePrefetch[S, T] {
	return SinglePrefetch[S, T]{selection: SelectRequiredForward(relation)}
}
func PrefetchNullableForward[S, T any](relation NullableForwardObject[S, T]) SinglePrefetch[S, T] {
	return SinglePrefetch[S, T]{selection: SelectNullableForward(relation)}
}
func PrefetchReverseOneToOne[S, T any](relation ReverseOneToOneObject[S, T]) SinglePrefetch[S, T] {
	return SinglePrefetch[S, T]{selection: SelectReverseOneToOne(relation)}
}
func (p SinglePrefetch[S, T]) WithChildren(children ...PrefetchSelection[T]) SinglePrefetch[S, T] {
	p.children = append(append([]PrefetchSelection[T](nil), p.children...), children...)
	return p
}
func (p SinglePrefetch[S, T]) WithConfigurationError(err error) SinglePrefetch[S, T] {
	if p.configurationErr == nil {
		p.configurationErr = err
	}
	return p
}

func (p SinglePrefetch[S, T]) targetQuery() QuerySet[T] {
	q := newQuerySet[T](nil, p.selection.state.targetDescriptor, p.selection.state.target.objectPlan)
	q.configurationErr = p.selection.configurationErr
	q = q.OrderBy(NewAutoField[T](p.selection.state.targetKey).Asc())
	if p.custom {
		q.plan = p.plan
	}
	if p.configurationErr != nil {
		q.configurationErr = p.configurationErr
	}
	return q
}
func (p SinglePrefetch[S, T]) withTarget(q QuerySet[T]) SinglePrefetch[S, T] {
	p.custom, p.plan = true, q.plan
	return p.WithConfigurationError(q.configurationErr)
}
func (p SinglePrefetch[S, T]) Filter(values ...Predicate[T]) SinglePrefetch[S, T] {
	return p.withTarget(p.targetQuery().Filter(values...))
}
func (p SinglePrefetch[S, T]) OrderBy(values ...Ordering[T]) SinglePrefetch[S, T] {
	return p.withTarget(p.targetQuery().OrderBy(values...))
}
func (p SinglePrefetch[S, T]) Distinct() SinglePrefetch[S, T] {
	return p.withTarget(p.targetQuery().Distinct())
}
func (p SinglePrefetch[S, T]) SelectRelated(values ...RelatedSelection[T]) SinglePrefetch[S, T] {
	if len(values) == 0 {
		return p.WithConfigurationError(relationInvalidPlan("target eager selection is empty"))
	}
	p.eager = append(append([]RelatedSelection[T](nil), p.eager...), values...)
	return p.withTarget(p.targetQuery())
}

type preparedSinglePrefetch[S, T any] struct {
	state          relatedSelectState[S, T]
	children       []preparedPrefetch[T]
	cachedChildren []preparedPrefetch[T]
	targets        []preparedRelatedSelection[T]
	eagerNodes     int
	plan           query.Plan
	custom         bool
	nodes          int
}

func (p SinglePrefetch[S, T]) preparePrefetch(depth int, remaining *int) (preparedPrefetch[S], error) {
	if p.configurationErr != nil {
		return nil, p.configurationErr
	}
	if p.selection.configurationErr != nil {
		return nil, p.selection.configurationErr
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
	targets, eagerNodes, err := preparePrefetchEager(p.eager, p.selection.state.target, depth+1, remaining)
	if err != nil {
		return nil, err
	}
	q := p.targetQuery()
	if q.configurationErr != nil {
		return nil, q.configurationErr
	}
	prepared := preparedSinglePrefetch[S, T]{state: p.selection.state, children: children, targets: targets, eagerNodes: eagerNodes, plan: q.plan, custom: p.custom, nodes: before - *remaining}
	if !p.custom {
		prepared.cachedChildren = children
	}
	if err := prepared.validate(); err != nil {
		return nil, err
	}
	return prepared, nil
}
func (p preparedSinglePrefetch[S, T]) owner() BoundModel[S] { return p.state.source }
func (p preparedSinglePrefetch[S, T]) name() string         { return p.state.path.path }
func (p preparedSinglePrefetch[S, T]) nodeBudget() int      { return p.nodes }
func (p preparedSinglePrefetch[S, T]) validate() error {
	if err := validateRelatedSelectState(p.state); err != nil {
		return err
	}
	if _, ok := p.state.target.objectDescriptor.(PrimaryKeyObjectDescriptor[T]); !ok {
		return relationInvalidPlan("single prefetch target has no primary-key descriptor")
	}
	if err := p.plan.ValidateOrderings(); err != nil {
		return err
	}
	if _, limited := p.plan.Limit(); limited {
		return relationInvalidPlan("single prefetch target cannot be sliced")
	}
	if offset, _ := p.plan.Offset(); offset != 0 {
		return relationInvalidPlan("single prefetch target cannot be sliced")
	}
	if err := validatePrefetchEager(p.targets, p.state.target); err != nil {
		return err
	}
	for _, child := range p.children {
		if err := child.validate(); err != nil {
			return err
		}
		owner := child.owner()
		if owner.snapshot != p.state.target.snapshot || owner.identity != p.state.target.identity {
			return relationInvalidPlan("single prefetch child belongs to another target binding")
		}
	}
	return nil
}
func (p preparedSinglePrefetch[S, T]) merge(other preparedPrefetch[S]) (preparedPrefetch[S], error) {
	value, ok := other.(preparedSinglePrefetch[S, T])
	if !ok || !p.state.path.projection.Equal(value.state.path.projection) || p.state.source.snapshot != value.state.source.snapshot ||
		p.state.target.identity != value.state.target.identity || reflect.TypeOf(p.state.targetDescriptor) != reflect.TypeOf(value.state.targetDescriptor) {
		return nil, relationInvalidPlan("repeated single prefetch has conflicting binding metadata")
	}
	if value.custom {
		return nil, relationInvalidPlan("prefetch target query redefines an already selected lookup")
	}
	children, err := mergePrefetchSet(append(append([]preparedPrefetch[T](nil), p.children...), value.children...))
	if err != nil {
		return nil, err
	}
	cachedChildren, err := mergePrefetchSet(append(append([]preparedPrefetch[T](nil), p.cachedChildren...), value.cachedChildren...))
	if err != nil {
		return nil, err
	}
	p.children, p.cachedChildren, p.nodes = children, cachedChildren, p.nodes+value.nodes
	return p, nil
}

func (p preparedSinglePrefetch[S, T]) reverse() bool {
	return p.state.path.projection.TerminalHop().Direction() == query.RelationReverse
}
func (p preparedSinglePrefetch[S, T]) ownerKey(value S) (query.Value, error) {
	if p.reverse() {
		descriptor, ok := p.state.source.objectDescriptor.(PrimaryKeyObjectDescriptor[S])
		if !ok {
			return query.Value{}, relationInvalidPlan("single reverse prefetch source has no primary-key descriptor")
		}
		key, err := manyObjectKey(descriptor, value)
		return query.Integer(key), err
	}
	key, present := p.state.sourceStorage.Value(value)
	cloned, clonePresent := p.state.sourceStorage.Value(p.state.sourceDescriptor.CloneModel(value))
	if !present || !clonePresent || !key.Equal(cloned) {
		return query.Value{}, relationInvalidPlan("single prefetch source FK is absent or changed by cloning")
	}
	if key.IsNull() {
		if !p.state.path.sourceKey.Nullable {
			return query.Value{}, relatedObjectProjectionError(p.state.path.sourceKey, "required source key is NULL")
		}
		return key, nil
	}
	if _, ok := key.Integer(); !ok {
		return query.Value{}, relationInvalidPlan("single prefetch source key is not an integer")
	}
	return key, nil
}

func (p preparedSinglePrefetch[S, T]) apply(ctx context.Context, backend db.Queryer, values []relatedSelectedValue[S]) error {
	if err := p.validate(); err != nil {
		return err
	}
	descriptor := p.state.target.objectDescriptor.(PrimaryKeyObjectDescriptor[T])
	keys := make([]query.Value, len(values))
	positions := make([]int, len(values))
	cached := make([]typedCachedRelatedTarget[T], len(values))
	requested := make(map[int64]struct{})
	needsFetch := false
	for i, value := range values {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := p.ownerKey(value.source)
		if err != nil {
			return err
		}
		keys[i], positions[i] = key, -1
		for index, target := range value.targets {
			if target.projection().TerminalHop().Accessor() != p.name() {
				continue
			}
			typed, ok := target.(typedCachedRelatedTarget[T])
			if !ok || !typed.selected.Equal(p.state.path.projection) || typed.binding.snapshot != p.state.target.snapshot || typed.binding.identity != p.state.target.identity {
				return relationInvalidPlan("single prefetch found a foreign eager target cache")
			}
			if typed.present {
				id, err := manyObjectKey(descriptor, typed.value)
				if err != nil {
					return err
				}
				membership := query.Integer(id)
				if p.reverse() {
					var present bool
					membership, present = p.state.targetStorage.Value(typed.value)
					if !present {
						return relationInvalidPlan("cached reverse target FK is absent")
					}
				}
				if !membership.Equal(key) {
					return relatedObjectProjectionError(p.state.path.sourceKey, "cached eager target does not belong to its source")
				}
			}
			positions[i], cached[i] = index, typed
			break
		}
		if positions[i] < 0 {
			needsFetch = true
			if !key.IsNull() {
				id, _ := key.Integer()
				requested[id] = struct{}{}
			}
		}
	}
	ordered := make([]int64, 0, len(requested))
	for key := range requested {
		ordered = append(ordered, key)
	}
	slices.Sort(ordered)
	groups := make(map[int64]relatedSelectedValue[T], len(ordered))
	field := fieldReference(p.state.targetKey)
	if p.reverse() {
		field = fieldReference(p.state.path.sourceKey)
	}
	base := newQuerySet(backend, p.state.targetDescriptor, p.plan)
	for start := 0; start < len(ordered); start += manyPrefetchBatchSize {
		batch := ordered[start:min(start+manyPrefetchBatchSize, len(ordered))]
		arguments := make([]query.Value, len(batch))
		for i, id := range batch {
			arguments[i] = query.Integer(id)
		}
		condition, err := query.NewInCondition(field, arguments)
		if err != nil {
			return err
		}
		q := base.Filter(predicateFromCondition[T](condition, nil))
		q.materialization = &queryMaterialization[T]{binding: p.state.target, targets: p.targets, eagerNodes: p.eagerNodes}
		if err := q.validateTerminal(ctx); err != nil {
			return err
		}
		raw, attachment, err := q.evaluateModels(ctx)
		if err != nil {
			return err
		}
		rows, err := materializationValues(p.state.target, raw, attachment)
		if err != nil {
			return err
		}
		for _, row := range rows {
			id, err := manyObjectKey(descriptor, row.source)
			if err != nil {
				return err
			}
			membership := query.Integer(id)
			if p.reverse() {
				var present bool
				membership, present = p.state.targetStorage.Value(row.source)
				cloned, clonePresent := p.state.targetStorage.Value(descriptor.CloneModel(row.source))
				if !present || !clonePresent || !membership.Equal(cloned) {
					return relationInvalidPlan("single reverse target FK is absent or changed by cloning")
				}
			}
			owner, integer := membership.Integer()
			if _, present := slices.BinarySearch(batch, owner); !integer || membership.IsNull() || !present {
				return relatedObjectProjectionError(p.state.path.sourceKey, "single prefetch row is outside its requested batch")
			}
			if prior, duplicate := groups[owner]; duplicate {
				priorID, err := manyObjectKey(descriptor, prior.source)
				if err != nil {
					return err
				}
				// Custom joins may repeat the same target. Keep the first row,
				// but never hide two different targets of a single relation.
				if !p.custom || priorID != id {
					return &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectCardinality, Field: p.state.path.sourceKey.Name, Detail: "single prefetch returned more than one target for a source key"}
				}
				continue
			}
			groups[owner] = row
		}
	}
	children := make([]relatedSelectedValue[T], 0, len(values))
	childPositions := make([]int, len(values))
	for i := range values {
		childPositions[i] = -1
		if positions[i] < 0 {
			cache := typedCachedRelatedTarget[T]{selected: p.state.path.projection, descriptor: p.state.targetDescriptor, binding: p.state.target}
			if !keys[i].IsNull() {
				key, _ := keys[i].Integer()
				target, present := groups[key]
				cache.value = p.state.targetDescriptor.CloneModel(target.source)
				cache.present = present
				cache.children, cache.collections = target.targets, target.collections
				plan, err := p.state.target.objectPlan.WithConditions(query.NewCondition(field, query.LookupExact, keys[i]))
				if err != nil {
					return err
				}
				cache.plan, err = plan.WithLimit(2)
				if err != nil {
					return err
				}
				// A missing row is cached for access without failing unrelated
				// owners. A required generated accessor still reports missing.
				cache.allowMissing = p.reverse() || !present
			}
			cached[i] = cache
		}
		if cached[i].present && (!needsFetch || positions[i] < 0) {
			childPositions[i] = len(children)
			// Descendant loading may replace entries. Keep an eager graph's
			// immutable maps and slices private to this new evaluation.
			collections := make(map[string]cachedPrefetch, len(cached[i].collections))
			for name, cache := range cached[i].collections {
				collections[name] = cache
			}
			children = append(children, relatedSelectedValue[T]{source: p.state.targetDescriptor.CloneModel(cached[i].value), targets: append([]cachedRelatedTarget(nil), cached[i].children...), collections: collections})
		}
	}
	selections := p.children
	if !needsFetch {
		// An already loaded parent bypasses the custom target query,
		// including that query's children. Explicit descendant paths remain.
		selections = p.cachedChildren
	}
	if err := loadPrefetchValues(ctx, backend, children, selections); err != nil {
		return err
	}
	for i := range values {
		if index := childPositions[i]; index >= 0 {
			cached[i].children = children[index].targets
			cached[i].collections = children[index].collections
		}
		if positions[i] >= 0 {
			values[i].targets[positions[i]] = cached[i]
		} else {
			values[i].targets = append(values[i].targets, cached[i])
		}
	}
	_, err := sessionReadResult(ctx, backend, struct{}{}, ctx.Err())
	return err
}
