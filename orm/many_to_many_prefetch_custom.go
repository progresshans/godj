package orm

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

func (r ManyToMany[O, T, L]) prefetchOwnerPath() (query.RelationPath, error) {
	key, present := relationAutoPrimaryKey(r.prefetchTarget.model)
	if !present || r.state == nil {
		return query.RelationPath{}, relationInvalidPlan("prefetch target has no canonical primary key")
	}
	return query.NewRelationChain(r.path.Hops(), []query.FieldRef{fieldReference(key), fieldReference(r.state.key)}, r.path.Terminal(), query.RelationTerminalRelatedField)
}

func (p preparedManyPrefetch[S, T, L]) loadCustom(ctx context.Context, backend db.Queryer, owners []S, borrowed bool) ([]*ManyCollection[T, L], error) {
	r := p.relation
	collections, keys, err := r.preparePrefetchOwners(ctx, backend, owners, borrowed)
	if err != nil {
		return nil, err
	}
	path, err := r.prefetchOwnerPath()
	if err != nil {
		return nil, err
	}
	descriptor, err := projectionDescriptorFor(r.prefetchTarget)
	if err != nil {
		return nil, err
	}
	groups := make(map[int64][]relatedSelectedValue[T], len(keys))
	configuration := &queryMaterialization[T]{binding: r.prefetchTarget, selections: p.queryChildren, targets: p.targets, eagerNodes: p.eagerNodes}
	base := newQuerySet(backend, r.target, p.targetPlan)
	related, err := configuration.relatedQuery(base)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(keys); start += manyPrefetchBatchSize {
		batch := keys[start:min(start+manyPrefetchBatchSize, len(keys))]
		plan, err := related.plan.ForPrefetchOwners(path, batch)
		if err != nil {
			return nil, err
		}
		if err := scanPrefetchTargets(ctx, backend, descriptor, r.target, plan, batch, r.state.source.Name, p.targets, groups); err != nil {
			return nil, err
		}
	}
	for _, collection := range collections {
		// The held query has the original custom predicates followed by this
		// manager's core membership. Its next Filter may reuse that core join.
		membership, err := query.NewExpression(query.NewRelatedCondition(r.path, query.LookupExact, query.Integer(collection.ownerKey)))
		if err != nil {
			return nil, err
		}
		plan, err := p.targetPlan.WithWhere(membership)
		if err != nil {
			return nil, err
		}
		plan, err = plan.ReuseNextCollectionFilter()
		if err != nil {
			return nil, err
		}
		set := newQuerySet(backend, r.target, plan)
		if len(p.queryChildren) > 0 || len(p.targets) > 0 {
			set.materialization = configuration
		}
		graph := copyRelatedValues(groups[collection.ownerKey])
		set.evaluation.values = make([]T, len(graph))
		for i, value := range graph {
			set.evaluation.values[i] = r.target.CloneModel(value.source)
		}
		set.evaluation.attachment = &materializedRows[T]{binding: r.prefetchTarget, values: graph}
		set.evaluation.ready = true
		collection.querySet = set
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, backend, collections, nil)
}

func scanPrefetchTargets[T any](ctx context.Context, backend db.Queryer, descriptor ProjectionDescriptor[T], target PrimaryKeyObjectDescriptor[T], plan query.Plan, keys []int64, ownerField string, targets []preparedRelatedSelection[T], groups map[int64][]relatedSelectedValue[T]) error {
	related := RelatedSelectQuery[T]{backend: backend, sourceDescriptor: descriptor, targets: targets}
	values, owners, err := related.scanProjected(ctx, plan, 0, 0, keys, ownerField)
	if err != nil {
		return err
	}
	for i, value := range values {
		if _, err := manyObjectKey(target, value.source); err != nil {
			return err
		}
		groups[owners[i]] = append(groups[owners[i]], value)
	}
	_, err = sessionReadResult(ctx, backend, struct{}{}, ctx.Err())
	return err
}
