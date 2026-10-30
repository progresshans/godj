package model

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// FormDefinition retains actual model fields and the selected write
// normalization before Form length/choice validation. Password command inputs
// belong to the transport and are never model fields in this definition.
func (management *AccountManagement[M]) FormDefinition(action admin.Action) (formmodel.Definition, error) {
	if management == nil || management.state == nil {
		return formmodel.Definition{}, managementError(CodeInvalidConfig, nil)
	}
	state := management.state
	switch action {
	case admin.ActionAdd:
		return state.creator.FormDefinition()
	case admin.ActionChange:
		return state.editor.FormDefinition()
	default:
		return formmodel.Definition{}, managementError(CodeInvalidInput, nil)
	}
}

// CheckCreateForm performs read-only, current-authorized advisory checks even
// when unrelated fields are invalid. It does not generate principal IDs, hash
// passwords, call final model validators or reserve unique values.
func (management *AccountManagement[M]) CheckCreateForm(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
	return management.checkForm(ctx, actor, 0, 0, bound, true)
}

// CheckChangeForm verifies exact model/target/revision ownership. Current
// authority, target integrity, memberships and partial uniqueness share one
// snapshot. No diagnostic or conflict is published before cleanup succeeds.
func (management *AccountManagement[M]) CheckChangeForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	return management.checkForm(ctx, actor, id, revision, bound, false)
}

func (management *AccountManagement[M]) checkForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm, create bool) error {
	if err := management.validCall(ctx, actor); err != nil {
		return err
	}
	state := management.state
	store := state.reader.state.store
	binding, role := store.state.binding, store.state.binding.state.role
	metadata, objects := state.creator.state.metadata, binding.state.objects
	keyField, _ := primaryField(metadata)
	if !bound.Form().Bound() || bound.Form().ReadOnly() || !bound.Model().Equal(metadata) || !create && (revision <= 0 || revision == math.MaxInt64) {
		return managementError(CodeInvalidInput, nil)
	}
	key, present := bound.Candidate().Get(keyField.Name)
	if !present || create && !key.IsNull() {
		return managementError(CodeInvalidInput, nil)
	}
	if !create {
		version, versionPresent := bound.Candidate().Integer(role.Revision)
		actual, integer := key.AsInteger()
		if !integer || actual != id || !versionPresent || version != revision {
			return managementError(CodeInvalidInput, nil)
		}
	}
	allowed, required := state.editor.state.allowed, state.editor.state.required
	relations := state.editor.state.relations
	collections := state.editor.state.collections
	origin := ProfileRelationOrigin{operation: ProfileRelationChange, id: id}
	if create {
		allowed, required = state.creator.state.allowed, state.creator.state.required
		relations, origin = state.creator.state.relations, ProfileRelationOrigin{operation: ProfileRelationCreate}
		collections = state.creator.state.collections
	}
	values, err := bound.ValidationValues()
	if err != nil {
		return managementError(CodeInvalidInput, err)
	}
	for name := range values {
		if !allowed[name] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, name := range bound.FieldNames() {
		_, scalar := namedField(metadata, name)
		modelField := scalar
		for _, collection := range metadata.ManyToMany {
			modelField = modelField || collection.Name == name
		}
		if modelField && !allowed[name] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	// A cleaned upload filename is not its eventual storage name. Keep its
	// ordinary Form validation, but defer field/compound uniqueness involving
	// that file until the lifecycle owner has actual publication results.
	var pendingFiles []string
	for _, field := range metadata.Fields {
		if !field.Kind.IsFile() {
			continue
		}
		value, _ := bound.Form().Cleaned().Get(field.Name)
		file, _ := value.AsFile()
		if _, pending := file.Upload(); pending {
			pendingFiles = append(pendingFiles, field.Name)
		}
	}
	var currentActor auth.Principal
	result, err := managementSnapshot(ctx, store.state.backend, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
		current, allowed, err := store.AuthorizeIn(work, scope, actor, state.reader.state.overlay, required[0], required[1:]...)
		currentActor = current
		return current, allowed, err
	}, func(work context.Context, scope db.Queryer, namespace auth.CredentialDomain) (catalogFormCheck, bool, error) {
		var current *M
		var memberships accountMemberships
		if !create {
			row, present, err := state.reader.target(work, scope, id)
			if err != nil || !present {
				return catalogFormCheck{}, present, err
			}
			memberships, err = store.state.relations.memberships(work, scope, id)
			if err != nil {
				return catalogFormCheck{}, true, err
			}
			if _, err := binding.prepareCredentialForDomain(namespace, row, memberships.grants); err != nil {
				return catalogFormCheck{}, true, err
			}
			actual, err := objects.ModelValues(row)
			if err != nil {
				return catalogFormCheck{}, true, err
			}
			version, valid := actual[role.Revision].Integer()
			if !valid || version <= 0 {
				return catalogFormCheck{}, true, invalidAccountCredential()
			}
			if version != revision {
				return catalogFormCheck{conflict: true}, true, nil
			}
			current = &row
		}
		var failures validation.Errors
		if err := checkAccountCollectionForm(role, collections, bound); err != nil {
			diagnostics, rejected := validation.Rejected(err)
			if !rejected {
				return catalogFormCheck{}, true, err
			}
			failures = validation.Join(failures, diagnostics)
		}
		profile := make(map[string][]int64)
		for _, collection := range collections {
			name := collection.state.field.Name
			if keys, selected := bound.Form().Cleaned().Integers(name); selected && failures.ByField(validation.Field(name)).Empty() {
				slices.Sort(keys)
				profile[name] = slices.Compact(keys)
			}
		}
		if err := checkProfileCollections(work, scope, currentActor, origin, collections, profile); err != nil {
			diagnostics, rejected := validation.Rejected(err)
			if !rejected || errors.Unwrap(err) != nil {
				return catalogFormCheck{}, true, err
			}
			failures = validation.Join(failures, diagnostics)
		}
		if err := checkProfileRelations(work, scope, currentActor, origin, relations, values); err != nil {
			diagnostics, rejected := validation.Rejected(err)
			if !rejected || errors.Unwrap(err) != nil {
				return catalogFormCheck{}, true, err
			}
			failures = validation.Join(failures, diagnostics)
		}
		for _, field := range metadata.Fields {
			value, selected := values[field.Name]
			if !selected {
				continue
			}
			if err := normalizeAccountInput(metadata, role, map[string]query.Value{field.Name: value}); err != nil {
				diagnostics, rejected := validation.Rejected(err)
				if !rejected || errors.Unwrap(err) != nil {
					return catalogFormCheck{}, true, err
				}
				failures = validation.Join(failures, diagnostics)
			}
		}
		for _, selection := range []struct {
			name        string
			maximum     int
			destination *[]int64
			check       func(context.Context, db.Queryer, []int64) error
		}{
			{role.Groups, MaximumAccountGroups, &memberships.groups, store.state.relations.checkGroups},
			{role.Permissions, auth.MaximumPermissions, &memberships.direct, store.state.relations.checkPermissions},
		} {
			keys, selected := bound.Form().Cleaned().Integers(selection.name)
			if !selected {
				continue
			}
			if len(keys) > selection.maximum {
				failures = validation.Join(failures, validation.NewErrors(validation.New(validation.Field(selection.name), "max_items")))
				continue
			}
			slices.Sort(keys)
			keys = slices.Compact(keys)
			if err := selection.check(work, scope, keys); err != nil {
				diagnostics, rejected := validation.Rejected(err)
				if !rejected || errors.Unwrap(err) != nil {
					return catalogFormCheck{}, true, err
				}
				failures = validation.Join(failures, diagnostics)
				continue
			}
			*selection.destination = keys
		}
		checked, err := bound.WithErrors(failures)
		if err != nil {
			return catalogFormCheck{}, true, err
		}
		// The write owner repeats the complete proposed-union check. Here a
		// valid pair can additionally report its effective-grant limit without
		// treating invalid selections as empty assignments.
		if checked.Form().Errors().ByField(validation.Field(role.Groups)).Empty() && checked.Form().Errors().ByField(validation.Field(role.Permissions)).Empty() {
			_, err := store.state.relations.proposed(work, scope, memberships.groups, memberships.direct)
			if errors.Is(err, errAccountGrantLimit) {
				failures = validation.Join(failures, validation.NewErrors(validation.New(validation.Field(role.Permissions), "max_items")))
			} else if err != nil {
				return catalogFormCheck{}, true, err
			}
		}
		checked, err = bound.WithErrors(failures)
		if err != nil {
			return catalogFormCheck{}, true, err
		}
		unique, err := checked.CheckDatabase(work, formmodel.DatabaseChecks{
			UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				for _, name := range pendingFiles {
					delete(values, name)
				}
				if err := normalizeAccountInput(metadata, role, values); err != nil {
					return validation.Errors{}, err
				}
				diagnostics, err := objects.ValidateUniqueFields(ctx, scope, values, current)
				if err != nil {
					return validation.Errors{}, err
				}
				if create && state.creator.state.config.CaseInsensitiveLogin && diagnostics.ByField(validation.Field(role.Login)).Empty() {
					if login, selected := values[role.Login].String(); selected {
						exists, err := objects.Using(scope).Filter(store.state.login.IExact(login)).Exists(ctx)
						if err != nil {
							return validation.Errors{}, err
						}
						if exists {
							diagnostics = validation.Join(diagnostics, validation.NewErrors(validation.New(validation.Field(role.Login), "unique")))
						}
					}
				}
				return diagnostics, nil
			},
			Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				for _, name := range pendingFiles {
					delete(values, name)
				}
				if err := normalizeAccountInput(metadata, role, values); err != nil {
					return validation.Errors{}, err
				}
				return objects.ValidateUniqueConstraints(ctx, scope, values, current)
			},
		})
		return catalogFormCheck{failures: validation.Join(failures, unique)}, true, err
	})
	if err != nil {
		return err
	}
	if result.conflict {
		return managementError(CodeConflict, nil)
	}
	return validation.Reject(result.failures, nil)
}
