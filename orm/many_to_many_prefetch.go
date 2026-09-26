package orm

import (
	"context"
	"reflect"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

const manyPrefetchBatchSize = 999

// Prefetch reads target membership for saved owner snapshots. Every returned
// handle owns an independent cache, including duplicate owners and empty sets.
// Each batch joins through rows to their targets using the ordinary Query AST;
// no handle is published until every batch and row has been validated.
func (r ManyToMany[O, T, L]) Prefetch(ctx context.Context, backend db.Queryer, owners []O) ([]*ManyCollection[T, L], error) {
	return r.prefetch(ctx, backend, owners, false)
}

// PrefetchInSession uses the existing transaction. Its ready handles and any
// derived queries expire with that session; it never starts a nested transaction.
func (r ManyToMany[O, T, L]) PrefetchInSession(ctx context.Context, session db.Session, owners []O) ([]*ManyCollection[T, L], error) {
	return r.prefetch(ctx, session, owners, true)
}

func (r ManyToMany[O, T, L]) prefetch(ctx context.Context, backend db.Queryer, owners []O, borrowed bool) ([]*ManyCollection[T, L], error) {
	result, keys, err := r.preparePrefetchOwners(ctx, backend, owners, borrowed)
	if err != nil {
		return nil, err
	}
	selection, storage, err := r.prefetchBinding()
	if err != nil {
		return nil, err
	}

	groups := make(map[int64][]T, len(keys))
	seenLinks := make(map[int64]struct{})
	base := newQuerySet(backend, r.through, r.prefetchSource.objectPlan)
	for start := 0; start < len(keys); start += manyPrefetchBatchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batchKeys := keys[start:min(start+manyPrefetchBatchSize, len(keys))]
		values := make([]query.Value, len(batchKeys))
		for i, key := range batchKeys {
			values[i] = query.Integer(key)
		}
		membership, err := query.NewInCondition(fieldReference(r.state.source), values)
		if err != nil {
			return nil, err
		}
		batch := base.Filter(predicateFromCondition[L](membership, nil), predicateFromCondition[L](query.NewCondition(fieldReference(r.state.target), query.LookupIsNull, query.Boolean(false)), nil)).OrderBy(NewAutoField[L](r.state.key).Asc())
		rows, err := selection.Select(batch).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			source, err := row.Source()
			if err != nil {
				return nil, err
			}
			foreignKey, present := storage.Value(source)
			ownerKey, integer := foreignKey.Integer()
			if !present || !integer || foreignKey.IsNull() {
				return nil, relatedSetMembershipError(r.state.source, "prefetch intermediary has no integer owner key")
			}
			if _, found := slices.BinarySearch(batchKeys, ownerKey); !found {
				return nil, relatedSetMembershipError(r.state.source, "prefetch intermediary is outside its requested owner batch")
			}
			linkKey, err := manyObjectKey[L](r.through, source)
			if err != nil {
				return nil, err
			}
			if _, duplicate := seenLinks[linkKey]; duplicate {
				return nil, relatedSetMembershipError(r.state.key, "prefetch returned the same intermediary row more than once")
			}
			seenLinks[linkKey] = struct{}{}
			related, err := selection.Related(row)
			if err != nil {
				return nil, err
			}
			target, present, err := related.Get(ctx)
			if err != nil {
				return nil, err
			}
			if !present {
				return nil, relatedSetMembershipError(r.state.target, "prefetch returned an absent target for a non-null link")
			}
			groups[ownerKey] = append(groups[ownerKey], target)
		}
	}
	if !reflect.DeepEqual(storage.Field(), r.state.source) {
		return nil, relationInvalidPlan("prefetch owner storage changed during evaluation")
	}
	for _, collection := range result {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cache := newEvaluationState[T]()
		cache.values = collection.querySet.cloneModels(groups[collection.ownerKey])
		cache.ready = true
		collection.querySet.evaluation = cache
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, backend, result, nil)
}

func (r ManyToMany[O, T, L]) preparePrefetchOwners(ctx context.Context, backend db.Queryer, owners []O, borrowed bool) ([]*ManyCollection[T, L], []int64, error) {
	if interfaceIsNil(ctx) {
		return nil, nil, relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if r.state == nil || interfaceIsNil(backend) {
		return nil, nil, relationInvalidPlan("collection is unbound or backend is nil")
	}
	var session db.RelationSession
	_, scoped := backend.(db.SessionValidator)
	if scoped != borrowed {
		return nil, nil, relationInvalidPlan("collection prefetch requires the matching root or session API")
	}
	if borrowed {
		// Reading a batch needs only the advertised session lifetime. Keep a
		// relation capability only when it actually exists; a read handle
		// must never fall back to starting a transaction for a later write.
		session, _ = backend.(db.RelationSession)
	}
	if err := validateQuerySession(ctx, backend); err != nil {
		return nil, nil, err
	}
	_, _, err := r.prefetchBinding()
	if err != nil {
		return nil, nil, err
	}

	// Validate all owners before the first query. Publication preserves caller
	// order, while sorted unique keys make batch boundaries deterministic.
	result := make([]*ManyCollection[T, L], len(owners))
	requested := make(map[int64]struct{}, len(owners))
	for i, owner := range owners {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		snapshot := r.owner.CloneModel(owner)
		key, err := manyObjectKey(r.owner, owner)
		if err != nil {
			return nil, nil, err
		}
		copyKey, err := manyObjectKey(r.owner, snapshot)
		if err != nil || copyKey != key {
			return nil, nil, relationInvalidPlan("prefetch owner clone changed its primary key")
		}
		collection, err := r.from(backend, snapshot)
		if err != nil {
			return nil, nil, err
		}
		collection.session = session
		result[i] = collection
		requested[key] = struct{}{}
	}
	keys := make([]int64, 0, len(requested))
	for key := range requested {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return result, keys, nil
}

func (r ManyToMany[O, T, L]) prefetchBinding() (RelatedSelect[L, T], RelationStorage[L], error) {
	var zero RelatedSelect[L, T]
	path, err := ResolveRelatedSelectPath(r.prefetchSource, r.state.target.Name)
	if err != nil {
		return zero, nil, err
	}
	storage, ok := r.through.BindRelationStorage(r.state.source.Clone())
	if !ok || interfaceIsNil(storage) || !immutableZeroStateValue(storage) || !reflect.DeepEqual(storage.Field(), r.state.source) {
		return zero, nil, relationInvalidPlan("prefetch requires canonical owner ForeignKey storage")
	}
	targetStorage, ok := r.through.BindRelationStorage(r.state.target.Clone())
	if !ok || interfaceIsNil(targetStorage) {
		return zero, nil, relationInvalidPlan("prefetch requires target ForeignKey storage")
	}
	selection, err := bindRelatedSelect(path.state, r.prefetchSource, r.prefetchTarget, targetStorage, nil)
	return selection, storage, err
}
