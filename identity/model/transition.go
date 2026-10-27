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
	"github.com/progresshans/godj/validation"
)

const MaximumTransitionAccounts = 65536

// AccountMapping explicitly pairs existing, staged target rows with every
// source account. Keys include zero and negative values. Revisions are checked
// alongside the complete observed rows and memberships, never instead of them.
type AccountMapping struct {
	SourceID       int64 `json:"source_id"`
	SourceRevision int64 `json:"source_revision"`
	TargetID       int64 `json:"target_id"`
	TargetRevision int64 `json:"target_revision"`
}

// TransitionConfig owns an explicit operator operation over two typed models.
// Targets must already have been staged by the host's data migration. The
// operation preserves principal/password/roles and the shared catalog's exact
// group/direct/effective grants. It does not infer a mapping, copy profile data,
// rewrite host references or adopt historical systemstate storage.
//
// Validate is optional application policy over detached, password-redacted
// source/target rows and complete profile memberships. It runs in preparation
// and again inside the final fence. It must not mutate/retain the borrowed
// reader, re-enter the backend or publish external effects. All callbacks must
// be safe for concurrent calls. MaxAccounts defaults to 4096 and fails closed.
type TransitionConfig[S, T any] struct {
	Permission            auth.Permission
	AdditionalPermissions []auth.Permission
	Authorizer            auth.Authorizer
	MaxAccounts           int
	Validate              func(context.Context, db.Queryer, S, T, AccountMemberships, AccountMemberships) (validation.Errors, error)
	Now                   func() time.Time
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
}

func (*Transition[S, T]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Transition{redacted}"))
}

// NewTransition binds both stores to the same actual backend, complete host
// source and catalog. Source and target account tables must differ. No I/O,
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
	if err != nil || left != right || a.role.Model == b.role.Model || !reflect.DeepEqual(a.role.Catalog, b.role.Catalog) {
		return nil, managementError(CodeInvalidConfig, err)
	}
	oldModel, _ := a.project.Model(a.role.Model)
	newModel, _ := b.project.Model(b.role.Model)
	if oldModel.DBTable == newModel.DBTable {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	if a.role.Model == a.role.Catalog.Group || a.role.Model == a.role.Catalog.Permission || b.role.Model == b.role.Catalog.Group || b.role.Model == b.role.Catalog.Permission {
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
	config.AdditionalPermissions = slices.Clone(config.AdditionalPermissions)
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
	return &Transition[S, T]{source: left, old: source, next: target, config: config, required: required,
		oldMemberships: oldMemberships, nextMemberships: nextMemberships}, nil
}

// TransitionManifest is deliberately exportable reconciliation evidence. It
// contains keys, revisions and salted state digests, never account values,
// credentials or session data. Retain it BEFORE Commit, including on unknown
// outcomes. The digest is evidence identity, not authorization or a bearer key.
type TransitionManifest struct {
	Version          int              `json:"version"`
	OperationID      string           `json:"operation_id"`
	PriorOperationID string           `json:"prior_operation_id"`
	PriorSequence    int64            `json:"prior_sequence"`
	ProjectSHA256    string           `json:"project_sha256"`
	SourceRoleSHA256 string           `json:"source_role_sha256"`
	TargetRoleSHA256 string           `json:"target_role_sha256"`
	StateSHA256      string           `json:"state_sha256"`
	Mappings         []AccountMapping `json:"mappings"`
}

func (TransitionManifest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionManifest{redacted}"))
}

type PreparedTransition[S, T any] struct{ state *preparedTransition[S, T] }
type preparedTransition[S, T any] struct {
	owner    *Transition[S, T]
	actor    string
	manifest TransitionManifest
	expected domain.Receipt
	change   domain.Change
	consumed atomic.Bool
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
	return result
}

// Prepare is read-only. operationID is one fresh canonical 256-bit hex ID
// chosen by the operator before the attempt. The complete source/target
// inventories must be covered one-to-one; coincident IDs never imply adoption.
func (owner *Transition[S, T]) Prepare(ctx context.Context, actor auth.Principal, operationID string, mappings []AccountMapping) (PreparedTransition[S, T], error) {
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
	if err := ctx.Err(); err != nil {
		return PreparedTransition[S, T]{}, managementError(CodeInvalidInput, err)
	}
	if err := owner.requireMigrations(ctx); err != nil {
		return PreparedTransition[S, T]{}, err
	}
	observation, err := db.ReadSnapshotResult(ctx, owner.old.state.backend, func(work context.Context, scope db.Queryer) (transitionObservation[S, T], error) {
		result, failure := owner.observe(work, scope, actor, operationID, owned)
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
	manifest := TransitionManifest{Version: 1, OperationID: operationID, PriorOperationID: observation.ownership.OperationID(),
		PriorSequence: observation.ownership.Sequence(), ProjectSHA256: owner.source, SourceRoleSHA256: oldRole,
		TargetRoleSHA256: nextRole, StateSHA256: observation.digest, Mappings: owned}
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
	Ownership       domain.Receipt
	MappedAccounts  int
	RevokedSessions int
}

func (TransitionResult) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.TransitionResult{redacted}"))
}

// Commit consumes the preparation once. Current authority, the full mapping,
// all scalar/collection values and application policy are rechecked under one
// native fence. Source rows become unusable tombstones; host references and
// historical memberships remain attached to those rows. All old-domain
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
	return managementWrite(ctx, owner.old.state.backend, func(work context.Context, scope db.Session) (TransitionResult, error) {
		current, err := owner.observe(work, scope, actor, state.manifest.OperationID, state.manifest.Mappings)
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
		bound, err := owner.old.namespaceIn(work, scope)
		if err != nil {
			return TransitionResult{}, err
		}
		scoped, err := bound.InSession(work, scope)
		if err != nil {
			return TransitionResult{}, err
		}
		revoked, err := scoped.DeleteAll(work)
		if err != nil {
			return TransitionResult{}, err
		}
		binding := owner.old.state.credentials.state.binding
		for index, pair := range current.pairs {
			retired, err := transitionRetirement(binding, pair.old, state.manifest.OperationID)
			if err != nil {
				return TransitionResult{}, err
			}
			row, err := retired.Model()
			if err != nil {
				return TransitionResult{}, err
			}
			if err := retired.Save(work, scope, &row); err != nil {
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
			if _, err := accountaudit.AppendIn(work, scope, event); err != nil {
				return TransitionResult{}, err
			}
		}
		// Database rules must not resurrect a credential or change a staged
		// target/profile/grant after retirement and its audit have executed.
		if err := owner.verifyRetirement(work, scope, current, state.manifest.Mappings); err != nil {
			return TransitionResult{}, err
		}
		receipt, err := domain.TransitionIn(work, scope, state.expected, state.change, at)
		if err != nil {
			return TransitionResult{}, err
		}
		// Receipt insertion is also inside the independent final readback.
		if err := owner.verifyRetirement(work, scope, current, state.manifest.Mappings); err != nil {
			return TransitionResult{}, err
		}
		ownership, err := domain.InspectIn(work, scope)
		if err != nil || ownership.OperationID() != receipt.OperationID() || ownership.Generation() != receipt.Generation() {
			return TransitionResult{}, managementError(CodePersistence, err)
		}
		remaining, err := scoped.DeleteAll(work)
		if err != nil || remaining != 0 {
			return TransitionResult{}, managementError(CodePersistence, err)
		}
		return TransitionResult{Ownership: receipt, MappedAccounts: len(current.pairs), RevokedSessions: revoked}, nil
	})
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
	if manifest.Version != 1 || manifest.PriorSequence < 1 || manifest.PriorSequence == math.MaxInt64 || len(manifest.Mappings) == 0 || len(manifest.Mappings) > MaximumTransitionAccounts {
		return "", managementError(CodeInvalidInput, nil)
	}
	for _, digest := range []string{manifest.OperationID, manifest.PriorOperationID, manifest.ProjectSHA256, manifest.SourceRoleSHA256, manifest.TargetRoleSHA256, manifest.StateSHA256} {
		if !transitionDigest(digest) {
			return "", managementError(CodeInvalidInput, nil)
		}
	}
	normalized, err := normalizeAccountMappings(manifest.Mappings)
	if err != nil || !slices.Equal(normalized, manifest.Mappings) {
		return "", managementError(CodeInvalidInput, err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", managementError(CodeInvalidInput, err)
	}
	digest := sha256.Sum256(append([]byte("godj.account-transition-manifest.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}

func transitionDigest(text string) bool {
	decoded, err := hex.DecodeString(text)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == text && !slices.Equal(decoded, make([]byte, sha256.Size))
}
