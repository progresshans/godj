package model

import (
	"context"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// The projection is private to one native write fence. It combines removed,
// nulled and newly inserted links before comparing distinct memberships. It
// never substitutes an in-memory query engine for typed account/catalog reads.
type lifecycleMembershipKey struct {
	owner deletionKey
	field string
}
type lifecycleMembershipSet struct{ before, after []int64 }
type lifecycleProjection struct {
	created           *lifecycleEdit
	effects           map[deletionKey]orm.RelationDeleteEffect
	sets              map[lifecycleMembershipKey]lifecycleMembershipSet
	touches           map[deletionKey]*deletionTouch
	directCollections map[lifecycleMembershipKey]bool
}

func (projection *lifecycleProjection) deleted(model ir.ModelIdentity, id int64) bool {
	return projection.effects[deletionKey{model, id}].Deleted
}

func (projection *lifecycleProjection) keys(model ir.ModelIdentity, id int64, field string, current []int64) []int64 {
	if proposed, found := projection.sets[lifecycleMembershipKey{deletionKey{model, id}, field}]; found {
		return slices.Clone(proposed.after)
	}
	return slices.Clone(current)
}

func (membership deletionMembership) project(ctx context.Context, scope db.Queryer, projection *lifecycleProjection, inserts []orm.CollectionInsertEffect) error {
	var keys []int64
	for key, effect := range projection.effects {
		if key.model == membership.through && (effect.Deleted || slices.Contains(effect.NullFields, membership.source.Name()) || slices.Contains(effect.NullFields, membership.target.Name())) {
			keys = append(keys, key.id)
		}
	}
	slices.Sort(keys)
	removed := make(map[int64]deletionLink, len(keys))
	owners := make(map[int64]int)
	for offset := 0; offset < len(keys); offset += catalogOwnerBatchSize {
		batch := keys[offset:min(offset+catalogOwnerBatchSize, len(keys))]
		values := make([]query.Value, len(batch))
		for index, key := range batch {
			values[index] = query.Integer(key)
		}
		condition, err := query.NewInCondition(membership.key, values)
		if err != nil {
			return err
		}
		rows, err := membership.read(ctx, scope, len(batch)+1, condition)
		if err != nil {
			return err
		}
		if len(rows) != len(batch) {
			return managementError(CodePersistence, nil)
		}
		for index, row := range rows {
			if row.key != batch[index] {
				return managementError(CodePersistence, nil)
			}
			removed[row.key] = row
			insertedRoot := projection.created != nil && projection.created.key.model == membership.through && projection.created.key.id == row.key
			if row.sourceValid && row.targetValid && !insertedRoot {
				owners[row.source]++
			}
		}
	}
	added := make(map[int64][]int64)
	for _, insertion := range inserts {
		if insertion.Model != membership.through {
			continue
		}
		left, right := insertion.Values[membership.source.Name()], insertion.Values[membership.target.Name()]
		source, sourceValid := left.Integer()
		target, targetValid := right.Integer()
		if !sourceValid && !(membership.source.Nullable() && left.IsNull()) || !targetValid && !(membership.target.Nullable() && right.IsNull()) {
			return managementError(CodePersistence, nil)
		}
		if sourceValid && targetValid {
			added[source] = append(added[source], target)
			if _, found := owners[source]; !found {
				owners[source] = 0
			}
		}
	}
	// A typed root INSERT already exists in this transaction. When that
	// identity row is itself a membership link, remove it from the logical
	// before-set and project its endpoints as a new insertion. Otherwise a
	// read made after INSERT would conceal the authority change.
	if root := projection.created; root != nil && root.key.model == membership.through {
		left, right := root.values[membership.source.Name()], root.values[membership.target.Name()]
		effect := projection.effects[root.key]
		if slices.Contains(effect.NullFields, membership.source.Name()) {
			left = query.Null()
		}
		if slices.Contains(effect.NullFields, membership.target.Name()) {
			right = query.Null()
		}
		source, sourceValid := left.Integer()
		target, targetValid := right.Integer()
		if !sourceValid && !(membership.source.Nullable() && left.IsNull()) || !targetValid && !(membership.target.Nullable() && right.IsNull()) {
			return managementError(CodePersistence, nil)
		}
		if sourceValid && targetValid {
			added[source] = append(added[source], target)
			if _, found := owners[source]; !found {
				owners[source] = 0
			}
		}
	}
	ordered := make([]int64, 0, len(owners))
	for id := range owners {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	for _, id := range ordered {
		if projection.deleted(membership.owner, id) {
			if len(added[id]) != 0 {
				return profileInputError(membership.field, "invalid_choice")
			}
			continue
		}
		before, after, err := membership.projectedSets(ctx, scope, id, removed, owners[id], projection.created)
		if err != nil {
			return err
		}
		after = append(after, added[id]...)
		slices.Sort(after)
		after = slices.Compact(after)
		if len(after) > membership.maximum {
			return profileInputError(membership.field, "max_items")
		}
		for _, target := range after {
			if projection.deleted(membership.targetModel, target) {
				return profileInputError(membership.field, "invalid_choice")
			}
		}
		owner := deletionKey{membership.owner, id}
		projection.sets[lifecycleMembershipKey{owner, membership.field}] = lifecycleMembershipSet{before, after}
		if !slices.Equal(before, after) {
			touch := projection.touches[owner]
			if touch == nil {
				touch = &deletionTouch{key: owner}
				projection.touches[owner] = touch
			}
			if projection.directCollections[lifecycleMembershipKey{owner, membership.field}] {
				touch.direct = true
			}
			touch.collections = append(touch.collections, membership.field)
		}
	}
	return nil
}

func (membership deletionMembership) projectedSets(ctx context.Context, scope db.Queryer, owner int64, removed map[int64]deletionLink, expected int, created *lifecycleEdit) ([]int64, []int64, error) {
	before, after := make(map[int64]bool), make(map[int64]bool)
	var last int64
	started, seen := false, 0
	for {
		conditions := []query.Condition{query.NewCondition(membership.source, query.LookupExact, query.Integer(owner))}
		if started {
			conditions = append(conditions, query.NewCondition(membership.key, query.LookupGreaterThan, query.Integer(last)))
		}
		rows, err := membership.read(ctx, scope, catalogOwnerBatchSize, conditions...)
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			if !row.sourceValid || row.source != owner || started && row.key <= last {
				return nil, nil, managementError(CodePersistence, nil)
			}
			last, started = row.key, true
			original, deleted := removed[row.key]
			if deleted && row != original {
				return nil, nil, managementError(CodePersistence, nil)
			}
			if !row.targetValid {
				continue
			}
			if created != nil && created.key.model == membership.through && row.key == created.key.id {
				continue
			}
			before[row.target] = true
			if deleted {
				seen++
			} else {
				after[row.target] = true
			}
			if len(before) > membership.maximum {
				return nil, nil, managementError(CodePersistence, nil)
			}
		}
		if len(rows) < catalogOwnerBatchSize {
			if seen != expected {
				return nil, nil, managementError(CodePersistence, nil)
			}
			return sortedLifecycleKeys(before), sortedLifecycleKeys(after), nil
		}
	}
}

func sortedLifecycleKeys(keys map[int64]bool) []int64 {
	result := make([]int64, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

// A group's grant changes may affect accounts whose own memberships did not
// change. Check them all without advancing their membership revision. Every
// raw rowset is bounded, ordered and closed before the next query.
func (membership deletionMembership) affectedGroupMembers(ctx context.Context, scope db.Queryer, projection *lifecycleProjection, role ir.AccountRole) error {
	if membership.owner != role.Model || membership.field != role.Groups {
		return nil
	}
	var groups []int64
	for key, set := range projection.sets {
		if key.owner.model == role.Catalog.Group && key.field == role.Catalog.GroupPermissions && !slices.Equal(set.before, set.after) {
			groups = append(groups, key.owner.id)
		}
	}
	slices.Sort(groups)
	for offset := 0; offset < len(groups); offset += catalogOwnerBatchSize {
		batch := groups[offset:min(offset+catalogOwnerBatchSize, len(groups))]
		values := make([]query.Value, len(batch))
		for index, group := range batch {
			values[index] = query.Integer(group)
		}
		condition, err := query.NewInCondition(membership.target, values)
		if err != nil {
			return err
		}
		var last int64
		started := false
		for {
			conditions := []query.Condition{condition}
			if started {
				conditions = append(conditions, query.NewCondition(membership.key, query.LookupGreaterThan, query.Integer(last)))
			}
			rows, err := membership.read(ctx, scope, catalogOwnerBatchSize, conditions...)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if !row.targetValid || !slices.Contains(batch, row.target) || started && row.key <= last {
					return managementError(CodePersistence, nil)
				}
				last, started = row.key, true
				key := deletionKey{role.Model, row.source}
				if row.sourceValid && projection.touches[key] == nil {
					projection.touches[key] = &deletionTouch{key: key}
				}
			}
			if len(rows) < catalogOwnerBatchSize {
				break
			}
		}
	}
	return nil
}
