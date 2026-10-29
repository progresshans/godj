package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

const MaximumTransitionAccounts = domain.MaximumMappings

// AccountMapping explicitly pairs actual target rows with every source
// account. Keys include zero and negative values. Revisions are checked
// alongside the complete observed rows and memberships, never instead of them.
type AccountMapping = domain.Mapping

// TransitionConfig owns an explicit operator operation over two typed models.
// Targets may be staged explicitly or created with TargetCreation and
// PrepareCreation. The operation preserves principal/password/roles and the
// exact group/direct/effective grants through a shared catalog or an explicit
// complete catalog mapping supplied to Prepare/PrepareCreation. It does not infer a
// mapping or profile conversion or adopt historical systemstate storage.
// Explicit References may populate staged host FK/OneToOne destination fields
// while retaining their source fields and declared schema meanings.
//
// Validate is optional application policy over detached, password-redacted
// source/target rows and complete profile memberships. It runs in preparation
// when a complete typed target exists, and again inside the final fence.
// Symbolic target references defer this policy until their real keys have
// been bound; an incomplete typed row is never passed as a preview.
// It must not mutate/retain the borrowed
// reader, re-enter the backend or publish external effects. All callbacks must
// be safe for concurrent calls. MaxAccounts defaults to 4096 and fails closed.
type TransitionConfig[S, T any] struct {
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	Authorizer            auth.Authorizer
	MaxAccounts           int
	MaxCatalogRows        int
	Validate              func(context.Context, db.Queryer, S, T, AccountMemberships, AccountMemberships) (validation.Errors, error)
	Now                   func() time.Time
	TargetCreation        TransitionTarget[T]
	CatalogCreation       CatalogCreation
	References            []TransitionReference[S, T]
	// Required when a target/catalog config declares ThroughCreations; the
	// operation admits at most this many distinct physical creation intents.
	ThroughCreationLimit int
}

func (TransitionConfig[S, T]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionConfig{redacted}"))
}
func (TransitionConfig[S, T]) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.model.TransitionConfig{redacted}"`), nil
}

type Transition[S, T any] struct {
	source          string
	old             *SessionStore[S]
	next            *SessionStore[T]
	config          TransitionConfig[S, T]
	required        []auth.Permission
	oldMemberships  []deletionMembership
	nextMemberships []deletionMembership
	creationBackend db.CoordinatedRelationAtomic
	through         map[transitionCollectionKey]*throughCreationState
}

func (*Transition[S, T]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Transition{redacted}"))
}

// NewTransition binds both stores to the same actual backend, complete host
// source. Source and target account tables must differ. Different catalogs
// require explicit correspondence at preparation. No I/O,
// entropy, hashing, policy callback or migration is performed here.
func NewTransition[S, T any](source *SessionStore[S], target *SessionStore[T], config TransitionConfig[S, T]) (*Transition[S, T], error) {
	if source == nil || source.state == nil || target == nil || target.state == nil || nilModelValue(config.Authorizer) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	old, next := source.state.credentials, target.state.credentials
	if old.valid() != nil || next.valid() != nil || !reflect.ValueOf(source.state.backend).Comparable() ||
		!reflect.ValueOf(target.state.backend).Comparable() || source.state.backend != target.state.backend || source.state.limits != target.state.limits {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	a, b := old.state.binding.state, next.state.binding.state
	left, err := a.project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	right, err := b.project.SourceSHA256()
	if err != nil || left != right || a.role.Model == b.role.Model {
		return nil, managementError(CodeInvalidConfig, err)
	}
	oldModel, _ := a.project.Model(a.role.Model)
	newModel, _ := b.project.Model(b.role.Model)
	if oldModel.DBTable == newModel.DBTable {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	for _, catalog := range []ir.AccountCatalog{a.role.Catalog, b.role.Catalog} {
		if a.role.Model == catalog.Group || a.role.Model == catalog.Permission || b.role.Model == catalog.Group || b.role.Model == catalog.Permission {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	if a.role.Catalog.Group == b.role.Catalog.Permission || a.role.Catalog.Permission == b.role.Catalog.Group {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	required, err := auth.RequiredPermissions(config.Permission, config.AdditionalPermissions...)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	if config.MaxAccounts == 0 {
		config.MaxAccounts = 4096
	}
	if config.MaxAccounts < 1 || config.MaxAccounts > MaximumTransitionAccounts {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if config.MaxCatalogRows == 0 {
		config.MaxCatalogRows = 4096
	}
	if config.MaxCatalogRows < 1 || config.MaxCatalogRows > MaximumTransitionCatalogRows {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	config.AdditionalPermissions = slices.Clone(config.AdditionalPermissions)
	if catalog := config.CatalogCreation.state; catalog != nil {
		if catalog.source != left || catalog.oldRole != a.roleFingerprint || catalog.nextRole != b.roleFingerprint {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		for _, permission := range catalog.required {
			if !slices.Contains(required, permission) {
				required = append(required, permission)
			}
		}
	}
	if len(config.References) > MaximumTransitionReferenceModels {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	config.References = slices.Clone(config.References)
	for _, reference := range config.References {
		if reference.state == nil || reference.state.source != left || reference.state.oldRole != a.roleFingerprint || reference.state.nextRole != b.roleFingerprint {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		for _, permission := range reference.state.required {
			if !slices.Contains(required, permission) {
				required = append(required, permission)
			}
		}
	}
	if _, err := auth.RequiredPermissions(required[0], required[1:]...); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	slices.SortFunc(config.References, func(a, b TransitionReference[S, T]) int {
		return compareTransitionReferenceIdentity(a.state.model, b.state.model)
	})
	for i := 1; i < len(config.References); i++ {
		if config.References[i-1].state.model == config.References[i].state.model {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	oldMemberships, err := bindDeletionMemberships(a.project, a.role)
	if err != nil {
		return nil, err
	}
	nextMemberships, err := bindDeletionMemberships(b.project, b.role)
	if err != nil {
		return nil, err
	}
	var creationBackend db.CoordinatedRelationAtomic
	if creation := config.TargetCreation.state; creation != nil {
		boundSource, err := creation.binding.state.project.SourceSHA256()
		if err != nil || boundSource != right || creation.binding.state.roleFingerprint != b.roleFingerprint {
			return nil, managementError(CodeInvalidConfig, err)
		}
	}
	if config.TargetCreation.state != nil || config.CatalogCreation.state != nil {
		var ok bool
		creationBackend, ok = source.state.backend.(db.CoordinatedRelationAtomic)
		if !ok || nilModelValue(creationBackend) {
			return nil, managementError(CodeInvalidConfig, nil)
		}
	}
	definitions := make(map[transitionCollectionKey]*throughCreationState)
	if creation := config.TargetCreation.state; creation != nil {
		for field, policy := range creation.through {
			definitions[transitionCollectionKey{b.role.Model, field}] = policy
		}
	}
	if catalog := config.CatalogCreation.state; catalog != nil {
		for key, policy := range catalog.through {
			if before := definitions[key]; before != nil && before != policy {
				return nil, managementError(CodeInvalidConfig, nil)
			}
			definitions[key] = policy
		}
	}
	for _, relation := range b.project.ManyToManyRelations() {
		selected := relation.Source == b.role.Model && (config.TargetCreation.state != nil && config.TargetCreation.state.allowed[relation.Field] ||
			(config.TargetCreation.state != nil || config.CatalogCreation.state != nil) && (relation.Field == b.role.Groups || relation.Field == b.role.Permissions)) ||
			relation.Source == b.role.Catalog.Group && config.CatalogCreation.state != nil && relation.Field == b.role.Catalog.GroupPermissions
		if selected {
			if err := validateLifecycleThroughStorage(a.role, relation.Through.Model); err != nil && definitions[transitionCollectionKey{relation.Source, relation.Field}] == nil {
				return nil, err
			}
		}
	}
	through, err := bindTransitionThroughDefinitions(next, definitions, config.ThroughCreationLimit)
	if err != nil {
		return nil, err
	}
	return &Transition[S, T]{source: left, old: source, next: target, config: config, required: required,
		oldMemberships: oldMemberships, nextMemberships: nextMemberships, creationBackend: creationBackend, through: through}, nil
}

// TransitionManifest is deliberately exportable reconciliation evidence. It
// contains keys, revisions and salted state digests, never account values,
// credentials or session data. Retain it BEFORE Commit, including on unknown
// outcomes. The digest is evidence identity, not authorization or a bearer key.
type TransitionManifest struct {
	Version          int                            `json:"version"`
	OperationID      string                         `json:"operation_id"`
	PriorOperationID string                         `json:"prior_operation_id"`
	PriorSequence    int64                          `json:"prior_sequence"`
	ProjectSHA256    string                         `json:"project_sha256"`
	SourceRoleSHA256 string                         `json:"source_role_sha256"`
	TargetRoleSHA256 string                         `json:"target_role_sha256"`
	StateSHA256      string                         `json:"state_sha256"`
	Mappings         []AccountMapping               `json:"mappings,omitempty"`
	CreationSources  []SourceAccount                `json:"creation_sources,omitempty"`
	References       []TransitionReferenceManifest  `json:"references,omitempty"`
	Catalog          *CatalogMappings               `json:"catalog,omitempty"`
	CatalogCreation  *CatalogCreationManifest       `json:"catalog_creation,omitempty"`
	Collections      []TransitionCollectionManifest `json:"collections,omitempty"`
}

func (TransitionManifest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionManifest{redacted}"))
}

type PreparedTransition[S, T any] struct{ state *preparedTransition[S, T] }
type preparedTransition[S, T any] struct {
	owner           *Transition[S, T]
	actor           string
	manifest        TransitionManifest
	expected        domain.Receipt
	change          domain.Change
	consumed        atomic.Bool
	creation        *preparedTransitionCreation[T]
	catalogCreation *preparedTransitionCatalogCreation[T]
	through         *transitionThroughPreparation
}

func (PreparedTransition[S, T]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PreparedTransition{redacted}"))
}
func (prepared PreparedTransition[S, T]) Manifest() TransitionManifest {
	if prepared.state == nil {
		return TransitionManifest{}
	}
	result := prepared.state.manifest
	result.Mappings = slices.Clone(result.Mappings)
	result.CreationSources = slices.Clone(result.CreationSources)
	result.References = cloneTransitionReferenceManifests(result.References)
	result.Catalog = cloneCatalogMappings(result.Catalog)
	result.CatalogCreation = cloneCatalogCreationManifest(result.CatalogCreation)
	result.Collections = slices.Clone(result.Collections)
	return result
}

// Prepare is read-only. operationID is one fresh canonical 256-bit hex ID
// chosen by the operator before the attempt. The complete source/target
// inventories must be covered one-to-one; coincident IDs never imply adoption.
func (owner *Transition[S, T]) Prepare(ctx context.Context, actor auth.Principal, operationID string, mappings []AccountMapping, catalogs ...CatalogMappings) (PreparedTransition[S, T], error) {
	if owner == nil || owner.old == nil || owner.next == nil {
		return PreparedTransition[S, T]{}, managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) || !transitionDigest(operationID) || len(mappings) == 0 || len(mappings) > owner.config.MaxAccounts {
		return PreparedTransition[S, T]{}, managementError(CodeInvalidInput, nil)
	}
	owned, err := normalizeAccountMappings(mappings)
	if err != nil {
		return PreparedTransition[S, T]{}, err
	}
	if owner.config.CatalogCreation.state != nil {
		if len(catalogs) != 0 {
			return PreparedTransition[S, T]{}, managementError(CodeInvalidInput, nil)
		}
		return owner.prepareWithCatalogCreation(ctx, actor, operationID, owned, nil)
	}
	catalog, err := owner.prepareCatalogMappings(catalogs)
	if err != nil {
		return PreparedTransition[S, T]{}, err
	}
	if err := ctx.Err(); err != nil {
		return PreparedTransition[S, T]{}, managementError(CodeInvalidInput, err)
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return PreparedTransition[S, T]{}, err
	}
	observation, err := db.ReadSnapshotResult(ctx, owner.old.state.backend, func(work context.Context, scope db.Queryer) (transitionObservation[S, T], error) {
		result, failure := owner.observe(work, scope, actor, operationID, owned, catalog)
		if expectedTransitionRefusal(failure) {
			return transitionObservation[S, T]{rejection: failure}, nil
		}
		return result, failure
	})
	if err != nil {
		return PreparedTransition[S, T]{}, managementError(CodePersistence, err)
	}
	if observation.rejection != nil {
		return PreparedTransition[S, T]{}, observation.rejection
	}
	oldRole, _ := owner.old.state.credentials.state.binding.RoleFingerprint()
	nextRole, _ := owner.next.state.credentials.state.binding.RoleFingerprint()
	manifest := TransitionManifest{Version: 4, OperationID: operationID, PriorOperationID: observation.ownership.OperationID(),
		PriorSequence: observation.ownership.Sequence(), ProjectSHA256: owner.source, SourceRoleSHA256: oldRole,
		TargetRoleSHA256: nextRole, StateSHA256: observation.digest, Mappings: owned, References: transitionReferenceManifests(observation.references), Catalog: catalog}
	manifest.Version = transitionManifestVersion(catalog, manifest.References, nil, nil)
	evidence, err := manifest.evidence()
	if err != nil {
		return PreparedTransition[S, T]{}, err
	}
	change, err := domain.PrepareOperation(ctx, operationID, nextRole, evidence)
	if err != nil {
		return PreparedTransition[S, T]{}, managementError(CodePersistence, err)
	}
	return PreparedTransition[S, T]{state: &preparedTransition[S, T]{owner: owner, actor: actor.ID(), manifest: manifest, expected: observation.ownership, change: change}}, nil
}

type TransitionResult struct {
	Ownership            domain.Receipt
	MappedAccounts       int
	CreatedAccounts      int
	CreatedGroups        int
	CreatedPermissions   int
	Mappings             []AccountMapping
	RevokedSessions      int
	RemappedRows         int
	Catalog              *CatalogMappings
	CollectionIdentities []domain.CollectionIdentity
}

func (TransitionResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionResult{redacted}"))
}

// Commit consumes the preparation once. Current authority, the full mapping,
// all scalar/collection values and application policy are rechecked under one
// native fence. Source rows become unusable tombstones; original host fields
// and historical memberships remain attached to those rows. Explicit target
// reference fields, their revisions and audit are coordinated here. All old-domain
// sessions (including anonymous reset proofs), retirement audit and the new
// ownership receipt commit atomically. Existing audit is never reassigned.
func (owner *Transition[S, T]) Commit(ctx context.Context, actor auth.Principal, prepared PreparedTransition[S, T]) (TransitionResult, error) {
	state := prepared.state
	if owner == nil || state == nil || state.owner != owner || state.actor != actor.ID() || nilModelValue(ctx) {
		return TransitionResult{}, managementError(CodeInvalidInput, nil)
	}
	if !state.consumed.CompareAndSwap(false, true) {
		return TransitionResult{}, managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return TransitionResult{}, managementError(CodeInvalidInput, err)
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return TransitionResult{}, err
	}
	if state.catalogCreation != nil {
		return owner.commitCatalogCreation(ctx, actor, state)
	}
	if state.creation != nil {
		return owner.commitCreation(ctx, actor, state)
	}
	return managementWrite(ctx, owner.old.state.backend, func(work context.Context, scope db.Session) (TransitionResult, error) {
		current, err := owner.observe(work, scope, actor, state.manifest.OperationID, state.manifest.Mappings, state.manifest.Catalog)
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
		return owner.completeIn(work, scope, current, state.expected, state.change, state.manifest.Mappings, at, nil, nil)
	})
}

func (owner *Transition[S, T]) completeIn(ctx context.Context, scope db.Session, current transitionObservation[S, T], expected domain.Receipt, change domain.Change, mappings []AccountMapping, at time.Time, finalPolicy, finalReadback func(context.Context, db.Queryer) error) (TransitionResult, error) {
	var references []transitionReferenceApplied
	remapped := 0
	var referenceKeys transitionReferenceMappings
	if len(current.references) > 0 {
		sources, targets := make(map[int64]bool, len(mappings)), make(map[int64]int64, len(mappings))
		for _, pair := range mappings {
			sources[pair.SourceID], targets[pair.SourceID] = true, pair.TargetID
		}
		referenceKeys = referenceMappings(sources, targets, current.catalog)
	}
	for i, observed := range current.references {
		applied, err := observed.apply(ctx, scope, current.actor, referenceKeys, at)
		if err != nil {
			return TransitionResult{}, err
		}
		references = append(references, applied)
		current.references[i].bytes = applied.bytes
		remapped += applied.changed
	}
	if len(references) > 0 {
		// Host changes cannot manufacture authority or silently alter an
		// account/catalog graph before the explicit retirement is performed.
		if _, _, err := owner.inspectAuthority(ctx, scope, current.actor); err != nil {
			return TransitionResult{}, err
		}
		if err := owner.verifyRows(ctx, scope, current, mappings); err != nil {
			return TransitionResult{}, err
		}
	}
	verifyReferences := func() error {
		for _, applied := range references {
			if err := applied.verify(ctx, scope); err != nil {
				return err
			}
		}
		return nil
	}
	verifyReferencePolicies := func() error {
		for _, applied := range references {
			if err := applied.verifyPolicy(ctx, scope); err != nil {
				return err
			}
		}
		return nil
	}
	bound, err := owner.old.namespaceIn(ctx, scope)
	if err != nil {
		return TransitionResult{}, err
	}
	scoped, err := bound.InSession(ctx, scope)
	if err != nil {
		return TransitionResult{}, err
	}
	revoked, err := scoped.DeleteAll(ctx)
	if err != nil {
		return TransitionResult{}, err
	}
	binding := owner.old.state.credentials.state.binding
	for index, pair := range current.pairs {
		retired, err := transitionRetirement(binding, pair.old, change.OperationID())
		if err != nil {
			return TransitionResult{}, err
		}
		row, err := retired.Model()
		if err != nil {
			return TransitionResult{}, err
		}
		if err := retired.Save(ctx, scope, &row); err != nil {
			return TransitionResult{}, err
		}
		current.pairs[index].old.row = row
		current.pairs[index].old.values, err = binding.state.objects.ModelValues(row)
		if err != nil {
			return TransitionResult{}, err
		}
		fields := slices.DeleteFunc(retired.ChangedFields(), func(field string) bool { return field == binding.state.role.Revision })
		event, err := accountaudit.Prepare(current.actor, binding.state.role.Model.AppLabel+"."+binding.state.role.Model.ModelName, pair.old.id, admin.ActionChange, fields, at)
		if err != nil {
			return TransitionResult{}, err
		}
		if _, err := accountaudit.AppendIn(ctx, scope, event); err != nil {
			return TransitionResult{}, err
		}
	}
	// Database rules must not resurrect a credential or change a staged
	// target/profile/grant after retirement and its audit have executed.
	if err := verifyReferencePolicies(); err != nil {
		return TransitionResult{}, err
	}
	if err := owner.verifyRows(ctx, scope, current, mappings); err != nil {
		return TransitionResult{}, err
	}
	if err := verifyReferences(); err != nil {
		return TransitionResult{}, err
	}
	receipt, err := domain.TransitionMappedIn(ctx, scope, expected, change, at, domain.MappingInventory{Accounts: mappings, Catalog: current.catalog.mapping, Collections: current.collectionIdentities})
	if err != nil {
		return TransitionResult{}, err
	}
	// Application policies finish before the final readback of every owner.
	// A policy cannot change a previously verified host row or receipt unseen.
	if finalPolicy != nil {
		if err := finalPolicy(ctx, scope); err != nil {
			return TransitionResult{}, err
		}
	}
	if err := verifyReferencePolicies(); err != nil {
		return TransitionResult{}, err
	}
	remaining, err := scoped.DeleteAll(ctx)
	if err != nil || remaining != 0 {
		return TransitionResult{}, managementError(CodePersistence, err)
	}
	if err := owner.verifyRows(ctx, scope, current, mappings); err != nil {
		return TransitionResult{}, err
	}
	if err := verifyReferences(); err != nil {
		return TransitionResult{}, err
	}
	if finalReadback != nil {
		if err := finalReadback(ctx, scope); err != nil {
			return TransitionResult{}, err
		}
	}
	ownership, err := domain.InspectIn(ctx, scope)
	if err != nil || ownership.OperationID() != receipt.OperationID() {
		return TransitionResult{}, managementError(CodePersistence, err)
	}
	// Lookup verifies the entire immutable receipt, including its private row
	// identity, predecessor, role, evidence and timestamp, then the full payload.
	actualInventory, err := domain.LookupMappingInventoryIn(ctx, scope, receipt)
	if err != nil || !slices.Equal(actualInventory.Accounts, mappings) || !equalCatalogMappings(actualInventory.Catalog, current.catalog.mapping) || !slices.Equal(actualInventory.Collections, current.collectionIdentities) {
		return TransitionResult{}, managementError(CodePersistence, err)
	}
	result := TransitionResult{Ownership: receipt, MappedAccounts: len(current.pairs), Mappings: slices.Clone(mappings), RevokedSessions: revoked, RemappedRows: remapped, Catalog: cloneCatalogMappings(current.catalog.mapping), CollectionIdentities: slices.Clone(current.collectionIdentities)}
	for _, row := range result.CollectionIdentities {
		if row.Additional {
			switch row.Kind {
			case "account":
				result.CreatedAccounts++
			case "group":
				result.CreatedGroups++
			case "permission":
				result.CreatedPermissions++
			}
		}
	}
	return result, nil
}

// Reconcile reads an exact immutable receipt using retained evidence. false
// means not observed, never confirmed rollback or permission to retry: an
// uncertain native write fence must be reconciled separately by the host.
// It works after restart and after a later ownership transition, without
// authenticating against retired source rows or replaying their mapping.
func (owner *Transition[S, T]) Reconcile(ctx context.Context, manifest TransitionManifest) (domain.Receipt, bool, error) {
	if owner == nil || owner.old == nil || nilModelValue(ctx) {
		return domain.Receipt{}, false, managementError(CodeInvalidInput, nil)
	}
	old, _ := owner.old.state.credentials.state.binding.RoleFingerprint()
	next, _ := owner.next.state.credentials.state.binding.RoleFingerprint()
	evidence, err := manifest.evidence()
	if err != nil || manifest.ProjectSHA256 != owner.source || manifest.SourceRoleSHA256 != old || manifest.TargetRoleSHA256 != next {
		return domain.Receipt{}, false, managementError(CodeInvalidInput, err)
	}
	type observed struct {
		receipt domain.Receipt
		found   bool
	}
	result, err := db.ReadSnapshotResult(ctx, owner.old.state.backend, func(work context.Context, scope db.Queryer) (observed, error) {
		receipt, found, err := domain.LookupIn(work, scope, manifest.OperationID)
		if err != nil || !found {
			return observed{}, err
		}
		prior, priorFound, err := domain.LookupIn(work, scope, manifest.PriorOperationID)
		if err != nil {
			return observed{}, err
		}
		if !priorFound || !receipt.Follows(prior) || receipt.RoleFingerprint() != next || receipt.EvidenceSHA256() != evidence || receipt.Sequence() != manifest.PriorSequence+1 {
			return observed{}, managementError(CodeConflict, nil)
		}
		inventory, err := domain.LookupMappingInventoryIn(work, scope, receipt)
		if err != nil {
			return observed{}, err
		}
		mappings := inventory.Accounts
		if len(inventory.Collections) != len(manifest.Collections) {
			return observed{}, managementError(CodeConflict, nil)
		}
		for i, row := range inventory.Collections {
			if row.Kind != string(manifest.Collections[i].Kind) {
				return observed{}, managementError(CodeConflict, nil)
			}
		}
		if len(manifest.CreationSources) > 0 {
			if len(mappings) != len(manifest.CreationSources) {
				return observed{}, managementError(CodeConflict, nil)
			}
			for i, source := range manifest.CreationSources {
				if mappings[i].SourceID != source.ID || mappings[i].SourceRevision != source.Revision || mappings[i].TargetRevision != 1 {
					return observed{}, managementError(CodeConflict, nil)
				}
			}
		} else if !slices.Equal(mappings, manifest.Mappings) {
			return observed{}, managementError(CodeConflict, nil)
		}
		catalogFound := inventory.Catalog != nil
		catalog := CatalogMappings{}
		if catalogFound {
			catalog = *inventory.Catalog
		}
		if manifest.CatalogCreation != nil {
			left, right := owner.old.state.credentials.state.binding.state.role.Catalog, owner.next.state.credentials.state.binding.state.role.Catalog
			if !catalogFound || manifest.CatalogCreation.CreateGroups != (left.Group != right.Group) || manifest.CatalogCreation.CreatePermissions != (left.Permission != right.Permission) || !catalogCreationMatches(*manifest.CatalogCreation, catalog) {
				return observed{}, managementError(CodeConflict, nil)
			}
		} else if catalogFound != (manifest.Catalog != nil) || catalogFound && !equalCatalogMappings(&catalog, manifest.Catalog) {
			return observed{}, managementError(CodeConflict, nil)
		}
		return observed{receipt, true}, nil
	})
	if err != nil {
		return domain.Receipt{}, false, managementError(CodePersistence, err)
	}
	return result.receipt, result.found, nil
}

func (owner *Transition[S, T]) requireMigrations(ctx context.Context) error {
	history, ok := owner.old.state.backend.(migrationbackend.AppliedMigrationReader)
	if !ok || nilModelValue(history) {
		return managementError(CodeInvalidConfig, nil)
	}
	return requireAccountMigrations(ctx, history)
}

func normalizeAccountMappings(input []AccountMapping) ([]AccountMapping, error) {
	result := slices.Clone(input)
	slices.SortFunc(result, func(a, b AccountMapping) int { return compareInt64(a.SourceID, b.SourceID) })
	targets := make(map[int64]bool, len(result))
	for i, pair := range result {
		if pair.SourceRevision <= 0 || pair.SourceRevision == math.MaxInt64 || pair.TargetRevision <= 0 || pair.TargetRevision == math.MaxInt64 ||
			i > 0 && result[i-1].SourceID == pair.SourceID || targets[pair.TargetID] {
			return nil, managementError(CodeInvalidInput, nil)
		}
		targets[pair.TargetID] = true
	}
	return result, nil
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (manifest TransitionManifest) evidence() (string, error) {
	if manifest.Version != transitionManifestVersion(manifest.Catalog, manifest.References, manifest.CatalogCreation, manifest.Collections) || manifest.PriorSequence < 1 || manifest.PriorSequence == math.MaxInt64 || (len(manifest.Mappings) == 0) == (len(manifest.CreationSources) == 0) || len(manifest.Mappings) > MaximumTransitionAccounts || len(manifest.CreationSources) > MaximumTransitionAccounts || manifest.CatalogCreation != nil && manifest.Catalog != nil || len(manifest.Collections) > MaximumThroughCreations || len(manifest.Collections) != 0 && len(manifest.CreationSources) == 0 && manifest.CatalogCreation == nil {
		return "", managementError(CodeInvalidInput, nil)
	}
	for _, collection := range manifest.Collections {
		if collection.Kind != TransitionReferenceAccount && collection.Kind != TransitionReferenceGroup && collection.Kind != TransitionReferencePermission || !transitionDigest(collection.DeclarationSHA256) {
			return "", managementError(CodeInvalidInput, nil)
		}
	}
	if err := validateCatalogCreationManifest(manifest.CatalogCreation); err != nil {
		return "", err
	}
	if catalog, err := normalizeCatalogMappings(manifest.Catalog); err != nil || !equalCatalogMappings(catalog, manifest.Catalog) {
		return "", managementError(CodeInvalidInput, err)
	}
	if err := validateTransitionReferenceManifests(manifest.References); err != nil {
		return "", err
	}
	for _, digest := range []string{manifest.OperationID, manifest.PriorOperationID, manifest.ProjectSHA256, manifest.SourceRoleSHA256, manifest.TargetRoleSHA256, manifest.StateSHA256} {
		if !transitionDigest(digest) {
			return "", managementError(CodeInvalidInput, nil)
		}
	}
	if len(manifest.CreationSources) > 0 {
		normalized, err := normalizeSourceAccounts(manifest.CreationSources)
		if err != nil || !slices.Equal(normalized, manifest.CreationSources) {
			return "", managementError(CodeInvalidInput, err)
		}
	} else {
		normalized, err := normalizeAccountMappings(manifest.Mappings)
		if err != nil || !slices.Equal(normalized, manifest.Mappings) {
			return "", managementError(CodeInvalidInput, err)
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", managementError(CodeInvalidInput, err)
	}
	digest := sha256.Sum256(append([]byte("godj.account-transition-manifest.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}

func transitionDigest(text string) bool {
	if len(text) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(text)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == text && !slices.Equal(decoded, make([]byte, sha256.Size))
}
