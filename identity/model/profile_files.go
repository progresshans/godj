package model

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
	"github.com/progresshans/godj/validation"
)

// ProfileFileConfig declares the explicit publication capability for an upload
// field. Name is pure and receives a detached candidate with old file references
// and no password hash or principal identity. Backend is borrowed and never
// closed by an owner.
// All callbacks must be safe for concurrent use.
//
// Canonical file choices select existing names and must not have Backend or
// Name. Image choices instead require ImageChoices for bounded content checks
// during Form binding. ImageLimits optionally configures the bounded image
// inspection policy; it retains the shared codec implementation and ceilings.
type ProfileFileConfig[M any] struct {
	Backend      storage.Backend
	Name         func(M, uploads.File) (string, error)
	ImageChoices forms.ImageChoiceInspector
	ImageLimits  *uploads.ImageLimits
}

// ProfileFile is immutable policy bound to a complete selected-account source.
// It grants neither current account authority nor permission to delete files.
type ProfileFile[M any] struct{ state *profileFileState[M] }
type profileFileState[M any] struct {
	field    ir.Field
	source   string
	role     [32]byte
	saver    formmodel.FileSaver[M]
	override []formmodel.Override
}

func (ProfileFile[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ProfileFile{redacted}"))
}

// NewProfileFile validates source and Form policy without invoking a callback,
// opening a file or touching a database. Storage configuration is not Schema IR.
func NewProfileFile[M any](binding Binding[M], name string, config ProfileFileConfig[M]) (ProfileFile[M], error) {
	var zero ProfileFile[M]
	if binding.state == nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	objects, role := binding.state.objects, binding.state.role
	metadata, err := objects.Metadata()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	field, found := namedField(metadata, name)
	if !found || !field.Kind.IsFile() || field.PrimaryKey || field.NonEditable {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if field.WidthField == role.Revision || field.HeightField == role.Revision {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if field.Choices == nil {
		if nilModelValue(config.Backend) || config.Name == nil || config.ImageChoices != nil {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	} else if !nilModelValue(config.Backend) || config.Name != nil || (field.Kind == ir.FieldImage) != (config.ImageChoices != nil) {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	var options []formmodel.OverrideOption
	if config.ImageChoices != nil {
		options = append(options, formmodel.WithImageChoiceInspector(config.ImageChoices))
	}
	if config.ImageLimits != nil {
		options = append(options, formmodel.WithImageLimits(*config.ImageLimits))
	}
	var overrides []formmodel.Override
	if len(options) != 0 {
		overrides = []formmodel.Override{formmodel.OverrideField(name, options...)}
	}
	if _, err := formmodel.NewSpecForFields(metadata, []string{name}, overrides...); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	source, err := binding.state.project.SourceSHA256()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	state := &profileFileState[M]{field: field.Clone(), source: source, role: binding.state.roleFingerprint, override: overrides}
	if field.Choices == nil {
		state.saver = formmodel.FileSaver[M]{Field: name, Backend: config.Backend, Name: func(candidate M, file uploads.File) (string, error) {
			// Naming is application policy, not a new way to observe a password
			// hash or principal/session identity from the internal current row.
			redacted, err := objects.ApplyValues(candidate, map[string]query.Value{role.Password: query.String(""), role.PrincipalID: query.String("")})
			if err != nil {
				return "", err
			}
			return config.Name(redacted, file)
		}}
	}
	return ProfileFile[M]{state: state}, nil
}

func prepareProfileFiles[M any](binding Binding[M], fields []string, configured []ProfileFile[M]) ([]ProfileFile[M], map[string]string, error) {
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, nil, managementError(CodeInvalidConfig, err)
	}
	dimensions, err := ir.ImageDimensionOwners(metadata)
	if err != nil {
		return nil, nil, managementError(CodeInvalidConfig, err)
	}
	for name := range dimensions {
		if slices.Contains(fields, name) {
			return nil, nil, managementError(CodeInvalidConfig, nil)
		}
	}
	needed := false
	for _, field := range metadata.Fields {
		needed = needed || field.Kind.IsFile() && slices.Contains(fields, field.Name)
	}
	if !needed && len(configured) == 0 {
		return nil, dimensions, nil
	}
	source, err := binding.state.project.SourceSHA256()
	if err != nil {
		return nil, nil, managementError(CodeInvalidConfig, err)
	}
	byName := make(map[string]ProfileFile[M], len(configured))
	for _, file := range configured {
		state := file.state
		if state == nil || state.source != source || state.role != binding.state.roleFingerprint || !slices.Contains(fields, state.field.Name) || byName[state.field.Name].state != nil {
			return nil, nil, managementError(CodeInvalidConfig, nil)
		}
		byName[state.field.Name] = file
	}
	var result []ProfileFile[M]
	for _, field := range metadata.Fields {
		if !field.Kind.IsFile() || !slices.Contains(fields, field.Name) {
			continue
		}
		file, present := byName[field.Name]
		if !present {
			return nil, nil, managementError(CodeInvalidConfig, nil)
		}
		result = append(result, file)
	}
	return result, dimensions, nil
}

func profileFileOverrides[M any](files []ProfileFile[M]) []formmodel.Override {
	var overrides []formmodel.Override
	for _, file := range files {
		overrides = append(overrides, file.state.override...)
	}
	return overrides
}

func profileFileNames[M any](files []ProfileFile[M]) []string {
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.state.field.Name
	}
	return names
}

func profileStoredFields(metadata ir.Model, fields []string, dimensions map[string]string) []string {
	result := slices.Clone(fields)
	for _, field := range metadata.Fields {
		if owner := dimensions[field.Name]; owner != "" && slices.Contains(fields, owner) {
			result = append(result, field.Name)
		}
	}
	return result
}

func profileFileSavers[M any](files []ProfileFile[M], pending []string) []formmodel.FileSaver[M] {
	var savers []formmodel.FileSaver[M]
	for _, file := range files {
		if slices.Contains(pending, file.state.field.Name) {
			savers = append(savers, file.state.saver)
		}
	}
	return savers
}

// checkProfileFileForm checks the whole declared exposure, not only successful
// fields. BoundForm owns verified upload/image capabilities and derived sizes;
// plain ModelInput cannot acquire that status by supplying stored-name strings.
func checkProfileFileForm(metadata ir.Model, allowed map[string]bool, dimensions map[string]string, revisionField string, id, revision int64, bound formmodel.BoundForm) error {
	if !bound.Model().Equal(metadata) || !bound.Form().Bound() || bound.Form().ReadOnly() || !bound.Form().Valid() {
		return managementError(CodeInvalidInput, nil)
	}
	input, err := bound.Input()
	if err != nil {
		return managementError(CodeInvalidInput, err)
	}
	if value, present := bound.Candidate().Get(revisionField); present {
		if actual, valid := value.AsInteger(); !valid || actual != revision {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			key, present := bound.Candidate().Integer(field.Name)
			if !present || key != id {
				return managementError(CodeInvalidInput, nil)
			}
		}
		if _, present := input.Get(field.Name); present && !allowed[field.Name] && !allowed[dimensions[field.Name]] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, field := range metadata.ManyToMany {
		if _, present := input.Get(field.Name); present && !allowed[field.Name] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, name := range bound.FieldNames() {
		if _, found := namedField(metadata, name); found && (!allowed[name] || dimensions[name] != "") {
			return managementError(CodeInvalidInput, nil)
		}
		for _, field := range metadata.ManyToMany {
			if field.Name == name && !allowed[name] {
				return managementError(CodeInvalidInput, nil)
			}
		}
	}
	return nil
}

// prepareProfileFileChange reads current authority, target integrity and revision
// before any publication. The native snapshot closes before returning the typed
// preparation; no transaction or upload lifetime escapes through this result.
func prepareProfileFileChange[M any](ctx context.Context, store *CredentialStore[M], actor auth.Principal, overlay auth.Authorizer, required []auth.Permission, relations []ProfileRelation[M], id, revision int64, bound formmodel.BoundForm) (formmodel.PreparedInstance[M], error) {
	type observation struct {
		prepared formmodel.PreparedInstance[M]
		conflict bool
		rejected error
	}
	var currentActor auth.Principal
	result, err := managementSnapshot(ctx, store.state.backend, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
		current, allowed, err := store.AuthorizeIn(work, scope, actor, overlay, required[0], required[1:]...)
		currentActor = current
		return current, allowed, err
	}, func(work context.Context, scope db.Queryer, namespace auth.CredentialDomain) (observation, bool, error) {
		binding := store.state.binding
		objects := binding.state.objects
		current, present, err := objects.Using(scope).Filter(store.state.key.Exact(id)).First(work)
		if err != nil || !present {
			return observation{}, present, err
		}
		memberships, err := store.state.relations.memberships(work, scope, id)
		if err != nil {
			return observation{}, true, err
		}
		if _, err := binding.prepareCredentialForDomain(namespace, current, memberships.grants); err != nil {
			return observation{}, true, err
		}
		values, err := objects.ModelValues(current)
		if err != nil {
			return observation{}, true, err
		}
		actual, valid := values[binding.state.role.Revision].Integer()
		if !valid || actual <= 0 {
			return observation{}, true, invalidAccountCredential()
		}
		if actual != revision {
			return observation{conflict: true}, true, nil
		}
		prepared, err := formmodel.PrepareInstance(objects, bound, &current)
		if err != nil {
			return observation{}, true, err
		}
		input := prepared.Input()
		for _, relation := range relations {
			name := relation.state.field.Name
			if value, present := input.Get(name); present {
				if value.IsNull() {
					values[name] = query.Null()
				} else if key, valid := value.AsInteger(); valid {
					values[name] = query.Integer(key)
				} else {
					return observation{}, true, managementError(CodeInvalidInput, nil)
				}
			}
		}
		check := func() error {
			if err := checkProfileRelations(work, scope, currentActor, ProfileRelationOrigin{operation: ProfileRelationChange, id: id}, relations, values); err != nil {
				return err
			}
			role := binding.state.role
			for _, selection := range []struct {
				name        string
				maximum     int
				destination *[]int64
				check       func(context.Context, db.Queryer, []int64) error
			}{{role.Groups, MaximumAccountGroups, &memberships.groups, store.state.relations.checkGroups}, {role.Permissions, auth.MaximumPermissions, &memberships.direct, store.state.relations.checkPermissions}} {
				if value, present := input.Get(selection.name); present {
					keys, valid := value.AsIntegers()
					if !valid {
						return managementError(CodeInvalidInput, nil)
					}
					if len(keys) > selection.maximum {
						return profileInputError(selection.name, "max_items")
					}
					slices.Sort(keys)
					keys = slices.Compact(keys)
					if err := selection.check(work, scope, keys); err != nil {
						return err
					}
					*selection.destination = keys
				}
			}
			_, err := store.state.relations.proposed(work, scope, memberships.groups, memberships.direct)
			if errors.Is(err, errAccountGrantLimit) {
				return profileInputError(role.Permissions, "max_items")
			}
			return err
		}
		if err := check(); err != nil {
			if _, rejected := validation.Rejected(err); rejected && errors.Unwrap(err) == nil {
				return observation{rejected: err}, true, nil
			}
			return observation{}, true, err
		}
		return observation{prepared: prepared}, true, nil
	})
	if err != nil {
		return formmodel.PreparedInstance[M]{}, err
	}
	if result.conflict {
		return formmodel.PreparedInstance[M]{}, managementError(CodeConflict, nil)
	}
	if result.rejected != nil {
		return formmodel.PreparedInstance[M]{}, result.rejected
	}
	return result.prepared, nil
}
