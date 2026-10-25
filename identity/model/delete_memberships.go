package model

import (
	"context"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// The IR identifies membership storage even when there is no public reverse
// accessor, the through model is explicit, or multiple collections share it.
// Only the owner/key/FK projection is dynamic; account and catalog state stays
// under the typed model loaders and the same database-independent Query AST.
type deletionMembership struct {
	through, owner      ir.ModelIdentity
	field, table        string
	key, source, target query.FieldRef
	maximum             int
}
type deletionLink struct {
	key, source, target      int64
	sourceValid, targetValid bool
}

func bindDeletionMemberships(project orm.ProjectBinding, role ir.AccountRole) ([]deletionMembership, error) {
	var result []deletionMembership
	for _, relation := range project.ManyToManyRelations() {
		maximum := auth.MaximumPermissions
		selected := relation.Source == role.Model && relation.Field == role.Permissions || relation.Source == role.Catalog.Group && relation.Field == role.Catalog.GroupPermissions
		if relation.Source == role.Model && relation.Field == role.Groups {
			selected, maximum = true, MaximumAccountGroups
		}
		if !selected {
			continue
		}
		model, present := project.Model(relation.Through.Model)
		key, keyPresent := primaryField(model)
		source, sourcePresent := namedField(model, relation.Through.SourceField)
		target, targetPresent := namedField(model, relation.Through.TargetField)
		if !present || !keyPresent || !sourcePresent || !targetPresent || source.Relation == nil || target.Relation == nil ||
			source.Relation.Target != relation.Source || target.Relation.Target != relation.Target {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		field := func(value ir.Field) query.FieldRef {
			return query.NewFieldRef(value.Name, value.Column, query.FieldInteger, value.Nullable)
		}
		result = append(result, deletionMembership{through: relation.Through.Model, owner: relation.Source, field: relation.Field, table: model.DBTable,
			key: field(key), source: field(source), target: field(target), maximum: maximum})
	}
	if len(result) != 3 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return result, nil
}

// collect compares complete distinct membership sets before and after the
// collected removals. Deleting one of several explicit-through copies does
// not advance a surviving owner's revision unless the last copy disappears.
// Materialize and close each bounded rowset before inspecting another model.
func (membership deletionMembership) collect(ctx context.Context, scope db.Queryer, effects []orm.RelationDeleteEffect, touches map[deletionKey]*deletionTouch) error {
	var keys []int64
	for _, effect := range effects {
		if effect.Model == membership.through && (effect.Deleted || slices.Contains(effect.NullFields, membership.source.Name()) || slices.Contains(effect.NullFields, membership.target.Name())) {
			keys = append(keys, effect.Key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
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
			if row.sourceValid && row.targetValid {
				owners[row.source]++
			}
		}
	}
	ordered := make([]int64, 0, len(owners))
	for id := range owners {
		ordered = append(ordered, id)
	}
	slices.Sort(ordered)
	for _, id := range ordered {
		key := deletionKey{membership.owner, id}
		if touch := touches[key]; touch != nil && touch.deleted {
			continue
		}
		changed, err := membership.changed(ctx, scope, id, removed, owners[id])
		if err != nil {
			return err
		}
		if changed {
			touch := touches[key]
			if touch == nil {
				touch = &deletionTouch{key: key}
				touches[key] = touch
			}
			touch.collections = append(touch.collections, membership.field)
		}
	}
	return nil
}

func (membership deletionMembership) changed(ctx context.Context, scope db.Queryer, owner int64, removed map[int64]deletionLink, expected int) (bool, error) {
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
			return false, err
		}
		for _, row := range rows {
			if !row.sourceValid || row.source != owner || started && row.key <= last {
				return false, managementError(CodePersistence, nil)
			}
			last, started = row.key, true
			original, deleted := removed[row.key]
			if deleted && row != original {
				return false, managementError(CodePersistence, nil)
			}
			if !row.targetValid {
				continue
			}
			before[row.target] = true
			if deleted {
				seen++
			} else {
				after[row.target] = true
			}
			if len(before) > membership.maximum {
				return false, managementError(CodePersistence, nil)
			}
		}
		if len(rows) < catalogOwnerBatchSize {
			if seen != expected {
				return false, managementError(CodePersistence, nil)
			}
			return len(before) != len(after), nil
		}
	}
}

func (membership deletionMembership) read(ctx context.Context, scope db.Queryer, limit int, conditions ...query.Condition) ([]deletionLink, error) {
	if len(conditions) == 0 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	expression, err := query.NewExpression(conditions[0])
	if err != nil {
		return nil, err
	}
	for _, condition := range conditions[1:] {
		next, err := query.NewExpression(condition)
		if err != nil {
			return nil, err
		}
		expression, err = query.AndExpressions(expression, next)
		if err != nil {
			return nil, err
		}
	}
	plan, err := query.NewPlan(membership.table, []query.FieldRef{membership.key, membership.source, membership.target}).WithWhere(expression)
	if err != nil {
		return nil, err
	}
	plan, err = plan.WithOrderings(query.NewOrdering(membership.key, query.Ascending)).WithLimit(limit)
	if err != nil {
		return nil, err
	}
	failure := func(err error) error { return managementError(CodePersistence, err) }
	rows := make([]deletionLink, 0, limit)
	err = rowset.Scan(ctx, scope, plan, failure, func(source db.Rows) (bool, error) {
		var rawKey, rawSource, rawTarget any
		if err := source.Scan(&rawKey, &rawSource, &rawTarget); err != nil {
			return false, failure(err)
		}
		var row deletionLink
		var keyValid bool
		row.key, keyValid = rawKey.(int64)
		row.source, row.sourceValid = rawSource.(int64)
		row.target, row.targetValid = rawTarget.(int64)
		if !keyValid || !row.sourceValid && (rawSource != nil || !membership.source.Nullable()) || !row.targetValid && (rawTarget != nil || !membership.target.Nullable()) ||
			len(rows) >= limit || len(rows) > 0 && rows[len(rows)-1].key >= row.key {
			return false, failure(nil)
		}
		rows = append(rows, row)
		return true, nil
	})
	return rows, err
}
