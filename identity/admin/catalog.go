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
	overrides := []formmodel.Override{formmodel.OverrideField("permissions", formmodel.WithRequired(false))}
	choices := func(action admin.Action) []admin.RelatedChoices {
		permission := identity.ChangeGroup
		if action == admin.ActionAdd {
			permission = identity.AddGroup
		}
		return []admin.RelatedChoices{{Field: "permissions", Permission: permission, Load: func(ctx context.Context, p auth.Principal) ([]forms.Choice, error) {
			return formChoices(a.manager.GroupPermissionChoices(ctx, p, action))
		}}}
	}
	return admin.RegisterModel(builder, admin.ModelConfig[groupRow]{
		AppLabel: "godj_identity", Slug: "groups", Model: models.GroupDescriptor{}.Metadata(), FormFields: fields, FormOverrides: overrides, RevisionField: "revision",
		RelatedChoices: choices(admin.ActionChange), CreateForm: &admin.FormConfig{Fields: fields, Overrides: overrides, RelatedChoices: choices(admin.ActionAdd)},
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
		Create: func(ctx context.Context, p auth.Principal, values forms.Values) (groupRow, error) {
			name, ok := values.String("name")
			ids, present := values.Integers("permissions")
			if !ok || !present {
				return groupRow{}, invalidInput()
			}
			value, err := a.manager.CreateGroup(ctx, p, identity.NewGroupCreate(name).WithPermissions(ids...))
			return groupRow{value, true}, operationError(err)
		},
		Update: func(ctx context.Context, p auth.Principal, m admin.Mutation, values forms.Values) (groupRow, []string, error) {
			name, ok := values.String("name")
			ids, present := values.Integers("permissions")
			if !ok || !present {
				return groupRow{}, nil, invalidInput()
			}
			before, err := a.manager.Group(ctx, p, m.ID)
			if err != nil {
				return groupRow{}, nil, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return groupRow{}, nil, err
			}
			after, err := a.manager.UpdateGroup(ctx, p, m.ID, m.Revision, identity.GroupPatch{}.WithName(name).WithPermissions(ids...))
			if err != nil {
				return groupRow{}, nil, operationError(err)
			}
			return groupRow{after, true}, changedFields(fields, groupInitial(before), groupInitial(after)), nil
		},
		Delete: func(ctx context.Context, p auth.Principal, m admin.Mutation) (groupRow, error) {
			before, err := a.manager.Group(ctx, p, m.ID)
			if err != nil {
				return groupRow{}, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return groupRow{}, err
			}
			_, err = a.manager.DeleteGroup(ctx, p, m.ID, m.Revision, a.deletions.Groups)
			if err != nil {
				return groupRow{}, operationError(err)
			}
			return groupRow{before, true}, nil
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
		Create: func(ctx context.Context, p auth.Principal, values forms.Values) (identity.PermissionProfile, error) {
			code, ok := values.String("code")
			name, present := values.String("name")
			if !ok || !present {
				return identity.PermissionProfile{}, invalidInput()
			}
			value, err := a.manager.CreatePermission(ctx, p, identity.NewPermissionCreate(code, name))
			return value, operationError(err)
		},
		Update: func(ctx context.Context, p auth.Principal, m admin.Mutation, values forms.Values) (identity.PermissionProfile, []string, error) {
			code, ok := values.String("code")
			name, present := values.String("name")
			if !ok || !present {
				return identity.PermissionProfile{}, nil, invalidInput()
			}
			before, err := a.manager.Permission(ctx, p, m.ID)
			if err != nil {
				return identity.PermissionProfile{}, nil, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return identity.PermissionProfile{}, nil, err
			}
			after, err := a.manager.UpdatePermission(ctx, p, m.ID, m.Revision, identity.PermissionPatch{}.WithCode(code).WithName(name))
			if err != nil {
				return identity.PermissionProfile{}, nil, operationError(err)
			}
			return after, changedFields(fields, permissionInitial(before), permissionInitial(after)), nil
		},
		Delete: func(ctx context.Context, p auth.Principal, m admin.Mutation) (identity.PermissionProfile, error) {
			before, err := a.manager.Permission(ctx, p, m.ID)
			if err != nil {
				return identity.PermissionProfile{}, operationError(err)
			}
			if err := checkRevision(m.Revision, before.Revision); err != nil {
				return identity.PermissionProfile{}, err
			}
			_, err = a.manager.DeletePermission(ctx, p, m.ID, m.Revision, a.deletions.Permissions)
			if err != nil {
				return identity.PermissionProfile{}, operationError(err)
			}
			return before, nil
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

func formChoices(choices []identity.ManagementChoice, err error) ([]forms.Choice, error) {
	if err != nil {
		return nil, operationError(err)
	}
	result := make([]forms.Choice, len(choices))
	for i, choice := range choices {
		result[i] = forms.Choice{Value: forms.Integer(choice.ID), Label: choice.Label}
	}
	return result, nil
}
