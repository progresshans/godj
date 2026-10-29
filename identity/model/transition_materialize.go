package model

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

// Both account-only creation and catalog creation use the same typed row/link
// writer. Correspondence must already be resolved; this owner never guesses a
// key or deletes a pre-existing relation while filling the reviewed targets.
func (owner *Transition[S, T]) materializeTargets(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], candidates []transitionCreationCandidate[T], creating bool, originals []transitionRow[T]) ([]AccountMapping, error) {
	if len(candidates) != len(current.pairs) || !creating && len(originals) != len(candidates) || current.catalog.pendingGroups || current.catalog.pendingPermissions {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	binding := owner.next.state.credentials.state.binding
	objects := binding.state.objects
	mappings := make([]AccountMapping, len(candidates))
	var changes []orm.CollectionChange
	for i, candidate := range candidates {
		row, err := candidate.prepared.Model()
		if err != nil {
			return nil, err
		}
		if creating || candidate.row.revision != originals[i].revision {
			if err := candidate.prepared.Save(ctx, scope, &row); err != nil {
				return nil, err
			}
		}
		key, present, err := objects.PrimaryKey(row)
		id, integer := key.Integer()
		if err != nil || !present || !integer || !creating && id != originals[i].id {
			return nil, invalidAccountCredential()
		}
		values, err := objects.ModelValues(row)
		if err != nil {
			return nil, err
		}
		revision, valid := values[binding.state.role.Revision].Integer()
		if !valid || revision != candidate.row.revision {
			return nil, invalidAccountCredential()
		}
		memberships, err := current.catalog.memberships(current.pairs[i].old.memberships)
		if err != nil {
			return nil, err
		}
		current.pairs[i].next = candidate.row
		current.pairs[i].next.row, current.pairs[i].next.id, current.pairs[i].next.values, current.pairs[i].next.memberships = row, id, values, memberships
		mappings[i] = AccountMapping{SourceID: current.pairs[i].old.id, SourceRevision: current.pairs[i].old.revision, TargetID: id, TargetRevision: revision}
		if catalog := owner.config.CatalogCreation.state; catalog != nil {
			selected, err := catalog.accounts(ctx, scope, id, memberships.groups, memberships.direct)
			if err != nil {
				return nil, err
			}
			changes = append(changes, selected...)
		} else {
			target := owner.config.TargetCreation.state
			if target == nil || !creating {
				return nil, managementError(CodeInvalidConfig, nil)
			}
			for _, selection := range []struct {
				prepare func(T, []int64) (orm.CollectionChange, error)
				keys    []int64
			}{{target.groups, memberships.groups}, {target.permissions, memberships.direct}} {
				change, err := selection.prepare(row, selection.keys)
				if err != nil {
					return nil, err
				}
				changes = append(changes, change)
			}
		}
		if creating {
			for _, collection := range owner.config.TargetCreation.state.collections {
				change, err := collection.state.prepare(row, candidate.row.profile[collection.state.field.Name])
				if err != nil {
					return nil, err
				}
				changes = append(changes, change)
			}
		}
	}
	batch, err := orm.PrepareCollectionChangesInSession(ctx, scope, changes...)
	if err != nil {
		return nil, err
	}
	if err := batch.ValidateSource(binding.state.project); err != nil {
		return nil, err
	}
	if len(batch.Effects().Deletions().Rows()) != 0 {
		return nil, managementError(CodeConflict, nil)
	}
	applied, err := batch.Apply(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := current.inserted.add(binding.state.project, applied); err != nil {
		return nil, err
	}
	rows := applied.Insertions()
	current.roleLinks, err = projectTransitionAccountLinks(current.roleLinks, rows)
	if err != nil {
		return nil, err
	}
	if err := owner.observeInsertedCatalog(ctx, scope, current, rows); err != nil {
		return nil, err
	}
	return mappings, nil
}
