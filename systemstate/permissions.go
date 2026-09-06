package systemstate

import (
	"context"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// WithPermissions returns the desired policy without changing identity,
// activation, password profile, or storage. Persist it explicitly with
// UpdateOperatorPermissions using the old policy as the expected value.
func (policy CredentialPolicy) WithPermissions(permissions ...auth.Permission) (CredentialPolicy, error) {
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: policy.Principal.ID(), Active: policy.Principal.Active(), Permissions: permissions})
	if err != nil {
		return CredentialPolicy{}, err
	}
	return CredentialPolicy{Principal: principal, PasswordHasher: policy.PasswordHasher}, nil
}

// UpdateOperatorPermissions is an explicit trusted maintenance operation. It
// compares the stored credential against expected, changes only permissions,
// and revokes all durable sessions in one coordinated transaction. A stale
// expectation fails without mutation. Username, hash, identity and active state
// are preserved; no raw password, automatic upgrade, or retry is involved.
//
// Reopen runtimes with the new policy after success. Existing runtimes reject
// subsequent Authenticate/Resolve calls; work authorized before this transaction
// commits is already admitted and is not retroactively cancelled. Unknown commit
// outcome must be reconciled by opening the expected resulting policy.
func UpdateOperatorPermissions(ctx context.Context, backend Backend, expected CredentialPolicy, permissions []auth.Permission) (resultErr error) {
	defer func() { resultErr = redactOperatorFailure(resultErr) }()
	if err := validateSystemStateCall(ctx, backend); err != nil {
		return err
	}
	policy, err := validateCredentialPolicy(expected)
	if err != nil {
		return err
	}
	payload, err := encodePermissions(append([]auth.Permission(nil), permissions...))
	if err != nil {
		return err
	}
	if err := requireInitialMigration(ctx, backend); err != nil {
		return err
	}
	var callbackState operatorErrorSnapshot
	err = backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		failure := func() error {
			rows, err := readCredentialRows(ctx, session)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				return &Error{Code: CodeCredentialAbsent, Field: "credential", Detail: "operator credential is absent"}
			}
			if len(rows) != 1 {
				return credentialCardinalityError()
			}
			row := rows[0]
			if _, err := validateStoredCredential(row, policy); err != nil {
				return err
			}
			if payload == row.permissions {
				return nil
			}
			affected, err := session.Update(ctx, query.NewUpdatePlan(credentialTableName,
				[]query.Assignment{query.NewAssignment(credentialPermissionsRef, query.String(payload))},
				credentialIDRef, query.Integer(row.id)))
			if err != nil {
				return persistenceFailure("update operator permissions", err)
			}
			if affected != 1 {
				return cardinalityFailure("credential", "permission compare-and-swap did not update one row")
			}
			// Read and delete in bounded batches; the cooperative fence keeps login
			// and session operations out until permission update and revocation commit.
			deleted := 0
			for {
				sessions, err := listSessionRows(ctx, session, 256)
				if err != nil {
					return err
				}
				if len(sessions) == 0 {
					return nil
				}
				deleted += len(sessions)
				if deleted > hardMaxDurableSessions {
					return cardinalityFailure("session", "session inventory exceeds its hard bound")
				}
				for _, row := range sessions {
					affected, err := session.Delete(ctx, query.NewDeletePlan(sessionTableName, systemRowIDField, query.Integer(row.id)))
					if err != nil {
						return persistenceFailure("revoke operator session", err)
					}
					if affected != 1 {
						return cardinalityFailure("session", "session revocation did not delete one row")
					}
				}
			}
		}()
		callbackState = snapshotOperatorError(failure)
		return failure
	})
	if err != nil {
		return redactOperatorAtomicFailure(err, callbackState)
	}
	return nil
}

// The cached verifier keeps password work outside the transaction. Each
// successful authentication/resolution then checks policy under the same fence
// as permission maintenance, so an old process cannot publish stale grants.
type policyAuthenticator struct {
	runtime *Runtime
	policy  credentialPolicyMaterial
	cached  *auth.MemoryAuthenticator
}

func (*policyAuthenticator) String() string   { return "systemstate.Authenticator{redacted}" }
func (*policyAuthenticator) GoString() string { return "systemstate.Authenticator{redacted}" }

func (a *policyAuthenticator) Authenticate(ctx context.Context, username, password string) (auth.Principal, error) {
	principal, err := a.cached.Authenticate(ctx, username, password)
	if err != nil {
		return auth.Principal{}, err
	}
	return a.check(ctx, principal)
}

func (a *policyAuthenticator) Resolve(ctx context.Context, id string) (auth.Principal, error) {
	principal, err := a.cached.Resolve(ctx, id)
	if err != nil {
		return auth.Principal{}, err
	}
	return a.check(ctx, principal)
}

func (a *policyAuthenticator) check(ctx context.Context, principal auth.Principal) (auth.Principal, error) {
	var callbackState operatorErrorSnapshot
	err := a.runtime.withAtomic(ctx, func(session db.Session) error {
		failure := func() error {
			rows, err := readCredentialRows(ctx, session)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return credentialCardinalityError()
			}
			_, err = validateStoredCredential(rows[0], a.policy)
			return err
		}()
		callbackState = snapshotOperatorError(failure)
		return failure
	})
	if err != nil {
		return auth.Principal{}, redactOperatorAtomicFailure(err, callbackState)
	}
	return principal, nil
}
