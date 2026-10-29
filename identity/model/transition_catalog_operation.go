package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"math"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

type preparedTransitionCatalogCreation[T any] struct {
	mappings  []AccountMapping
	requests  []AccountCreation[T]
	createdAt time.Time
}

type catalogTransitionObservation[S, T any] struct {
	transitionObservation[S, T]
	preparedCatalog preparedCatalogCreation
	candidates      []transitionCreationCandidate[T]
	originalTargets []transitionRow[T]
}

func (owner *Transition[S, T]) prepareWithCatalogCreation(ctx context.Context, actor auth.Principal, operation string, mappings []AccountMapping, requests []AccountCreation[T]) (PreparedTransition[S, T], error) {
	var zero PreparedTransition[S, T]
	if owner.config.CatalogCreation.state == nil || nilModelValue(owner.creationBackend) || (len(mappings) == 0) == (len(requests) == 0) {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if owner.config.CatalogCreation.state.groupInputs > owner.config.MaxCatalogRows || owner.config.CatalogCreation.state.permissionInputs > owner.config.MaxCatalogRows {
		return zero, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return zero, managementError(CodeInvalidInput, err)
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return zero, err
	}
	inputs := &preparedTransitionCatalogCreation[T]{mappings: slices.Clone(mappings), requests: slices.Clone(requests)}
	if len(requests) != 0 {
		inputs.createdAt = owner.config.Now().UTC().Truncate(time.Microsecond)
		if !validTransitionTime(inputs.createdAt) {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	observed, err := db.ReadSnapshotResult(ctx, owner.old.state.backend, func(work context.Context, scope db.Queryer) (catalogTransitionObservation[S, T], error) {
		result, failure := owner.observeWithCatalogCreation(work, scope, actor, operation, inputs)
		if expectedTransitionRefusal(failure) {
			return catalogTransitionObservation[S, T]{transitionObservation: transitionObservation[S, T]{rejection: failure}}, nil
		}
		return result, failure
	})
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	if observed.rejection != nil {
		return zero, observed.rejection
	}
	oldRole, _ := owner.old.state.credentials.state.binding.RoleFingerprint()
	nextRole, _ := owner.next.state.credentials.state.binding.RoleFingerprint()
	manifest := TransitionManifest{Version: 7, OperationID: operation, PriorOperationID: observed.ownership.OperationID(), PriorSequence: observed.ownership.Sequence(),
		ProjectSHA256: owner.source, SourceRoleSHA256: oldRole, TargetRoleSHA256: nextRole, StateSHA256: observed.digest,
		References: transitionReferenceManifests(observed.references), CatalogCreation: cloneCatalogCreationManifest(&observed.preparedCatalog.manifest)}
	if len(requests) != 0 {
		for _, request := range requests {
			manifest.CreationSources = append(manifest.CreationSources, request.Source)
		}
	} else {
		for _, pair := range observed.pairs {
			manifest.Mappings = append(manifest.Mappings, AccountMapping{SourceID: pair.old.id, SourceRevision: pair.old.revision, TargetID: pair.next.id, TargetRevision: pair.next.revision})
		}
	}
	evidence, err := manifest.evidence()
	if err != nil {
		return zero, err
	}
	change, err := domain.PrepareOperation(ctx, operation, nextRole, evidence)
	if err != nil {
		return zero, managementError(CodePersistence, err)
	}
	return PreparedTransition[S, T]{state: &preparedTransition[S, T]{owner: owner, actor: actor.ID(), manifest: manifest, expected: observed.ownership, change: change, catalogCreation: inputs}}, nil
}

func validTransitionTime(at time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999
}

func (owner *Transition[S, T]) observeWithCatalogCreation(ctx context.Context, scope db.Queryer, actor auth.Principal, operation string, inputs *preparedTransitionCatalogCreation[T]) (catalogTransitionObservation[S, T], error) {
	var zero catalogTransitionObservation[S, T]
	ownership, current, err := owner.inspectAuthority(ctx, scope, actor)
	if err != nil {
		return zero, err
	}
	catalog, err := owner.observeCatalogRows(ctx, scope, operation)
	if err != nil {
		return zero, err
	}
	plan := owner.config.CatalogCreation.state
	catalog.pendingGroups, catalog.pendingPermissions = plan.groupCreated, plan.permissionCreated
	self := func(rows map[int64]transitionCatalogRow, pending bool) map[int64]int64 {
		if pending {
			return nil
		}
		result := make(map[int64]int64, len(rows))
		for id := range rows {
			result[id] = id
		}
		return result
	}
	catalog.groups, catalog.direct = self(catalog.before.groups, catalog.pendingGroups), self(catalog.before.permissions, catalog.pendingPermissions)
	before, err := readTransitionRows(ctx, scope, owner.old.state.credentials, owner.oldMemberships, owner.config.MaxAccounts, ownership.Generation())
	if err != nil {
		return zero, err
	}
	after, err := readTransitionRows(ctx, scope, owner.next.state.credentials, owner.nextMemberships, owner.config.MaxAccounts, ownership.Generation())
	if err != nil {
		return zero, err
	}
	creatingAccounts := len(inputs.requests) != 0
	if creatingAccounts {
		if len(after) != 0 {
			return zero, managementError(CodeNotFresh, nil)
		}
		if len(before) != len(inputs.requests) {
			return zero, managementError(CodeConflict, nil)
		}
		if err := requireEmptyTransitionIncoming(ctx, scope, owner.config.TargetCreation.state.incoming); err != nil {
			return zero, err
		}
	} else if len(before) != len(inputs.mappings) || len(after) != len(inputs.mappings) {
		return zero, managementError(CodeConflict, nil)
	}
	var referenceSources []SourceAccount
	for id, row := range before {
		referenceSources = append(referenceSources, SourceAccount{ID: id, Revision: row.revision})
	}
	var accountKeys map[int64]int64
	if !creatingAccounts {
		accountKeys = make(map[int64]int64, len(inputs.mappings))
		for _, mapping := range inputs.mappings {
			accountKeys[mapping.SourceID] = mapping.TargetID
		}
	}
	references := targetReferenceMappings(referenceSources, accountKeys, catalog)
	preparedCatalog, err := plan.prepare(ctx, scope, current, catalog, references)
	if err != nil {
		return zero, err
	}
	result := catalogTransitionObservation[S, T]{transitionObservation: transitionObservation[S, T]{ownership: ownership, actor: current, catalog: catalog}, preparedCatalog: preparedCatalog}
	digest := &transitionDigestWriter{hash: sha256.New()}
	digest.part([]byte("godj.account-catalog-created-transition-state.v2"))
	digest.part([]byte(operation))
	digest.part([]byte(current.ID()))
	if err := appendTransitionCatalogDigest(digest, catalog); err != nil {
		return zero, err
	}
	if err := appendTransitionCatalogDigest(digest, transitionCatalogObservation{digest: preparedCatalog.digest, bytes: preparedCatalog.bytes}); err != nil {
		return zero, err
	}
	result.roleLinks, err = owner.observeTransitionAccountLinks(ctx, scope, digest, before, after, catalog)
	if err != nil {
		return zero, err
	}
	for i, id := range slices.Sorted(maps.Keys(before)) {
		source := before[id]
		memberships, err := catalog.memberships(source.memberships)
		if err != nil {
			return zero, err
		}
		var candidate transitionCreationCandidate[T]
		if creatingAccounts {
			request := inputs.requests[i]
			if request.Source.ID != id || request.Source.Revision != source.revision {
				return zero, managementError(CodeConflict, nil)
			}
			candidate, err = prepareTransitionTarget(ctx, scope, current, owner.config.TargetCreation.state, source, memberships, owner.old.state.credentials.state.binding.state.role, request.Input, request.References, references, inputs.createdAt, ownership.Generation())
		} else {
			pair := inputs.mappings[i]
			target, present := after[pair.TargetID]
			if !present || pair.SourceID != id || pair.SourceRevision != source.revision || target.revision != pair.TargetRevision ||
				!slices.Equal(target.memberships.groups, memberships.groups) || !slices.Equal(target.memberships.direct, memberships.direct) {
				return zero, managementError(CodeConflict, nil)
			}
			result.originalTargets = append(result.originalTargets, target)
			if err := writeTransitionRow(digest, target); err != nil {
				return zero, err
			}
			values := map[string]query.Value{}
			revision := target.revision
			if catalog.pendingGroups && len(source.memberships.groups) != 0 || catalog.pendingPermissions && len(source.memberships.direct) != 0 || !slices.Equal(target.memberships.grants, source.memberships.grants) {
				if revision == math.MaxInt64 {
					return zero, managementError(CodeInvalidInput, nil)
				}
				revision++
				values[owner.next.state.credentials.state.binding.state.role.Revision] = query.Integer(revision)
			}
			objects := owner.next.state.credentials.state.binding.state.objects
			candidate.prepared, err = objects.PrepareValues(&target.row, values)
			if err == nil {
				candidate.row = target
				candidate.row.row, err = candidate.prepared.Model()
			}
			if err == nil {
				candidate.row.values, err = objects.ModelValues(candidate.row.row)
			}
			candidate.row.revision, candidate.row.memberships = revision, memberships
		}
		if err != nil {
			return zero, err
		}
		if err := owner.validatePairPolicy(ctx, scope, operation, source, candidate.row, catalog, candidate.resolve == nil); err != nil {
			return zero, err
		}
		if err := writeTransitionRow(digest, source); err != nil {
			return zero, err
		}
		if err := writeTransitionRow(digest, candidate.row); err != nil {
			return zero, err
		}
		if err := writeTransitionTargetReferences(digest, candidate.references); err != nil {
			return zero, err
		}
		result.pairs = append(result.pairs, transitionPair[S, T]{source, candidate.row})
		result.candidates = append(result.candidates, candidate)
	}
	if len(owner.config.References) != 0 {
		sources := make(map[int64]bool, len(before))
		var targets map[int64]int64
		if !creatingAccounts {
			targets = make(map[int64]int64, len(before))
		}
		for _, pair := range result.pairs {
			sources[pair.old.id] = true
			if targets != nil {
				targets[pair.old.id] = pair.next.id
			}
		}
		result.references, err = owner.observeReferences(ctx, scope, operation, referenceMappings(sources, targets, catalog), digest)
		if err != nil {
			return zero, err
		}
	}
	if digest.err != nil {
		return zero, digest.err
	}
	result.digest = hex.EncodeToString(digest.hash.Sum(nil))
	return result, validateAccountSessionScope(ctx, scope)
}

func (owner *Transition[S, T]) commitCatalogCreation(ctx context.Context, actor auth.Principal, state *preparedTransition[S, T]) (TransitionResult, error) {
	if state.catalogCreation == nil || owner.config.CatalogCreation.state == nil || nilModelValue(owner.creationBackend) {
		return TransitionResult{}, managementError(CodeInvalidConfig, nil)
	}
	return managementRelationWrite(ctx, owner.creationBackend, func(work context.Context, scope db.RelationSession) (TransitionResult, error) {
		current, err := owner.observeWithCatalogCreation(work, scope, actor, state.manifest.OperationID, state.catalogCreation)
		if err != nil {
			return TransitionResult{}, err
		}
		if current.digest != state.manifest.StateSHA256 || current.ownership.OperationID() != state.expected.OperationID() {
			return TransitionResult{}, managementError(CodeConflict, nil)
		}
		at := owner.config.Now().UTC().Truncate(time.Microsecond)
		if !validTransitionTime(at) {
			return TransitionResult{}, managementError(CodeInvalidConfig, nil)
		}
		created, err := current.preparedCatalog.apply(work, scope, current.actor, at)
		if err != nil {
			return TransitionResult{}, err
		}
		if err := current.inserted.addRows(owner.next.state.credentials.state.binding.state.project, created.identities.rows); err != nil {
			return TransitionResult{}, err
		}
		current.inserted.identities = append(current.inserted.identities, created.identities.identities...)
		projectRows := func(rows []orm.CollectionInsertedRow) error {
			var err error
			created.before, err = projectTransitionCatalog(owner.oldMemberships, owner.old.state.credentials.state.binding.state.role, created.before, rows)
			if err != nil {
				return err
			}
			created.after, err = projectTransitionCatalog(owner.nextMemberships, owner.next.state.credentials.state.binding.state.role, created.after, rows)
			if err != nil {
				return err
			}
			current.roleLinks, err = projectTransitionAccountLinks(current.roleLinks, rows)
			return err
		}
		if err := projectRows(created.identities.rows); err != nil {
			return TransitionResult{}, err
		}
		for _, batch := range created.insertions {
			if err := current.inserted.add(owner.next.state.credentials.state.binding.state.project, batch); err != nil {
				return TransitionResult{}, err
			}
			if err := projectRows(batch.Insertions()); err != nil {
				return TransitionResult{}, err
			}
		}
		resolved, err := owner.observeCatalogRows(work, scope, state.manifest.OperationID)
		if err != nil {
			return TransitionResult{}, err
		}
		if err := created.verify(resolved); err != nil {
			return TransitionResult{}, err
		}
		if !catalogCreationMatches(current.preparedCatalog.manifest, *created.mapping) {
			return TransitionResult{}, managementError(CodeConflict, nil)
		}
		resolved.mapping = cloneCatalogMappings(created.mapping)
		keys := func(pairs []AccountMapping) map[int64]int64 {
			result := make(map[int64]int64, len(pairs))
			for _, pair := range pairs {
				result[pair.SourceID] = pair.TargetID
			}
			return result
		}
		resolved.groups, resolved.direct = keys(created.mapping.Groups), keys(created.mapping.Permissions)
		pending := current.catalog
		current.catalog = resolved
		// Catalog writes cannot manufacture source authority or adopt target
		// account rows. All later writes still use the already authorized actor.
		if _, _, err := owner.inspectAuthority(work, scope, current.actor); err != nil {
			return TransitionResult{}, err
		}
		creatingAccounts := len(state.catalogCreation.requests) != 0
		if err := owner.verifyCatalogAccountBaseline(work, scope, current, creatingAccounts); err != nil {
			return TransitionResult{}, err
		}
		mappings, err := owner.materializeTargets(work, scope, &current.transitionObservation, current.candidates, creatingAccounts, current.originalTargets)
		if err != nil {
			return TransitionResult{}, err
		}
		if !creatingAccounts && !slices.Equal(mappings, state.manifest.Mappings) {
			return TransitionResult{}, managementError(CodeConflict, nil)
		}
		if err := owner.verifyRows(work, scope, current.transitionObservation, mappings); err != nil {
			return TransitionResult{}, err
		}
		for i, pair := range current.pairs {
			if err := owner.validatePair(work, scope, state.manifest.OperationID, pair.old, pair.next, resolved); err != nil {
				return TransitionResult{}, err
			}
			var fields []string
			action := admin.ActionChange
			if creatingAccounts {
				if err := owner.checkCreatedChoices(work, scope, current.actor, pair.next); err != nil {
					return TransitionResult{}, err
				}
				action, fields = admin.ActionAdd, owner.config.TargetCreation.state.auditFields
			} else if pair.next.revision != current.originalTargets[i].revision {
				fields = owner.catalogAccountChangedFields(pair.old.memberships, pending)
			}
			if len(fields) != 0 {
				model := owner.next.state.credentials.state.binding.state.role.Model
				event, err := accountaudit.Prepare(current.actor, model.AppLabel+"."+model.ModelName, pair.next.id, action, fields, at)
				if err != nil {
					return TransitionResult{}, err
				}
				if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
					return TransitionResult{}, err
				}
			}
		}
		if err := created.verifyPolicy(work, scope); err != nil {
			return TransitionResult{}, err
		}
		if err := owner.verifyRows(work, scope, current.transitionObservation, mappings); err != nil {
			return TransitionResult{}, err
		}
		if _, _, err := owner.inspectAuthority(work, scope, current.actor); err != nil {
			return TransitionResult{}, err
		}
		result, err := owner.completeIn(work, scope, current.transitionObservation, state.expected, state.change, mappings, at, func(ctx context.Context, scope db.Queryer) error {
			if creatingAccounts {
				for _, pair := range current.pairs {
					if err := owner.checkCreatedChoices(ctx, scope, current.actor, pair.next); err != nil {
						return err
					}
				}
			}
			return created.verifyPolicy(ctx, scope)
		}, func(ctx context.Context, scope db.Queryer) error {
			return owner.verifyCatalogCreationIncoming(ctx, scope, current.references)
		})
		if err != nil {
			return TransitionResult{}, err
		}
		if creatingAccounts {
			result.CreatedAccounts = len(mappings)
		}
		if current.preparedCatalog.manifest.CreateGroups {
			result.CreatedGroups = len(created.mapping.Groups)
		}
		if current.preparedCatalog.manifest.CreatePermissions {
			result.CreatedPermissions = len(created.mapping.Permissions)
		}
		return result, nil
	})
}

func (owner *Transition[S, T]) catalogAccountChangedFields(source accountMemberships, pending transitionCatalogObservation) []string {
	role := owner.next.state.credentials.state.binding.state.role
	groupChange := pending.pendingGroups && len(source.groups) != 0
	if pending.pendingPermissions {
		for _, id := range source.groups {
			groupChange = groupChange || len(pending.before.groups[id].permissions) != 0
		}
	}
	permissionChange := pending.pendingPermissions && len(source.direct) != 0
	metadata, _ := owner.next.state.credentials.state.binding.state.objects.Metadata()
	var fields []string
	for _, field := range metadata.ManyToMany {
		if field.Name == role.Groups && groupChange || field.Name == role.Permissions && permissionChange {
			fields = append(fields, field.Name)
		}
	}
	return fields
}
