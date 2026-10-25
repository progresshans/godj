package model

import (
	"context"
	"errors"
	"math"
	"slices"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
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
	ID, Revision  int64
	ChangedFields []string
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
	required, action := state.permissions.change, admin.ActionChange
	if create {
		required, action = state.permissions.add, admin.ActionAdd
	}
	return managementRelationWrite(ctx, state.shared.writer, func(work context.Context, scope db.RelationSession) (CatalogChange, error) {
		currentActor, allowed, err := state.shared.authorize(work, scope, actor, required)
		if err != nil {
			return CatalogChange{}, err
		}
		if !allowed {
			return CatalogChange{}, managementError(CodePermission, nil)
		}
		namespace, _ := currentActor.CredentialDomain()
		var changedCollection bool
		keys, selected := prepared.Collection(state.collection)
		options := crud.SaveOptions[M]{Before: func(ctx context.Context, session db.Session, current, candidate *M) error {
			if candidate == nil || create != (current == nil) {
				return managementError(CodePersistence, nil)
			}
			var before map[string]query.Value
			var currentKeys []int64
			if current != nil {
				var currentID, actual int64
				before, currentID, actual, err = state.inspect(*current, true)
				if err != nil {
					return err
				}
				if currentID != id {
					return managementError(CodePersistence, nil)
				}
				if actual != revision {
					return managementError(CodeConflict, nil)
				}
				if state.readKeys != nil {
					currentKeys, err = state.readKeys(ctx, session, id)
					if err != nil {
						return err
					}
				}
			}
			if selected {
				if err := state.checkKeys(ctx, session, keys); err != nil {
					return err
				}
				changedCollection = !slices.Equal(currentKeys, keys)
				if changedCollection && !create {
					if err := state.checkMembers(ctx, session, namespace, id, keys); err != nil {
						return err
					}
				}
			}
			after, _, _, err := state.inspect(*candidate, false)
			if err != nil {
				return err
			}
			if err := state.validateCandidate(ctx, session, current, *candidate); err != nil {
				return err
			}
			changed := create || changedCollection
			for _, field := range state.model.Fields {
				if current != nil && !before[field.Name].Equal(after[field.Name]) {
					changed = true
				}
			}
			if changed {
				next := revision + 1
				if create {
					next = 1
				}
				updated, err := state.resource.Manager().ApplyValues(*candidate, map[string]query.Value{state.revision: query.Integer(next)})
				if err != nil {
					return err
				}
				*candidate = updated
			}
			return nil
		}}
		if selected {
			options.Collections = []crud.CollectionSaver[M]{{Field: state.collection, Save: func(ctx context.Context, session db.Session, row M, selected []int64) (bool, error) {
				if !changedCollection {
					return false, nil
				}
				if err := state.saveKeys(ctx, session, row, selected); err != nil {
					return false, err
				}
				return true, nil
			}}}
		}
		var mutation crud.Mutation[M]
		if create {
			mutation, err = state.resource.CreateInSession(work, scope, prepared, options)
		} else {
			mutation, err = state.resource.UpdateInSession(work, scope, id, prepared, options)
		}
		if err == crud.ErrNotFound {
			return CatalogChange{}, managementError(CodeNotFound, nil)
		}
		if err != nil {
			return CatalogChange{}, err
		}
		_, key, actual, err := state.inspect(mutation.Value, true)
		if err != nil {
			return CatalogChange{}, err
		}
		changed := make([]string, 0, len(mutation.ChangedFields))
		for _, name := range mutation.ChangedFields {
			if state.allowed[name] {
				changed = append(changed, name)
			}
		}
		if create {
			changed = slices.Clone(state.writeFields)
		}
		if create || len(changed) != 0 {
			if err := state.audit(work, scope, currentActor, key, action, changed); err != nil {
				return CatalogChange{}, err
			}
		}
		return CatalogChange{ID: key, Revision: actual, ChangedFields: changed}, nil
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

func (state *catalogModelState[M]) audit(ctx context.Context, scope db.Session, actor auth.Principal, id int64, action admin.Action, fields []string) error {
	event, err := accountaudit.Prepare(actor, state.modelName(), id, action, fields, state.shared.now())
	if err != nil {
		return err
	}
	_, err = accountaudit.AppendIn(ctx, scope, event)
	return err
}
