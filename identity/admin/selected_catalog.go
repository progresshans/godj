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

// CatalogPresentation selects routing and columns within the catalog's explicit
// exposure. Empty ListFields uses all selected scalar read fields. Labels and
// writable fields must also be readable; no adapter widens the owner's policy.
type CatalogPresentation struct {
	Slug       string
	ListFields []string
}

type CatalogRegistration struct {
	Groups, Permissions CatalogPresentation
}

// RegisterCatalog binds the actual selected Group/Permission types and current
// lifecycle owner to the common Admin. It performs no I/O. Publish the Builder
// only after registration succeeds; a failure may leave earlier registrations.
func RegisterCatalog[G, P any](builder *admin.Builder, catalog *accountmodel.Catalog[G, P], config CatalogRegistration) error {
	groups, permissions := catalog.Groups(), catalog.Permissions()
	groupDescription, err := groups.Description()
	if err != nil {
		return err
	}
	var addChoices, changeChoices []admin.RelatedChoices
	if slices.Contains(groups.WriteFields(), groupDescription.CollectionField) {
		add, err := catalog.GroupPermissionChoiceSource(admin.ActionAdd)
		if err != nil {
			return err
		}
		change, err := catalog.GroupPermissionChoiceSource(admin.ActionChange)
		if err != nil {
			return err
		}
		addChoices, changeChoices = []admin.RelatedChoices{add}, []admin.RelatedChoices{change}
	}
	group, err := catalogAdminConfig(groups, config.Groups, addChoices, changeChoices)
	if err != nil {
		return err
	}
	permission, err := catalogAdminConfig(permissions, config.Permissions, nil, nil)
	if err != nil {
		return err
	}
	if err := admin.RegisterModel(builder, group); err != nil {
		return err
	}
	return admin.RegisterModel(builder, permission)
}

func catalogAdminConfig[M any](resource accountmodel.CatalogResource[M], presentation CatalogPresentation, addChoices, changeChoices []admin.RelatedChoices) (admin.ModelConfig[accountmodel.CatalogRecord[M]], error) {
	var zero admin.ModelConfig[accountmodel.CatalogRecord[M]]
	description, err := resource.Description()
	if err != nil {
		return zero, err
	}
	model, err := resource.Manager().Metadata()
	if err != nil {
		return zero, err
	}
	read, write := resource.ReadFields(), resource.WriteFields()
	for _, name := range append(slices.Clone(write), description.LabelField) {
		if !slices.Contains(read, name) {
			return zero, &admin.ConfigError{Path: "catalog.read_fields", Code: "missing_form_or_label"}
		}
	}
	list := slices.Clone(presentation.ListFields)
	if len(list) == 0 {
		list = resource.ListFields()
	}
	for _, name := range list {
		if name != description.KeyField && name != description.RevisionField && !slices.Contains(resource.ListFields(), name) {
			return zero, &admin.ConfigError{Path: "catalog.list_fields", Code: "not_readable_scalar"}
		}
	}
	form, err := formmodel.NewSpecForFields(model, write)
	if err != nil {
		return zero, err
	}
	scalars := resource.ListFields()
	for _, name := range []string{description.KeyField, description.RevisionField} {
		if !slices.Contains(scalars, name) {
			scalars = append(scalars, name)
		}
	}
	projector, err := admin.NewModelProjector(model, func(row accountmodel.CatalogRecord[M], field ir.Field) (query.Value, bool) {
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
		return zero, err
	}
	policy := description.Permissions
	return admin.ModelConfig[accountmodel.CatalogRecord[M]]{
		AppLabel: description.Identity.AppLabel, Slug: presentation.Slug, Model: model,
		FormFields: write, ListFields: list, RevisionField: description.RevisionField,
		Permissions:           admin.Permissions{View: policy.View, Add: policy.Add, Change: policy.Change, Delete: policy.Delete},
		AdditionalPermissions: policy.Additional,
		RelatedChoices:        changeChoices,
		CreateForm:            &admin.FormConfig{Definition: formmodel.Definition{Fields: write}, RelatedChoices: addChoices},
		List: func(ctx context.Context, actor auth.Principal, request admin.ListRequest) (admin.Page[accountmodel.CatalogRecord[M]], error) {
			page, err := resource.List(ctx, actor, request.Offset, request.Limit)
			return admin.Page[accountmodel.CatalogRecord[M]]{Items: page.Items, Total: page.Total, Offset: request.Offset, Limit: request.Limit}, operationError(err)
		},
		Get: func(ctx context.Context, actor auth.Principal, id int64) (accountmodel.CatalogRecord[M], bool, error) {
			row, err := resource.Get(ctx, actor, id)
			if failure, ok := err.(*accountmodel.Error); ok && failure.Code == accountmodel.CodeNotFound {
				return row, false, nil
			}
			return row, err == nil, operationError(err)
		},
		Snapshot: func(row accountmodel.CatalogRecord[M]) (admin.Object, error) {
			value, present := row.View().Get(description.LabelField)
			label, textual := value.String()
			if !present || !textual {
				return admin.Object{}, &admin.ConfigError{Path: "catalog.label", Code: "missing"}
			}
			object, err := projector.Project(row, row.ID(), label)
			if err != nil {
				return admin.Object{}, err
			}
			keys, selected := row.View().Collection(description.CollectionField)
			if !selected {
				return object, nil
			}
			members, _ := object.Values().Members()
			values := make(map[string]templates.Value, len(members)+1)
			for _, member := range members {
				values[member.Name()] = member.Value()
			}
			values[description.CollectionField] = integerValues(keys)
			return admin.NewObject(row.ID(), label, values)
		},
		Initial: func(row accountmodel.CatalogRecord[M]) (map[string]forms.Value, error) {
			return formmodel.InitialView(resource.Manager(), form, row.View())
		},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return operationError(resource.CheckCreateForm(ctx, actor, bound))
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return operationError(resource.CheckChangeForm(ctx, actor, mutation.ID, mutation.Revision, bound))
		},
		Create: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, error) {
			input, err := formmodel.PrepareInput(resource.Manager(), bound)
			if err != nil {
				return admin.MutationResult{}, err
			}
			result, err := resource.Create(ctx, actor, input)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, operationError(err)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			input, err := formmodel.PrepareInput(resource.Manager(), bound)
			if err != nil {
				return admin.MutationResult{}, nil, err
			}
			result, err := resource.Update(ctx, actor, mutation.ID, mutation.Revision, input)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, result.ChangedFields, operationError(err)
		},
		Delete: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation) (admin.MutationResult, error) {
			result, err := resource.Delete(ctx, actor, mutation.ID, mutation.Revision)
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, operationError(err)
		},
		History: func(ctx context.Context, actor auth.Principal, id int64, request admin.HistoryRequest) ([]admin.AuditEntry, error) {
			entries, err := resource.History(ctx, actor, id, request.Limit)
			if err != nil {
				return nil, operationError(err)
			}
			result := make([]admin.AuditEntry, len(entries))
			for index, entry := range entries {
				result[index] = admin.AuditEntry{Sequence: uint64(entry.Sequence()), ActorID: entry.ActorID(), Model: entry.Model(), ObjectID: entry.ObjectID(), Action: entry.Action(), ChangedFields: entry.ChangedFields()}
			}
			return result, nil
		},
	}, nil
}
