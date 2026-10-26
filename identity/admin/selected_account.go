package identityadmin

import (
	"context"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

// AccountRegistration controls presentation inside the management owner's
// explicit exposure. Login and every edit field must be readable. An empty
// ListFields uses the reader's scalar fields; create-only fields need not be
// exposed by a later read. Creation also has private password/confirmation and
// an explicit unusable-password choice, all kept outside the stored model.
type AccountRegistration struct {
	Slug       string
	ListFields []string
}

// RegisterAccount binds the actual selected account and all lifecycle owners
// to common Admin. It is pure construction; no full credential row or legacy
// User adapter crosses the read boundary. Publish the Builder after success.
func RegisterAccount[M any](builder *admin.Builder, management *accountmodel.AccountManagement[M], presentation AccountRegistration) error {
	description, err := management.Description()
	if err != nil {
		return err
	}
	objects := management.Manager()
	metadata, err := objects.Metadata()
	if err != nil {
		return err
	}
	reader, creator, editor, deleter := management.Reader(), management.Creator(), management.Editor(), management.Deleter()
	for _, name := range append(editor.Fields(), description.LoginField) {
		if !slices.Contains(reader.Fields(), name) {
			return &admin.ConfigError{Path: "account.read_fields", Code: "missing_form_or_label"}
		}
	}
	list := slices.Clone(presentation.ListFields)
	if len(list) == 0 {
		list = reader.ListFields()
	}
	for _, name := range list {
		if name != description.KeyField && name != description.RevisionField && !slices.Contains(reader.ListFields(), name) {
			return &admin.ConfigError{Path: "account.list_fields", Code: "not_readable_scalar"}
		}
	}
	change, err := management.FormDefinition(admin.ActionChange)
	if err != nil {
		return err
	}
	changeSpec, err := change.Spec(metadata)
	if err != nil {
		return err
	}
	creation, err := management.FormDefinition(admin.ActionAdd)
	if err != nil {
		return err
	}
	passwords, err := accountCreationForm(metadata, creation, management.Passwords().MaxPasswordBytes())
	if err != nil {
		return err
	}
	addChoices, err := management.ChoiceSources(admin.ActionAdd)
	if err != nil {
		return err
	}
	changeChoices, err := management.ChoiceSources(admin.ActionChange)
	if err != nil {
		return err
	}
	commands, err := PasswordCommands(management.Passwords())
	if err != nil {
		return err
	}
	scalars := reader.ListFields()
	for _, name := range []string{description.KeyField, description.RevisionField} {
		if !slices.Contains(scalars, name) {
			scalars = append(scalars, name)
		}
	}
	projector, err := admin.NewModelProjector(metadata, func(row accountmodel.Profile[M], field ir.Field) (query.Value, bool) {
		switch field.Name {
		case description.KeyField:
			return query.Integer(row.ID()), true
		case description.RevisionField:
			return query.Integer(row.Revision()), true
		default:
			return row.View().Get(field.Name)
		}
	}, scalars...)
	if err != nil {
		return err
	}
	policy := description.Permissions
	return admin.RegisterModel(builder, admin.ModelConfig[accountmodel.Profile[M]]{
		AppLabel: description.Identity.AppLabel, Slug: presentation.Slug, Model: metadata,
		FormFields: change.Fields, FormOverrides: change.Overrides, ListFields: list, RevisionField: description.RevisionField,
		Permissions:           admin.Permissions{View: policy.View, Add: policy.Add, Change: policy.Change, Delete: policy.Delete},
		AdditionalPermissions: policy.Additional, AdditionalAddPermissions: []auth.Permission{policy.Change},
		RelatedChoices: changeChoices,
		CreateForm:     &admin.FormConfig{Definition: passwords.definition, RelatedChoices: addChoices}, Commands: commands,
		List: func(ctx context.Context, actor auth.Principal, request admin.ListRequest) (admin.Page[accountmodel.Profile[M]], error) {
			page, err := reader.List(ctx, actor, request.Offset, request.Limit)
			return admin.Page[accountmodel.Profile[M]]{Items: page.Items, Total: page.Total, Offset: request.Offset, Limit: request.Limit}, operationError(err)
		},
		Get: func(ctx context.Context, actor auth.Principal, id int64) (accountmodel.Profile[M], bool, error) {
			row, err := reader.Get(ctx, actor, id)
			if failure, ok := err.(*accountmodel.Error); ok && failure.Code == accountmodel.CodeNotFound {
				return row, false, nil
			}
			return row, err == nil, operationError(err)
		},
		Snapshot: func(row accountmodel.Profile[M]) (admin.Object, error) {
			value, present := row.View().Get(description.LoginField)
			label, textual := value.String()
			if !present || !textual {
				return admin.Object{}, &admin.ConfigError{Path: "account.label", Code: "missing"}
			}
			object, err := projector.Project(row, row.ID(), label)
			if err != nil {
				return admin.Object{}, err
			}
			members, _ := object.Values().Members()
			values := make(map[string]templates.Value, len(members)+2)
			for _, member := range members {
				values[member.Name()] = member.Value()
			}
			for _, name := range []string{description.GroupsField, description.PermissionsField} {
				if keys, loaded := row.View().Collection(name); loaded {
					values[name] = integerValues(keys)
				}
			}
			return admin.NewObject(row.ID(), label, values)
		},
		Initial: func(row accountmodel.Profile[M]) (map[string]forms.Value, error) {
			return formmodel.InitialView(objects, changeSpec, row.View())
		},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return operationError(management.CheckCreateForm(ctx, actor, bound))
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return operationError(management.CheckChangeForm(ctx, actor, mutation.ID, mutation.Revision, bound))
		},
		Create: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, error) {
			input, err := formmodel.PrepareInput(objects, bound)
			if err != nil {
				return admin.MutationResult{}, err
			}
			values, err := bound.Input()
			if err != nil {
				return admin.MutationResult{}, err
			}
			unusable, present := values.Boolean(passwords.unusable)
			if !present {
				return admin.MutationResult{}, invalidInput()
			}
			var prepared accountmodel.PreparedCreation[M]
			if unusable {
				prepared, err = creator.PrepareUnusable(ctx, actor, input)
			} else {
				raw, a := values.String(passwords.first)
				confirm, b := values.String(passwords.confirm)
				if !a || !b || raw != confirm {
					return admin.MutationResult{}, invalidInput()
				}
				prepared, err = creator.Prepare(ctx, actor, input, raw)
			}
			if err != nil {
				return admin.MutationResult{}, selectedPasswordInputError(err, passwords.confirm)
			}
			result, err := creator.Commit(ctx, actor, prepared)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, selectedPasswordInputError(err, passwords.confirm)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			input, err := formmodel.PrepareInput(objects, bound)
			if err != nil {
				return admin.MutationResult{}, nil, err
			}
			result, err := editor.Update(ctx, actor, mutation.ID, mutation.Revision, input)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, result.ChangedFields, operationError(err)
		},
		Delete: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation) (admin.MutationResult, error) {
			result, err := deleter.Delete(ctx, actor, mutation.ID, mutation.Revision)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, operationError(err)
		},
		History: func(ctx context.Context, actor auth.Principal, id int64, request admin.HistoryRequest) ([]admin.AuditEntry, error) {
			entries, err := reader.History(ctx, actor, id, request.Limit)
			if err != nil {
				return nil, operationError(err)
			}
			result := make([]admin.AuditEntry, len(entries))
			for index, entry := range entries {
				result[index] = admin.AuditEntry{Sequence: uint64(entry.Sequence()), ActorID: entry.ActorID(), Model: entry.Model(), ObjectID: entry.ObjectID(), Action: entry.Action(), ChangedFields: entry.ChangedFields()}
			}
			return result, nil
		},
	})
}
