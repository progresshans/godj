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
	grants, err := state.prepareGrantWrite(ctx, actor, id, revision, prepared, required, create)
	if err != nil {
		return CatalogChange{}, err
	}
	return managementRelationWrite(ctx, state.shared.writer, func(work context.Context, scope db.RelationSession) (CatalogChange, error) {
		write, err := state.readWrite(work, scope, actor, id, revision, prepared, required, create)
		if err != nil {
			return CatalogChange{}, err
		}
		objects := state.resource.Manager()
		pending := grants.edit
		if create {
			if err := grants.creation.check(work, scope, write.actor, false); err != nil {
				return CatalogChange{}, err
			}
			// A generated owner key is needed to inspect any links established
			// by database rules. This provisional insert and every later refusal
			// remain under the same native transaction; nothing is published yet.
			failures, err := write.model.ValidateUnique(work, scope, write.candidate)
			if err != nil {
				return CatalogChange{}, err
			}
			if err := validation.Reject(failures, nil); err != nil {
				return CatalogChange{}, err
			}
			if err := write.model.Save(work, scope, &write.candidate); err != nil {
				return CatalogChange{}, err
			}
			_, id, revision, err = state.inspect(write.candidate, true)
			if err != nil || revision != 1 {
				return CatalogChange{}, managementError(CodePersistence, err)
			}
			// The final scalar save is now update-only, never a second insert.
			write.model, err = objects.PrepareValues(&write.candidate, nil)
			if err != nil {
				return CatalogChange{}, err
			}
			pending, err = grants.creation.bind(work, scope, write.actor, id)
			if err != nil {
				return CatalogChange{}, err
			}
		}
		var batch orm.PreparedCollectionChanges
		var effects orm.CollectionChangeEffects
		if write.selected {
			change, err := state.grantChange(work, scope, write, pending, true)
			if err != nil {
				return CatalogChange{}, err
			}
			batch, err = grants.edit.prepareChanges(work, scope, change)
			if err != nil {
				return CatalogChange{}, err
			}
			if err := batch.ValidateSource(state.shared.project); err != nil {
				return CatalogChange{}, err
			}
			effects = batch.Effects()
		}
		if pending != nil {
			if err := pending.check(work, scope, write.actor, effects, true); err != nil {
				return CatalogChange{}, err
			}
		}
		plan, err := state.planWrite(work, scope, &write, id, revision, required, effects)
		if err != nil {
			return CatalogChange{}, err
		}
		if err := state.validateCandidate(work, scope, write.current, write.candidate); err != nil {
			return CatalogChange{}, err
		}
		revoked := 0
		var applied orm.AppliedCollectionChanges
		if write.selected {
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
		failures, err := write.model.ValidateUnique(work, scope, write.candidate)
		if err != nil {
			return CatalogChange{}, err
		}
		if err := validation.Reject(failures, nil); err != nil {
			return CatalogChange{}, err
		}
		if err := write.model.Save(work, scope, &write.candidate); err != nil {
			return CatalogChange{}, err
		}
		if pending != nil && len(pending.ordered) > 0 {
			verifiers, err := pending.finish(work, scope, write.actor, applied, state.shared.now())
			if err != nil {
				return CatalogChange{}, err
			}
			for _, verify := range verifiers {
				if err := verify(work, scope); err != nil {
					return CatalogChange{}, err
				}
			}
		}
		if err := plan.verify(work, scope); err != nil {
			return CatalogChange{}, err
		}
		if write.selected {
			if err := applied.Verify(work); err != nil {
				return CatalogChange{}, err
			}
		}
		touch, nextRevision, _ := plan.rootState()
		return CatalogChange{ID: id, Revision: nextRevision, ChangedFields: lifecycleChangedFields(state.model, touch), RevokedSessions: revoked}, nil
	})
}

type catalogWrite[M any] struct {
	actor     auth.Principal
	current   *M
	model     orm.PreparedModel[M]
	candidate M
	keys      []int64
	selected  bool
}

// readWrite checks the actor and the current root before any provisional
// insertion. The same checks run again after preparing selected through rows.
func (state *catalogModelState[M]) readWrite(ctx context.Context, scope db.Queryer, actor auth.Principal, id, revision int64, input orm.ModelInput[M], required []auth.Permission, create bool) (catalogWrite[M], error) {
	var write catalogWrite[M]
	currentActor, allowed, err := state.shared.authorize(ctx, scope, actor, required)
	if err != nil {
		return write, err
	}
	if !allowed {
		return write, managementError(CodePermission, nil)
	}
	write.actor = currentActor
	if !create {
		row, found, err := state.resource.Get(ctx, scope, id)
		if err != nil {
			return write, err
		}
		if !found {
			return write, managementError(CodeNotFound, nil)
		}
		_, key, actual, err := state.inspect(row, true)
		if err != nil || key != id {
			return write, managementError(CodePersistence, err)
		}
		if actual != revision {
			return write, managementError(CodeConflict, nil)
		}
		write.current = &row
	}
	write.keys, write.selected = input.Collection(state.collection)
	if write.selected {
		if err := state.checkKeys(ctx, scope, write.keys); err != nil {
			return write, err
		}
	}
	objects := state.resource.Manager()
	if create {
		input, err = objects.WithServerValues(input, map[string]query.Value{state.revision: query.Integer(1)})
		if err != nil {
			return write, err
		}
	}
	write.model, err = objects.PrepareInput(input, write.current)
	if err != nil {
		return write, err
	}
	write.candidate, err = write.model.Model()
	if err != nil {
		return write, err
	}
	_, _, _, err = state.inspect(write.candidate, false)
	return write, err
}

func (state *catalogModelState[M]) planWrite(ctx context.Context, scope db.Queryer, write *catalogWrite[M], id, revision int64, required []auth.Permission, effects orm.CollectionChangeEffects) (lifecycleWritePlan, error) {
	fields := write.model.ChangedFields()
	create := write.current == nil
	if create {
		fields = slices.Clone(state.writeFields)
	}
	objects := state.resource.Manager()
	values, err := objects.ModelValues(write.candidate)
	if err != nil {
		return nil, err
	}
	plan, err := state.shared.prepare(ctx, scope, write.actor, effects.Deletions(), effects.Insertions(), &lifecycleEdit{
		key: deletionKey{state.identity, id}, revision: revision, required: required, fields: fields, values: values, created: create,
	})
	if err != nil {
		return nil, err
	}
	touch, nextRevision, _ := plan.rootState()
	final := map[string]query.Value{state.revision: query.Integer(nextRevision)}
	for _, field := range touch.nulls {
		final[field] = query.Null()
	}
	write.candidate, err = objects.ApplyValues(write.candidate, final)
	return plan, err
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
