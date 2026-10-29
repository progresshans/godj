package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

type transitionCollectionKey struct {
	owner ir.ModelIdentity
	field string
}

func transitionThroughDefinitionLimit(configured map[string]ThroughCreation) int {
	if len(configured) == 0 {
		return 0
	}
	return MaximumThroughCreations
}

func bindTransitionThroughDefinitions[M any](store *CredentialStore[M], definitions map[transitionCollectionKey]*throughCreationState, limit int) (map[transitionCollectionKey]*throughCreationState, error) {
	if len(definitions) == 0 && limit != 0 || len(definitions) != 0 && (limit < 1 || limit > MaximumThroughCreations) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	result := make(map[transitionCollectionKey]*throughCreationState, len(definitions))
	attached := make(map[*throughCreationState]*throughCreationState)
	models := make(map[ir.ModelIdentity]*throughCreationState)
	for key, policy := range definitions {
		if previous := models[policy.model]; previous != nil && previous != policy {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		models[policy.model] = policy
		bound := attached[policy]
		if bound == nil {
			var err error
			bound, err = policy.attach(store)
			if err != nil {
				return nil, err
			}
			attached[policy] = bound
		}
		result[key] = bound
	}
	return result, nil
}

// TransitionCollectionManifest records the role of each reviewed physical
// creation intent, in deterministic order. Native keys are assigned only by
// Commit and retained in the ownership receipt. StateSHA256 binds the complete
// endpoint correspondence, declaration and encoded typed scalar intent.
type TransitionCollectionManifest struct {
	Kind              TransitionReferenceKind `json:"kind"`
	DeclarationSHA256 string                  `json:"declaration_sha256"`
}

type transitionThroughIntent struct {
	policy *throughCreationState
	pair   ThroughEndpoints
	kind   TransitionReferenceKind
	draft  *throughDraft
}

type transitionThroughPreparation struct {
	ordered           []transitionThroughIntent
	encoded           bool
	authority, domain auth.CredentialDomain
	actorID           string
	limit             int
	bytes             int
}

type transitionLogicalEndpoint struct {
	field     string
	key       int64
	present   bool
	reference TransitionTargetReference
}
type transitionLogicalPair struct {
	model       ir.ModelIdentity
	left, right transitionLogicalEndpoint
}

func transitionThroughPairKey(policy *throughCreationState, pair ThroughEndpoints) transitionLogicalPair {
	left := transitionLogicalEndpoint{pair.source.Name, pair.sourceKey, pair.sourcePresent, pair.sourceReference}
	right := transitionLogicalEndpoint{pair.target.Name, pair.targetKey, pair.targetPresent, pair.targetReference}
	if left.field > right.field {
		left, right = right, left
	}
	return transitionLogicalPair{policy.model, left, right}
}

func transitionIdentityKind(role ir.AccountRole, model ir.ModelIdentity) (TransitionReferenceKind, error) {
	switch model {
	case role.Model:
		return TransitionReferenceAccount, nil
	case role.Catalog.Group:
		return TransitionReferenceGroup, nil
	case role.Catalog.Permission:
		return TransitionReferencePermission, nil
	default:
		return "", managementError(CodeInvalidConfig, nil)
	}
}

func (owner *Transition[S, T]) transitionThroughIntents(current transitionObservation[S, T], creating bool) ([]transitionThroughIntent, error) {
	if len(owner.through) == 0 {
		return nil, nil
	}
	role, project := owner.next.state.credentials.state.binding.state.role, owner.next.state.credentials.state.binding.state.project
	keys := slices.Collect(maps.Keys(owner.through))
	slices.SortFunc(keys, func(a, b transitionCollectionKey) int {
		if compared := compareTransitionReferenceIdentity(a.owner, b.owner); compared != 0 {
			return compared
		}
		if a.field < b.field {
			return -1
		}
		if a.field > b.field {
			return 1
		}
		return 0
	})
	var intents []transitionThroughIntent
	seen := make(map[transitionLogicalPair]bool)
	for _, selected := range keys {
		policy := owner.through[selected]
		kind, err := transitionIdentityKind(role, policy.model)
		if err != nil {
			return nil, err
		}
		var source, target ir.Field
		for _, relation := range project.ManyToManyRelations() {
			if relation.Source == selected.owner && relation.Field == selected.field {
				metadata, found := project.Model(relation.Through.Model)
				var left, right bool
				source, left = namedField(metadata, relation.Through.SourceField)
				target, right = namedField(metadata, relation.Through.TargetField)
				if !found || !left || !right {
					return nil, managementError(CodeInvalidConfig, nil)
				}
			}
		}
		if source.Name == "" || target.Name == "" {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		appendPair := func(pair ThroughEndpoints) error {
			if pair.sourcePresent && pair.targetPresent {
				var rows []deletionLink
				if selected.owner == role.Catalog.Group {
					rows = current.catalog.after.links
				} else {
					for _, inventory := range current.roleLinks {
						if inventory.membership.owner == selected.owner && inventory.membership.field == selected.field {
							rows = inventory.rows
						}
					}
				}
				for _, row := range rows {
					if row.sourceValid && row.targetValid && row.source == pair.sourceKey && row.target == pair.targetKey {
						return nil
					}
				}
			}
			key := transitionThroughPairKey(policy, pair)
			if seen[key] {
				return nil
			}
			if len(intents) >= owner.config.ThroughCreationLimit {
				return managementError(CodeInvalidInput, nil)
			}
			seen[key] = true
			intents = append(intents, transitionThroughIntent{policy: policy, pair: pair, kind: kind})
			return nil
		}
		catalogEndpoint := func(kind TransitionReferenceKind, id int64) (int64, bool, TransitionTargetReference, error) {
			rows, targets, pending := current.catalog.before.groups, current.catalog.groups, current.catalog.pendingGroups
			if kind == TransitionReferencePermission {
				rows, targets, pending = current.catalog.before.permissions, current.catalog.direct, current.catalog.pendingPermissions
			}
			row, present := rows[id]
			if !present {
				return 0, false, TransitionTargetReference{}, managementError(CodeConflict, nil)
			}
			reference := TransitionTargetReference{Kind: kind, Source: SourceAccount{ID: id, Revision: row.revision}}
			if pending {
				return 0, false, reference, nil
			}
			key, found := targets[id]
			if !found {
				return 0, false, reference, managementError(CodeConflict, nil)
			}
			return key, true, reference, nil
		}
		if selected.owner == role.Model {
			for _, account := range current.pairs {
				pair := ThroughEndpoints{source: source.Clone(), target: target.Clone(), sourceKey: account.next.id, sourcePresent: !creating,
					sourceReference: TransitionTargetReference{Kind: TransitionReferenceAccount, Source: SourceAccount{ID: account.old.id, Revision: account.old.revision}}}
				if creating {
					pair.sourceKey = 0
				}
				chosen := account.next.profile[selected.field]
				targetKind := TransitionReferenceKind("")
				if selected.field == role.Groups {
					chosen, targetKind = account.old.memberships.groups, TransitionReferenceGroup
				}
				if selected.field == role.Permissions {
					chosen, targetKind = account.old.memberships.direct, TransitionReferencePermission
				}
				for _, id := range chosen {
					pair.targetKey, pair.targetPresent, pair.targetReference = id, true, TransitionTargetReference{}
					if targetKind != "" {
						pair.targetKey, pair.targetPresent, pair.targetReference, err = catalogEndpoint(targetKind, id)
						if err != nil {
							return nil, err
						}
					}
					if err := appendPair(pair); err != nil {
						return nil, err
					}
				}
			}
		} else if selected.owner == role.Catalog.Group && owner.config.CatalogCreation.state != nil {
			for _, id := range slices.Sorted(maps.Keys(current.catalog.before.groups)) {
				pair := ThroughEndpoints{source: source.Clone(), target: target.Clone()}
				pair.sourceKey, pair.sourcePresent, pair.sourceReference, err = catalogEndpoint(TransitionReferenceGroup, id)
				if err != nil {
					return nil, err
				}
				for _, permission := range current.catalog.before.groups[id].permissions {
					pair.targetKey, pair.targetPresent, pair.targetReference, err = catalogEndpoint(TransitionReferencePermission, permission)
					if err != nil {
						return nil, err
					}
					if err := appendPair(pair); err != nil {
						return nil, err
					}
				}
			}
		} else {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	return intents, nil
}

func (owner *Transition[S, T]) prepareTransitionThrough(ctx context.Context, scope db.Queryer, current *transitionObservation[S, T], creating bool) error {
	intents, err := owner.transitionThroughIntents(*current, creating)
	if err != nil {
		return err
	}
	if len(intents) == 0 {
		return nil
	}
	authority, ok := current.actor.CredentialDomain()
	if !ok {
		return managementError(CodePermission, nil)
	}
	domain, err := owner.next.state.credentials.state.binding.CredentialDomain(current.ownership.Generation())
	if err != nil {
		return err
	}
	pending := &transitionThroughPreparation{ordered: intents, authority: authority, domain: domain, actorID: current.actor.ID(), limit: owner.config.ThroughCreationLimit}
	for i := range pending.ordered {
		entry := &pending.ordered[i]
		if entry.policy.draft == nil {
			return managementError(CodeInvalidConfig, nil)
		}
		entry.draft, err = entry.policy.draft(ctx, scope, current.actor, false, entry.pair, domain)
		if err != nil {
			return err
		}
		if entry.draft == nil || entry.draft.check == nil || entry.draft.encode == nil || entry.draft.bind == nil || entry.draft.values == nil {
			return managementError(CodeInvalidConfig, nil)
		}
	}
	current.through = pending
	return pending.check(ctx, scope, current.actor)
}

func (pending *transitionThroughPreparation) check(ctx context.Context, scope db.Queryer, actor auth.Principal) error {
	if pending == nil {
		return nil
	}
	if actor.ID() != pending.actorID || !pending.authority.MatchesPrincipal(actor) {
		return managementError(CodePermission, nil)
	}
	for _, entry := range pending.ordered {
		if err := entry.draft.check(ctx, scope, actor, false); err != nil {
			return err
		}
	}
	return validateAccountSessionScope(ctx, scope)
}

func (pending *transitionThroughPreparation) encode(ctx context.Context) error {
	if pending == nil {
		return nil
	}
	if pending.encoded {
		return managementError(CodeConflict, nil)
	}
	// Bound the aggregate before beginning password work, then again after
	// encoding. Raw passwords are never included in exported digests.
	budget := &transitionDigestWriter{hash: sha256.New()}
	for _, entry := range pending.ordered {
		if err := writeTransitionValues(budget, entry.draft.values()); err != nil {
			return err
		}
	}
	for _, entry := range pending.ordered {
		if err := entry.draft.encode(ctx); err != nil {
			return err
		}
	}
	pending.encoded = true
	return ctx.Err()
}

func (pending *transitionThroughPreparation) seal(base, operation string) (string, error) {
	if pending == nil {
		return base, nil
	}
	if !pending.encoded {
		return "", managementError(CodeInvalidConfig, nil)
	}
	digest := &transitionDigestWriter{hash: sha256.New()}
	digest.part([]byte("godj.transition-through.v1"))
	digest.part([]byte(operation))
	digest.part([]byte(base))
	for _, entry := range pending.ordered {
		pair := entry.pair
		encoded, err := json.Marshal(struct {
			Model                                 ir.ModelIdentity
			Declaration, SourceField, TargetField string
			SourceKey, TargetKey                  int64
			SourcePresent, TargetPresent          bool
			SourceReference, TargetReference      TransitionTargetReference
		}{entry.policy.model, entry.policy.declaration, pair.source.Name, pair.target.Name, pair.sourceKey, pair.targetKey, pair.sourcePresent, pair.targetPresent, pair.sourceReference, pair.targetReference})
		if err != nil {
			return "", err
		}
		digest.part(encoded)
		if err := writeTransitionValues(digest, entry.draft.values()); err != nil {
			return "", err
		}
	}
	pending.bytes = digest.size
	return hex.EncodeToString(digest.hash.Sum(nil)), digest.err
}

func (pending *transitionThroughPreparation) manifest() []TransitionCollectionManifest {
	if pending == nil {
		return nil
	}
	result := make([]TransitionCollectionManifest, len(pending.ordered))
	for i, entry := range pending.ordered {
		result[i] = TransitionCollectionManifest{entry.kind, entry.policy.declaration}
	}
	return result
}

func (owner *Transition[S, T]) checkPreparedTransitionThrough(ctx context.Context, scope db.Queryer, current *transitionObservation[S, T], creating bool, pending *transitionThroughPreparation, operation string) error {
	intents, err := owner.transitionThroughIntents(*current, creating)
	if err != nil {
		return err
	}
	if pending == nil {
		if len(intents) != 0 {
			return managementError(CodeConflict, nil)
		}
		return nil
	}
	if len(intents) != len(pending.ordered) {
		return managementError(CodeConflict, nil)
	}
	for i, entry := range intents {
		before := pending.ordered[i]
		if entry.policy != before.policy || transitionThroughPairKey(entry.policy, entry.pair) != transitionThroughPairKey(before.policy, before.pair) {
			return managementError(CodeConflict, nil)
		}
	}
	if err := pending.check(ctx, scope, current.actor); err != nil {
		return err
	}
	current.through = pending
	current.digest, err = pending.seal(current.digest, operation)
	return err
}

// Kept here with the typed intent owner: an unresolved key is never encoded as
// a sentinel integer, even when a native sequence can allocate zero or negatives.
func resolveTransitionThroughPair(pair ThroughEndpoints, mappings transitionTargetMappings) (ThroughEndpoints, error) {
	resolve := func(key *int64, present *bool, reference TransitionTargetReference) error {
		if reference.Kind == "" {
			if !*present {
				return managementError(CodeInvalidConfig, nil)
			}
			return nil
		}
		namespace, found := mappings[reference.Kind]
		actual, known := namespace.targets[reference.Source.ID]
		if !found || !known || namespace.revisions[reference.Source.ID] != reference.Source.Revision || *present && *key != actual {
			return managementError(CodeConflict, nil)
		}
		*key, *present = actual, true
		return nil
	}
	if err := resolve(&pair.sourceKey, &pair.sourcePresent, pair.sourceReference); err != nil {
		return ThroughEndpoints{}, err
	}
	if err := resolve(&pair.targetKey, &pair.targetPresent, pair.targetReference); err != nil {
		return ThroughEndpoints{}, err
	}
	return pair, nil
}
