package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// ProfileValidator checks current application policy after authorization and
// fresh target lookup, before uniqueness or DML. Both model values are detached.
// The reader is borrowed: the validator may read current choices, but must not
// mutate, retain it, acquire another transaction or publish external effects.
// The callback must be safe for concurrent calls to this immutable editor.
type ProfileValidator[M any] func(context.Context, db.Queryer, M, M) (validation.Errors, error)

// ProfileConfig explicitly selects scalar, single-valued relation and file
// fields. Files require a ProfileFile policy and the UpdateForm path; raw
// ModelInput cannot assign their stored names. Credential secrets,
// principal identity, revision, server timestamps, role and permission fields
// cannot be selected. A new model field is never automatically writable.
//
// Input must have passed its Form/JSON cleaning policy; this service retains
// that typed intent and rechecks current model policy through Validate. It does
// not repeat transport parsing or silently treat collection intent as scalars.
type ProfileConfig[M any] struct {
	Fields                []string
	Relations             []ProfileRelation[M]
	Files                 []ProfileFile[M]
	ChangePermission      auth.Permission
	AdditionalPermissions []auth.Permission
	Validate              ProfileValidator[M]
	// Now is an optional pure server clock, safe for concurrent calls. It is
	// invoked only for an actual change; nil uses time.Now.
	Now func() time.Time
}

// ProfileEditor performs selected-account profile edits. It owns authorization,
// revision and domain-qualified audit in one coordinated native transaction.
// Credential/role/catalog writes and collection edits have separate policies;
// this editor rejects them and does not implement account creation or deletion.
type ProfileEditor[M any] struct{ state *profileState[M] }
type profileState[M any] struct {
	store      *CredentialStore[M]
	backend    db.CoordinatedAtomic
	overlay    auth.Authorizer
	resource   crud.Resource[M]
	metadata   ir.Model
	allowed    map[string]bool
	relations  []ProfileRelation[M]
	files      []ProfileFile[M]
	dimensions map[string]string
	required   []auth.Permission
	validate   ProfileValidator[M]
	now        func() time.Time
}

func (ProfileEditor[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ProfileEditor{redacted}"))
}

func NewProfileEditor[M any](store *CredentialStore[M], overlay auth.Authorizer, config ProfileConfig[M]) (*ProfileEditor[M], error) {
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if nilModelValue(overlay) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if len(config.Fields) > admin.MaximumChangedFields {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	backend, ok := store.state.backend.(db.CoordinatedAtomic)
	if !ok || nilModelValue(backend) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	required, err := auth.RequiredPermissions(config.ChangePermission, config.AdditionalPermissions...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	binding := store.state.binding
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	role := binding.state.role
	protected := map[string]bool{role.PrincipalID: true, role.Password: true, role.Revision: true, role.LastLogin: true, role.CreatedAt: true,
		role.Active.Field: true, role.Staff.Field: true, role.Superuser.Field: true, role.Groups: true, role.Permissions: true}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		field, found := namedField(metadata, name)
		if !found || protected[name] || allowed[name] || field.PrimaryKey || field.NonEditable {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		allowed[name] = true
	}
	relations, err := prepareProfileRelations(binding, config.Fields, config.Relations)
	if err != nil {
		return nil, err
	}
	if err := requireEditableProfileRelations(relations); err != nil {
		return nil, err
	}
	files, dimensions, err := prepareProfileFiles(binding, config.Fields, config.Files)
	if err != nil {
		return nil, err
	}
	storedFields := profileStoredFields(metadata, config.Fields, dimensions)
	if _, err := admin.PrepareEventTemplate("profile-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionChange, storedFields, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	resource, err := crud.New(binding.state.objects, crud.Config{Fields: config.Fields})
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &ProfileEditor[M]{state: &profileState[M]{store: store, backend: backend, overlay: overlay, resource: resource,
		metadata: metadata, allowed: allowed, relations: relations, files: files, dimensions: dimensions, required: required, validate: config.Validate, now: now}}, nil
}

// Fields returns an owned selection for building the model Form and serializer.
func (editor *ProfileEditor[M]) Fields() []string {
	if editor == nil || editor.state == nil {
		return nil
	}
	return editor.state.resource.Fields()
}

// ProfileChange contains only confirmed operation metadata. It never returns
// the account row, principal identity, password hash or session material.
type ProfileChange struct {
	ID            int64
	Revision      int64
	ChangedFields []string
}

// Update rebases prepared input on the fresh authorized row and requires the
// caller's expected revision. Input source and any original target binding are
// checked before I/O. Login and email use their selected write policies. When
// they share a field, login normalization precedes email normalization. Neither
// policy creates an implicit case-insensitive uniqueness or login lookup.
//
// A no-op still checks current domain, authority, target and revision, but does
// not increment revision or append audit. Any failure publishes a zero result.
// Unknown transaction outcomes remain errors and are never retried. Confirmed
// commit wins over a cancellation arriving after the owner reports success.
func (editor *ProfileEditor[M]) Update(ctx context.Context, actor auth.Principal, id, expectedRevision int64, input orm.ModelInput[M]) (ProfileChange, error) {
	return editor.update(ctx, actor, id, expectedRevision, input, false)
}

func (editor *ProfileEditor[M]) update(ctx context.Context, actor auth.Principal, id, expectedRevision int64, input orm.ModelInput[M], files bool) (ProfileChange, error) {
	if editor == nil || editor.state == nil {
		return ProfileChange{}, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || expectedRevision <= 0 || expectedRevision == math.MaxInt64 {
		return ProfileChange{}, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return ProfileChange{}, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return ProfileChange{}, managementError(CodePermission, nil)
	}
	prepared, err := editor.prepareInput(id, input, files)
	if err != nil {
		return ProfileChange{}, err
	}
	state := editor.state
	// Image dimensions are server-derived changes, never CRUD's exposed form
	// fields. Apply them in Before so validation sees the actual final candidate,
	// including noneditable dimension columns, without making those writable.
	derived := make(map[string]query.Value)
	if files {
		values := prepared.Values()
		for name := range state.dimensions {
			if value, present := values[name]; present {
				derived[name] = value
				delete(values, name)
			}
		}
		if len(derived) != 0 {
			prepared, err = state.resource.Manager().Input(values)
			if err != nil {
				return ProfileChange{}, err
			}
			prepared, err = prepared.WithTargetKey(query.Integer(id))
			if err != nil {
				return ProfileChange{}, err
			}
		}
	}
	return managementWrite(ctx, state.backend, func(work context.Context, session db.Session) (ProfileChange, error) {
		currentActor, allowed, err := state.store.AuthorizeIn(work, session, actor, state.overlay, state.required[0], state.required[1:]...)
		if err != nil {
			return ProfileChange{}, err
		}
		if !allowed {
			return ProfileChange{}, managementError(CodePermission, nil)
		}
		namespace, _ := currentActor.CredentialDomain()
		mutation, err := state.resource.UpdateInSession(work, session, id, prepared, crud.SaveOptions[M]{
			Before: func(work context.Context, session db.Session, current, candidate *M) error {
				if len(derived) != 0 {
					updated, err := state.resource.Manager().ApplyValues(*candidate, derived)
					if err != nil {
						return err
					}
					*candidate = updated
				}
				return editor.beforeUpdate(work, session, currentActor, namespace, expectedRevision, current, candidate)
			},
		})
		if err == crud.ErrNotFound {
			return ProfileChange{}, managementError(CodeNotFound, nil)
		}
		if err != nil {
			return ProfileChange{}, err
		}
		values, err := state.resource.Manager().ModelValues(mutation.Value)
		if err != nil {
			return ProfileChange{}, err
		}
		revision, ok := values[state.store.state.binding.state.role.Revision].Integer()
		if !ok {
			return ProfileChange{}, invalidAccountCredential()
		}
		changed := slices.DeleteFunc(slices.Clone(mutation.ChangedFields), func(name string) bool { return name == state.store.state.binding.state.role.Revision })
		if len(changed) != 0 {
			role := state.store.state.binding.state.role
			event, err := accountaudit.Prepare(currentActor, role.Model.AppLabel+"."+role.Model.ModelName, id, admin.ActionChange, changed, state.now())
			if err != nil {
				return ProfileChange{}, err
			}
			if _, err := accountaudit.AppendIn(work, session, event); err != nil {
				return ProfileChange{}, err
			}
		}
		return ProfileChange{ID: id, Revision: revision, ChangedFields: changed}, nil
	})
}

func (editor *ProfileEditor[M]) beforeUpdate(ctx context.Context, session db.Session, actor auth.Principal, namespace auth.CredentialDomain, revision int64, current, candidate *M) error {
	if current == nil || candidate == nil {
		return managementError(CodePersistence, nil)
	}
	validator, borrowed := session.(db.SessionValidator)
	if !borrowed || nilModelValue(validator) {
		return managementError(CodeInvalidConfig, nil)
	}
	state := editor.state
	binding := state.store.state.binding
	objects := state.resource.Manager()
	before, err := objects.ModelValues(*current)
	if err != nil {
		return err
	}
	actual, ok := before[binding.state.role.Revision].Integer()
	if !ok || actual <= 0 {
		return invalidAccountCredential()
	}
	if actual != revision {
		return managementError(CodeConflict, nil)
	}
	key, present, err := objects.PrimaryKey(*current)
	id, integer := key.Integer()
	if err != nil || !present || !integer {
		return invalidAccountCredential()
	}
	permissions, err := state.store.state.permissions(ctx, session, id)
	if err != nil {
		return err
	}
	if _, err := binding.prepareCredentialForDomain(namespace, *current, permissions); err != nil {
		return err
	}
	if len(state.relations) != 0 {
		proposed, err := objects.ModelValues(*candidate)
		if err != nil {
			return err
		}
		if err := checkProfileRelations(ctx, session, actor, ProfileRelationOrigin{operation: ProfileRelationChange, id: id}, state.relations, proposed); err != nil {
			return err
		}
	}
	if state.validate != nil {
		ownedCurrent, err := objects.ApplyValues(*current, nil)
		if err != nil {
			return err
		}
		ownedCandidate, err := objects.ApplyValues(*candidate, nil)
		if err != nil {
			return err
		}
		diagnostics, validationErr := state.validate(ctx, session, ownedCurrent, ownedCandidate)
		if err := errors.Join(validationErr, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
			return err
		}
		if !diagnostics.Empty() {
			return validation.Reject(diagnostics, nil)
		}
	}
	after, err := objects.ModelValues(*candidate)
	if err != nil {
		return err
	}
	for _, field := range state.metadata.Fields {
		if !before[field.Name].Equal(after[field.Name]) {
			updated, err := objects.ApplyValues(*candidate, map[string]query.Value{binding.state.role.Revision: query.Integer(revision + 1)})
			if err != nil {
				return err
			}
			*candidate = updated
			break
		}
	}
	return nil
}

func (editor *ProfileEditor[M]) prepareInput(id int64, input orm.ModelInput[M], files bool) (orm.ModelInput[M], error) {
	state := editor.state
	model, valid := input.Model()
	if !valid || !model.Equal(state.metadata) || len(input.Collections()) != 0 {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
	}
	if key, bound := input.TargetKey(); bound && !key.Equal(query.Integer(id)) {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
	}
	values := input.Values()
	for name := range values {
		field, _ := namedField(state.metadata, name)
		if !state.allowed[name] && !(files && state.allowed[state.dimensions[name]]) || !files && (field.Kind.IsFile() || state.dimensions[name] != "") {
			return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
		}
	}
	role := state.store.state.binding.state.role
	if err := normalizeAccountInput(state.metadata, role, values); err != nil {
		return orm.ModelInput[M]{}, err
	}
	result, err := state.resource.Manager().Input(values)
	if err != nil {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, err)
	}
	if key, bound := input.TargetKey(); bound {
		return result.WithTargetKey(key)
	}
	return result, nil
}

func (editor *ProfileEditor[M]) FileFields() []string {
	if editor == nil || editor.state == nil {
		return nil
	}
	return profileFileNames(editor.state.files)
}

// FormDefinition retains the selected input and explicit image policy.
func (editor *ProfileEditor[M]) FormDefinition() (formmodel.Definition, error) {
	if editor == nil || editor.state == nil {
		return formmodel.Definition{}, managementError(CodeInvalidConfig, nil)
	}
	return editor.state.store.state.binding.formDefinition(editor.Fields(), profileFileOverrides(editor.state.files))
}

// UpdateForm keeps file publication separate from the final account write.
// Current authority, target integrity and revision are checked before storage
// I/O and again at the final native write. The returned publication list must be
// retained on success, rejection and uncertain database outcomes. No file is
// automatically deleted or retried, and upload lifetime remains request-owned.
func (editor *ProfileEditor[M]) UpdateForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) (ProfileChange, []formmodel.FilePublication, error) {
	if editor == nil || editor.state == nil {
		return ProfileChange{}, nil, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || revision <= 0 || revision == math.MaxInt64 {
		return ProfileChange{}, nil, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return ProfileChange{}, nil, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return ProfileChange{}, nil, managementError(CodePermission, nil)
	}
	state := editor.state
	if err := checkProfileFileForm(state.metadata, state.allowed, state.dimensions, state.store.state.binding.state.role.Revision, id, revision, bound); err != nil {
		return ProfileChange{}, nil, err
	}
	if len(state.files) == 0 {
		input, err := formmodel.PrepareInput(state.resource.Manager(), bound)
		if err != nil {
			return ProfileChange{}, nil, err
		}
		result, err := editor.Update(ctx, actor, id, revision, input)
		return result, nil, err
	}
	prepared, err := prepareProfileFileChange(ctx, state.store, actor, state.overlay, state.required, state.relations, id, revision, bound)
	if err != nil {
		return ProfileChange{}, nil, err
	}
	resolved, publications, err := prepared.SaveFiles(ctx, profileFileSavers(state.files, prepared.PendingFiles())...)
	if err != nil {
		return ProfileChange{}, publications, managementError(CodePersistence, err)
	}
	input, err := resolved.ModelInput()
	if err != nil {
		return ProfileChange{}, publications, managementError(CodeInvalidInput, err)
	}
	result, err := editor.update(ctx, actor, id, revision, input, true)
	return result, publications, err
}

func profileInputError(field, code string) error {
	return validation.Reject(validation.NewErrors(validation.New(validation.Field(field), validation.Code(code))), nil)
}
