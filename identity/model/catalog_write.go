package model

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/crud"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// CatalogChange exposes only confirmed mutation metadata, not an implicit read
// of a model whose view permission the writer might not hold.
type CatalogChange struct {
	ID, Revision    int64
	ChangedFields   []string
	RevokedSessions int
}
type CatalogDeletion struct {
	ID, Revision, DeletedRows int64
	RevokedSessions           int
}

// Create consumes already cleaned typed Form/JSON intent. The owner checks
// current authority, catalog scalar/choice/unique policy and audit in one
// coordinated transaction. Server revision starts at one independently of
// input/default values. Additional custom validation sees the actual model.
func (resource CatalogResource[M]) Create(ctx context.Context, actor auth.Principal, input orm.ModelInput[M]) (CatalogChange, error) {
	return resource.save(ctx, actor, 0, 0, input, true)
}

// Update preserves omitted scalars/collections; an explicit empty collection
// clears it. Raw input count is bounded before de-duplication. Every affected
// account is checked before a permission expansion is written. A no-op still
// checks current authority, target integrity and the expected revision.
func (resource CatalogResource[M]) Update(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M]) (CatalogChange, error) {
	return resource.save(ctx, actor, id, revision, input, false)
}

func (resource CatalogResource[M]) save(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M], create bool) (CatalogChange, error) {
	if err := resource.validCall(ctx, actor); err != nil {
		return CatalogChange{}, err
	}
	if !create && (revision <= 0 || revision == math.MaxInt64) {
		return CatalogChange{}, managementError(CodeInvalidInput, nil)
	}
	prepared, err := resource.prepareInput(input, id, create)
	if err != nil {
		return CatalogChange{}, err
	}
	state := resource.state
	required := state.permissions.change
	if create {
		required = state.permissions.add
	}
	return managementRelationWrite(ctx, state.shared.writer, func(work context.Context, scope db.RelationSession) (CatalogChange, error) {
		currentActor, allowed, err := state.shared.authorize(work, scope, actor, required)
		if err != nil {
			return CatalogChange{}, err
		}
		if !allowed {
			return CatalogChange{}, managementError(CodePermission, nil)
		}
		var current *M
		if !create {
			row, found, err := state.resource.Get(work, scope, id)
			if err != nil {
				return CatalogChange{}, err
			}
			if !found {
				return CatalogChange{}, managementError(CodeNotFound, nil)
			}
			_, key, actual, err := state.inspect(row, true)
			if err != nil || key != id {
				return CatalogChange{}, managementError(CodePersistence, err)
			}
			if actual != revision {
				return CatalogChange{}, managementError(CodeConflict, nil)
			}
			current = &row
		}
		keys, selected := prepared.Collection(state.collection)
		if selected {
			if err := state.checkKeys(work, scope, keys); err != nil {
				return CatalogChange{}, err
			}
		}
		objects := state.resource.Manager()
		if create {
			prepared, err = objects.WithServerValues(prepared, map[string]query.Value{state.revision: query.Integer(1)})
			if err != nil {
				return CatalogChange{}, err
			}
		}
		model, err := objects.PrepareInput(prepared, current)
		if err != nil {
			return CatalogChange{}, err
		}
		candidate, err := model.Model()
		if err != nil {
			return CatalogChange{}, err
		}
		if _, _, _, err := state.inspect(candidate, false); err != nil {
			return CatalogChange{}, err
		}
		if create {
			// A generated owner key is needed to inspect any links established
			// by database rules. This provisional insert and every later refusal
			// remain under the same native transaction; nothing is published yet.
			failures, err := model.ValidateUnique(work, scope, candidate)
			if err != nil {
				return CatalogChange{}, err
			}
			if err := validation.Reject(failures, nil); err != nil {
				return CatalogChange{}, err
			}
			if err := model.Save(work, scope, &candidate); err != nil {
				return CatalogChange{}, err
			}
			_, id, revision, err = state.inspect(candidate, true)
			if err != nil || revision != 1 {
				return CatalogChange{}, managementError(CodePersistence, err)
			}
			// The final scalar save is now update-only, never a second insert.
			model, err = objects.PrepareValues(&candidate, nil)
			if err != nil {
				return CatalogChange{}, err
			}
		}
		var batch orm.PreparedCollectionChanges
		var effects orm.CollectionChangeEffects
		if selected {
			change, err := state.collectionChange(candidate, keys)
			if err != nil {
				return CatalogChange{}, err
			}
			batch, err = orm.PrepareCollectionChangesInSession(work, scope, change)
			if err != nil {
				return CatalogChange{}, err
			}
			if err := batch.ValidateSource(state.shared.project); err != nil {
				return CatalogChange{}, err
			}
			effects = batch.Effects()
		}
		fields := model.ChangedFields()
		if create {
			fields = slices.Clone(state.writeFields)
		}
		values, err := objects.ModelValues(candidate)
		if err != nil {
			return CatalogChange{}, err
		}
		plan, err := state.shared.prepare(work, scope, currentActor, effects.Deletions(), effects.Insertions(), &lifecycleEdit{
			key: deletionKey{state.identity, id}, revision: revision, required: required, fields: fields, values: values, created: create,
		})
		if err != nil {
			return CatalogChange{}, err
		}
		touch, nextRevision, _ := plan.rootState()
		final := map[string]query.Value{state.revision: query.Integer(nextRevision)}
		for _, field := range touch.nulls {
			final[field] = query.Null()
		}
		candidate, err = objects.ApplyValues(candidate, final)
		if err != nil {
			return CatalogChange{}, err
		}
		if err := state.validateCandidate(work, scope, current, candidate); err != nil {
			return CatalogChange{}, err
		}
		revoked := 0
		var applied orm.AppliedCollectionChanges
		if selected {
			applied, err = batch.Apply(work, func(ctx context.Context, _ orm.CollectionChangeEffects) error {
				var err error
				revoked, err = plan.apply(ctx, scope)
				return err
			})
		} else {
			revoked, err = plan.apply(work, scope)
		}
		if err != nil {
			return CatalogChange{}, err
		}
		failures, err := model.ValidateUnique(work, scope, candidate)
		if err != nil {
			return CatalogChange{}, err
		}
		if err := validation.Reject(failures, nil); err != nil {
			return CatalogChange{}, err
		}
		if err := model.Save(work, scope, &candidate); err != nil {
			return CatalogChange{}, err
		}
		if err := plan.verify(work, scope); err != nil {
			return CatalogChange{}, err
		}
		if selected {
			if err := applied.Verify(work); err != nil {
				return CatalogChange{}, err
			}
		}
		return CatalogChange{ID: id, Revision: nextRevision, ChangedFields: lifecycleChangedFields(state.model, touch), RevokedSessions: revoked}, nil
	})
}

func (resource CatalogResource[M]) prepareInput(input orm.ModelInput[M], id int64, create bool) (orm.ModelInput[M], error) {
	state := resource.state
	model, valid := input.Model()
	if !valid || !model.Equal(state.model) {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
	}
	if key, bound := input.TargetKey(); bound && (create && !key.IsNull() || !create && !key.Equal(query.Integer(id))) {
		return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
	}
	for name := range input.Values() {
		if !state.allowed[name] {
			return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
		}
	}
	if err := state.checkScalars(input.Values()); err != nil {
		return orm.ModelInput[M]{}, err
	}
	collections := input.Collections()
	for name, keys := range collections {
		if name == "" || name != state.collection || !state.allowed[name] {
			return orm.ModelInput[M]{}, managementError(CodeInvalidInput, nil)
		}
		if len(keys) > auth.MaximumPermissions {
			return orm.ModelInput[M]{}, profileInputError(name, "max_items")
		}
		slices.Sort(keys)
		collections[name] = slices.Compact(keys)
	}
	return input.WithCollections(collections)
}

func (state *catalogModelState[M]) validateCandidate(ctx context.Context, scope db.Session, current *M, candidate M) error {
	validator, ok := scope.(db.SessionValidator)
	if !ok || nilModelValue(validator) {
		return managementError(CodeInvalidConfig, nil)
	}
	if err := validateAuthorityScope(ctx, validator); err != nil {
		return err
	}
	if state.validate == nil {
		return nil
	}
	objects := state.resource.Manager()
	var before *M
	if current != nil {
		owned, err := objects.ApplyValues(*current, nil)
		if err != nil {
			return err
		}
		before = &owned
	}
	after, err := objects.ApplyValues(candidate, nil)
	if err != nil {
		return err
	}
	failures, failure := state.validate(ctx, scope, before, after)
	if err := errors.Join(failure, ctx.Err(), validator.ValidateSession(ctx)); err != nil {
		return err
	}
	return validation.Reject(failures, nil)
}

func (resource CatalogResource[M]) Delete(ctx context.Context, actor auth.Principal, id, revision int64) (CatalogDeletion, error) {
	if err := resource.validCall(ctx, actor); err != nil {
		return CatalogDeletion{}, err
	}
	if revision <= 0 || revision == math.MaxInt64 {
		return CatalogDeletion{}, managementError(CodeInvalidInput, nil)
	}
	state := resource.state
	return managementRelationWrite(ctx, state.shared.writer, func(work context.Context, scope db.RelationSession) (CatalogDeletion, error) {
		current, allowed, err := state.shared.authorize(work, scope, actor, state.permissions.delete)
		if err != nil {
			return CatalogDeletion{}, err
		}
		if !allowed {
			return CatalogDeletion{}, managementError(CodePermission, nil)
		}
		var count int64
		var revoked int
		_, err = state.resource.DeleteInSession(work, scope, id, crud.DeleteOptions[M]{
			Before: func(ctx context.Context, scope db.Session, row M) error {
				_, key, actual, err := state.inspect(row, true)
				if err != nil {
					return err
				}
				if key != id {
					return managementError(CodePersistence, nil)
				}
				if actual != revision {
					return managementError(CodeConflict, nil)
				}
				if state.readKeys != nil {
					if _, err := state.readKeys(ctx, scope, id); err != nil {
						return err
					}
				}
				return nil
			},
			Delete: func(ctx context.Context, scope db.Session, row M) (int64, error) {
				var err error
				count, revoked, err = state.delete(ctx, scope, current, row)
				return count, err
			},
		})
		if err == crud.ErrNotFound {
			return CatalogDeletion{}, managementError(CodeNotFound, nil)
		}
		if err != nil {
			return CatalogDeletion{}, err
		}
		return CatalogDeletion{ID: id, Revision: revision, DeletedRows: count, RevokedSessions: revoked}, nil
	})
}
