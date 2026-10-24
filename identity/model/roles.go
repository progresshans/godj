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
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/validation"
)

// RoleMemberships owns a complete proposed selection. Its accessors return
// copies, and diagnostic formatting never prints membership keys.
type RoleMemberships struct{ state *roleMembershipState }
type roleMembershipState struct{ groups, permissions []int64 }

func (RoleMemberships) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.RoleMemberships{redacted}"))
}
func (selection RoleMemberships) Groups() []int64 {
	if selection.state == nil {
		return nil
	}
	return slices.Clone(selection.state.groups)
}
func (selection RoleMemberships) Permissions() []int64 {
	if selection.state == nil {
		return nil
	}
	return slices.Clone(selection.state.permissions)
}

// RoleValidator receives detached models and owned proposed membership after
// current authorization, revision and choice checks. The reader is borrowed;
// it must not be retained, mutated, re-entered or used for external effects.
// The validator and clock must be safe for concurrent calls.
type RoleValidator[M any] func(context.Context, db.Queryer, M, M, RoleMemberships) (validation.Errors, error)

// RoleConfig explicitly selects writable active/staff/superuser fields and
// direct group/permission collections from the selected account role. Constant
// roles have no input field. Profile, credential and server-owned fields are
// rejected. ChangePermission and every Additional grant are required.
type RoleConfig[M any] struct {
	Fields                []string
	ChangePermission      auth.Permission
	AdditionalPermissions []auth.Permission
	Validate              RoleValidator[M]
	Now                   func() time.Time
}

// RoleEditor owns account-role writes, revisions, audit and deactivation's
// session revocation in one native coordinated relation transaction. It uses
// the exact SessionStore's backend, credential binding and namespace policy.
type RoleEditor[M any] struct{ state *roleState[M] }
type roleState[M any] struct {
	sessions                    *SessionStore[M]
	backend                     db.CoordinatedRelationAtomic
	overlay                     auth.Authorizer
	resource                    crud.Resource[M]
	metadata                    ir.Model
	allowed                     map[string]bool
	required                    []auth.Permission
	validate                    RoleValidator[M]
	now                         func() time.Time
	saveGroups, savePermissions func(context.Context, db.Session, M, []int64) error
}

func (RoleEditor[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.RoleEditor{redacted}"))
}

// NewRoleEditor is normally called by the generated project factory. Expected
// must match the session owner's normalized role; both collection capabilities
// must match its complete host source. Construction performs no I/O.
func NewRoleEditor[M, G, P, GL, PL any](sessionStore *SessionStore[M], expected Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], overlay auth.Authorizer, config RoleConfig[M]) (*RoleEditor[M], error) {
	if sessionStore == nil || sessionStore.state == nil || expected.state == nil || nilModelValue(overlay) || len(config.Fields) == 0 || len(config.Fields) > admin.MaximumChangedFields {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	store := sessionStore.state.credentials
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	backend, ok := store.state.backend.(db.CoordinatedRelationAtomic)
	if !ok || nilModelValue(backend) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	binding := store.state.binding
	if binding.state.roleFingerprint != expected.state.roleFingerprint {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role, project := binding.state.role, binding.state.project
	for _, err := range []error{
		groups.ValidateSource(project, role.Model, role.Groups),
		permissions.ValidateSource(project, role.Model, role.Permissions),
		validateRoleDeleteTopology(project, role, config.Fields),
	} {
		if err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	required, err := auth.RequiredPermissions(config.ChangePermission, config.AdditionalPermissions...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if _, err := admin.PrepareEventTemplate("role-startup", role.Model.AppLabel+"."+role.Model.ModelName, admin.ActionChange, config.Fields, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		if name == "" || allowed[name] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if name != role.Groups && name != role.Permissions {
			field, found := namedField(metadata, name)
			if !found || field.Kind != ir.FieldBoolean || field.NonEditable || field.PrimaryKey ||
				(name != role.Active.Field && name != role.Staff.Field && name != role.Superuser.Field) {
				return nil, managementError(CodeInvalidConfig, nil)
			}
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
	state := &roleState[M]{sessions: sessionStore, backend: backend, overlay: overlay, resource: resource, metadata: metadata, allowed: allowed, required: required, validate: config.Validate, now: now}
	state.saveGroups = func(ctx context.Context, scope db.Session, row M, keys []int64) error {
		collection, err := groups.InSession(scope, row)
		if err != nil {
			return err
		}
		return collection.SetKeys(ctx, keys)
	}
	state.savePermissions = func(ctx context.Context, scope db.Session, row M, keys []int64) error {
		collection, err := permissions.InSession(scope, row)
		if err != nil {
			return err
		}
		return collection.SetKeys(ctx, keys)
	}
	return &RoleEditor[M]{state: state}, nil
}

func (editor *RoleEditor[M]) Fields() []string {
	if editor == nil || editor.state == nil {
		return nil
	}
	return editor.state.resource.Fields()
}

// Manager supplies pure model metadata for Form/API preparation; it has no
// database binding and does not grant read or write authority.
func (editor *RoleEditor[M]) Manager() orm.Manager[M] {
	if editor == nil || editor.state == nil {
		return orm.Manager[M]{}
	}
	return editor.state.resource.Manager()
}

type RoleChange struct {
	ID, Revision    int64
	ChangedFields   []string
	RevokedSessions int
}

// Update preserves omitted fields; an explicitly empty collection clears it.
// No-op still checks current authority, stored integrity and revision. Only
// actual changes advance revision/audit. Flags and grants preserve the session
// stamp; an active-to-inactive transition revokes this domain's target sessions.
// Results are published only after confirmed commit, without automatic retry.
func (editor *RoleEditor[M]) Update(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (RoleChange, error) {
	if editor == nil || editor.state == nil {
		return RoleChange{}, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || revision <= 0 || revision == math.MaxInt64 {
		return RoleChange{}, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return RoleChange{}, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return RoleChange{}, managementError(CodePermission, nil)
	}
	prepared, err := editor.prepareInput(id, input)
	if err != nil {
		return RoleChange{}, err
	}
	state := editor.state
	store := state.sessions.state.credentials
	binding, role := store.state.binding, store.state.binding.state.role
	return managementRelationWrite(ctx, state.backend, func(work context.Context, scope db.RelationSession) (RoleChange, error) {
		currentActor, allowed, err := store.AuthorizeIn(work, scope, actor, state.overlay, state.required[0], state.required[1:]...)
		if err != nil {
			return RoleChange{}, err
		}
		if !allowed {
			return RoleChange{}, managementError(CodePermission, nil)
		}
		namespace, _ := currentActor.CredentialDomain()
		groupKeys, groupsSelected := prepared.Collection(role.Groups)
		permissionKeys, permissionsSelected := prepared.Collection(role.Permissions)
		var groupsChanged, permissionsChanged, deactivate bool
		var principalID string
		options := crud.SaveOptions[M]{Before: func(ctx context.Context, session db.Session, current, candidate *M) error {
			if current == nil || candidate == nil {
				return managementError(CodePersistence, nil)
			}
			objects := state.resource.Manager()
			before, err := objects.ModelValues(*current)
			if err != nil {
				return err
			}
			actual, valid := before[role.Revision].Integer()
			if !valid || actual <= 0 {
				return invalidAccountCredential()
			}
			if actual != revision {
				return managementError(CodeConflict, nil)
			}
			key, present, err := objects.PrimaryKey(*current)
			currentID, integer := key.Integer()
			if err != nil || !present || !integer || currentID != id {
				return invalidAccountCredential()
			}
			currentMemberships, err := store.state.relations.memberships(ctx, session, id)
			if err != nil {
				return err
			}
			credential, err := binding.prepareCredentialForDomain(namespace, *current, currentMemberships.grants)
			if err != nil {
				return err
			}
			proposedGroups, proposedPermissions := currentMemberships.groups, currentMemberships.direct
			if groupsSelected {
				proposedGroups = groupKeys
				groupsChanged = !slices.Equal(groupKeys, currentMemberships.groups)
			}
			if permissionsSelected {
				proposedPermissions = permissionKeys
				permissionsChanged = !slices.Equal(permissionKeys, currentMemberships.direct)
			}
			grants := currentMemberships.grants
			if groupsSelected || permissionsSelected {
				grants, err = store.state.relations.proposed(ctx, session, proposedGroups, proposedPermissions)
				if errors.Is(err, errAccountGrantLimit) {
					field := role.Groups
					if permissionsSelected {
						field = role.Permissions
					}
					return profileInputError(field, "max_items")
				}
				if err != nil {
					return err
				}
			}
			if _, err := binding.prepareCredentialForDomain(namespace, *candidate, grants); err != nil {
				return err
			}
			if err := state.validateCandidate(ctx, session, *current, *candidate, proposedGroups, proposedPermissions); err != nil {
				return err
			}
			after, err := objects.ModelValues(*candidate)
			if err != nil {
				return err
			}
			wasActive, err := credentialBoolean(before, role.Active)
			if err != nil {
				return err
			}
			active, err := credentialBoolean(after, role.Active)
			if err != nil {
				return err
			}
			deactivate = wasActive && !active
			principalID = credential.Principal().ID()
			changed := groupsChanged || permissionsChanged
			for _, field := range state.metadata.Fields {
				if !before[field.Name].Equal(after[field.Name]) {
					changed = true
				}
			}
			if changed {
				updated, err := objects.ApplyValues(*candidate, map[string]query.Value{role.Revision: query.Integer(revision + 1)})
				if err != nil {
					return err
				}
				*candidate = updated
			}
			return nil
		}}
		for _, collection := range []struct {
			name     string
			selected bool
			changed  *bool
			save     func(context.Context, db.Session, M, []int64) error
		}{
			{role.Groups, groupsSelected, &groupsChanged, state.saveGroups}, {role.Permissions, permissionsSelected, &permissionsChanged, state.savePermissions},
		} {
			if !collection.selected {
				continue
			}
			options.Collections = append(options.Collections, crud.CollectionSaver[M]{Field: collection.name, Save: func(ctx context.Context, session db.Session, row M, keys []int64) (bool, error) {
				if !*collection.changed {
					return false, nil
				}
				if err := collection.save(ctx, session, row, keys); err != nil {
					return false, err
				}
				return true, nil
			}})
		}
		mutation, err := state.resource.UpdateInSession(work, scope, id, prepared, options)
		if err == crud.ErrNotFound {
			return RoleChange{}, managementError(CodeNotFound, nil)
		}
		if err != nil {
			return RoleChange{}, err
		}
		values, err := state.resource.Manager().ModelValues(mutation.Value)
		if err != nil {
			return RoleChange{}, err
		}
		actual, valid := values[role.Revision].Integer()
		if !valid || actual <= 0 {
			return RoleChange{}, invalidAccountCredential()
		}
		changed := slices.DeleteFunc(slices.Clone(mutation.ChangedFields), func(name string) bool { return name == role.Revision })
		revoked := 0
		if deactivate {
			revoked, err = state.sessions.RevokeIn(work, scope, principalID, sessions.ID{})
			if err != nil {
				return RoleChange{}, err
			}
		}
		if len(changed) != 0 {
			event, err := accountaudit.Prepare(currentActor, role.Model.AppLabel+"."+role.Model.ModelName, id, admin.ActionChange, changed, state.now())
			if err != nil {
				return RoleChange{}, err
			}
			if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
				return RoleChange{}, err
			}
		}
		return RoleChange{ID: id, Revision: actual, ChangedFields: changed, RevokedSessions: revoked}, nil
	})
}

func (editor *RoleEditor[M]) prepareInput(id int64, input orm.ModelInput[M]) (orm.ModelInput[M], error) {
	state := editor.state
	model, valid := input.Model()
	if !valid || !model.Equal(state.metadata) {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
	}
	if key, bound := input.TargetKey(); bound && !key.Equal(query.Integer(id)) {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
	}
	for name, value := range input.Values() {
		if _, valid := value.Boolean(); !state.allowed[name] || !valid {
			return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
		}
	}
	role := state.sessions.state.credentials.state.binding.state.role
	collections := input.Collections()
	for name, keys := range collections {
		if !state.allowed[name] || (name != role.Groups && name != role.Permissions) {
			return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
		}
		maximum := auth.MaximumPermissions
		if name == role.Groups {
			maximum = MaximumAccountGroups
		}
		if len(keys) > maximum {
			return orm.ModelInput[M]{}, profileInputError(name, "max_items")
		}
		slices.Sort(keys)
		collections[name] = slices.Compact(keys)
	}
	return input.WithCollections(collections)
}

func (state *roleState[M]) validateCandidate(ctx context.Context, scope db.Session, current, candidate M, groups, permissions []int64) error {
	validator, ok := scope.(db.SessionValidator)
	if !ok || nilModelValue(validator) {
		return managementError(CodeInvalidConfig, nil)
	}
	if state.validate == nil {
		return errors.Join(ctx.Err(), validator.ValidateSession(ctx))
	}
	objects := state.resource.Manager()
	ownedCurrent, err := objects.ApplyValues(current, nil)
	if err != nil {
		return err
	}
	ownedCandidate, err := objects.ApplyValues(candidate, nil)
	if err != nil {
		return err
	}
	selection := RoleMemberships{state: &roleMembershipState{slices.Clone(groups), slices.Clone(permissions)}}
	diagnostics, err := state.validate(ctx, scope, ownedCurrent, ownedCandidate, selection)
	if err := errors.Join(err, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return err
	}
	if !diagnostics.Empty() {
		return validation.Reject(diagnostics, nil)
	}
	return nil
}
