package model

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
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

// AccountEditValidator receives detached current/final models and final distinct
// memberships after the entire collected graph has been prepared. The candidate
// includes collected NULLs and its next revision. The reader is borrowed;
// it must not be retained, mutated, re-entered or used for external effects.
// The validator and clock must be safe for concurrent calls.
type AccountEditValidator[M any] func(context.Context, db.Queryer, M, M, RoleMemberships) (validation.Errors, error)

// AccountEditConfig explicitly selects scalar and single-valued profile fields, stored role flags
// and direct group/permission collections. No new field is automatically writable.
// Credential secrets, principal identity, revision, selected server timestamps,
// files and other collections are excluded. Every selected FK/OneToOne needs
// an explicit Relations policy. ChangePermission and every Additional
// grant cover the whole edit; a partial input cannot bypass either grant.
type AccountEditConfig[M any] struct {
	Fields                []string
	Relations             []ProfileRelation[M]
	ChangePermission      auth.Permission
	AdditionalPermissions []auth.Permission
	RelatedPermissions    RelatedRowPermissions
	Validate              AccountEditValidator[M]
	Now                   func() time.Time
}

// AccountEditor owns combined profile/role writes and the selected-model effects
// of their host graph. Each surviving changed owner advances once; direct effects
// have one audit and deleted/deactivated accounts lose their sessions in one
// native coordinated relation
// transaction. It uses the exact SessionStore's backend, credential binding
// and namespace policy. ProfileEditor does not change roles or memberships.
type AccountEditor[M any] struct{ state *accountEditState[M] }
type accountEditState[M any] struct {
	sessions                        *SessionStore[M]
	backend                         db.CoordinatedRelationAtomic
	overlay                         auth.Authorizer
	resource                        crud.Resource[M]
	metadata                        ir.Model
	allowed                         map[string]bool
	relations                       []ProfileRelation[M]
	required                        []auth.Permission
	validate                        AccountEditValidator[M]
	groupsChange, permissionsChange func(M, []int64) (orm.CollectionChange, error)
	lifecycle                       *deletionLifecycle[M]
}

func (AccountEditor[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.AccountEditor{redacted}"))
}

// NewAccountEditor is normally called by the generated project factory. Expected
// must match the session owner's normalized role; both collection capabilities
// must match its complete host source. Construction performs no I/O.
func NewAccountEditor[M, G, P, GL, PL any](sessionStore *SessionStore[M], expected Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], overlay auth.Authorizer, config AccountEditConfig[M]) (*AccountEditor[M], error) {
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
		validateAccountCollectionStorage(project, role, config.Fields),
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
	protected := map[string]bool{role.PrincipalID: true, role.Password: true, role.Revision: true, role.LastLogin: true, role.CreatedAt: true}
	allowed := make(map[string]bool, len(config.Fields))
	for _, name := range config.Fields {
		if name == "" || allowed[name] || protected[name] {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if name != role.Groups && name != role.Permissions {
			field, found := namedField(metadata, name)
			if !found || field.NonEditable || field.PrimaryKey || field.Kind.IsFile() {
				return nil, managementError(CodeInvalidConfig, nil)
			}
		}
		allowed[name] = true
	}
	relations, err := prepareProfileRelations(binding, config.Fields, config.Relations)
	if err != nil {
		return nil, err
	}
	resource, err := crud.New(binding.state.objects, crud.Config{Fields: config.Fields})
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	state := &accountEditState[M]{sessions: sessionStore, backend: backend, overlay: overlay, resource: resource, metadata: metadata, allowed: allowed, relations: relations, required: required, validate: config.Validate}
	if store.state.relatedLifecycle == nil {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	state.lifecycle, err = store.state.relatedLifecycle(sessionStore, expected, overlay, AccountDeleteConfig{
		Account: config.RelatedPermissions.Account, Group: config.RelatedPermissions.Group, Permission: config.RelatedPermissions.Permission, Now: now,
	})
	if err != nil {
		return nil, err
	}
	state.groupsChange = func(row M, keys []int64) (orm.CollectionChange, error) { return groups.SetKeysChange(row, keys) }
	state.permissionsChange = func(row M, keys []int64) (orm.CollectionChange, error) { return permissions.SetKeysChange(row, keys) }
	return &AccountEditor[M]{state: state}, nil
}

func (editor *AccountEditor[M]) Fields() []string {
	if editor == nil || editor.state == nil {
		return nil
	}
	return editor.state.resource.Fields()
}

// Manager supplies pure model metadata for Form/API preparation; it has no
// database binding and does not grant read or write authority.
func (editor *AccountEditor[M]) Manager() orm.Manager[M] {
	if editor == nil || editor.state == nil {
		return orm.Manager[M]{}
	}
	return editor.state.resource.Manager()
}

type AccountChange struct {
	ID, Revision    int64
	ChangedFields   []string
	RevokedSessions int
}

// Update accepts already-cleaned Form/JSON input and applies the selected login
// and email write policies before I/O. It preserves omitted fields; an explicitly
// empty collection clears it. The validator observes the entire proposed edit.
// No-op still checks current authority, stored integrity and revision. Only
// actual changes advance revision/audit. Flags and grants preserve the session
// stamp; an active-to-inactive transition revokes this domain's target sessions.
// Results are published only after confirmed commit, without automatic retry.
func (editor *AccountEditor[M]) Update(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (AccountChange, error) {
	if editor == nil || editor.state == nil {
		return AccountChange{}, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || revision <= 0 || revision == math.MaxInt64 {
		return AccountChange{}, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return AccountChange{}, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return AccountChange{}, managementError(CodePermission, nil)
	}
	prepared, err := editor.prepareInput(id, input)
	if err != nil {
		return AccountChange{}, err
	}
	state := editor.state
	store := state.sessions.state.credentials
	binding, role := store.state.binding, store.state.binding.state.role
	return managementRelationWrite(ctx, state.backend, func(work context.Context, scope db.RelationSession) (AccountChange, error) {
		currentActor, allowed, err := store.AuthorizeIn(work, scope, actor, state.overlay, state.required[0], state.required[1:]...)
		if err != nil {
			return AccountChange{}, err
		}
		if !allowed {
			return AccountChange{}, managementError(CodePermission, nil)
		}
		current, found, err := state.resource.Get(work, scope, id)
		if err != nil {
			return AccountChange{}, err
		}
		if !found {
			return AccountChange{}, managementError(CodeNotFound, nil)
		}
		objects := state.resource.Manager()
		before, err := objects.ModelValues(current)
		if err != nil {
			return AccountChange{}, err
		}
		actual, valid := before[role.Revision].Integer()
		if !valid || actual <= 0 {
			return AccountChange{}, invalidAccountCredential()
		}
		if actual != revision {
			return AccountChange{}, managementError(CodeConflict, nil)
		}
		memberships, err := store.state.relations.memberships(work, scope, id)
		if err != nil {
			return AccountChange{}, err
		}
		namespace, _ := currentActor.CredentialDomain()
		if _, err := binding.prepareCredentialForDomain(namespace, current, memberships.grants); err != nil {
			return AccountChange{}, err
		}
		model, err := objects.PrepareInput(prepared, &current)
		if err != nil {
			return AccountChange{}, err
		}
		candidate, err := model.Model()
		if err != nil {
			return AccountChange{}, err
		}
		values, err := objects.ModelValues(candidate)
		if err != nil {
			return AccountChange{}, err
		}
		wasActive, err := credentialBoolean(before, role.Active)
		if err != nil {
			return AccountChange{}, err
		}
		active, err := credentialBoolean(values, role.Active)
		if err != nil {
			return AccountChange{}, err
		}
		var changes []orm.CollectionChange
		for _, collection := range []struct {
			field   string
			prepare func(M, []int64) (orm.CollectionChange, error)
		}{{role.Groups, state.groupsChange}, {role.Permissions, state.permissionsChange}} {
			if keys, selected := prepared.Collection(collection.field); selected {
				change, err := collection.prepare(current, keys)
				if err != nil {
					return AccountChange{}, err
				}
				changes = append(changes, change)
			}
		}
		var batch orm.PreparedCollectionChanges
		var effects orm.CollectionChangeEffects
		if len(changes) != 0 {
			batch, err = orm.PrepareCollectionChangesInSession(work, scope, changes...)
			if err != nil {
				return AccountChange{}, err
			}
			if err := batch.ValidateSource(binding.state.project); err != nil {
				return AccountChange{}, err
			}
			effects = batch.Effects()
		}
		plan, err := state.lifecycle.prepare(work, scope, currentActor, effects.Deletions(), effects.Insertions(), &lifecycleEdit{
			key: deletionKey{role.Model, id}, revision: revision, required: state.required, fields: model.ChangedFields(), values: values, deactivate: wasActive && !active,
		})
		if err != nil {
			return AccountChange{}, err
		}
		final := map[string]query.Value{role.Revision: query.Integer(plan.rootRevision)}
		for _, field := range plan.root.nulls {
			final[field] = query.Null()
		}
		candidate, err = objects.ApplyValues(candidate, final)
		if err != nil {
			return AccountChange{}, err
		}
		if len(state.relations) != 0 {
			finalValues, err := objects.ModelValues(candidate)
			if err != nil {
				return AccountChange{}, err
			}
			if err := checkProfileRelations(work, scope, currentActor, ProfileRelationOrigin{operation: ProfileRelationChange, id: id}, state.relations, finalValues); err != nil {
				return AccountChange{}, err
			}
		}
		groups := plan.projection.keys(role.Model, id, role.Groups, memberships.groups)
		direct := plan.projection.keys(role.Model, id, role.Permissions, memberships.direct)
		if err := state.validateCandidate(work, scope, current, candidate, groups, direct); err != nil {
			return AccountChange{}, err
		}
		revoked := 0
		if len(changes) != 0 {
			err = batch.Apply(work, func(ctx context.Context, _ orm.CollectionChangeEffects) error {
				var err error
				revoked, err = plan.apply(ctx, scope)
				return err
			})
		} else {
			revoked, err = plan.apply(work, scope)
		}
		if err != nil {
			return AccountChange{}, err
		}
		// Advisory uniqueness observes the collected final graph. Any refusal
		// rolls back its earlier companion and relation writes in this scope.
		failures, err := model.ValidateUnique(work, scope, candidate)
		if err != nil {
			return AccountChange{}, err
		}
		if err := validation.Reject(failures, nil); err != nil {
			return AccountChange{}, err
		}
		if err := model.Save(work, scope, &candidate); err != nil {
			return AccountChange{}, err
		}
		if err := plan.verify(work, scope); err != nil {
			return AccountChange{}, err
		}
		return AccountChange{ID: id, Revision: plan.rootRevision, ChangedFields: lifecycleChangedFields(state.metadata, plan.root), RevokedSessions: revoked}, nil
	})
}

func (editor *AccountEditor[M]) prepareInput(id int64, input orm.ModelInput[M]) (orm.ModelInput[M], error) {
	state := editor.state
	model, valid := input.Model()
	if !valid || !model.Equal(state.metadata) {
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
	role := state.sessions.state.credentials.state.binding.state.role
	if err := normalizeAccountInput(state.metadata, role, values); err != nil {
		return orm.ModelInput[M]{}, err
	}
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
	prepared, err := state.resource.Manager().Input(values)
	if err != nil {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, err)
	}
	prepared, err = prepared.WithCollections(collections)
	if err != nil {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, err)
	}
	if key, bound := input.TargetKey(); bound {
		return prepared.WithTargetKey(key)
	}
	return prepared, nil
}

func (state *accountEditState[M]) validateCandidate(ctx context.Context, scope db.Session, current, candidate M, groups, permissions []int64) error {
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
