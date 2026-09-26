package orm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

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
	groups := make(map[int64][]T, len(keys))
	for start := 0; start < len(keys); start += manyPrefetchBatchSize {
		batch := keys[start:min(start+manyPrefetchBatchSize, len(keys))]
		plan, err := p.targetPlan.ForPrefetchOwners(path, batch)
		if err != nil {
			return nil, err
		}
		if err := scanPrefetchTargets(ctx, backend, descriptor, r.target, plan, batch, r.state.source.Name, groups); err != nil {
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
		if len(p.queryChildren) > 0 {
			set.materialization = &queryMaterialization[T]{binding: r.prefetchTarget, selections: p.queryChildren}
		}
		set.evaluation.values = set.cloneModels(groups[collection.ownerKey])
		set.evaluation.ready = true
		collection.querySet = set
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, backend, collections, nil)
}

func scanPrefetchTargets[T any](ctx context.Context, backend db.Queryer, descriptor ProjectionDescriptor[T], target PrimaryKeyObjectDescriptor[T], plan query.Plan, keys []int64, ownerField string, groups map[int64][]T) error {
	rows, err := openQueryRows(ctx, backend, plan)
	if err != nil {
		return err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			break
		}
		scan := descriptor.NewProjectionScan()
		if interfaceIsNil(scan) {
			err = relationInvalidPlan("prefetch descriptor returned nil scan")
			break
		}
		cells := scan.Destinations()
		if !validProjectionDestinations(cells, len(plan.SourceFields())) {
			err = relationInvalidPlan("prefetch target columns do not match model")
			break
		}
		var owner sql.NullInt64
		if err = rows.Scan(append(append([]any(nil), cells...), &owner)...); err != nil {
			err = fmt.Errorf("scan prefetch target row: %w", err)
			break
		}
		if _, present := slices.BinarySearch(keys, owner.Int64); !owner.Valid || !present {
			err = &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Field: ownerField, Detail: "prefetch target row is outside its requested owner batch"}
			break
		}
		value, key, presence := scan.Decode()
		if _, integer := key.Integer(); presence != ProjectionPresent || key.IsNull() || !integer {
			err = relationInvalidPlan("prefetch target did not decode a present model with an integer key")
			break
		}
		integer, keyErr := manyObjectKey(target, value)
		if keyErr != nil {
			err = keyErr
			break
		}
		if !key.Equal(query.Integer(integer)) {
			err = relationInvalidPlan("prefetch decoded primary key does not match its model")
			break
		}
		groups[owner.Int64] = append(groups[owner.Int64], descriptor.CloneModel(value))
	}
	_, err = sessionReadResult(ctx, backend, struct{}{}, lifecycle.finish(ctx, err))
	return err
}
