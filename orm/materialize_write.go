package orm

import "github.com/progresshans/godj/db"

// Write results are prepared while the internal session is live, including
// every descendant collection. Their new handles are bound to the caller's
// original backend, which may be a temporarily suspended savepoint parent.
// Nothing validates that parent until RELEASE returns it to the caller.
func cloneWrittenSelection[M any](backend db.Queryer, binding BoundModel[M], descriptor ModelDescriptor[M], value relatedSelectedValue[M]) (*RelatedSelected[M], error) {
	owned, err := cloneWrittenValue(backend, descriptor, value)
	if err != nil {
		return nil, err
	}
	return selectionFromOwnedValue(backend, binding, descriptor, owned), nil
}

func cloneWrittenValue[M any](backend db.Queryer, descriptor ModelDescriptor[M], value relatedSelectedValue[M]) (relatedSelectedValue[M], error) {
	owned := relatedSelectedValue[M]{source: descriptor.CloneModel(value.source), targets: make([]cachedRelatedTarget, len(value.targets)), collections: make(map[string]cachedPrefetch, len(value.collections))}
	for i, target := range value.targets {
		cloned, err := target.cloneForWrite(backend)
		if err != nil {
			return relatedSelectedValue[M]{}, err
		}
		owned.targets[i] = cloned
	}
	for name, collection := range value.collections {
		cloned, err := collection.cloneForWrite(backend)
		if err != nil {
			return relatedSelectedValue[M]{}, err
		}
		owned.collections[name] = cloned
	}
	return owned, nil
}

func (target typedCachedRelatedTarget[T]) cloneForWrite(backend db.Queryer) (cachedRelatedTarget, error) {
	value := relatedSelectedValue[T]{source: target.value, targets: target.children, collections: target.collections}
	owned, err := cloneWrittenValue(backend, target.descriptor, value)
	if err != nil {
		return nil, err
	}
	target.value, target.children, target.collections = owned.source, owned.targets, owned.collections
	return target, nil
}

func cloneWrittenQuery[M any](backend db.Queryer, source QuerySet[M]) (QuerySet[M], error) {
	if source.evaluation == nil {
		return QuerySet[M]{}, relationInvalidPlan("written collection has no evaluation")
	}
	raw, attachment, ready := source.evaluation.cachedResult()
	if !ready {
		return QuerySet[M]{}, relationInvalidPlan("written collection graph is incomplete")
	}
	state := newEvaluationState[M]()
	state.values = make([]M, len(raw))
	for i, value := range raw {
		state.values[i] = source.descriptor.CloneModel(value)
	}
	if attachment != nil {
		graph, ok := attachment.(*materializedRows[M])
		if !ok || graph == nil || len(graph.values) != len(raw) {
			return QuerySet[M]{}, relationInvalidPlan("written collection has a foreign graph or row count")
		}
		owned := &materializedRows[M]{binding: graph.binding, values: make([]relatedSelectedValue[M], len(graph.values))}
		for i, value := range graph.values {
			var err error
			owned.values[i], err = cloneWrittenValue(backend, source.descriptor, value)
			if err != nil {
				return QuerySet[M]{}, err
			}
		}
		state.attachment = owned
	}
	state.ready = true
	source.backend, source.evaluation = backend, state
	return source, nil
}

func (c cachedManyPrefetch[T, L]) cloneForWrite(backend db.Queryer) (cachedPrefetch, error) {
	source, err := c.collection.Query()
	if err != nil {
		return nil, err
	}
	owned, err := cloneWrittenQuery(backend, source)
	if err != nil {
		return nil, err
	}
	original := c.collection
	result := &ManyCollection[T, L]{querySet: owned, basePlan: original.basePlan, target: original.target, through: original.through, state: original.state, backend: backend, ownerKey: original.ownerKey}
	result._self = result
	return cachedManyPrefetch[T, L]{collection: result}, nil
}

func (c cachedReversePrefetch[T]) cloneForWrite(backend db.Queryer) (cachedPrefetch, error) {
	source, err := c.set.Query()
	if err != nil {
		return nil, err
	}
	owned, err := cloneWrittenQuery(backend, source)
	if err != nil {
		return nil, err
	}
	result := newRelatedSet(owned)
	result.basePlan = c.set.basePlan
	return cachedReversePrefetch[T]{set: result, binding: c.binding, relation: c.relation}, nil
}
