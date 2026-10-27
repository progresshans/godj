package model

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// FirstAccountConfig identifies an explicit privileged bootstrap declaration.
// EvidenceSHA256 is the nonzero canonical declaration digest (lowercase hex),
// never a password/hash/profile value. The policy selects login and profile;
// selected active/staff/superuser fields are server-owned by this operation.
type FirstAccountConfig[M any] struct {
	CreationPolicy[M]
	EvidenceSHA256 string
}

// FirstAccount is a privileged operator capability for a fresh selected model.
// Do not expose it as a user-facing authorization bypass. Both ownership history
// and selected account rows must be empty in the same preflight/write scope.
// It never adopts an existing row, resets a domain, or reuses historical identity.
type FirstAccount[M any] struct{ state *firstAccountState[M] }
type firstAccountState[M any] struct {
	creation              *creationState[M]
	fingerprint, evidence string
}

func (FirstAccount[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.FirstAccount{redacted}"))
}

// NewFirstAccount is normally called by the generated project factory. The
// command's role meaning is fixed: active, staff AND superuser. A false
// constant role fails at composition, before any database/hash work.
func NewFirstAccount[M, G, P, GL, PL any](store *CredentialStore[M], expected Binding[M], groups orm.ManyToMany[M, G, GL], permissions orm.ManyToMany[M, P, PL], hasher auth.PasswordHasher, config FirstAccountConfig[M]) (*FirstAccount[M], error) {
	if err := store.valid(); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	forced, err := firstAccountForced(store.state.binding, config.EvidenceSHA256)
	if err != nil {
		return nil, err
	}
	state, err := newCreationState(store, expected, groups, permissions, hasher, config.CreationPolicy, forced)
	if err != nil {
		return nil, err
	}
	fingerprint, err := store.state.binding.RoleFingerprint()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	return &FirstAccount[M]{state: &firstAccountState[M]{creation: state, fingerprint: fingerprint, evidence: config.EvidenceSHA256}}, nil
}

func firstAccountForced[M any](binding Binding[M], evidence string) (map[string]query.Value, error) {
	if binding.state == nil || !validFirstAccountDigest(evidence) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	role := binding.state.role
	forced := map[string]query.Value{}
	for _, flag := range []ir.AccountBoolean{role.Active, role.Staff, role.Superuser} {
		if flag.Constant != nil {
			if !*flag.Constant {
				return nil, managementError(CodeInvalidConfig, nil)
			}
		} else {
			forced[flag.Field] = query.Boolean(true)
		}
	}
	return forced, nil
}
func validFirstAccountDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	nonzero := false
	for i := range len(value) {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
		nonzero = nonzero || c != '0'
	}
	return nonzero
}
func (owner *FirstAccount[M]) Fields() []string {
	if owner == nil || owner.state == nil {
		return nil
	}
	return slices.Clone(owner.state.creation.fields)
}
func (owner *FirstAccount[M]) Manager() orm.Manager[M] {
	if owner == nil || owner.state == nil {
		return orm.Manager[M]{}
	}
	return owner.state.creation.store.state.binding.state.objects
}
func (owner *FirstAccount[M]) valid(ctx context.Context) error {
	if owner == nil || owner.state == nil {
		return managementError(CodeInvalidConfig, nil)
	}
	if nilModelValue(ctx) {
		return managementError(CodeInvalidInput, nil)
	}
	if err := ctx.Err(); err != nil {
		return managementError(CodeInvalidInput, err)
	}
	return nil
}

type PreparedFirstAccount[M any] struct{ state *preparedFirstAccount[M] }
type preparedFirstAccount[M any] struct {
	owner     *FirstAccount[M]
	change    domain.Change
	input     *creationCandidate[M]
	attempted atomic.Bool
}

func (PreparedFirstAccount[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.PreparedFirstAccount{redacted}"))
}
func (PreparedFirstAccount[M]) MarshalJSON() ([]byte, error) {
	return []byte(`"identity.model.PreparedFirstAccount{redacted}"`), nil
}

// OperationID remains available after the attempt, including uncertain outcomes.
// Retain it before Commit and use domain.Lookup for receipt reconciliation.
func (prepared PreparedFirstAccount[M]) OperationID() string {
	if prepared.state == nil {
		return ""
	}
	return prepared.state.change.OperationID()
}

// Prepare creates a fresh operation ID, validates a fresh domain and typed
// candidate in one snapshot, closes it, then hashes once outside the DB.
func (owner *FirstAccount[M]) Prepare(ctx context.Context, input orm.ModelInput[M], password string) (PreparedFirstAccount[M], error) {
	return owner.prepare(ctx, "", input, password)
}

// PrepareOperation accepts an operator-created ID so a parent process can
// retain it before sending sensitive input to a child. This is not an
// idempotent retry: an existing or uncertain operation must be reconciled.
func (owner *FirstAccount[M]) PrepareOperation(ctx context.Context, operationID string, input orm.ModelInput[M], password string) (PreparedFirstAccount[M], error) {
	if !validFirstAccountDigest(operationID) {
		return PreparedFirstAccount[M]{}, managementError(CodeInvalidInput, nil)
	}
	return owner.prepare(ctx, operationID, input, password)
}
func (owner *FirstAccount[M]) prepare(ctx context.Context, operationID string, input orm.ModelInput[M], password string) (PreparedFirstAccount[M], error) {
	if err := owner.valid(ctx); err != nil {
		return PreparedFirstAccount[M]{}, err
	}
	state := owner.state.creation
	if password == "" {
		return PreparedFirstAccount[M]{}, profileInputError("password", "required")
	}
	if len(password) > state.config.MaxPasswordBytes {
		return PreparedFirstAccount[M]{}, profileInputError("password", "max_length")
	}
	candidate, err := state.prepareInput(ctx, input)
	if err != nil {
		return PreparedFirstAccount[M]{}, err
	}
	candidate.raw = password
	var change domain.Change
	if operationID == "" {
		change, err = domain.Prepare(ctx, owner.state.fingerprint, owner.state.evidence)
	} else {
		change, err = domain.PrepareOperation(ctx, operationID, owner.state.fingerprint, owner.state.evidence)
	}
	if err != nil {
		return PreparedFirstAccount[M]{}, managementError(CodeInvalidInput, err)
	}
	namespace, err := state.store.state.binding.CredentialDomain(change.Generation())
	if err != nil {
		return PreparedFirstAccount[M]{}, err
	}
	if _, err := state.store.state.binding.prepareCandidateCredential(namespace, candidate.row, nil); err != nil {
		return PreparedFirstAccount[M]{}, managementError(CodeInvalidConfig, err)
	}
	if err := creationSnapshot(ctx, state.store.state.backend, func(work context.Context, scope db.Queryer) error {
		if err := owner.state.requireFresh(work, scope); err != nil {
			return err
		}
		return state.checkCandidate(work, scope, namespace, auth.Principal{}, ProfileRelationFirstAccount, candidate, false)
	}); err != nil {
		return PreparedFirstAccount[M]{}, err
	}
	if err := state.encodeCandidate(ctx, namespace, candidate); err != nil {
		return PreparedFirstAccount[M]{}, err
	}
	return PreparedFirstAccount[M]{state: &preparedFirstAccount[M]{owner: owner, change: change, input: candidate}}, nil
}

func (state *firstAccountState[M]) requireFresh(ctx context.Context, scope db.Queryer) error {
	_, err := domain.RequireIn(ctx, scope, state.fingerprint)
	if err == nil || err == domain.ErrMismatch {
		return managementError(CodeNotFresh, nil)
	}
	// Only exact, successfully observed absence is eligible. A backend error
	// wrapping this sentinel must not turn into permission to initialize.
	if err != domain.ErrUninitialized {
		return err
	}
	exists, err := state.creation.store.state.binding.state.objects.Using(scope).Exists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return managementError(CodeNotFresh, nil)
	}
	return nil
}

// FirstAccountCreation contains only confirmed public mutation metadata and
// its ownership receipt. No provisional result survives a write/cleanup error.
type FirstAccountCreation struct {
	Account   Creation
	Ownership domain.Receipt
}

// Commit claims one attempt shared by every copy. Freshness, all unique and
// policy checks, domain initialization, typed account, memberships and the
// first audit commit together. The created domain-bound active superuser is
// that first audit's actor. Unknown outcomes publish zero and are never retried.
func (owner *FirstAccount[M]) Commit(ctx context.Context, prepared PreparedFirstAccount[M]) (FirstAccountCreation, error) {
	if err := owner.valid(ctx); err != nil {
		return FirstAccountCreation{}, err
	}
	pending := prepared.state
	if pending == nil || pending.owner != owner {
		return FirstAccountCreation{}, managementError(CodeInvalidInput, nil)
	}
	if !pending.attempted.CompareAndSwap(false, true) {
		return FirstAccountCreation{}, managementError(CodeConflict, nil)
	}
	candidate := pending.input
	pending.input = nil
	state := owner.state.creation
	return managementRelationWrite(ctx, state.backend, func(work context.Context, scope db.RelationSession) (FirstAccountCreation, error) {
		if err := owner.state.requireFresh(work, scope); err != nil {
			return FirstAccountCreation{}, err
		}
		binding := state.store.state.binding
		namespace, err := binding.CredentialDomain(pending.change.Generation())
		if err != nil {
			return FirstAccountCreation{}, err
		}
		if err := state.checkCandidate(work, scope, namespace, auth.Principal{}, ProfileRelationFirstAccount, candidate, true); err != nil {
			return FirstAccountCreation{}, err
		}
		at := state.config.Now()
		receipt, err := domain.InitializeIn(work, scope, pending.change, at)
		if err != nil {
			return FirstAccountCreation{}, err
		}
		row, result, err := state.insertIn(work, scope, namespace, candidate)
		if err != nil {
			return FirstAccountCreation{}, err
		}
		credential, err := binding.prepareCredentialForDomain(namespace, row, nil)
		if err != nil {
			return FirstAccountCreation{}, err
		}
		actor := credential.Principal()
		if !actor.Active() || !actor.Staff() || !actor.Superuser() || !credential.HasUsablePassword() {
			return FirstAccountCreation{}, invalidAccountCredential()
		}
		if err := state.auditAtIn(work, scope, actor, result, at); err != nil {
			return FirstAccountCreation{}, err
		}
		return FirstAccountCreation{Account: result, Ownership: receipt}, nil
	})
}
