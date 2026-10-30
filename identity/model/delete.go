package model

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// AccountDeleter coordinates a complete host relation deletion with all
// affected selected accounts/catalogs, direct membership revisions, current
// domain session revocation and value-free audits in one native transaction.
// It never publishes an account profile or retries an uncertain outcome.
type AccountDeleter[M any] struct{ state *accountDeleteState[M] }
type accountDeleteState[M any] struct {
	lifecycle *deletionLifecycle[M]
	backend   db.CoordinatedRelationAtomic
	policy    orm.RelationDeleter[M]
	required  []auth.Permission
}

func (AccountDeleter[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.AccountDeleter{redacted}"))
}

// NewAccountDeleter is normally called by the generated project factory.
// Session ownership, typed descriptors, role and the complete host relation
// source must all agree. Construction performs no I/O or application callbacks.
func NewAccountDeleter[M, G, P any](sessionStore *SessionStore[M], expected Binding[M], groups orm.RelationObjectDescriptor[G], permissions orm.RelationObjectDescriptor[P], policy orm.RelationDeleter[M], overlay auth.Authorizer, config AccountDeleteConfig) (*AccountDeleter[M], error) {
	lifecycle, err := newDeletionLifecycle(sessionStore, expected, groups, permissions, overlay, config)
	if err != nil {
		return nil, err
	}
	store := sessionStore.state.credentials
	binding := store.state.binding
	if err := policy.ValidateSource(binding.state.project, binding.state.role.Model); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	return &AccountDeleter[M]{state: &accountDeleteState[M]{lifecycle: lifecycle, backend: store.state.backend.(db.CoordinatedRelationAtomic),
		policy: policy, required: slices.Clone(lifecycle.models[binding.state.role.Model].remove)}}, nil
}

func (deleter *AccountDeleter[M]) Permissions() []auth.Permission {
	if deleter == nil || deleter.state == nil {
		return nil
	}
	return slices.Clone(deleter.state.required)
}

type AccountDeletion struct {
	ID, Revision, DeletedRows int64
	RevokedSessions           int
}

func (deleter *AccountDeleter[M]) Delete(ctx context.Context, actor auth.Principal, id, revision int64) (AccountDeletion, error) {
	if deleter == nil || deleter.state == nil {
		return AccountDeletion{}, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || revision <= 0 || revision == math.MaxInt64 {
		return AccountDeletion{}, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return AccountDeletion{}, managementError(CodeInvalidInput, err)
	}
	if !actor.Authenticated() {
		return AccountDeletion{}, managementError(CodePermission, nil)
	}
	state := deleter.state
	store := state.lifecycle.credentials
	binding := store.state.binding
	return managementRelationWrite(ctx, state.backend, func(work context.Context, scope db.RelationSession) (AccountDeletion, error) {
		current, allowed, err := store.AuthorizeIn(work, scope, actor, state.lifecycle.overlay, state.required[0], state.required[1:]...)
		if err != nil {
			return AccountDeletion{}, err
		}
		if !allowed {
			return AccountDeletion{}, managementError(CodePermission, nil)
		}
		bounded, err := binding.state.objects.Using(scope).Filter(store.state.key.Exact(id)).OrderBy(store.state.key.Asc()).Limit(2)
		if err != nil {
			return AccountDeletion{}, err
		}
		rows, err := bounded.All(work)
		if err != nil {
			return AccountDeletion{}, err
		}
		if len(rows) == 0 {
			return AccountDeletion{}, managementError(CodeNotFound, nil)
		}
		if len(rows) != 1 {
			return AccountDeletion{}, managementError(CodePersistence, nil)
		}
		stored, err := prepareDeletionStored(binding.state.objects, rows[0], id, binding.state.role.Revision)
		if err != nil {
			return AccountDeletion{}, err
		}
		if stored.revision != revision {
			return AccountDeletion{}, managementError(CodeConflict, nil)
		}
		revoked := 0
		var plan *lifecyclePlan[M]
		count, err := state.policy.DeleteWithEffectsInSession(work, scope, rows[0], func(ctx context.Context, effects orm.RelationDeleteEffects) error {
			var err error
			plan, err = state.lifecycle.prepare(ctx, scope, current, effects, nil, nil)
			if err != nil {
				return err
			}
			revoked, err = plan.apply(ctx, scope)
			return err
		})
		if err != nil {
			return AccountDeletion{}, err
		}
		if err := plan.verify(work, scope); err != nil {
			return AccountDeletion{}, err
		}
		return AccountDeletion{ID: id, Revision: revision, DeletedRows: count, RevokedSessions: revoked}, nil
	})
}
