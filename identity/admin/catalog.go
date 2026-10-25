package identityadmin

import (
	"context"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/templates"
)

type groupRow struct {
	identity.GroupDetails
	full bool
}

func (a *registration) registerGroup(builder *admin.Builder) error {
	fields := []string{"name", "permissions"}
	changeChoices, err := a.manager.GroupPermissionChoiceSource(admin.ActionChange)
	if err != nil {
		return err
	}
	addChoices, err := a.manager.GroupPermissionChoiceSource(admin.ActionAdd)
	if err != nil {
		return err
	}
	return admin.RegisterModel(builder, admin.ModelConfig[groupRow]{
		AppLabel: "godj_identity", Slug: "groups", Model: models.GroupDescriptor{}.Metadata(), FormFields: fields, RevisionField: "revision",
		RelatedChoices: []admin.RelatedChoices{changeChoices}, CreateForm: &admin.FormConfig{Definition: formmodel.Definition{Fields: fields}, RelatedChoices: []admin.RelatedChoices{addChoices}},
		ListFields: []string{"id", "name"}, Permissions: admin.Permissions{View: identity.ViewGroup, Add: identity.AddGroup, Change: identity.ChangeGroup, Delete: identity.DeleteGroup},
		List: func(ctx context.Context, p auth.Principal, r admin.ListRequest) (admin.Page[groupRow], error) {
			page, err := a.manager.Groups(ctx, p, r.Offset, r.Limit)
			if err != nil {
				return admin.Page[groupRow]{}, operationError(err)
			}
			items := make([]groupRow, len(page.Groups))
			for i, value := range page.Groups {
				items[i] = groupRow{GroupDetails: identity.GroupDetails{GroupProfile: value}}
			}
			return admin.Page[groupRow]{Items: items, Total: page.Total, Offset: r.Offset, Limit: r.Limit}, nil
		},
		Get: func(ctx context.Context, p auth.Principal, id int64) (groupRow, bool, error) {
			value, err := a.manager.Group(ctx, p, id)
			if notFound(err) {
				return groupRow{}, false, nil
			}
			return groupRow{value, true}, err == nil, operationError(err)
		},
		Snapshot: func(value groupRow) (admin.Object, error) {
			values := map[string]templates.Value{"id": templates.Integer(value.ID), "name": templates.String(value.Name), "revision": templates.Integer(value.Revision)}
			if value.full {
				values["permissions"] = integerValues(value.PermissionIDs)
			}
			return admin.NewObject(value.ID, value.Name, values)
		},
		Initial: func(value groupRow) (map[string]forms.Value, error) { return groupInitial(value.GroupDetails), nil },
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return operationError(a.manager.CheckGroupCreate(ctx, actor, bound))
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return operationError(a.manager.CheckGroupChange(ctx, actor, mutation.ID, mutation.Revision, bound))
		},
		Create: func(ctx context.Context, p auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, error) {
			values, inputErr := bound.Input()
			if inputErr != nil {
				return admin.MutationResult{}, inputErr
			}

			name, ok := values.String("name")
			ids, present := values.Integers("permissions")
			if !ok || !present {
				return admin.MutationResult{}, invalidInput()
			}
			value, err := a.manager.CreateGroup(ctx, p, identity.NewGroupCreate(name).WithPermissions(ids...))
			return admin.MutationResult{ID: value.ID, Revision: value.Revision}, operationError(err)
		},
		Update: func(ctx context.Context, p auth.Principal, m admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			values, inputErr := bound.Input()
			if inputErr != nil {
				return admin.MutationResult{}, nil, inputErr
			}

			name, ok := values.String("name")
			ids, present := values.Integers("permissions")
			if !ok || !present {
				return admin.MutationResult{}, nil, invalidInput()
			}
			before, err := a.manager.Group(ctx, p, m.ID)
			if err != nil {
				return admin.MutationResult{}, nil, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return admin.MutationResult{}, nil, err
			}
			after, err := a.manager.UpdateGroup(ctx, p, m.ID, m.Revision, identity.GroupPatch{}.WithName(name).WithPermissions(ids...))
			if err != nil {
				return admin.MutationResult{}, nil, operationError(err)
			}
			return admin.MutationResult{ID: after.ID, Revision: after.Revision}, changedFields(fields, groupInitial(before), groupInitial(after)), nil
		},
		Delete: func(ctx context.Context, p auth.Principal, m admin.Mutation) (admin.MutationResult, error) {
			before, err := a.manager.Group(ctx, p, m.ID)
			if err != nil {
				return admin.MutationResult{}, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return admin.MutationResult{}, err
			}
			_, err = a.manager.DeleteGroup(ctx, p, m.ID, m.Revision, a.deletions.Groups)
			if err != nil {
				return admin.MutationResult{}, operationError(err)
			}
			return admin.MutationResult{ID: before.ID, Revision: before.Revision}, nil
		},
		History: func(ctx context.Context, p auth.Principal, id int64, r admin.HistoryRequest) ([]admin.AuditEntry, error) {
			entries, err := a.manager.GroupHistory(ctx, p, id, r.Limit)
			return entries, operationError(err)
		},
	})
}

func groupInitial(value identity.GroupDetails) map[string]forms.Value {
	return map[string]forms.Value{"name": forms.String(value.Name), "permissions": forms.Integers(value.PermissionIDs...)}
}

func (a *registration) registerPermission(builder *admin.Builder) error {
	fields := []string{"code", "name"}
	return admin.RegisterModel(builder, admin.ModelConfig[identity.PermissionProfile]{
		AppLabel: "godj_identity", Slug: "permissions", Model: models.PermissionDescriptor{}.Metadata(), FormFields: fields, RevisionField: "revision", ListFields: []string{"id", "code", "name"},
		Permissions: admin.Permissions{View: identity.ViewPermission, Add: identity.AddPermission, Change: identity.ChangePermission, Delete: identity.DeletePermission},
		List: func(ctx context.Context, p auth.Principal, r admin.ListRequest) (admin.Page[identity.PermissionProfile], error) {
			page, err := a.manager.Permissions(ctx, p, r.Offset, r.Limit)
			if err != nil {
				return admin.Page[identity.PermissionProfile]{}, operationError(err)
			}
			return admin.Page[identity.PermissionProfile]{Items: page.Permissions, Total: page.Total, Offset: r.Offset, Limit: r.Limit}, nil
		},
		Get: func(ctx context.Context, p auth.Principal, id int64) (identity.PermissionProfile, bool, error) {
			value, err := a.manager.Permission(ctx, p, id)
			if notFound(err) {
				return identity.PermissionProfile{}, false, nil
			}
			return value, err == nil, operationError(err)
		},
		Snapshot: func(value identity.PermissionProfile) (admin.Object, error) {
			return admin.NewObject(value.ID, value.Name, map[string]templates.Value{"id": templates.Integer(value.ID), "code": templates.String(value.Code), "name": templates.String(value.Name), "revision": templates.Integer(value.Revision)})
		},
		Initial: func(value identity.PermissionProfile) (map[string]forms.Value, error) {
			return permissionInitial(value), nil
		},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return operationError(a.manager.CheckPermissionCreate(ctx, actor, bound))
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return operationError(a.manager.CheckPermissionChange(ctx, actor, mutation.ID, mutation.Revision, bound))
		},
		Create: func(ctx context.Context, p auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, error) {
			values, inputErr := bound.Input()
			if inputErr != nil {
				return admin.MutationResult{}, inputErr
			}

			code, ok := values.String("code")
			name, present := values.String("name")
			if !ok || !present {
				return admin.MutationResult{}, invalidInput()
			}
			value, err := a.manager.CreatePermission(ctx, p, identity.NewPermissionCreate(code, name))
			return admin.MutationResult{ID: value.ID, Revision: value.Revision}, operationError(err)
		},
		Update: func(ctx context.Context, p auth.Principal, m admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			values, inputErr := bound.Input()
			if inputErr != nil {
				return admin.MutationResult{}, nil, inputErr
			}

			code, ok := values.String("code")
			name, present := values.String("name")
			if !ok || !present {
				return admin.MutationResult{}, nil, invalidInput()
			}
			before, err := a.manager.Permission(ctx, p, m.ID)
			if err != nil {
				return admin.MutationResult{}, nil, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return admin.MutationResult{}, nil, err
			}
			after, err := a.manager.UpdatePermission(ctx, p, m.ID, m.Revision, identity.PermissionPatch{}.WithCode(code).WithName(name))
			if err != nil {
				return admin.MutationResult{}, nil, operationError(err)
			}
			return admin.MutationResult{ID: after.ID, Revision: after.Revision}, changedFields(fields, permissionInitial(before), permissionInitial(after)), nil
		},
		Delete: func(ctx context.Context, p auth.Principal, m admin.Mutation) (admin.MutationResult, error) {
			before, err := a.manager.Permission(ctx, p, m.ID)
			if err != nil {
				return admin.MutationResult{}, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return admin.MutationResult{}, err
			}
			_, err = a.manager.DeletePermission(ctx, p, m.ID, m.Revision, a.deletions.Permissions)
			if err != nil {
				return admin.MutationResult{}, operationError(err)
			}
			return admin.MutationResult{ID: before.ID, Revision: before.Revision}, nil
		},
		History: func(ctx context.Context, p auth.Principal, id int64, r admin.HistoryRequest) ([]admin.AuditEntry, error) {
			entries, err := a.manager.PermissionHistory(ctx, p, id, r.Limit)
			return entries, operationError(err)
		},
	})
}

func permissionInitial(value identity.PermissionProfile) map[string]forms.Value {
	return map[string]forms.Value{"code": forms.String(value.Code), "name": forms.String(value.Name)}
}

func changedFields(names []string, before, after map[string]forms.Value) []string {
	var changed []string
	for _, name := range names {
		if !before[name].Equal(after[name]) {
			changed = append(changed, name)
		}
	}
	return changed
}

func integerValues(ids []int64) templates.Value {
	values := make([]templates.Value, len(ids))
	for i, id := range ids {
		values[i] = templates.Integer(id)
	}
	return templates.List(values...)
}
