package systemstate

import (
	"context"
	"errors"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
)

// ProvisionIdentityConfig is the explicit first-account input for a fresh
// identity domain. The caller chooses active/staff/superuser and initial grants
// on Principal; no existing operator is adopted by this operation.
type ProvisionIdentityConfig struct {
	Principal      auth.Principal
	Username       string
	Password       string              `json:"-"`
	PasswordHasher auth.PasswordHasher `json:"-"`
	DateJoined     time.Time
}

func (ProvisionIdentityConfig) String() string {
	return "systemstate.ProvisionIdentityConfig{redacted}"
}
func (ProvisionIdentityConfig) GoString() string {
	return "systemstate.ProvisionIdentityConfig{redacted}"
}

// ProvisionIdentity creates the first user and ownership receipt together.
// It requires empty legacy credential, identity user, session and audit stores;
// a preexisting permission catalog is allowed and never overwritten. Hashing
// happens outside the authoritative coordinated transaction, at most once.
func ProvisionIdentity(ctx context.Context, backend IdentityBackend, config ProvisionIdentityConfig) (result IdentityTransition, resultErr error) {
	defer func() {
		resultErr = redactOperatorFailure(resultErr)
		if resultErr != nil {
			result = IdentityTransition{}
		}
	}()
	if err := validateSystemStateCall(ctx, backend); err != nil {
		return result, err
	}
	if isNilInterface(config.PasswordHasher) {
		return result, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "identity password hasher is nil"}
	}
	if config.Password == "" {
		return result, &Error{Code: CodeInvalidConfig, Field: "password", Detail: "initial identity password is empty"}
	}
	if _, err := auth.NewCredential(config.Username, "identity-provision-validation", config.Principal); err != nil {
		return result, &Error{Code: CodeInvalidConfig, Field: "principal", Detail: "initial identity input is invalid", Cause: err}
	}
	at, err := transitionInstant(config.DateJoined)
	if err != nil {
		return result, err
	}
	if err := requireIdentityMigrations(ctx, backend); err != nil {
		return result, err
	}
	// This observation only avoids needless password work. The coordinated
	// callback rechecks every precondition and owns the mutation decision.
	var preflightFailure error
	preflightCalls := 0
	preflightErr := backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		preflightCalls++
		if preflightCalls != 1 || isNilInterface(reader) {
			preflightFailure = identityCallbackFailure()
			return preflightFailure
		}
		preflightFailure = requireFreshIdentity(ctx, reader)
		return preflightFailure
	})
	if err := errors.Join(preflightErr, preflightFailure, ctx.Err()); err != nil {
		return result, redactOperatorAtomicFailure(err, snapshotOperatorError(preflightFailure))
	}
	if preflightCalls != 1 {
		return result, identityCallbackFailure()
	}
	encoded, err := config.PasswordHasher.Hash(ctx, config.Password)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return result, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "identity password hashing failed", Cause: err}
	}
	if err := config.PasswordHasher.ValidateEncoded(encoded); err != nil {
		return result, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "identity hash does not match its bounded work profile", Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if _, err := auth.NewCredential(config.Username, encoded, config.Principal); err != nil {
		return result, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "identity hash exceeds the credential envelope", Cause: err}
	}
	payload, err := encodePermissions(config.Principal.Permissions())
	if err != nil {
		return result, err
	}
	var callbackState operatorErrorSnapshot
	var callbackFailure error
	calls := 0
	err = backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		calls++
		if calls != 1 || isNilInterface(session) {
			callbackFailure = identityCallbackFailure()
			return callbackFailure
		}
		failure := func() error {
			if err := requireFreshIdentity(ctx, session); err != nil {
				return err
			}
			user, err := createIdentityUser(ctx, session, config.Principal.ID(), config.Username, encoded, config.Principal.Active(), config.Principal.Staff(), config.Principal.Superuser(), config.Principal.Permissions(), at)
			if err != nil {
				return err
			}
			candidate := IdentityTransition{state: &identityTransitionState{kind: "bootstrap", principalID: user.PrincipalID, userID: user.ID, staff: user.Staff, superuser: user.Superuser, at: at, fingerprint: credentialFingerprint(credentialRow{principalID: user.PrincipalID, username: user.Username, encodedPassword: encoded, active: user.Active, permissions: payload, definitionDigest: transferredCredentialDigest})}}
			if err := writeIdentityTransition(ctx, session, candidate); err != nil {
				return err
			}
			empty, err := encodePermissions(nil)
			if err != nil {
				return err
			}
			if _, err := insertCredential(ctx, session, credentialRow{principalID: user.PrincipalID, username: user.Username, encodedPassword: transferredPassword, active: false, permissions: empty, definitionDigest: transferredCredentialDigest}); err != nil {
				return err
			}
			result = candidate
			return ctx.Err()
		}()
		callbackFailure = failure
		callbackState = snapshotOperatorError(failure)
		return failure
	})
	err = errors.Join(err, callbackFailure)
	if err != nil {
		return IdentityTransition{}, redactOperatorAtomicFailure(err, callbackState)
	}
	if calls != 1 || result.UserID() == 0 {
		return IdentityTransition{}, identityCallbackFailure()
	}
	return result, nil
}

func requireFreshIdentity(ctx context.Context, reader db.Queryer) error {
	receipts, err := readIdentityTransitions(ctx, reader)
	if err != nil {
		return err
	}
	if len(receipts) > 0 {
		return identityInitializedError()
	}
	credentials, err := readCredentialRows(ctx, reader)
	if err != nil {
		return err
	}
	if len(credentials) > 1 {
		return credentialCardinalityError()
	}
	if len(credentials) > 0 {
		return &Error{Code: CodeIdentityTransitionRequired, Field: "credential", Detail: "existing operator requires explicit adoption"}
	}
	present, err := models.UserObjects.Using(reader).Exists(ctx)
	if err != nil {
		return persistenceFailure("inspect initial identity users", err)
	}
	if present {
		return &Error{Code: CodeCorruptState, Field: "identity_transition", Detail: "identity users exist without an ownership receipt"}
	}
	sessions, err := inspectProvisionSessionTable(ctx, reader)
	if err != nil {
		return err
	}
	audit, err := inspectProvisionAuditTable(ctx, reader)
	if err != nil {
		return err
	}
	if sessions || audit {
		return &Error{Code: CodeCorruptState, Field: "identity_transition", Detail: "dependent system rows exist without initialized identity ownership"}
	}
	return nil
}
