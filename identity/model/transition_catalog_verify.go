package model

import (
	"context"
	"maps"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func (created createdCatalogState) verify(actual transitionCatalogObservation) error {
	rowsEqual := func(a, b map[int64]transitionCatalogRow) bool {
		return maps.EqualFunc(a, b, func(a, b transitionCatalogRow) bool {
			return a.revision == b.revision && a.code == b.code && maps.Equal(a.values, b.values) && slices.Equal(a.permissions, b.permissions)
		})
	}
	if !rowsEqual(created.before.groups, actual.before.groups) || !rowsEqual(created.before.permissions, actual.before.permissions) ||
		!rowsEqual(created.after.groups, actual.after.groups) || !rowsEqual(created.after.permissions, actual.after.permissions) ||
		!slices.Equal(created.before.links, actual.before.links) {
		return managementError(CodeConflict, nil)
	}
	retained := make(map[int64]deletionLink, len(created.after.links))
	for _, row := range created.after.links {
		retained[row.key] = row
	}
	added := maps.Clone(created.newLinks)
	for _, row := range actual.after.links {
		if before, present := retained[row.key]; present {
			if before != row {
				return managementError(CodeConflict, nil)
			}
			delete(retained, row.key)
			continue
		}
		pair := transitionCatalogPair{row.source, row.target}
		if !row.sourceValid || !row.targetValid || !added[pair] {
			return managementError(CodeConflict, nil)
		}
		delete(added, pair)
	}
	if len(retained) != 0 || len(added) != 0 {
		return managementError(CodeConflict, nil)
	}
	return nil
}

// A newly allocated catalog identity may acquire only the declared role links
// and explicit host-reference targets. Other incoming fields were empty before
// allocation and must remain so; trigger-created rows cannot be silently adopted.
func (owner *Transition[S, T]) verifyCatalogCreationIncoming(ctx context.Context, scope db.Queryer, references []transitionReferenceObservation) error {
	state := owner.config.CatalogCreation.state
	binding := owner.next.state.credentials.state.binding.state
	role, project := binding.role, binding.project
	allowed := make(map[ir.ModelIdentity]map[string]bool)
	add := func(model ir.ModelIdentity, field string) {
		if allowed[model] == nil {
			allowed[model] = make(map[string]bool)
		}
		allowed[model][field] = true
	}
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source == role.Model && (relation.Field == role.Groups || relation.Field == role.Permissions) || relation.Source == role.Catalog.Group && relation.Field == role.Catalog.GroupPermissions {
			add(relation.Through.Model, relation.Through.SourceField)
			add(relation.Through.Model, relation.Through.TargetField)
		}
	}
	for _, reference := range references {
		for _, field := range reference.manifest.Fields {
			add(reference.manifest.Model, field.Target)
		}
	}
	var plans []query.Plan
	for _, relation := range project.ForwardRelations() {
		created := state.groupCreated && relation.Target == role.Catalog.Group || state.permissionCreated && relation.Target == role.Catalog.Permission
		if !created || allowed[relation.Source][relation.Field] || relation.Source == relation.Target {
			continue
		}
		model, found := project.Model(relation.Source)
		if !found {
			return managementError(CodeInvalidConfig, nil)
		}
		field := query.NewFieldRef(relation.Field, relation.Column, query.FieldInteger, relation.Nullable)
		condition, err := query.NewExpression(query.NewCondition(field, query.LookupIsNull, query.Boolean(false)))
		if err != nil {
			return err
		}
		plan, err := query.NewPlan(model.DBTable, []query.FieldRef{field}).WithWhere(condition)
		if err != nil {
			return err
		}
		plan, err = plan.WithLimit(1)
		if err != nil {
			return err
		}
		plans = append(plans, plan)
	}
	return requireEmptyTransitionIncoming(ctx, scope, plans)
}
