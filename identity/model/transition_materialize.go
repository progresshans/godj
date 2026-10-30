package model

import (
	"context"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type transitionTargetAllocation struct {
	nodes    []transitionAllocation
	mappings []AccountMapping
	inserted transitionInsertions
}

// Row allocation is separate from membership materialization. Account and
// catalog FK dependencies can share one graph without inventing intermediate
// keys or presenting incomplete membership state as a final choice policy.
func (owner *Transition[S, T]) targetAllocations(current *transitionObservation[S, T], candidates []transitionCreationCandidate[T], creating bool, originals []transitionRow[T]) (*transitionTargetAllocation, error) {
	if len(candidates) != len(current.pairs) || !creating && len(originals) != len(candidates) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	binding := owner.next.state.credentials.state.binding
	objects := binding.state.objects
	metadata, err := objects.Metadata()
	if err != nil {
		return nil, err
	}
	result := &transitionTargetAllocation{mappings: make([]AccountMapping, len(candidates))}
	partial := make([]transitionInsertions, len(candidates))
	for i, initial := range candidates {
		source := SourceAccount{ID: current.pairs[i].old.id, Revision: current.pairs[i].old.revision}
		persist := func(ctx context.Context, scope db.RelationSession, candidate transitionCreationCandidate[T], inventory *transitionInsertions, expected *int64) (int64, error) {
			row, err := candidate.prepared.Model()
			if err != nil {
				return 0, err
			}
			if creating || candidate.row.revision != originals[i].revision {
				if err := candidate.prepared.Save(ctx, scope, &row); err != nil {
					return 0, err
				}
			}
			key, present, err := objects.PrimaryKey(row)
			id, integer := key.Integer()
			if err != nil || !present || !integer || !creating && id != originals[i].id || expected != nil && id != *expected {
				return 0, invalidAccountCredential()
			}
			values, err := objects.ModelValues(row)
			if err != nil {
				return 0, err
			}
			revision, valid := values[binding.state.role.Revision].Integer()
			if !valid || revision != candidate.row.revision {
				return 0, invalidAccountCredential()
			}
			current.pairs[i].next = candidate.row
			current.pairs[i].next.row, current.pairs[i].next.id, current.pairs[i].next.values = row, id, values
			if creating {
				if _, err := addTransitionIdentity(ctx, scope, inventory, binding.state.project, binding.state.role.Model, objects, row); err != nil {
					return 0, err
				}
			}
			result.mappings[i] = AccountMapping{SourceID: source.ID, SourceRevision: source.Revision, TargetID: id, TargetRevision: revision}
			return id, nil
		}
		result.nodes = append(result.nodes, transitionAllocation{reference: newTransitionReferenceNode(TransitionReferenceAccount, source, initial.references, metadata),
			insert: func(ctx context.Context, scope db.RelationSession, actor auth.Principal, references transitionTargetMappings, deferred map[string]bool) (int64, error) {
				candidate := initial
				if len(deferred) != 0 && (!creating || candidate.resolve == nil) {
					return 0, managementError(CodeInvalidConfig, nil)
				}
				if candidate.resolve != nil {
					var err error
					candidate, err = candidate.resolve(ctx, scope, actor, references, transitionCreationPhase[T]{allocating: true, deferred: deferred})
					if err != nil {
						return 0, err
					}
					if candidate.resolve != nil {
						return 0, managementError(CodeConflict, nil)
					}
				}
				inventory := &result.inserted
				if len(deferred) != 0 {
					inventory = &partial[i]
				}
				return persist(ctx, scope, candidate, inventory, nil)
			},
			finish: func(ctx context.Context, scope db.RelationSession, actor auth.Principal, references transitionTargetMappings, deferred map[string]bool) error {
				if !creating || initial.resolve == nil || len(deferred) == 0 {
					return managementError(CodeInvalidConfig, nil)
				}
				if err := partial[i].verify(ctx); err != nil {
					return err
				}
				row, key := current.pairs[i].next.row, current.pairs[i].next.id
				candidate, err := initial.resolve(ctx, scope, actor, references, transitionCreationPhase[T]{allocating: true, current: &row})
				if err != nil {
					return err
				}
				if candidate.resolve != nil {
					return managementError(CodeConflict, nil)
				}
				if err := validateTransitionDeferredUpdate(candidate.prepared.ChangedFields(), deferred); err != nil {
					return err
				}
				_, err = persist(ctx, scope, candidate, &result.inserted, &key)
				if err == nil {
					partial[i] = transitionInsertions{}
				}
				return err
			},
			values: func() map[string]query.Value { return current.pairs[i].next.values },
		})
	}
	return result, nil
}

func (owner *Transition[S, T]) materializeTargets(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], candidates []transitionCreationCandidate[T], creating bool, originals []transitionRow[T], catalogChanges []orm.CollectionChange, at time.Time) ([]AccountMapping, error) {
	if current.catalog.pendingGroups || current.catalog.pendingPermissions {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	allocation, err := owner.targetAllocations(current, candidates, creating, originals)
	if err != nil {
		return nil, err
	}
	sources := make([]SourceAccount, len(current.pairs))
	for i, pair := range current.pairs {
		sources[i] = SourceAccount{ID: pair.old.id, Revision: pair.old.revision}
	}
	if _, err := applyTransitionAllocations(ctx, scope, current.actor, targetReferenceMappings(sources, nil, current.catalog), allocation.nodes); err != nil {
		return nil, err
	}
	if err := current.inserted.include(owner.next.state.credentials.state.binding.state.project, allocation.inserted); err != nil {
		return nil, err
	}
	if err := owner.materializeTargetLinks(ctx, scope, current, creating, allocation.inserted.rows, catalogChanges, at); err != nil {
		return nil, err
	}
	return allocation.mappings, nil
}

func (owner *Transition[S, T]) materializeTargetLinks(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], creating bool, insertedIdentities []orm.CollectionInsertedRow, catalogChanges []orm.CollectionChange, at time.Time) error {
	if current.catalog.pendingGroups || current.catalog.pendingPermissions {
		return managementError(CodeInvalidConfig, nil)
	}
	binding := owner.next.state.credentials.state.binding
	if _, _, err := owner.inspectAuthority(ctx, scope, current.actor); err != nil {
		return err
	}
	bound, err := owner.bindTransitionThrough(ctx, scope, current)
	if err != nil {
		return err
	}
	var changes []orm.CollectionChange
	for _, change := range catalogChanges {
		change, err = owner.bindTransitionCollection(ctx, scope, current, bound, transitionCollectionKey{binding.state.role.Catalog.Group, binding.state.role.Catalog.GroupPermissions}, change)
		if err != nil {
			return err
		}
		changes = append(changes, change)
	}
	for i := range current.pairs {
		pair := &current.pairs[i]
		row, id := pair.next.row, pair.next.id
		memberships, err := current.catalog.memberships(pair.old.memberships)
		if err != nil {
			return err
		}
		pair.next.memberships = memberships
		if catalog := owner.config.CatalogCreation.state; catalog != nil {
			selected, err := catalog.accounts(ctx, scope, id, memberships.groups, memberships.direct)
			if err != nil {
				return err
			}
			if len(selected) != 2 {
				return managementError(CodeInvalidConfig, nil)
			}
			for index, field := range []string{binding.state.role.Groups, binding.state.role.Permissions} {
				change, err := owner.bindTransitionCollection(ctx, scope, current, bound, transitionCollectionKey{binding.state.role.Model, field}, selected[index])
				if err != nil {
					return err
				}
				changes = append(changes, change)
			}
		} else {
			target := owner.config.TargetCreation.state
			if target == nil || !creating {
				return managementError(CodeInvalidConfig, nil)
			}
			for _, selection := range []struct {
				field   string
				prepare func(T, []int64) (orm.CollectionChange, error)
				keys    []int64
			}{{binding.state.role.Groups, target.groups, memberships.groups}, {binding.state.role.Permissions, target.permissions, memberships.direct}} {
				change, err := selection.prepare(row, selection.keys)
				if err != nil {
					return err
				}
				change, err = owner.bindTransitionCollection(ctx, scope, current, bound, transitionCollectionKey{binding.state.role.Model, selection.field}, change)
				if err != nil {
					return err
				}
				changes = append(changes, change)
			}
		}
		if creating {
			for _, collection := range owner.config.TargetCreation.state.collections {
				change, err := collection.state.prepare(row, pair.next.profile[collection.state.field.Name])
				if err != nil {
					return err
				}
				change, err = owner.bindTransitionCollection(ctx, scope, current, bound, transitionCollectionKey{binding.state.role.Model, collection.state.field.Name}, change)
				if err != nil {
					return err
				}
				changes = append(changes, change)
			}
		}
	}
	batch, err := orm.PrepareCollectionChangesInSession(ctx, scope, changes...)
	if err != nil {
		return err
	}
	if err := batch.ValidateSource(binding.state.project); err != nil {
		return err
	}
	if len(batch.Effects().Deletions().Rows()) != 0 {
		return managementError(CodeConflict, nil)
	}
	if err := bound.pending.check(ctx, scope, current.actor, batch.Effects(), true); err != nil {
		return err
	}
	applied, err := batch.Apply(ctx, nil)
	if err != nil {
		return err
	}
	if err := current.inserted.add(binding.state.project, applied); err != nil {
		return err
	}
	rows := append(insertedIdentities, applied.Insertions()...)
	current.roleLinks, err = projectTransitionAccountLinks(current.roleLinks, rows)
	if err != nil {
		return err
	}
	if err := owner.observeInsertedCatalog(ctx, scope, current, rows); err != nil {
		return err
	}
	if err := owner.finishTransitionThrough(ctx, scope, current, bound, applied, at); err != nil {
		return err
	}
	return nil
}
