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
	through, owner, targetModel ir.ModelIdentity
	field, table                string
	key, source, target         query.FieldRef
	targetTable                 string
	targetKey                   query.FieldRef
	maximum                     int
	profile                     bool
}
type deletionLink struct {
	key, source, target      int64
	sourceValid, targetValid bool
}

func bindDeletionMemberships(project orm.ProjectBinding, role ir.AccountRole) ([]deletionMembership, error) {
	var result []deletionMembership
	critical := 0
	for _, relation := range project.ManyToManyRelations() {
		maximum := auth.MaximumPermissions
		selected := relation.Source == role.Model && relation.Field == role.Permissions || relation.Source == role.Catalog.Group && relation.Field == role.Catalog.GroupPermissions
		if relation.Source == role.Model && relation.Field == role.Groups {
			selected, maximum = true, MaximumAccountGroups
		}
		profile := relation.Source == role.Model && relation.Field != role.Groups && relation.Field != role.Permissions
		if selected {
			critical++
		} else if profile {
			selected, maximum = true, MaximumProfileCollectionItems
		}
		if !selected {
			continue
		}
		model, present := project.Model(relation.Through.Model)
		key, keyPresent := primaryField(model)
		source, sourcePresent := namedField(model, relation.Through.SourceField)
		target, targetPresent := namedField(model, relation.Through.TargetField)
		targetModel, targetFound := project.Model(relation.Target)
		targetKey, targetKeyFound := primaryField(targetModel)
		if !present || !keyPresent || !sourcePresent || !targetPresent || source.Relation == nil || target.Relation == nil ||
			source.Relation.Target != relation.Source || target.Relation.Target != relation.Target || !targetFound || !targetKeyFound {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		field := func(value ir.Field) query.FieldRef {
			return query.NewFieldRef(value.Name, value.Column, query.FieldInteger, value.Nullable)
		}
		result = append(result, deletionMembership{through: relation.Through.Model, owner: relation.Source, targetModel: relation.Target, field: relation.Field, table: model.DBTable,
			key: field(key), source: field(source), target: field(target), targetTable: targetModel.DBTable, targetKey: field(targetKey), maximum: maximum, profile: profile})
	}
	if critical != 3 {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return result, nil
}

// readKeys validates the complete stored logical set, including its target
// existence. Nullable physical links do not become zero-valued memberships;
// distinct zero/negative target keys remain ordinary present keys.
func (membership deletionMembership) readKeys(ctx context.Context, scope db.Queryer, owner int64) ([]int64, error) {
	keys, _, err := membership.projectedSets(ctx, scope, owner, nil, 0, nil)
	if err != nil || len(keys) == 0 {
		return keys, err
	}
	values := make([]query.Value, len(keys))
	for index, key := range keys {
		values[index] = query.Integer(key)
	}
	condition, err := query.NewInCondition(membership.targetKey, values)
	if err != nil {
		return nil, err
	}
	expression, err := query.NewExpression(condition)
	if err != nil {
		return nil, err
	}
	plan, err := query.NewPlan(membership.targetTable, []query.FieldRef{membership.targetKey}).WithWhere(expression)
	if err != nil {
		return nil, err
	}
	plan = plan.WithOrderings(query.NewOrdering(membership.targetKey, query.Ascending))
	plan, err = plan.WithLimit(len(keys) + 1)
	if err != nil {
		return nil, err
	}
	actual := make([]int64, 0, len(keys))
	failure := func(err error) error { return managementError(CodePersistence, err) }
	err = rowset.Scan(ctx, scope, plan, failure, func(rows db.Rows) (bool, error) {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return false, failure(err)
		}
		key, valid := raw.(int64)
		if !valid || len(actual) >= len(keys) || key != keys[len(actual)] {
			return false, failure(nil)
		}
		actual = append(actual, key)
		return true, nil
	})
	if err != nil || !slices.Equal(keys, actual) {
		return nil, failure(err)
	}
	return keys, nil
}

func profileMemberships(memberships []deletionMembership) []deletionMembership {
	var result []deletionMembership
	for _, membership := range memberships {
		if membership.profile {
			result = append(result, membership)
		}
	}
	return result
}

func readProfileMemberships(ctx context.Context, scope db.Queryer, memberships []deletionMembership, owner int64, projection *lifecycleProjection) (map[string][]int64, error) {
	result := make(map[string][]int64, len(memberships))
	for _, membership := range memberships {
		if !membership.profile {
			continue
		}
		keys, err := membership.readKeys(ctx, scope, owner)
		if err != nil {
			return nil, err
		}
		if projection != nil {
			key := lifecycleMembershipKey{deletionKey{membership.owner, owner}, membership.field}
			if expected, present := projection.sets[key]; present {
				keys = slices.Clone(expected.after)
			} else {
				projection.sets[key] = lifecycleMembershipSet{slices.Clone(keys), slices.Clone(keys)}
			}
		}
		result[membership.field] = keys
	}
	return result, nil
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
