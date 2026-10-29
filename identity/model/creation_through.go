package model

import (
	"context"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
)

type creationThroughDraft struct {
	policy *throughCreationState
	pair   ThroughEndpoints
	draft  *throughDraft
}
type creationThroughPreparation struct {
	domain   auth.CredentialDomain
	operator bool
	limit    int
	encoded  bool
	ordered  []creationThroughDraft
}

func (state *creationState[M]) prepareCreationThrough(ctx context.Context, scope db.Queryer, actor auth.Principal, domain auth.CredentialDomain, candidate *creationCandidate[M], operator bool) error {
	if len(state.through) == 0 {
		return nil
	}
	if candidate.through != nil {
		return candidate.through.check(ctx, scope, actor, operator)
	}
	pending := &creationThroughPreparation{domain: domain, operator: operator, limit: state.config.ThroughCreationLimit}
	seen := make(map[throughPairKey]bool)
	project, role := state.binding.state.project, state.binding.state.role
	for _, relation := range project.ManyToManyRelations() {
		if relation.Source != role.Model {
			continue
		}
		policy := state.through[relation.Field]
		if policy == nil {
			continue
		}
		if policy.draft == nil {
			return managementError(CodeInvalidConfig, nil)
		}
		keys := candidate.profile[relation.Field]
		if relation.Field == role.Groups {
			keys = candidate.groups
		}
		if relation.Field == role.Permissions {
			keys = candidate.permissions
		}
		metadata, found := project.Model(relation.Through.Model)
		source, sourceFound := namedField(metadata, relation.Through.SourceField)
		target, targetFound := namedField(metadata, relation.Through.TargetField)
		if !found || !sourceFound || !targetFound {
			return managementError(CodeInvalidConfig, nil)
		}
		for _, key := range keys {
			pairKey := throughPairKey{policy: policy, source: source.Name, target: target.Name, targetKey: key}
			if seen[pairKey] {
				continue
			}
			if len(pending.ordered) >= pending.limit {
				return managementError(CodeInvalidInput, nil)
			}
			pair := ThroughEndpoints{source: source.Clone(), target: target.Clone(), targetKey: key}
			draft, err := policy.draft(ctx, scope, actor, operator, pair, domain)
			if err != nil {
				return err
			}
			if draft == nil || draft.encode == nil || draft.check == nil || draft.bind == nil {
				return managementError(CodeInvalidConfig, nil)
			}
			pending.ordered = append(pending.ordered, creationThroughDraft{policy, pair, draft})
			seen[pairKey] = true
		}
	}
	candidate.through = pending
	return pending.check(ctx, scope, actor, operator)
}

func (pending *creationThroughPreparation) check(ctx context.Context, scope db.Queryer, actor auth.Principal, operator bool) error {
	if pending == nil {
		return nil
	}
	if pending.operator != operator || !operator && !pending.domain.MatchesPrincipal(actor) {
		return managementError(CodePermission, nil)
	}
	for _, entry := range pending.ordered {
		if err := entry.draft.check(ctx, scope, actor, operator); err != nil {
			return err
		}
	}
	return nil
}

func (pending *creationThroughPreparation) encode(ctx context.Context) error {
	if pending == nil {
		return nil
	}
	if pending.encoded {
		return managementError(CodeConflict, nil)
	}
	for _, entry := range pending.ordered {
		if err := entry.draft.encode(ctx); err != nil {
			return err
		}
	}
	pending.encoded = true
	return ctx.Err()
}

func (pending *creationThroughPreparation) bind(ctx context.Context, scope db.Queryer, actor auth.Principal, id int64) (*throughPreparation, error) {
	if pending == nil {
		return nil, nil
	}
	if !pending.encoded || !pending.domain.MatchesPrincipal(actor) {
		return nil, managementError(CodePermission, nil)
	}
	result := &throughPreparation{domain: pending.domain, actorID: actor.ID(), limit: pending.limit, pairs: make(map[throughPairKey]*throughCandidate), identities: make(map[throughIdentity]*throughCandidate)}
	for _, entry := range pending.ordered {
		if err := entry.draft.check(ctx, scope, actor, false); err != nil {
			return nil, err
		}
		pair := entry.pair
		pair.sourceKey, pair.sourcePresent = id, true
		candidate, err := entry.draft.bind(ctx, pair, pending.domain)
		if err != nil {
			return nil, err
		}
		if candidate == nil || candidate.policy != entry.policy || candidate.identity.model != entry.policy.model || candidate.identity.value == "" || result.identities[candidate.identity] != nil {
			return nil, managementError(CodeInvalidInput, nil)
		}
		key := throughPairKey{policy: entry.policy, source: pair.source.Name, target: pair.target.Name, sourceKey: id, targetKey: pair.targetKey}
		result.pairs[key], result.identities[candidate.identity] = candidate, candidate
		result.ordered = append(result.ordered, candidate)
	}
	return result, nil
}

func (state *creationState[M]) bindCreationThrough(ctx context.Context, scope db.Queryer, actor auth.Principal, pending *throughPreparation, field string, change orm.CollectionChange) (orm.CollectionChange, error) {
	policy := state.through[field]
	if policy == nil {
		return change, nil
	}
	if pending == nil {
		return orm.CollectionChange{}, managementError(CodeInvalidConfig, nil)
	}
	return policy.bind(ctx, scope, actor, pending, true, change)
}
