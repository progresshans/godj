package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/unicode16"
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

// ProfileConfig selects scalar profile fields explicitly. Credential secrets,
// principal identity, revision, server timestamps, role and permission fields
// cannot be selected. A new model field is never automatically writable.
//
// Input must have passed its Form/JSON cleaning policy; this service retains
// that typed intent and rechecks current model policy through Validate. It does
// not repeat transport parsing or silently treat collection intent as scalars.
type ProfileConfig[M any] struct {
	Fields                []string
	ChangePermission      auth.Permission
	AdditionalPermissions []auth.Permission
	Validate              ProfileValidator[M]
	// Now is an optional pure server clock, safe for concurrent calls. It is
	// invoked only for an actual change; nil uses time.Now.
	Now func() time.Time
}

// ProfileEditor performs selected-account scalar edits. It owns authorization,
// revision and domain-qualified audit in one coordinated native transaction.
// Credential/role/catalog writes and collection edits have separate policies;
// this editor rejects them and does not implement account creation or deletion.
type ProfileEditor[M any] struct{ state *profileState[M] }
type profileState[M any] struct {
	store    *CredentialStore[M]
	backend  db.CoordinatedAtomic
	overlay  auth.Authorizer
	resource crud.Resource[M]
	metadata ir.Model
	allowed  map[string]bool
	required []auth.Permission
	validate ProfileValidator[M]
	now      func() time.Time
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
	if _, err := admin.PrepareEventTemplate("profile-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionChange, config.Fields, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	protected := map[string]bool{role.PrincipalID: true, role.Password: true, role.Revision: true, role.LastLogin: true, role.CreatedAt: true,
		role.Active.Field: true, role.Staff.Field: true, role.Superuser.Field: true, role.Groups: true, role.Permissions: true}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		field, found := namedField(metadata, name)
		if !found || protected[name] || allowed[name] || field.PrimaryKey || field.NonEditable || field.Kind.IsFile() || field.Relation != nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		allowed[name] = true
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
		metadata: metadata, allowed: allowed, required: required, validate: config.Validate, now: now}}, nil
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
	prepared, err := editor.prepareInput(id, input)
	if err != nil {
		return ProfileChange{}, err
	}
	state := editor.state
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
				return editor.beforeUpdate(work, session, namespace, expectedRevision, current, candidate)
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

func (editor *ProfileEditor[M]) beforeUpdate(ctx context.Context, session db.Session, namespace auth.CredentialDomain, revision int64, current, candidate *M) error {
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

func (editor *ProfileEditor[M]) prepareInput(id int64, input orm.ModelInput[M]) (orm.ModelInput[M], error) {
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
		if !state.allowed[name] {
			return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
		}
	}
	role := state.store.state.binding.state.role
	for _, field := range state.metadata.Fields {
		value, present := values[field.Name]
		if !present {
			continue
		}
		if field.Name == role.Login {
			login, valid := value.String()
			if !valid || auth.ValidateUsername(login) != nil {
				return orm.ModelInput[M]{}, profileInputError(field.Name, "invalid")
			}
			if role.WriteLoginNormalization == ir.AccountUnicode16NFKC {
				login = unicode16.NFKC(login)
			}
			if auth.ValidateUsername(login) != nil {
				return orm.ModelInput[M]{}, profileInputError(field.Name, "invalid")
			}
			value = query.String(login)
			values[field.Name] = value
		}
		if field.Name == role.Email {
			email, valid := value.String()
			if !valid || len(email) > 4096 || !utf8.ValidString(email) || strings.ContainsRune(email, 0) {
				return orm.ModelInput[M]{}, profileInputError(field.Name, "invalid")
			}
			if role.WriteEmailNormalization == ir.AccountUnicode16EmailDomain {
				trimmed := unicode16.TrimSpace(email)
				if at := strings.LastIndexByte(trimmed, '@'); at >= 0 {
					email = trimmed[:at+1] + unicode16.Lower(trimmed[at+1:])
				}
			}
			if field.Name == role.Login && auth.ValidateUsername(email) != nil {
				return orm.ModelInput[M]{}, profileInputError(field.Name, "invalid")
			}
			value = query.String(email)
			values[field.Name] = value
		}
		if text, ok := value.String(); ok {
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
				return orm.ModelInput[M]{}, profileInputError(field.Name, "invalid")
			}
			if field.MaxLength > 0 && utf8.RuneCountInString(text) > field.MaxLength {
				return orm.ModelInput[M]{}, profileInputError(field.Name, "max_length")
			}
		}
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

func profileInputError(field, code string) error {
	return validation.Reject(validation.NewErrors(validation.New(validation.Field(field), validation.Code(code))), nil)
}
