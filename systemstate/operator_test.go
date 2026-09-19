package systemstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
)

func TestProvisionOperatorAndOpenExistingSeparateMutationFromRestart(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "operator.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	backend := &observedRuntimeBackend{Backend: database}
	hasher := newOperatorHasherSpy(t, 10_000)
	policy := operatorTestPolicy(t, hasher, "article-development-admin", true)
	runtimeConfig := operatorRuntimeConfig(policy)

	runtime, err := OpenExisting(ctx, backend, runtimeConfig)
	if runtime != nil || !errors.Is(err, &Error{Code: CodeCredentialAbsent, Field: "credential"}) {
		t.Fatalf("OpenExisting(clean migrated state) = (%v, %#v)", runtime, err)
	}
	if backend.atomicCalls.Load() != 1 || backend.insertCalls.Load() != 0 || backend.updateCalls.Load() != 0 || backend.deleteCalls.Load() != 0 {
		t.Fatalf(
			"clean OpenExisting calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			backend.atomicCalls.Load(), backend.insertCalls.Load(), backend.updateCalls.Load(), backend.deleteCalls.Load(),
		)
	}
	if hasher.verifyCalls.Load() != 0 {
		t.Fatalf("clean OpenExisting Verify calls = %d, want 0", hasher.verifyCalls.Load())
	}

	backend.resetObservation()
	hasher.resetObservation()
	provision := ProvisionOperatorConfig{
		Username:         "admin",
		Password:         "operator-password-secret-marker",
		CredentialPolicy: policy,
	}
	if err := ProvisionOperator(ctx, backend, provision); err != nil {
		t.Fatalf("ProvisionOperator(empty): %v", err)
	}
	if backend.atomicCalls.Load() != 1 || backend.insertCalls.Load() != 1 || backend.updateCalls.Load() != 0 || backend.deleteCalls.Load() != 0 {
		t.Fatalf(
			"first provision calls = atomic %d/insert %d/update %d/delete %d, want 1/1/0/0",
			backend.atomicCalls.Load(), backend.insertCalls.Load(), backend.updateCalls.Load(), backend.deleteCalls.Load(),
		)
	}
	if hasher.hashCalls.Load() != 1 || hasher.verifyCalls.Load() != 0 {
		t.Fatalf("first provision hasher calls = hash %d/verify %d, want 1/0", hasher.hashCalls.Load(), hasher.verifyCalls.Load())
	}

	backend.resetObservation()
	hasher.resetObservation()
	already := provision
	already.Username = "different-admin"
	already.Password = "different-password-secret-marker"
	if err := ProvisionOperator(ctx, backend, already); !errors.Is(err, &Error{Code: CodeCredentialAlreadyExists, Field: "credential"}) {
		t.Fatalf("ProvisionOperator(existing with different supplied material) = %#v", err)
	}
	if backend.atomicCalls.Load() != 1 || backend.insertCalls.Load() != 0 || backend.updateCalls.Load() != 0 || backend.deleteCalls.Load() != 0 {
		t.Fatalf(
			"existing provision calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			backend.atomicCalls.Load(), backend.insertCalls.Load(), backend.updateCalls.Load(), backend.deleteCalls.Load(),
		)
	}
	if hasher.hashCalls.Load() != 0 || hasher.verifyCalls.Load() != 0 {
		t.Fatalf("existing provision hasher calls = hash %d/verify %d, want 0/0", hasher.hashCalls.Load(), hasher.verifyCalls.Load())
	}

	backend.resetObservation()
	hasher.resetObservation()
	runtime, err = OpenExisting(ctx, backend, runtimeConfig)
	if err != nil || runtime == nil || runtime.Authenticator() == nil || runtime.SessionStore() == nil {
		t.Fatalf("OpenExisting(provisioned) = (%v, %v)", runtime, err)
	}
	if backend.atomicCalls.Load() != 1 || backend.insertCalls.Load() != 0 || backend.updateCalls.Load() != 0 || backend.deleteCalls.Load() != 0 {
		t.Fatalf(
			"provisioned OpenExisting calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			backend.atomicCalls.Load(), backend.insertCalls.Load(), backend.updateCalls.Load(), backend.deleteCalls.Load(),
		)
	}
	if hasher.verifyCalls.Load() != 0 {
		t.Fatalf("OpenExisting Verify calls = %d, want 0", hasher.verifyCalls.Load())
	}
	principal, err := runtime.Authenticator().Authenticate(ctx, provision.Username, provision.Password)
	if err != nil || principal.ID() != policy.Principal.ID() || !principal.Authenticated() {
		t.Fatalf("Authenticate(opened credential) = (%v, %v)", principal, err)
	}
	if hasher.verifyCalls.Load() != 1 {
		t.Fatalf("Authenticate Verify calls = %d, want 1", hasher.verifyCalls.Load())
	}
}

func TestProvisionOperatorAndOpenExistingRejectPolicyMismatchWithoutVerifyOrMutation(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "policy.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	backend := &observedRuntimeBackend{Backend: database}
	hasher := newOperatorHasherSpy(t, 10_000)
	policy := operatorTestPolicy(t, hasher, "article-development-admin", true)
	if err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{
		Username:         "admin",
		Password:         "policy-password-secret-marker",
		CredentialPolicy: policy,
	}); err != nil {
		t.Fatalf("ProvisionOperator(setup): %v", err)
	}

	makePolicy := func(id string, active bool, permissions []auth.Permission) CredentialPolicy {
		principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: id, Active: active, Permissions: permissions})
		if err != nil {
			t.Fatalf("auth.NewPrincipal(mismatch): %v", err)
		}
		return CredentialPolicy{Principal: principal, PasswordHasher: hasher}
	}
	permissions := policy.Principal.Permissions()
	reordered := append([]auth.Permission(nil), permissions...)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	mismatches := []struct {
		name   string
		policy CredentialPolicy
	}{
		{name: "principal identifier", policy: makePolicy("different-principal", true, permissions)},
		{name: "active flag", policy: makePolicy(policy.Principal.ID(), false, permissions)},
		{name: "ordered permissions", policy: makePolicy(policy.Principal.ID(), true, reordered)},
	}
	for _, test := range mismatches {
		t.Run(test.name, func(t *testing.T) {
			backend.resetObservation()
			hasher.resetObservation()
			if runtime, err := OpenExisting(ctx, backend, operatorRuntimeConfig(test.policy)); runtime != nil ||
				!errors.Is(err, &Error{Code: CodeCredentialPolicyMismatch, Field: "credential_policy"}) {
				t.Fatalf("OpenExisting(policy mismatch) = (%v, %#v)", runtime, err)
			}
			if backend.atomicCalls.Load() != 1 || backend.insertCalls.Load() != 0 ||
				backend.updateCalls.Load() != 0 || backend.deleteCalls.Load() != 0 {
				t.Fatalf(
					"policy-mismatched open calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
					backend.atomicCalls.Load(), backend.insertCalls.Load(), backend.updateCalls.Load(), backend.deleteCalls.Load(),
				)
			}
			if hasher.hashCalls.Load() != 0 || hasher.verifyCalls.Load() != 0 {
				t.Fatalf("policy-mismatched open hasher calls = hash %d/verify %d, want 0/0", hasher.hashCalls.Load(), hasher.verifyCalls.Load())
			}

			backend.resetObservation()
			hasher.resetObservation()
			if err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{
				Username:         "irrelevant-admin",
				Password:         "irrelevant-password-secret-marker",
				CredentialPolicy: test.policy,
			}); !errors.Is(err, &Error{Code: CodeCredentialPolicyMismatch, Field: "credential_policy"}) {
				t.Fatalf("ProvisionOperator(policy mismatch) = %#v", err)
			}
			if backend.atomicCalls.Load() != 1 || backend.insertCalls.Load() != 0 ||
				backend.updateCalls.Load() != 0 || backend.deleteCalls.Load() != 0 {
				t.Fatalf(
					"policy-mismatched provision calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
					backend.atomicCalls.Load(), backend.insertCalls.Load(), backend.updateCalls.Load(), backend.deleteCalls.Load(),
				)
			}
			if hasher.hashCalls.Load() != 0 || hasher.verifyCalls.Load() != 0 {
				t.Fatalf("policy-mismatched provision hasher calls = hash %d/verify %d, want 0/0", hasher.hashCalls.Load(), hasher.verifyCalls.Load())
			}
		})
	}
}

func TestProvisionOperatorAndOpenExistingValidateNilAndCanceledInputsBeforeBackendUse(t *testing.T) {
	ctx := context.Background()
	backend := &serialRuntimeBackend{}
	policy := operatorTestPolicy(t, newOperatorHasherSpy(t, 10_000), "operator", true)
	provision := ProvisionOperatorConfig{
		Username:         "admin",
		Password:         "validation-password-secret-marker",
		CredentialPolicy: policy,
	}
	runtimeConfig := operatorRuntimeConfig(policy)

	if err := ProvisionOperator(nil, backend, provision); !errors.Is(err, &Error{Code: CodeInvalidInput, Field: "context"}) {
		t.Fatalf("ProvisionOperator(nil context) = %#v", err)
	}
	if runtime, err := OpenExisting(nil, backend, runtimeConfig); runtime != nil ||
		!errors.Is(err, &Error{Code: CodeInvalidInput, Field: "context"}) {
		t.Fatalf("OpenExisting(nil context) = (%v, %#v)", runtime, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := ProvisionOperator(canceled, backend, provision); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProvisionOperator(canceled context) = %#v", err)
	}
	if runtime, err := OpenExisting(canceled, backend, runtimeConfig); runtime != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenExisting(canceled context) = (%v, %#v)", runtime, err)
	}
	if err := ProvisionOperator(ctx, nil, provision); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "backend"}) {
		t.Fatalf("ProvisionOperator(nil backend) = %#v", err)
	}
	if runtime, err := OpenExisting(ctx, nil, runtimeConfig); runtime != nil ||
		!errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "backend"}) {
		t.Fatalf("OpenExisting(nil backend) = (%v, %#v)", runtime, err)
	}
	if err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{}); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "password_hasher"}) {
		t.Fatalf("ProvisionOperator(zero config) = %#v", err)
	}
	if runtime, err := OpenExisting(ctx, backend, RuntimeConfig{}); runtime != nil ||
		!errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "password_hasher"}) {
		t.Fatalf("OpenExisting(zero config) = (%v, %#v)", runtime, err)
	}
	withoutPassword := provision
	withoutPassword.Password = " \t"
	if err := ProvisionOperator(ctx, backend, withoutPassword); !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "password"}) {
		t.Fatalf("ProvisionOperator(empty password) = %#v", err)
	}
	if backend.atomicCalls.Load() != 0 {
		t.Fatalf("invalid calls reached CoordinatedAtomic %d times, want 0", backend.atomicCalls.Load())
	}
}

func TestOperatorPublicErrorsStripRawAndTypedNilExternalCauses(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "operator-error-redaction.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)

	const marker = "postgres://operator:password-secret-marker@example.invalid/private"
	rawCause := errors.New(marker)
	var typedNilCause *query.Error
	for _, test := range []struct {
		name  string
		cause error
	}{
		{name: "raw", cause: rawCause},
		{name: "typed nil", cause: typedNilCause},
		{name: "panicking", cause: operatorPanickingError{}},
	} {
		t.Run("hasher "+test.name, func(t *testing.T) {
			base := newOperatorHasherSpy(t, 10_000)
			hasher := &operatorHashFailureHasher{PasswordHasher: base, hashErr: test.cause}
			policy := operatorTestPolicy(t, hasher, "operator", true)
			err := ProvisionOperator(ctx, database, ProvisionOperatorConfig{
				Username:         "admin",
				Password:         "operator-password-secret-marker",
				CredentialPolicy: policy,
			})
			assertOperatorErrorIsSanitized(t, err, &Error{Code: CodeInvalidConfig, Field: "password_hasher"}, rawCause, marker)
		})

		t.Run("migration reader "+test.name, func(t *testing.T) {
			policy := operatorTestPolicy(t, newOperatorHasherSpy(t, 10_000), "operator", true)
			backend := &operatorMigrationReadFailureBackend{Backend: database, readErr: test.cause}
			err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{
				Username:         "admin",
				Password:         "operator-password-secret-marker",
				CredentialPolicy: policy,
			})
			assertOperatorErrorIsSanitized(t, err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}, rawCause, marker)

			opened, openErr := OpenExisting(ctx, backend, operatorRuntimeConfig(policy))
			if opened != nil {
				t.Fatalf("OpenExisting(migration reader %s) returned runtime", test.name)
			}
			assertOperatorErrorIsSanitized(t, openErr, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}, rawCause, marker)
		})
	}
}

func TestOperatorAtomicBackendCannotForgePublicStateIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "operator-forged-state.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	const marker = "postgres://operator:password-secret-marker@example.invalid/private"
	backend := &operatorAtomicFailureBackend{
		Backend: database,
		atomicErr: &Error{
			Code:   CodeCredentialAlreadyExists,
			Field:  marker,
			Detail: marker,
		},
	}
	policy := operatorTestPolicy(t, newOperatorHasherSpy(t, 10_000), "operator", true)
	err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{
		Username:         "admin",
		Password:         "operator-password-secret-marker",
		CredentialPolicy: policy,
	})
	if !errors.Is(err, &Error{Code: CodePersistence, Field: "credential"}) ||
		errors.Is(err, &Error{Code: CodeCredentialAlreadyExists}) || strings.Contains(fmt.Sprintf("%#v", err), marker) {
		t.Fatalf("ProvisionOperator(forged backend state) = %#v", err)
	}

	opened, openErr := OpenExisting(ctx, backend, operatorRuntimeConfig(policy))
	if opened != nil || !errors.Is(openErr, &Error{Code: CodePersistence, Field: "credential"}) ||
		errors.Is(openErr, &Error{Code: CodeCredentialAlreadyExists}) || strings.Contains(fmt.Sprintf("%#v", openErr), marker) {
		t.Fatalf("OpenExisting(forged backend state) = (%v, %#v)", opened, openErr)
	}
}

func TestOperatorAtomicBackendCannotMutateTrustedCallbackState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "operator-mutated-state.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	if _, err := database.ExecContext(ctx, `DROP TABLE "godj_system_session"`); err != nil {
		t.Fatalf("drop required session table: %v", err)
	}
	const marker = "postgres://operator:password-secret-marker@example.invalid/private"
	backend := &operatorMutatingCallbackErrorBackend{Backend: database, marker: marker}
	policy := operatorTestPolicy(t, newOperatorHasherSpy(t, 10_000), "operator", true)
	err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{
		Username:         "admin",
		Password:         "operator-password-secret-marker",
		CredentialPolicy: policy,
	})
	if !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName}) ||
		errors.Is(err, &Error{Code: CodeCredentialAlreadyExists}) || errors.Unwrap(err) != nil ||
		strings.Contains(fmt.Sprintf("%#v", err), marker) {
		t.Fatalf("ProvisionOperator(mutated callback state) = %#v", err)
	}

	opened, openErr := OpenExisting(ctx, backend, operatorRuntimeConfig(policy))
	if opened != nil || !errors.Is(openErr, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName}) ||
		errors.Is(openErr, &Error{Code: CodeCredentialAlreadyExists}) || errors.Unwrap(openErr) != nil ||
		strings.Contains(fmt.Sprintf("%#v", openErr), marker) {
		t.Fatalf("OpenExisting(mutated callback state) = (%v, %#v)", opened, openErr)
	}
}

func assertOperatorErrorIsSanitized(t *testing.T, err, want, rawCause error, marker string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("operator error = %#v, want %v", err, want)
	}
	if errors.Is(err, rawCause) {
		t.Fatalf("operator error retained raw external cause: %#v", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, &query.Error{Code: query.CodeMissingTable}) {
		t.Fatalf("operator error acquired an unrelated cause: %#v", err)
	}
	for depth, current := 0, err; current != nil; depth++ {
		if depth > 8 {
			t.Fatalf("operator error unwrap chain is unexpectedly deep: %#v", err)
		}
		if strings.Contains(fmt.Sprintf("%v", current), marker) || strings.Contains(fmt.Sprintf("%#v", current), marker) {
			t.Fatalf("operator error unwrap chain exposes external cause: %#v", current)
		}
		current = errors.Unwrap(current)
	}
}

func TestProvisionOperatorRejectsControlUsernameBeforeBackendOrHash(t *testing.T) {
	ctx := context.Background()
	hasher := newOperatorHasherSpy(t, 10_000)
	policy := operatorTestPolicy(t, hasher, "operator", true)
	backend := &unusedOperatorBackend{}
	for _, username := range []string{"ad\nmin", "ad\x7fmin"} {
		t.Run(fmt.Sprintf("%x", []byte(username)), func(t *testing.T) {
			err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{
				Username:         username,
				Password:         "control-username-password-secret-marker",
				CredentialPolicy: policy,
			})
			if !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "username"}) {
				t.Fatalf("ProvisionOperator(control username) = %#v", err)
			}
		})
	}
	if hasher.hashCalls.Load() != 0 || hasher.verifyCalls.Load() != 0 || hasher.validateCalls.Load() != 0 {
		t.Fatalf(
			"invalid username hasher calls = hash %d/verify %d/validate %d, want 0/0/0",
			hasher.hashCalls.Load(), hasher.verifyCalls.Load(), hasher.validateCalls.Load(),
		)
	}
}

func TestProvisionOperatorAndOpenExistingRejectStoredControlUsernameWithoutWriting(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "control-username.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	hasher := newOperatorHasherSpy(t, 10_000)
	policy := operatorTestPolicy(t, hasher, "operator", true)
	provision := ProvisionOperatorConfig{
		Username:         "admin",
		Password:         "stored-control-password-secret-marker",
		CredentialPolicy: policy,
	}
	if err := ProvisionOperator(ctx, database, provision); err != nil {
		t.Fatalf("ProvisionOperator(setup): %v", err)
	}
	credentials, err := readCredentialRows(ctx, database)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("read setup credential = (%+v, %v)", credentials, err)
	}
	const storedUsername = "ad\nmin"
	affected, err := database.Update(ctx, query.NewUpdatePlan(
		credentialTableName,
		[]query.Assignment{query.NewAssignment(credentialUsernameRef, query.String(storedUsername))},
		credentialIDRef,
		query.Integer(credentials[0].id),
	))
	if err != nil || affected != 1 {
		t.Fatalf("replace stored username = affected %d/error %v", affected, err)
	}

	observed := &observedRuntimeBackend{Backend: database}
	hasher.resetObservation()
	if err := ProvisionOperator(ctx, observed, provision); !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("ProvisionOperator(stored control username) = %#v", err)
	}
	if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
		observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatalf(
			"stored-control provision calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
		)
	}
	if hasher.hashCalls.Load() != 0 || hasher.verifyCalls.Load() != 0 || hasher.validateCalls.Load() != 0 {
		t.Fatalf(
			"stored-control provision hasher calls = hash %d/verify %d/validate %d, want 0/0/0",
			hasher.hashCalls.Load(), hasher.verifyCalls.Load(), hasher.validateCalls.Load(),
		)
	}

	observed.resetObservation()
	hasher.resetObservation()
	runtime, err := OpenExisting(ctx, observed, operatorRuntimeConfig(policy))
	if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("OpenExisting(stored control username) = (%v, %#v)", runtime, err)
	}
	if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
		observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatalf(
			"stored-control open calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
		)
	}
	if hasher.hashCalls.Load() != 0 || hasher.verifyCalls.Load() != 0 || hasher.validateCalls.Load() != 0 {
		t.Fatalf(
			"stored-control open hasher calls = hash %d/verify %d/validate %d, want 0/0/0",
			hasher.hashCalls.Load(), hasher.verifyCalls.Load(), hasher.validateCalls.Load(),
		)
	}
	if strings.Contains(err.Error(), storedUsername) {
		t.Fatalf("stored-control error leaked username: %v", err)
	}
}

func TestProvisionOperatorRejectsInvalidHasherEncodingBeforeCoordinatedWrite(t *testing.T) {
	for _, encoded := range []string{"", "bad\rhash", "bad\nhash", "bad\x00hash"} {
		t.Run(fmt.Sprintf("%x", []byte(encoded)), func(t *testing.T) {
			ctx := context.Background()
			database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "invalid-encoded.sqlite3"))+"?mode=rwc")
			t.Cleanup(func() { _ = database.Close() })
			explicitlyMigrateSystemState(t, ctx, database)
			hasher := &acceptingInvalidEncodedHasher{encoded: encoded}
			policy := operatorTestPolicy(t, hasher, "operator", true)
			observed := &observedRuntimeBackend{Backend: database}
			err := ProvisionOperator(ctx, observed, ProvisionOperatorConfig{
				Username:         "admin",
				Password:         "invalid-encoded-password-secret-marker",
				CredentialPolicy: policy,
			})
			if !errors.Is(err, &Error{Code: CodeInvalidConfig, Field: "password_hasher"}) {
				t.Fatalf("ProvisionOperator(invalid encoded password) = %#v", err)
			}
			if hasher.hashCalls.Load() != 1 || hasher.validateCalls.Load() != 1 || hasher.verifyCalls.Load() != 0 {
				t.Fatalf(
					"invalid encoding hasher calls = hash %d/validate %d/verify %d, want 1/1/0",
					hasher.hashCalls.Load(), hasher.validateCalls.Load(), hasher.verifyCalls.Load(),
				)
			}
			if observed.atomicCalls.Load() != 0 || observed.insertCalls.Load() != 0 ||
				observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
				t.Fatalf(
					"invalid encoding calls = atomic %d/insert %d/update %d/delete %d, want 0/0/0/0",
					observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
				)
			}

			observed.resetObservation()
			opened, openErr := OpenExisting(ctx, observed, operatorRuntimeConfig(policy))
			if opened != nil || !errors.Is(openErr, &Error{Code: CodeCredentialAbsent, Field: "credential"}) {
				t.Fatalf("OpenExisting(after invalid encoding) = (%v, %#v)", opened, openErr)
			}
			if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
				observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
				t.Fatalf(
					"absent reconciliation calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
					observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
				)
			}
		})
	}
}

func TestOperatorConfigsFormattingAndJSONAreRedacted(t *testing.T) {
	const (
		passwordMarker = "operator-password-secret-marker"
		hasherMarker   = "operator-password-hasher-secret-marker"
	)
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "operator", Active: true})
	if err != nil {
		t.Fatalf("auth.NewPrincipal(): %v", err)
	}
	policy := CredentialPolicy{
		Principal:      principal,
		PasswordHasher: bootstrapMarkerHasher{Pepper: hasherMarker},
	}
	values := []struct {
		name  string
		want  string
		value any
	}{
		{name: "credential policy", want: "systemstate.CredentialPolicy{redacted}", value: policy},
		{name: "runtime config", want: "systemstate.RuntimeConfig{redacted}", value: RuntimeConfig{CredentialPolicy: policy}},
		{name: "provision config", want: "systemstate.ProvisionOperatorConfig{redacted}", value: ProvisionOperatorConfig{
			Username: "admin", Password: passwordMarker, CredentialPolicy: policy,
		}},
	}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			for _, rendered := range []string{fmt.Sprint(test.value), fmt.Sprintf("%#v", test.value)} {
				if rendered != test.want || strings.Contains(rendered, passwordMarker) || strings.Contains(rendered, hasherMarker) {
					t.Fatalf("config formatting = %q, want %q", rendered, test.want)
				}
			}
			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatalf("json.Marshal(config): %v", err)
			}
			if strings.Contains(string(encoded), passwordMarker) || strings.Contains(string(encoded), hasherMarker) ||
				strings.Contains(string(encoded), `"Password"`) || strings.Contains(string(encoded), `"PasswordHasher"`) {
				t.Fatalf("config JSON publishes a secret-bearing field: %s", encoded)
			}
		})
	}
}

type operatorHasherSpy struct {
	auth.PasswordHasher
	hashCalls     atomic.Int64
	verifyCalls   atomic.Int64
	validateCalls atomic.Int64
}

type unusedOperatorBackend struct{ Backend }

type operatorHashFailureHasher struct {
	auth.PasswordHasher
	hashErr error
}

type operatorPanickingError struct{}

func (operatorPanickingError) Error() string { return "operator external failure" }
func (operatorPanickingError) Is(error) bool { panic("operator external Is must be contained") }
func (operatorPanickingError) Unwrap() error { panic("operator external Unwrap must be contained") }

func (hasher *operatorHashFailureHasher) Hash(context.Context, string) (string, error) {
	return "", hasher.hashErr
}

type operatorMigrationReadFailureBackend struct {
	Backend
	readErr error
}

type operatorAtomicFailureBackend struct {
	Backend
	atomicErr error
}

type operatorMutatingCallbackErrorBackend struct {
	Backend
	marker string
}

func (backend *operatorAtomicFailureBackend) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	return backend.atomicErr
}

func (backend *operatorMutatingCallbackErrorBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		err := callback(session)
		if stateError, ok := err.(*Error); ok && stateError != nil {
			stateError.Code = CodeCredentialAlreadyExists
			stateError.Field = backend.marker
			stateError.Detail = backend.marker
			stateError.Cause = errors.New(backend.marker)
		}
		return fmt.Errorf("%s: %w", backend.marker, err)
	})
}

func (backend *operatorMigrationReadFailureBackend) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	return nil, backend.readErr
}

type acceptingInvalidEncodedHasher struct {
	encoded       string
	hashCalls     atomic.Int64
	verifyCalls   atomic.Int64
	validateCalls atomic.Int64
}

func (hasher *acceptingInvalidEncodedHasher) Hash(context.Context, string) (string, error) {
	hasher.hashCalls.Add(1)
	return hasher.encoded, nil
}

func (hasher *acceptingInvalidEncodedHasher) Verify(context.Context, string, string) (bool, error) {
	hasher.verifyCalls.Add(1)
	return false, nil
}

func (hasher *acceptingInvalidEncodedHasher) ValidateEncoded(string) error {
	hasher.validateCalls.Add(1)
	return nil
}

func newOperatorHasherSpy(t *testing.T, iterations int) *operatorHasherSpy {
	t.Helper()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: iterations})
	if err != nil {
		t.Fatalf("auth.NewPBKDF2(): %v", err)
	}
	return &operatorHasherSpy{PasswordHasher: hasher}
}

func (hasher *operatorHasherSpy) Hash(ctx context.Context, password string) (string, error) {
	hasher.hashCalls.Add(1)
	return hasher.PasswordHasher.Hash(ctx, password)
}

func (hasher *operatorHasherSpy) Verify(ctx context.Context, password, encoded string) (bool, error) {
	hasher.verifyCalls.Add(1)
	return hasher.PasswordHasher.Verify(ctx, password, encoded)
}

func (hasher *operatorHasherSpy) ValidateEncoded(encoded string) error {
	hasher.validateCalls.Add(1)
	return hasher.PasswordHasher.ValidateEncoded(encoded)
}

func (hasher *operatorHasherSpy) resetObservation() {
	hasher.hashCalls.Store(0)
	hasher.verifyCalls.Store(0)
	hasher.validateCalls.Store(0)
}

func operatorTestPolicy(t *testing.T, hasher auth.PasswordHasher, principalID string, active bool) CredentialPolicy {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     principalID,
		Active: active,
		Permissions: []auth.Permission{
			mustPermission(t, "admin.site.access"),
			mustPermission(t, "article.article.view"),
			mustPermission(t, "article.article.add"),
			mustPermission(t, "article.article.change"),
			mustPermission(t, "article.article.delete"),
		},
	})
	if err != nil {
		t.Fatalf("auth.NewPrincipal(): %v", err)
	}
	return CredentialPolicy{Principal: principal, PasswordHasher: hasher}
}

func operatorRuntimeConfig(policy CredentialPolicy) RuntimeConfig {
	return RuntimeConfig{
		CredentialPolicy: policy,
		MaxSessions:      8,
		AuditCapacity:    16,
	}
}
