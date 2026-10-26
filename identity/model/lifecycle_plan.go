package model

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
)

type lifecycleEdit struct {
	key        deletionKey
	revision   int64
	required   []auth.Permission
	fields     []string
	values     map[string]query.Value
	deactivate bool
	created    bool
}

type lifecycleWritePlan interface {
	apply(context.Context, db.RelationSession) (int, error)
	verify(context.Context, db.RelationSession) error
	rootState() (*deletionTouch, int64, *lifecycleProjection)
}

func (plan *lifecyclePlan[M]) rootState() (*deletionTouch, int64, *lifecycleProjection) {
	return plan.root, plan.rootRevision, plan.projection
}

type lifecyclePlan[M any] struct {
	owner             *deletionLifecycle[M]
	projection        *lifecycleProjection
	ordered           []*deletionTouch
	stored            []deletionStored
	verifiers         []lifecycleVerifier
	events            []accountaudit.Event
	deactivate        bool
	root              *deletionTouch
	rootRevision      int64
	applied, verified bool
}

// prepare validates the full current/final selected-model union and prepares
// all events without writing. The caller owns the native fence and propagates
// every later error, including the final typed and membership readback.
func (state *deletionLifecycle[M]) prepare(ctx context.Context, scope db.RelationSession, actor auth.Principal, effects orm.RelationDeleteEffects, insertions []orm.CollectionInsertEffect, root *lifecycleEdit) (*lifecyclePlan[M], error) {
	projection := &lifecycleProjection{effects: make(map[deletionKey]orm.RelationDeleteEffect), sets: make(map[lifecycleMembershipKey]lifecycleMembershipSet), touches: make(map[deletionKey]*deletionTouch)}
	var required []auth.Permission
	if root != nil {
		required = append(required, root.required...)
	}
	for _, effect := range effects.Rows() {
		key := deletionKey{effect.Model, effect.Key}
		if _, repeated := projection.effects[key]; repeated {
			return nil, managementError(CodePersistence, nil)
		}
		projection.effects[key] = effect
		model, selected := state.models[effect.Model]
		if !selected {
			continue
		}
		if !effect.Deleted && len(effect.NullFields) == 0 {
			return nil, managementError(CodePersistence, nil)
		}
		touch := &deletionTouch{key: key, deleted: effect.Deleted, direct: true, nulls: slices.Clone(effect.NullFields)}
		for _, name := range touch.nulls {
			field, found := namedField(model.metadata, name)
			if !found || !field.Nullable || field.Relation == nil || field.Relation.OnDelete != ir.DeleteSetNull {
				return nil, managementError(CodePersistence, nil)
			}
		}
		projection.touches[key] = touch
		if root != nil && key == root.key {
			// An update needs a surviving final target. Do not publish an edit
			// result for a row its own requested graph would delete.
			if effect.Deleted {
				return nil, managementError(CodeConflict, nil)
			}
			continue
		}
		grants := model.change
		if effect.Deleted {
			grants = model.remove
		}
		if len(grants) == 0 {
			return nil, managementError(CodePermission, nil)
		}
		required = append(required, grants...)
	}
	if root != nil {
		if _, exists := state.models[root.key.model]; !exists || root.revision <= 0 {
			return nil, managementError(CodePersistence, nil)
		}
		touch := projection.touches[root.key]
		if touch == nil {
			touch = &deletionTouch{key: root.key}
			projection.touches[root.key] = touch
		}
		touch.root, touch.direct, touch.fields, touch.created = true, true, slices.Clone(root.fields), root.created
	}
	if len(required) == 0 {
		return nil, managementError(CodePersistence, nil)
	}
	slices.Sort(required)
	required = slices.Compact(required)
	required, err := auth.RequiredPermissions(required[0], required[1:]...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	current, allowed, err := state.authorizeEffects(ctx, scope, actor, required, root != nil && root.created)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, managementError(CodePermission, nil)
	}
	domain, bound := current.CredentialDomain()
	if !bound {
		return nil, managementError(CodePersistence, nil)
	}
	for _, membership := range state.memberships {
		if err := membership.project(ctx, scope, projection, insertions); err != nil {
			return nil, err
		}
	}
	role := state.sessions.state.credentials.state.binding.state.role
	for _, membership := range state.memberships {
		if err := membership.affectedGroupMembers(ctx, scope, projection, role); err != nil {
			return nil, err
		}
	}
	plan := &lifecyclePlan[M]{owner: state, projection: projection}
	for _, touch := range projection.touches {
		plan.ordered = append(plan.ordered, touch)
	}
	slices.SortFunc(plan.ordered, func(a, b *deletionTouch) int { return compareDeletionKeys(a.key, b.key) })
	principals := make(map[string]bool)
	var now time.Time
	for _, touch := range plan.ordered {
		if touch.direct && touch.changed() {
			now = state.now()
			break
		}
	}
	for _, touch := range plan.ordered {
		row, err := state.models[touch.key.model].load(ctx, scope, domain, touch.key.id)
		if err != nil {
			return nil, err
		}
		if !touch.deleted && touch.changed() && row.revision == math.MaxInt64 || row.future == nil {
			return nil, managementError(CodePersistence, nil)
		}
		var values map[string]query.Value
		if touch.root {
			if row.revision != root.revision {
				return nil, managementError(CodeConflict, nil)
			}
			plan.root, plan.rootRevision, plan.deactivate = touch, row.revision, root.deactivate
			if touch.changed() && !touch.created {
				plan.rootRevision++
			}
			values = root.values
		}
		if touch.deleted && row.principalID != "" {
			if principals[row.principalID] {
				return nil, managementError(CodePersistence, nil)
			}
			principals[row.principalID] = true
		}
		verifier, err := row.future(ctx, scope, domain, projection, touch, values)
		if err != nil {
			return nil, err
		}
		plan.stored = append(plan.stored, row)
		plan.verifiers = append(plan.verifiers, verifier)
		if touch.direct && touch.changed() {
			action, fields := admin.ActionDelete, []string(nil)
			if !touch.deleted {
				action, fields = admin.ActionChange, lifecycleChangedFields(state.models[touch.key.model].metadata, touch)
			}
			if touch.created {
				action = admin.ActionAdd
			}
			event, err := accountaudit.Prepare(current, touch.key.model.AppLabel+"."+touch.key.model.ModelName, touch.key.id, action, fields, now)
			if err != nil {
				return nil, err
			}
			plan.events = append(plan.events, event)
		}
	}
	return plan, nil
}

func (state *deletionLifecycle[M]) authorizeEffects(ctx context.Context, scope db.RelationSession, actor auth.Principal, required []auth.Permission, created bool) (auth.Principal, bool, error) {
	store := state.sessions.state.credentials
	if !created {
		return store.AuthorizeIn(ctx, scope, actor, state.overlay, required[0], required[1:]...)
	}
	// The private creation path already loaded this actor in this same fence,
	// before its provisional INSERT. Database rules must not use that insert
	// to confer extra authority for the effects discovered afterwards.
	validator, ok := scope.(db.SessionValidator)
	if !ok || nilModelValue(validator) {
		return auth.Principal{}, false, invalidAuthorityScope()
	}
	if _, err := store.state.binding.RequirePrincipalDomainIn(ctx, scope, actor); err != nil {
		return auth.Principal{}, false, err
	}
	for _, permission := range required {
		if !actor.Has(permission) {
			return finishAuthority(ctx, validator, auth.Principal{}, false)
		}
	}
	for _, permission := range required {
		allowed, err := state.overlay.Allowed(ctx, actor, permission)
		if err := errors.Join(err, validateAuthorityScope(ctx, validator)); err != nil {
			return auth.Principal{}, false, err
		}
		if !allowed {
			return finishAuthority(ctx, validator, auth.Principal{}, false)
		}
	}
	return finishAuthority(ctx, validator, actor, true)
}

// apply only changes companion scalars, sessions and audit. The collected
// relation engine owns every PK/FK mutation; the edit owner writes its root's
// final scalar candidate and revision once, after the collected graph runs.
func (plan *lifecyclePlan[M]) apply(ctx context.Context, scope db.RelationSession) (int, error) {
	if plan == nil || plan.applied {
		return 0, managementError(CodePersistence, nil)
	}
	plan.applied = true
	revoked := 0
	for index, touch := range plan.ordered {
		row := plan.stored[index]
		if !touch.deleted && !touch.root && touch.changed() {
			if err := row.advance(ctx, scope); err != nil {
				return 0, err
			}
		}
		if row.principalID != "" && (touch.deleted || touch.root && plan.deactivate) {
			count, err := plan.owner.sessions.RevokeIn(ctx, scope, row.principalID, sessions.ID{})
			if err != nil {
				return 0, err
			}
			revoked += count
		}
	}
	for _, event := range plan.events {
		if _, err := accountaudit.AppendIn(ctx, scope, event); err != nil {
			return 0, err
		}
	}
	return revoked, nil
}

func (plan *lifecyclePlan[M]) verify(ctx context.Context, scope db.RelationSession) error {
	if plan == nil || !plan.applied || plan.verified {
		return managementError(CodePersistence, nil)
	}
	plan.verified = true
	for _, verify := range plan.verifiers {
		if verify == nil {
			return managementError(CodePersistence, nil)
		}
		if err := verify(ctx, scope); err != nil {
			return err
		}
	}
	return nil
}

func lifecycleChangedFields(metadata ir.Model, touch *deletionTouch) []string {
	selected := touch.changedFields()
	var result []string
	for _, field := range metadata.Fields {
		if slices.Contains(selected, field.Name) {
			result = append(result, field.Name)
		}
	}
	for _, field := range metadata.ManyToMany {
		if slices.Contains(selected, field.Name) {
			result = append(result, field.Name)
		}
	}
	return result
}
