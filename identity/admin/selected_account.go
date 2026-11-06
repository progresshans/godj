package identityadmin

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/identity/internal/forminput"
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
// Existing-object pages display the password login state from the same
// authorized profile, with a separate name when a real field uses that name.
// The selected LastLogin field is displayed only when the owner explicitly
// includes it in ReadFields; it remains absent from create/edit inputs.
type AccountRegistration struct {
	Slug       string
	ListFields []string
	// CreationLoginInput optionally narrows the declared login's creation Form
	// input. It does not change storage, editing or the owner's unique policy.
	CreationLoginInput accountmodel.CreationLoginInput
	// ReportFileEdit is required when the edit owner accepts uploads. It
	// receives every attempted publication and the final account outcome before
	// HTTP error mapping can discard either. The application owns retention and
	// reconciliation; this callback does not authorize deletion or retry. It
	// runs outside every DB scope, including on a canceled request, and must be
	// safe for concurrent calls. A reporting error remains an execution error.
	ReportFileEdit func(context.Context, AccountFileEditReport) error
	// ReportFileCreate has the same retention/failure contract for creation.
	// It is required only when the creation form accepts actual uploads.
	ReportFileCreate func(context.Context, AccountFileCreationReport) error
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
	reader, editor, deleter := management.Reader(), management.Editor(), management.Deleter()
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
	dimensions, err := ir.ImageDimensionOwners(metadata)
	if err != nil {
		return err
	}
	for dimension, owner := range dimensions {
		if (slices.Contains(editor.Fields(), owner) || slices.Contains(list, owner)) && !slices.Contains(reader.ListFields(), dimension) {
			return &admin.ConfigError{Path: "account.read_fields", Code: "missing_image_dimension"}
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
	for _, field := range changeSpec.Fields() {
		if field.AcceptsUpload() && presentation.ReportFileEdit == nil {
			return &admin.ConfigError{Path: "account.files", Code: "missing_reporter"}
		}
	}
	creation, err := accountmodel.NewCreationForm(management, accountmodel.CreationFormConfig{AllowUnusablePassword: true, LoginInput: presentation.CreationLoginInput})
	if err != nil {
		return err
	}
	creationSpec := creation.Spec()
	for _, field := range creationSpec.Fields() {
		if field.AcceptsUpload() && presentation.ReportFileCreate == nil {
			return &admin.ConfigError{Path: "account.create_files", Code: "missing_reporter"}
		}
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
	statusName := forminput.AuxiliaryNames(metadata)("password_usable", "_status")
	readOnly := []admin.ReadOnlyField[accountmodel.Profile[M]]{{Name: statusName, Label: "Password login", Value: func(profile accountmodel.Profile[M]) (string, error) {
		if profile.PasswordUsable() {
			return "Enabled", nil
		}
		return "Disabled", nil
	}}}
	if field := description.LastLoginField; field != "" && slices.Contains(reader.Fields(), field) {
		readOnly = append(readOnly, admin.ReadOnlyField[accountmodel.Profile[M]]{Name: field, Label: "Last login", Value: func(profile accountmodel.Profile[M]) (string, error) {
			value, present := profile.View().Get(field)
			if !present {
				return "", &admin.ConfigError{Path: "account.last_login", Code: "missing_snapshot_value"}
			}
			if value.IsNull() {
				return "Never", nil
			}
			instant, valid := value.DateTime()
			if !valid {
				return "", &admin.ConfigError{Path: "account.last_login", Code: "invalid_snapshot_value"}
			}
			return instant.UTC().Format(time.RFC3339Nano), nil
		}})
	}
	return admin.RegisterModel(builder, admin.ModelConfig[accountmodel.Profile[M]]{
		AppLabel: description.Identity.AppLabel, Slug: presentation.Slug, Model: metadata,
		FormFields: change.Fields, FormOverrides: change.Overrides, ListFields: list, RevisionField: description.RevisionField,
		Permissions:           admin.Permissions{View: policy.View, Add: policy.Add, Change: policy.Change, Delete: policy.Delete},
		AdditionalPermissions: policy.Additional, AdditionalAddPermissions: []auth.Permission{policy.Change},
		ReadOnlyFields: readOnly,
		RelatedChoices: changeChoices,
		CreateForm:     &admin.FormConfig{Definition: creation.Definition(), RelatedChoices: addChoices}, Commands: commands,
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
			for _, field := range metadata.ManyToMany {
				if keys, loaded := row.View().ReadCollection(field); loaded {
					values[field.Name] = integerValues(keys)
				}
			}
			return admin.NewObject(row.ID(), label, values)
		},
		Initial: func(row accountmodel.Profile[M]) (map[string]forms.Value, error) {
			initial, err := formmodel.InitialView(objects, changeSpec, row.View())
			if err != nil {
				return nil, err
			}
			for dimension, owner := range dimensions {
				if !slices.Contains(change.Fields, owner) {
					continue
				}
				value, present := row.View().Get(dimension)
				if !present {
					return nil, &admin.ConfigError{Path: "account.initial", Code: "missing_image_dimension"}
				}
				if value.IsNull() {
					initial[dimension] = forms.Null()
				} else if integer, valid := value.Integer(); valid {
					initial[dimension] = forms.Integer(integer)
				} else {
					return nil, &admin.ConfigError{Path: "account.initial", Code: "invalid_image_dimension"}
				}
			}
			return initial, nil
		},
		ValidateCreate: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
			return operationError(creation.Check(ctx, actor, bound))
		},
		ValidateChange: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm) error {
			return operationError(management.CheckChangeForm(ctx, actor, mutation.ID, mutation.Revision, bound))
		},
		Create: func(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, error) {
			result, publications, err := creation.Create(ctx, actor, bound)
			if len(publications) != 0 {
				report := newAccountFileCreationReport(result, publications, err)
				if reported := presentation.ReportFileCreate(ctx, report); reported != nil {
					err = errors.Join(err, reported)
				}
			}
			return admin.MutationResult{ID: result.ID, Revision: result.Revision}, operationError(err)
		},
		Update: func(ctx context.Context, actor auth.Principal, mutation admin.Mutation, bound formmodel.BoundForm, _ admin.InlineSubmission) (admin.MutationResult, []string, error) {
			result, publications, err := editor.UpdateForm(ctx, actor, mutation.ID, mutation.Revision, bound)
			if len(publications) != 0 {
				report := newAccountFileEditReport(mutation.ID, result, publications, err)
				if reported := presentation.ReportFileEdit(ctx, report); reported != nil {
					err = errors.Join(err, reported)
				}
			}
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
