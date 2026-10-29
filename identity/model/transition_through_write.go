package model

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

type transitionBoundThrough struct {
	pending    *throughPreparation
	identities []domain.CollectionIdentity
	indexes    map[throughIdentity]int
}

func (owner *Transition[S, T]) bindTransitionThrough(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T]) (*transitionBoundThrough, error) {
	authority, ok := current.actor.CredentialDomain()
	if !ok {
		return nil, managementError(CodePermission, nil)
	}
	namespace, err := owner.next.state.credentials.state.binding.CredentialDomain(current.ownership.Generation())
	if err != nil {
		return nil, err
	}
	result := &transitionBoundThrough{pending: &throughPreparation{domain: namespace, authority: authority, actorID: current.actor.ID(), limit: owner.config.ThroughCreationLimit, pairs: make(map[throughPairKey]*throughCandidate), identities: make(map[throughIdentity]*throughCandidate)}, indexes: make(map[throughIdentity]int)}
	if current.through == nil {
		return result, nil
	}
	if !current.through.encoded || current.through.limit != owner.config.ThroughCreationLimit || current.through.domain.Identifier() != namespace.Identifier() {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if err := current.through.check(ctx, scope, current.actor); err != nil {
		return nil, err
	}
	var sources []SourceAccount
	keys := make(map[int64]int64, len(current.pairs))
	for _, pair := range current.pairs {
		sources = append(sources, SourceAccount{ID: pair.old.id, Revision: pair.old.revision})
		keys[pair.old.id] = pair.next.id
	}
	references := targetReferenceMappings(sources, keys, current.catalog)
	result.identities = make([]domain.CollectionIdentity, len(current.through.ordered))
	for index, entry := range current.through.ordered {
		pair, err := resolveTransitionThroughPair(entry.pair, references)
		if err != nil {
			return nil, err
		}
		// A direct, owned identity INSERT may already have satisfied this
		// physical pair. Only its actual whole-row insertion proof can do so.
		var retained *orm.CollectionInsertedRow
		for i := range current.inserted.rows {
			row := &current.inserted.rows[i]
			left, leftValid := row.Values[pair.source.Name].Integer()
			right, rightValid := row.Values[pair.target.Name].Integer()
			if row.Model == entry.policy.model && leftValid && rightValid && left == pair.sourceKey && right == pair.targetKey && (retained == nil || row.Key < retained.Key) {
				retained = row
			}
		}
		if retained != nil {
			revision, err := transitionInsertedRevision(owner.next.state.credentials.state.binding.state.role, *retained)
			if err != nil {
				return nil, err
			}
			result.identities[index] = domain.CollectionIdentity{Kind: string(entry.kind), Key: retained.Key, Revision: revision}
			continue
		}
		candidate, err := entry.draft.bind(ctx, pair, namespace)
		if err != nil {
			return nil, err
		}
		if candidate == nil || candidate.policy != entry.policy || candidate.identity.model != entry.policy.model || candidate.identity.value == "" || result.pending.identities[candidate.identity] != nil {
			return nil, managementError(CodeInvalidInput, nil)
		}
		result.pending.identities[candidate.identity] = candidate
		result.pending.ordered = append(result.pending.ordered, candidate)
		result.indexes[candidate.identity] = index
		result.identities[index] = domain.CollectionIdentity{Kind: string(entry.kind), Revision: 1, Additional: true}
		// Both declarations of a shared physical pair use the same sealed
		// candidate, including reversed field orientation.
		result.pending.pairs[throughPairKey{policy: entry.policy, source: pair.source.Name, target: pair.target.Name, sourceKey: pair.sourceKey, targetKey: pair.targetKey}] = candidate
		result.pending.pairs[throughPairKey{policy: entry.policy, source: pair.target.Name, target: pair.source.Name, sourceKey: pair.targetKey, targetKey: pair.sourceKey}] = candidate
	}
	counts := map[string]int{"account": len(current.pairs), "group": len(current.catalog.after.groups), "permission": len(current.catalog.after.permissions)}
	for _, row := range result.identities {
		if row.Additional {
			counts[row.Kind]++
		}
	}
	if counts["account"] > owner.config.MaxAccounts || counts["group"] > owner.config.MaxCatalogRows || counts["permission"] > owner.config.MaxCatalogRows {
		return nil, managementError(CodeInvalidInput, nil)
	}
	return result, nil
}

func transitionInsertedRevision(role ir.AccountRole, row orm.CollectionInsertedRow) (int64, error) {
	field := ""
	switch row.Model {
	case role.Model:
		field = role.Revision
	case role.Catalog.Group:
		field = role.Catalog.GroupRevision
	case role.Catalog.Permission:
		field = role.Catalog.PermissionRevision
	default:
		return 0, managementError(CodeInvalidConfig, nil)
	}
	revision, valid := row.Values[field].Integer()
	if !valid || revision <= 0 {
		return 0, managementError(CodePersistence, nil)
	}
	return revision, nil
}

func (owner *Transition[S, T]) bindTransitionCollection(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], bound *transitionBoundThrough, key transitionCollectionKey, change orm.CollectionChange) (orm.CollectionChange, error) {
	policy := owner.through[key]
	if policy == nil {
		return change, nil
	}
	return policy.bind(ctx, scope, current.actor, bound.pending, true, change)
}

func (owner *Transition[S, T]) finishTransitionThrough(ctx context.Context, scope db.RelationSession, current *transitionObservation[S, T], bound *transitionBoundThrough, applied orm.AppliedCollectionChanges, at time.Time) error {
	verifiers, err := bound.pending.finish(ctx, scope, current.actor, applied, at)
	if err != nil {
		return err
	}
	remaining := maps.Clone(bound.indexes)
	var extraAccounts []orm.CollectionInsertedRow
	role := owner.next.state.credentials.state.binding.state.role
	for _, row := range applied.Insertions() {
		for _, candidate := range bound.pending.ordered {
			if candidate.policy.model != row.Model {
				continue
			}
			value, valid := row.Values[candidate.policy.identity].String()
			identity := throughIdentity{row.Model, value}
			index, found := remaining[identity]
			if !valid || !found {
				continue
			}
			revision, err := transitionInsertedRevision(role, row)
			if err != nil || revision != 1 {
				return managementError(CodePersistence, err)
			}
			bound.identities[index].Key = row.Key
			delete(remaining, identity)
			if row.Model == role.Model {
				extraAccounts = append(extraAccounts, row)
			}
			break
		}
	}
	if len(remaining) != 0 {
		return managementError(CodePersistence, nil)
	}
	if len(extraAccounts) != 0 {
		actual, err := readTransitionRows(ctx, scope, owner.next.state.credentials, owner.nextMemberships, owner.config.MaxAccounts, current.ownership.Generation())
		if err != nil {
			return err
		}
		if len(actual) != len(current.pairs)+len(extraAccounts) {
			return managementError(CodeConflict, nil)
		}
		current.extraAccounts = make(map[int64]transitionRow[T], len(extraAccounts))
		for _, inserted := range extraAccounts {
			row, present := actual[inserted.Key]
			if !present || !maps.Equal(row.values, inserted.Values) || len(row.memberships.groups)+len(row.memberships.direct)+len(row.memberships.grants) != 0 {
				return managementError(CodeConflict, nil)
			}
			for _, values := range row.profile {
				if len(values) != 0 {
					return managementError(CodeConflict, nil)
				}
			}
			current.extraAccounts[inserted.Key] = row
		}
	}
	current.collectionIdentities = slices.Clone(bound.identities)
	current.throughChecks = append(current.throughChecks, verifiers...)
	return nil
}
