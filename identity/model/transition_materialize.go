package model

import (
	"context"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
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
	result := &transitionTargetAllocation{mappings: make([]AccountMapping, len(candidates))}
	for i, initial := range candidates {
		source := SourceAccount{ID: current.pairs[i].old.id, Revision: current.pairs[i].old.revision}
		result.nodes = append(result.nodes, transitionAllocation{reference: transitionReferenceNode{kind: TransitionReferenceAccount, source: source, references: initial.references},
			insert: func(ctx context.Context, scope db.RelationSession, actor auth.Principal, references transitionTargetMappings) (int64, error) {
				candidate := initial
				var err error
				if candidate.resolve != nil {
					candidate, err = candidate.resolve(ctx, scope, actor, references, true)
					if err != nil {
						return 0, err
					}
					if candidate.resolve != nil {
						return 0, managementError(CodeConflict, nil)
					}
				}
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
				if err != nil || !present || !integer || !creating && id != originals[i].id {
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
					if _, err := addTransitionIdentity(ctx, scope, &result.inserted, binding.state.project, binding.state.role.Model, objects, row); err != nil {
						return 0, err
					}
				}
				result.mappings[i] = AccountMapping{SourceID: source.ID, SourceRevision: source.Revision, TargetID: id, TargetRevision: revision}
				return id, nil
			},
		})
	}
	return result, nil
}

func (owner *Transition[S, T]) materializeTargets(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], candidates []transitionCreationCandidate[T], creating bool, originals []transitionRow[T]) ([]AccountMapping, error) {
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
	if err := owner.materializeTargetLinks(ctx, scope, current, creating, allocation.inserted.rows); err != nil {
		return nil, err
	}
	return allocation.mappings, nil
}

func (owner *Transition[S, T]) materializeTargetLinks(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], creating bool, insertedIdentities []orm.CollectionInsertedRow) error {
	if current.catalog.pendingGroups || current.catalog.pendingPermissions {
		return managementError(CodeInvalidConfig, nil)
	}
	binding := owner.next.state.credentials.state.binding
	var changes []orm.CollectionChange
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
			changes = append(changes, selected...)
		} else {
			target := owner.config.TargetCreation.state
			if target == nil || !creating {
				return managementError(CodeInvalidConfig, nil)
			}
			for _, selection := range []struct {
				prepare func(T, []int64) (orm.CollectionChange, error)
				keys    []int64
			}{{target.groups, memberships.groups}, {target.permissions, memberships.direct}} {
				change, err := selection.prepare(row, selection.keys)
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
	return nil
}
