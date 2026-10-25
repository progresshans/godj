package model

import (
	"context"

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
	maximum                     int
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
		result = append(result, deletionMembership{through: relation.Through.Model, owner: relation.Source, targetModel: relation.Target, field: relation.Field, table: model.DBTable,
			key: field(key), source: field(source), target: field(target), maximum: maximum})
	}
	if len(result) != 3 {
		return nil, managementError(CodeInvalidConfig, nil)
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
