package systemstate

import (
	"context"
	"errors"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/sessions"
)

// IdentityBackend adds a coherent read capability to the system coordination
// owner. Password verification runs outside both kinds of database scope.
type IdentityBackend interface {
	Backend
	db.SnapshotReader
}

type IdentityRuntimeConfig struct {
	PasswordHasher auth.PasswordHasher `json:"-"`
	SessionLimits  sessions.Limits
	MaxSessions    int
	AuditCapacity  int
}

func (IdentityRuntimeConfig) String() string   { return "systemstate.IdentityRuntimeConfig{redacted}" }
func (IdentityRuntimeConfig) GoString() string { return "systemstate.IdentityRuntimeConfig{redacted}" }

// OpenIdentity reopens an explicitly initialized identity domain. It never
// migrates, adopts, repairs, or verifies a raw password. The same durable
// session/audit ownership is retained while each request resolves current users.
func OpenIdentity(ctx context.Context, backend IdentityBackend, config IdentityRuntimeConfig) (result *Runtime, resultErr error) {
	defer func() {
		resultErr = redactOperatorFailure(resultErr)
		if resultErr != nil {
			result = nil
		}
	}()
	if err := validateSystemStateCall(ctx, backend); err != nil {
		return nil, err
	}
	if isNilInterface(config.PasswordHasher) {
		return nil, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "identity password hasher is nil"}
	}
	runtime := &Runtime{backend: backend}
	store, err := newDurableSessionStore(runtime, config.SessionLimits, config.MaxSessions)
	if err != nil {
		return nil, err
	}
	capacity, err := normalizeAuditCapacity(config.AuditCapacity)
	if err != nil {
		return nil, err
	}
	if err := requireIdentityMigrations(ctx, backend); err != nil {
		return nil, err
	}
	var callbackState operatorErrorSnapshot
	var callbackFailure error
	calls := 0
	err = backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || isNilInterface(reader) {
			callbackFailure = identityCallbackFailure()
			return callbackFailure
		}
		failure := func() error {
			if _, err := inspectIdentityTransition(ctx, reader); err != nil {
				if !errors.Is(err, &Error{Code: CodeIdentityTransitionRequired}) {
					return err
				}
				// Public-only startup is allowed only for a coherently empty
				// domain. Legacy operators and orphaned dependent rows remain
				// explicit refusals; startup never chooses adoption roles.
				if err := requireFreshIdentity(ctx, reader); err != nil {
					return err
				}
				return &Error{Code: CodeCredentialAbsent, Field: "credential", Detail: "identity domain has no initial account"}
			}
			if _, err := inspectSessionTable(ctx, reader, store.limits, store.maxRecords); err != nil {
				return err
			}
			if _, err := inspectAuditTable(ctx, reader, capacity); err != nil {
				return err
			}
			return nil
		}()
		callbackFailure = failure
		callbackState = snapshotOperatorError(failure)
		return failure
	})
	err = errors.Join(err, callbackFailure, ctx.Err())
	if err != nil {
		return nil, redactOperatorAtomicFailure(err, callbackState)
	}
	if calls != 1 {
		return nil, identityCallbackFailure()
	}
	directory, err := identity.NewDirectory(backend)
	if err != nil {
		return nil, err
	}
	authenticator, err := identity.NewAuthenticator(ctx, directory, config.PasswordHasher)
	if err != nil {
		return nil, err
	}
	runtime.authenticator = authenticator
	runtime.sessionStore = store
	runtime.auditCapacity = capacity
	return runtime, nil
}

// InspectIdentityTransition reads the durable ownership outcome after a
// successful or uncertain attempt. It does not replay that attempt or infer a
// result from target user values that may have been changed since adoption.
func InspectIdentityTransition(ctx context.Context, backend IdentityBackend) (result IdentityTransition, resultErr error) {
	defer func() {
		resultErr = redactOperatorFailure(resultErr)
		if resultErr != nil {
			result = IdentityTransition{}
		}
	}()
	if err := validateSystemStateCall(ctx, backend); err != nil {
		return result, err
	}
	if err := requireIdentityMigrations(ctx, backend); err != nil {
		return result, err
	}
	var callbackState operatorErrorSnapshot
	var callbackFailure error
	calls := 0
	err := backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || isNilInterface(reader) {
			callbackFailure = identityCallbackFailure()
			return callbackFailure
		}
		var err error
		result, err = inspectIdentityTransition(ctx, reader)
		callbackFailure = err
		callbackState = snapshotOperatorError(err)
		return err
	})
	err = errors.Join(err, callbackFailure, ctx.Err())
	if err != nil {
		return IdentityTransition{}, redactOperatorAtomicFailure(err, callbackState)
	}
	if calls != 1 || result.UserID() == 0 {
		return IdentityTransition{}, identityCallbackFailure()
	}
	return result, nil
}

func inspectIdentityTransition(ctx context.Context, reader db.Queryer) (IdentityTransition, error) {
	receipts, err := readIdentityTransitions(ctx, reader)
	if err != nil {
		return IdentityTransition{}, err
	}
	if len(receipts) == 0 {
		return IdentityTransition{}, &Error{Code: CodeIdentityTransitionRequired, Field: "identity_transition", Detail: "identity ownership has not been initialized"}
	}
	receipt := receipts[0]
	credentials, err := readCredentialRows(ctx, reader)
	if err != nil {
		return IdentityTransition{}, err
	}
	empty, _ := encodePermissions(nil)
	if len(credentials) != 1 || credentials[0].id <= 0 || receipt.value().kind == "operator" && credentials[0].id != receipt.value().sourceID || credentials[0].principalID != receipt.value().principalID || credentials[0].active || credentials[0].encodedPassword != transferredPassword || credentials[0].permissions != empty || credentials[0].definitionDigest != transferredCredentialDigest {
		return IdentityTransition{}, &Error{Code: CodeCorruptState, Field: "credential", Detail: "retired operator domain marker is inconsistent"}
	}

	return receipt, nil
}
