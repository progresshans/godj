package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/rowset"
)

type preparedTransitionCreation[M any] struct {
	requests  []AccountCreation[M]
	createdAt time.Time
}
type transitionCreationObservation[S, T any] struct {
	transitionObservation[S, T]
	candidates []transitionCreationCandidate[T]
}

func normalizeSourceAccounts(sources []SourceAccount) ([]SourceAccount, error) {
	if len(sources) < 1 || len(sources) > MaximumTransitionAccounts {
		return nil, managementError(CodeInvalidInput, nil)
	}
	owned := slices.Clone(sources)
	slices.SortFunc(owned, func(a, b SourceAccount) int { return compareInt64(a.ID, b.ID) })
	for i, source := range owned {
		if source.Revision <= 0 || i > 0 && source.ID == owned[i-1].ID {
			return nil, managementError(CodeInvalidInput, nil)
		}
	}
	return owned, nil
}

// PrepareCreation reviews every source account and an empty target model in
// one snapshot. Inputs are owned typed login/profile intent; the actual native
// target keys will be written to the durable mapping at Commit. Retain the
// returned manifest before attempting Commit, including across unknown outcomes.
func (owner *Transition[S, T]) PrepareCreation(ctx context.Context, actor auth.Principal, operationID string, requests []AccountCreation[T], catalogs ...CatalogMappings) (PreparedTransition[S, T], error) {
	var zero PreparedTransition[S, T]
	if owner == nil || owner.config.TargetCreation.state == nil || nilModelValue(owner.creationBackend) {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || !transitionDigest(operationID) || len(requests) < 1 || len(requests) > owner.config.MaxAccounts {
		return zero, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return zero, managementError(CodeInvalidInput, err)
	}
	owned := slices.Clone(requests)
	slices.SortFunc(owned, func(a, b AccountCreation[T]) int { return compareInt64(a.Source.ID, b.Source.ID) })
	sources := make([]SourceAccount, len(owned))
	for i, request := range owned {
		sources[i] = request.Source
	}
	if _, err := normalizeSourceAccounts(sources); err != nil {
		return zero, err
	}
	if owner.config.CatalogCreation.state != nil {
		if len(catalogs) != 0 {
			return zero, managementError(CodeInvalidInput, nil)
		}
		return owner.prepareWithCatalogCreation(ctx, actor, operationID, nil, owned)
	}
	catalog, err := owner.prepareCatalogMappings(catalogs)
	if err != nil {
		return zero, err
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return zero, err
	}
	createdAt := owner.config.Now().UTC().Truncate(time.Microsecond)
	if createdAt.IsZero() || createdAt.Year() < 1 || createdAt.Year() > 9999 {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	observation, err := db.ReadSnapshotResult(ctx, owner.old.state.backend, func(work context.Context, scope db.Queryer) (transitionCreationObservation[S, T], error) {
		result, failure := owner.observeCreation(work, scope, actor, operationID, owned, createdAt, catalog)
		if expectedTransitionRefusal(failure) {
			return transitionCreationObservation[S, T]{transitionObservation: transitionObservation[S, T]{rejection: failure}}, nil
		}
		return result, failure
	})
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	if observation.rejection != nil {
		return zero, observation.rejection
	}
	oldRole, _ := owner.old.state.credentials.state.binding.RoleFingerprint()
	nextRole, _ := owner.next.state.credentials.state.binding.RoleFingerprint()
	manifest := TransitionManifest{Version: 4, OperationID: operationID, PriorOperationID: observation.ownership.OperationID(), PriorSequence: observation.ownership.Sequence(),
		ProjectSHA256: owner.source, SourceRoleSHA256: oldRole, TargetRoleSHA256: nextRole, StateSHA256: observation.digest, CreationSources: sources, References: transitionReferenceManifests(observation.references), Catalog: catalog}
	manifest.Version = transitionManifestVersion(catalog, manifest.References, nil)
	evidence, err := manifest.evidence()
	if err != nil {
		return zero, err
	}
	change, err := domain.PrepareOperation(ctx, operationID, nextRole, evidence)
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	return PreparedTransition[S, T]{state: &preparedTransition[S, T]{owner: owner, actor: actor.ID(), manifest: manifest, expected: observation.ownership, change: change,
		creation: &preparedTransitionCreation[T]{requests: owned, createdAt: createdAt}}}, nil
}

func (owner *Transition[S, T]) observeCreation(ctx context.Context, scope db.Queryer, actor auth.Principal, operation string, requests []AccountCreation[T], createdAt time.Time, catalog *CatalogMappings) (transitionCreationObservation[S, T], error) {
	var zero transitionCreationObservation[S, T]
	ownership, current, err := owner.inspectAuthority(ctx, scope, actor)
	if err != nil {
		return zero, err
	}
	observedCatalog, err := owner.observeCatalog(ctx, scope, operation, catalog)
	if err != nil {
		return zero, err
	}
	old, next := owner.old.state.credentials, owner.next.state.credentials
	present, err := next.state.binding.state.objects.Using(scope).Exists(ctx)
	if err != nil {
		return zero, err
	}
	if present {
		return zero, managementError(CodeNotFresh, nil)
	}
	// Probe all target columns even on an empty table. A bound declaration is
	// not evidence that its actual schema has been installed.
	if _, _, err := next.state.binding.state.objects.Using(scope).OrderBy(next.state.key.Asc()).First(ctx); err != nil {
		return zero, err
	}
	// Empty target storage must have no incoming non-NULL reference. Otherwise
	// a generated key could silently adopt orphan host rows or memberships from
	// damaged storage. Every registered incoming FK uses a bounded native query.
	for _, plan := range owner.config.TargetCreation.state.incoming {
		found := false
		err := rowset.Scan(ctx, scope, plan, func(err error) error { return managementError(CodePersistence, err) }, func(rows db.Rows) (bool, error) {
			var key int64
			if err := rows.Scan(&key); err != nil {
				return false, err
			}
			found = true
			return false, nil
		})
		if err != nil {
			return zero, err
		}
		if found {
			return zero, managementError(CodeNotFresh, nil)
		}
	}
	before, err := readTransitionRows(ctx, scope, old, owner.oldMemberships, owner.config.MaxAccounts, ownership.Generation())
	if err != nil {
		return zero, err
	}
	if len(before) != len(requests) {
		return zero, managementError(CodeConflict, nil)
	}
	result := transitionCreationObservation[S, T]{transitionObservation: transitionObservation[S, T]{ownership: ownership, actor: current, catalog: observedCatalog}}
	digest := &transitionDigestWriter{hash: sha256.New()}
	digest.part([]byte("godj.account-created-transition-state.v1"))
	digest.part([]byte(operation))
	digest.part([]byte(current.ID()))
	if err := appendTransitionCatalogDigest(digest, observedCatalog); err != nil {
		return zero, err
	}
	for _, request := range requests {
		source, present := before[request.Source.ID]
		if !present || source.revision != request.Source.Revision {
			return zero, managementError(CodeConflict, nil)
		}
		mapped, err := observedCatalog.memberships(source.memberships)
		if err != nil {
			return zero, err
		}
		candidate, err := prepareTransitionTarget(ctx, scope, current, owner.config.TargetCreation.state, source, mapped, old.state.binding.state.role, request.Input, createdAt, ownership.Generation())
		if err != nil {
			return zero, err
		}
		if err := owner.validatePair(ctx, scope, operation, source, candidate.row, observedCatalog); err != nil {
			return zero, err
		}
		if err := writeTransitionRow(digest, source); err != nil {
			return zero, err
		}
		if err := writeTransitionRow(digest, candidate.row); err != nil {
			return zero, err
		}
		result.pairs = append(result.pairs, transitionPair[S, T]{old: source, next: candidate.row})
		result.candidates = append(result.candidates, candidate)
	}
	if len(owner.config.References) > 0 {
		sources := make(map[int64]bool, len(before))
		for id := range before {
			sources[id] = true
		}
		result.references, err = owner.observeReferences(ctx, scope, operation, referenceMappings(sources, nil, observedCatalog), digest)
		if err != nil {
			return zero, err
		}
	}
	result.digest = hex.EncodeToString(digest.hash.Sum(nil))
	return result, validateAccountSessionScope(ctx, scope)
}

func (owner *Transition[S, T]) commitCreation(ctx context.Context, actor auth.Principal, state *preparedTransition[S, T]) (TransitionResult, error) {
	if state.creation == nil || owner.config.TargetCreation.state == nil || nilModelValue(owner.creationBackend) {
		return TransitionResult{}, managementError(CodeInvalidConfig, nil)
	}
	return managementRelationWrite(ctx, owner.creationBackend, func(work context.Context, scope db.RelationSession) (TransitionResult, error) {
		current, err := owner.observeCreation(work, scope, actor, state.manifest.OperationID, state.creation.requests, state.creation.createdAt, state.manifest.Catalog)
		if err != nil {
			return TransitionResult{}, err
		}
		if current.digest != state.manifest.StateSHA256 || current.ownership.OperationID() != state.expected.OperationID() {
			return TransitionResult{}, managementError(CodeConflict, nil)
		}
		at := owner.config.Now().UTC().Truncate(time.Microsecond)
		if at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
			return TransitionResult{}, managementError(CodeInvalidConfig, nil)
		}
		target := owner.config.TargetCreation.state
		mappings, err := owner.materializeTargets(work, scope, &current.transitionObservation, current.candidates, true, nil)
		if err != nil {
			return TransitionResult{}, err
		}
		observed, err := owner.observe(work, scope, actor, state.manifest.OperationID, mappings, state.manifest.Catalog)
		if err != nil {
			return TransitionResult{}, err
		}
		if len(observed.pairs) != len(current.pairs) {
			return TransitionResult{}, managementError(CodePersistence, nil)
		}
		if observed.catalog.digest != current.catalog.digest {
			return TransitionResult{}, managementError(CodeConflict, nil)
		}
		if !sameTransitionReferenceManifests(transitionReferenceManifests(observed.references), transitionReferenceManifests(current.references)) {
			return TransitionResult{}, managementError(CodeConflict, nil)
		}
		for i, pair := range observed.pairs {
			if !sameTransitionRow(pair.old, current.pairs[i].old) || !sameTransitionRow(pair.next, current.pairs[i].next) {
				return TransitionResult{}, managementError(CodeConflict, nil)
			}
			if err := owner.checkCreatedChoices(work, scope, current.actor, pair.next); err != nil {
				return TransitionResult{}, err
			}
			event, err := accountaudit.Prepare(current.actor, target.binding.state.role.Model.AppLabel+"."+target.binding.state.role.Model.ModelName, pair.next.id, admin.ActionAdd, target.auditFields, at)
			if err != nil {
				return TransitionResult{}, err
			}
			if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
				return TransitionResult{}, err
			}
		}
		// The common owner retires sources, revokes their entire session namespace
		// and records the actual keys. Its final readback includes every target.
		result, err := owner.completeIn(work, scope, observed, state.expected, state.change, mappings, at, func(ctx context.Context, scope db.Queryer) error {
			for _, pair := range observed.pairs {
				if err := owner.checkCreatedChoices(ctx, scope, current.actor, pair.next); err != nil {
					return err
				}
			}
			return nil
		}, nil)
		if err != nil {
			return TransitionResult{}, err
		}
		result.CreatedAccounts = len(mappings)
		return result, nil
	})
}

func (owner *Transition[S, T]) checkCreatedChoices(ctx context.Context, scope db.Queryer, actor auth.Principal, row transitionRow[T]) error {
	target := owner.config.TargetCreation.state
	origin := ProfileRelationOrigin{operation: ProfileRelationTransition}
	if err := checkProfileRelations(ctx, scope, actor, origin, target.relations, row.values); err != nil {
		return err
	}
	return checkProfileCollections(ctx, scope, actor, origin, target.collections, row.profile)
}
