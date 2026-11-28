package model

import (
	"context"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/validation"
)

type catalogGrantPreparation struct {
	creation *creationThroughPreparation
	edit     *throughPreparation
}

func (state *catalogModelState[M]) prepareGrantWrite(ctx context.Context, actor auth.Principal, id, revision int64, input orm.ModelInput[M], required []auth.Permission, create bool) (catalogGrantPreparation, error) {
	var zero catalogGrantPreparation
	if state.through[state.collection] == nil {
		return zero, nil
	}
	if _, selected := input.Collection(state.collection); !selected {
		return zero, nil
	}
	preparation, err := managementPreparationSnapshot(ctx, state.shared.reader, func(work context.Context, scope db.Queryer) (catalogGrantPreparation, error) {
		write, err := state.readWrite(work, scope, actor, id, revision, input, required, create)
		if err != nil {
			return zero, err
		}
		domain, bound := write.actor.CredentialDomain()
		if !bound {
			return zero, managementError(CodePersistence, nil)
		}
		if create {
			// The root has no key yet. Check its native uniqueness and keep
			// child FKs unbound until the write acquires the actual root key.
			failures, err := write.model.ValidateUnique(work, scope, write.candidate)
			if err != nil {
				return zero, err
			}
			if err := validation.Reject(failures, nil); err != nil {
				return zero, err
			}
			drafts := &creationThroughPreparation{domain: domain, limit: state.throughLimit}
			if err := drafts.prepare(work, scope, write.actor, state.shared.project, state.identity, state.through, func(string) []int64 { return write.keys }); err != nil {
				return zero, err
			}
			return catalogGrantPreparation{creation: drafts}, nil
		}
		pending := &throughPreparation{domain: domain, authority: domain, actorID: write.actor.ID(), limit: state.throughLimit,
			pairs: make(map[throughPairKey]*throughCandidate), identities: make(map[throughIdentity]*throughCandidate)}
		change, err := state.grantChange(work, scope, write, pending, false)
		if err != nil {
			return zero, err
		}
		pending.observation, err = orm.ObserveCollectionChangesInReadScope(work, scope, change)
		if err != nil {
			return zero, err
		}
		effects := pending.observation.Effects()
		if _, err := state.planWrite(work, scope, &write, id, revision, required, effects); err != nil {
			return zero, err
		}
		var deleted []int64
		for _, effect := range effects.Deletions().Rows() {
			if effect.Deleted && effect.Model == state.identity {
				deleted = append(deleted, effect.Key)
			}
		}
		failures, err := write.model.ValidateUniqueAfterDeletion(work, scope, write.candidate, deleted)
		if err != nil {
			return zero, err
		}
		if err := validation.Reject(failures, nil); err != nil {
			return zero, err
		}
		if err := pending.check(work, scope, write.actor, effects, false); err != nil {
			return zero, err
		}
		return catalogGrantPreparation{edit: pending}, nil
	})
	if err != nil {
		return zero, err
	}
	if create {
		err = preparation.creation.encode(ctx)
	} else {
		err = preparation.edit.encode(ctx)
	}
	if err != nil {
		return zero, err
	}
	return preparation, nil
}

func (state *catalogModelState[M]) grantChange(ctx context.Context, scope db.Queryer, write catalogWrite[M], pending *throughPreparation, final bool) (orm.CollectionChange, error) {
	change, err := state.collectionChange(write.candidate, write.keys)
	if err != nil {
		return orm.CollectionChange{}, err
	}
	policy := state.through[state.collection]
	if policy == nil {
		return change, nil
	}
	if pending == nil {
		return orm.CollectionChange{}, managementError(CodeInvalidConfig, nil)
	}
	return policy.bind(ctx, scope, write.actor, pending, final, change)
}
