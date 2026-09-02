package systemstate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

func TestRuntimeExplicitProvisionRestartAndDatabaseInterfaces(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "runtime.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	firstDatabase := openSessionStoreBackend(t, ctx, dataSourceName)
	explicitlyMigrateSystemState(t, ctx, firstDatabase)
	firstBackend := &observedRuntimeBackend{Backend: firstDatabase}
	config := runtimeTestConfig(t, "bootstrap-password-secret-marker")

	if err := ProvisionOperator(ctx, firstBackend, config.provisionConfig(t)); err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("ProvisionOperator(first): %v", err)
	}
	if firstBackend.atomicCalls.Load() != 1 || firstBackend.insertCalls.Load() != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("provision calls = atomic %d/insert %d, want 1/1", firstBackend.atomicCalls.Load(), firstBackend.insertCalls.Load())
	}
	firstBackend.resetObservation()
	firstRuntime, err := OpenExisting(ctx, firstBackend, config.runtimeConfig(t))
	if err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("OpenExisting(first): %v", err)
	}
	if firstBackend.atomicCalls.Load() != 1 || firstBackend.insertCalls.Load() != 0 {
		_ = firstDatabase.Close()
		t.Fatalf("open-existing calls = atomic %d/insert %d, want 1/0", firstBackend.atomicCalls.Load(), firstBackend.insertCalls.Load())
	}
	if firstRuntime.Authenticator() == nil || firstRuntime.SessionStore() == nil {
		_ = firstDatabase.Close()
		t.Fatal("opened Runtime did not publish authenticator/session store")
	}
	principal, err := firstRuntime.Authenticator().Authenticate(ctx, config.Username, config.Password)
	if err != nil || principal.ID() != config.PrincipalID || !principal.Authenticated() {
		_ = firstDatabase.Close()
		t.Fatalf("Authenticate(first runtime) = (%v,%v)", principal, err)
	}
	credentials, err := readCredentialRows(ctx, firstDatabase)
	if err != nil || len(credentials) != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("durable credentials after bootstrap = (%+v,%v)", credentials, err)
	}
	firstCredential := credentials[0]
	if firstCredential.definitionDigest != initialDefinitionDigest || strings.Contains(firstCredential.encodedPassword, config.Password) {
		_ = firstDatabase.Close()
		t.Fatal("bootstrap stored an incorrect digest or raw password")
	}
	if got := fmt.Sprint(firstRuntime); got != "systemstate.Runtime{redacted}" || strings.Contains(got, config.Password) {
		_ = firstDatabase.Close()
		t.Fatalf("Runtime.String() = %q", got)
	}

	firstBackend.resetObservation()
	auditID, err := firstRuntime.Insert(ctx, runtimeAuditInsertPlan("before"))
	if err != nil || auditID <= 0 || firstBackend.atomicCalls.Load() != 1 || firstBackend.insertCalls.Load() != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("Runtime.Insert() = id %d/error %v/calls %d/%d", auditID, err, firstBackend.atomicCalls.Load(), firstBackend.insertCalls.Load())
	}
	firstBackend.resetObservation()
	rows, err := firstRuntime.Query(ctx, query.NewPlan(auditTableName, auditFieldRefs).WithConditions(
		query.NewCondition(auditIDRef, query.LookupExact, query.Integer(auditID)),
	))
	if err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("Runtime.Query(): %v", err)
	}
	if !rows.Next() {
		_ = rows.Close()
		_ = firstDatabase.Close()
		t.Fatal("Runtime.Query() returned no inserted audit row")
	}
	var gotID int64
	var actorID, model, objectID, action, changedFields, displayLabel string
	if err := rows.Scan(&gotID, &actorID, &model, &objectID, &action, &changedFields, &displayLabel); err != nil {
		_ = rows.Close()
		_ = firstDatabase.Close()
		t.Fatalf("scan Runtime.Query() row: %v", err)
	}
	if err := rows.Close(); err != nil || gotID != auditID || displayLabel != "before" || firstBackend.atomicCalls.Load() != 0 {
		_ = firstDatabase.Close()
		t.Fatalf("Runtime.Query() result = id %d/label %q/close %v/atomic %d", gotID, displayLabel, err, firstBackend.atomicCalls.Load())
	}
	firstBackend.resetObservation()
	affected, err := firstRuntime.Update(ctx, query.NewUpdatePlan(
		auditTableName,
		[]query.Assignment{query.NewAssignment(auditDisplayLabelRef, query.String("after"))},
		auditIDRef,
		query.Integer(auditID),
	))
	if err != nil || affected != 1 || firstBackend.atomicCalls.Load() != 1 || firstBackend.updateCalls.Load() != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("Runtime.Update() = affected %d/error %v/calls %d/%d", affected, err, firstBackend.atomicCalls.Load(), firstBackend.updateCalls.Load())
	}
	firstBackend.resetObservation()
	affected, err = firstRuntime.Delete(ctx, query.NewDeletePlan(auditTableName, auditIDRef, query.Integer(auditID)))
	if err != nil || affected != 1 || firstBackend.atomicCalls.Load() != 1 || firstBackend.deleteCalls.Load() != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("Runtime.Delete() = affected %d/error %v/calls %d/%d", affected, err, firstBackend.atomicCalls.Load(), firstBackend.deleteCalls.Load())
	}
	if err := firstDatabase.Close(); err != nil {
		t.Fatalf("close first runtime database: %v", err)
	}

	secondDatabase := openSessionStoreBackend(t, ctx, dataSourceName)
	t.Cleanup(func() { _ = secondDatabase.Close() })
	secondBackend := &observedRuntimeBackend{Backend: secondDatabase}
	secondConfig := runtimeTestConfig(t, "unused-restart-password-marker")
	secondRuntime, err := OpenExisting(ctx, secondBackend, secondConfig.runtimeConfig(t))
	if err != nil {
		t.Fatalf("OpenExisting(restart): %v", err)
	}
	if secondBackend.atomicCalls.Load() != 1 || secondBackend.insertCalls.Load() != 0 {
		t.Fatalf("identical restart calls = atomic %d/insert %d, want 1/0", secondBackend.atomicCalls.Load(), secondBackend.insertCalls.Load())
	}
	secondCredentials, err := readCredentialRows(ctx, secondDatabase)
	if err != nil || len(secondCredentials) != 1 || secondCredentials[0] != firstCredential {
		t.Fatalf("restart credential changed = (%+v,%v), want %+v", secondCredentials, err, firstCredential)
	}
	resolved, err := secondRuntime.Authenticator().Resolve(ctx, config.PrincipalID)
	if err != nil || resolved.ID() != config.PrincipalID || secondRuntime.SessionStore() == nil {
		t.Fatalf("restart resolve/session store = (%v,%v,%v)", resolved, err, secondRuntime.SessionStore())
	}
	if (*Runtime)(nil).Authenticator() != nil || (*Runtime)(nil).SessionStore() != nil {
		t.Fatal("nil Runtime published an accessor value")
	}
}

func TestRuntimePasswordWorkStaysOutsideDatabaseCoordinationFence(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "password-work.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	backend := &coordinationDepthRuntimeBackend{Backend: database}
	config := runtimeTestConfig(t, "password-work-outside-fence-secret-marker")
	hasher := &coordinationCheckingHasher{
		PasswordHasher: config.PasswordHasher,
		active:         &backend.active,
	}
	config.PasswordHasher = hasher

	if err := ProvisionOperator(ctx, backend, config.provisionConfig(t)); err != nil {
		t.Fatalf("ProvisionOperator(first Runtime) = %v", err)
	}
	if runtime, err := OpenExisting(ctx, backend, config.runtimeConfig(t)); err != nil || runtime == nil {
		t.Fatalf("OpenExisting(first Runtime) = (%v, %v)", runtime, err)
	}
	if runtime, err := OpenExisting(ctx, backend, config.runtimeConfig(t)); err != nil || runtime == nil {
		t.Fatalf("OpenExisting(restarted Runtime) = (%v, %v)", runtime, err)
	}
	if got := backend.maximum.Load(); got != 1 {
		t.Fatalf("maximum active coordination calls = %d, want 1", got)
	}
	if hasher.hashCalls.Load() == 0 || hasher.verifyCalls.Load() != 0 || hasher.validateCalls.Load() == 0 {
		t.Fatalf(
			"password hasher calls = hash %d/verify %d/validate %d, want hash+validate and zero startup Verify",
			hasher.hashCalls.Load(),
			hasher.verifyCalls.Load(),
			hasher.validateCalls.Load(),
		)
	}
	if got := hasher.activeViolations.Load(); got != 0 {
		t.Fatalf("password hasher calls while coordination was active = %d, want 0", got)
	}
}

func TestRuntimePreliminaryCredentialDisappearanceFailsClosedWithoutReplacement(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "credential-disappears.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	config := runtimeTestConfig(t, "credential-disappearance-secret-marker")
	if err := ProvisionOperator(ctx, database, config.provisionConfig(t)); err != nil {
		t.Fatalf("ProvisionOperator(setup) = %v", err)
	}
	credentials, err := readCredentialRows(ctx, database)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("bootstrap credentials = (%+v, %v), want one row", credentials, err)
	}
	backend := &credentialRemovedBeforeCoordinationBackend{
		Backend:      database,
		credentialID: credentials[0].id,
	}
	restartConfig := runtimeTestConfig(t, config.Password)
	err = ProvisionOperator(ctx, backend, restartConfig.provisionConfig(t))
	if !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("ProvisionOperator(after preliminary credential disappearance) = %#v", err)
	}
	if backend.coordinatedCalls.Load() != 1 || backend.callbackCalls.Load() != 1 {
		t.Fatalf(
			"coordinated disappearance inspection calls = transaction %d/callback %d, want 1/1",
			backend.coordinatedCalls.Load(),
			backend.callbackCalls.Load(),
		)
	}
	credentials, readErr := readCredentialRows(ctx, database)
	if readErr != nil || len(credentials) != 0 {
		t.Fatalf("credentials after fail-closed startup = (%+v, %v), want no replacement", credentials, readErr)
	}
}

func TestOpenExistingRejectsStoredPasswordProfileMismatchWithoutWriting(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "profile-mismatch.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	baseline := runtimeTestConfig(t, "correct-password-secret-marker")
	if err := ProvisionOperator(ctx, database, baseline.provisionConfig(t)); err != nil {
		t.Fatalf("ProvisionOperator(setup): %v", err)
	}
	credentials, err := readCredentialRows(ctx, database)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("read setup credential = (%+v, %v)", credentials, err)
	}
	otherHasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 20_000})
	if err != nil {
		t.Fatalf("auth.NewPBKDF2(other profile): %v", err)
	}
	const storedSecret = "stored-profile-secret-marker"
	encoded, err := otherHasher.Hash(ctx, storedSecret)
	if err != nil {
		t.Fatalf("Hash(other profile): %v", err)
	}
	affected, err := database.Update(ctx, query.NewUpdatePlan(
		credentialTableName,
		[]query.Assignment{query.NewAssignment(credentialEncodedPasswordRef, query.String(encoded))},
		credentialIDRef,
		query.Integer(credentials[0].id),
	))
	if err != nil || affected != 1 {
		t.Fatalf("replace stored profile = affected %d/error %v", affected, err)
	}
	observed := &observedRuntimeBackend{Backend: database}
	if err := ProvisionOperator(ctx, observed, baseline.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("ProvisionOperator(profile mismatch) = %#v", err)
	}
	observed.resetObservation()
	runtime, err := OpenExisting(ctx, observed, baseline.runtimeConfig(t))
	if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("OpenExisting(profile mismatch) = (%v, %#v)", runtime, err)
	}
	if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
		observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatalf(
			"profile mismatch calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
		)
	}
	if strings.Contains(err.Error(), storedSecret) || strings.Contains(err.Error(), encoded) {
		t.Fatalf("profile mismatch error leaked stored material: %v", err)
	}
}

func TestProvisionOperatorAndOpenExistingRejectStoredDefinitionDigestMismatchWithoutWriting(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "definition-mismatch.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	config := runtimeTestConfig(t, "definition-password-secret-marker")
	if err := ProvisionOperator(ctx, database, config.provisionConfig(t)); err != nil {
		t.Fatalf("ProvisionOperator(setup): %v", err)
	}
	credentials, err := readCredentialRows(ctx, database)
	if err != nil || len(credentials) != 1 {
		t.Fatalf("read setup credential = (%+v, %v)", credentials, err)
	}
	const storedMarker = "wrong-definition-digest-marker"
	affected, err := database.Update(ctx, query.NewUpdatePlan(
		credentialTableName,
		[]query.Assignment{query.NewAssignment(credentialDefinitionDigestRef, query.String(storedMarker))},
		credentialIDRef,
		query.Integer(credentials[0].id),
	))
	if err != nil || affected != 1 {
		t.Fatalf("replace stored definition digest = affected %d/error %v", affected, err)
	}
	observed := &observedRuntimeBackend{Backend: database}
	if err := ProvisionOperator(ctx, observed, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("ProvisionOperator(definition mismatch) = %#v", err)
	}
	if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
		observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatalf(
			"definition-mismatched provision calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
		)
	}
	observed.resetObservation()
	runtime, err := OpenExisting(ctx, observed, config.runtimeConfig(t))
	if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
		t.Fatalf("OpenExisting(definition mismatch) = (%v, %#v)", runtime, err)
	}
	if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
		observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatalf(
			"definition-mismatched open calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
			observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
		)
	}
	if strings.Contains(err.Error(), storedMarker) {
		t.Fatalf("definition mismatch error leaked stored digest: %v", err)
	}
}

func TestRuntimeFailsClosedOnCorruptDuplicateAndOrphanCredentialState(t *testing.T) {
	ctx := context.Background()

	t.Run("corrupt permission payload", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "corrupt.sqlite3")
		dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
		database := openSessionStoreBackend(t, ctx, dataSourceName)
		explicitlyMigrateSystemState(t, ctx, database)
		config := runtimeTestConfig(t, "corrupt-password-secret-marker")
		_ = provisionAndOpenTestRuntime(t, ctx, database, config)
		credentials, err := readCredentialRows(ctx, database)
		if err != nil || len(credentials) != 1 {
			_ = database.Close()
			t.Fatalf("read corrupt setup credential: %v/%+v", err, credentials)
		}
		const storedMarker = "v9.SHOULD_NOT_ESCAPE"
		affected, err := database.Update(ctx, query.NewUpdatePlan(
			credentialTableName,
			[]query.Assignment{query.NewAssignment(credentialPermissionsRef, query.String(storedMarker))},
			credentialIDRef,
			query.Integer(credentials[0].id),
		))
		if err != nil || affected != 1 {
			_ = database.Close()
			t.Fatalf("corrupt stored permission = affected %d/error %v", affected, err)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close corrupt setup database: %v", err)
		}
		reopened := openSessionStoreBackend(t, ctx, dataSourceName)
		defer func() { _ = reopened.Close() }()
		observed := &observedRuntimeBackend{Backend: reopened}
		if err := ProvisionOperator(ctx, observed, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeCorruptState}) {
			t.Fatalf("ProvisionOperator(corrupt permission) = %#v", err)
		}
		observed.resetObservation()
		runtime, err := OpenExisting(
			ctx,
			observed,
			runtimeTestConfig(t, "unused-restart-password").runtimeConfig(t),
		)
		if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState}) {
			t.Fatalf("OpenExisting(corrupt permission) = (%v,%#v)", runtime, err)
		}
		if strings.Contains(err.Error(), storedMarker) || strings.Contains(err.Error(), config.Password) {
			t.Fatalf("corrupt credential error leaked stored/configured material: %v", err)
		}
	})

	t.Run("duplicate credential", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "duplicate.sqlite3")
		dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
		database := openSessionStoreBackend(t, ctx, dataSourceName)
		explicitlyMigrateSystemState(t, ctx, database)
		config := runtimeTestConfig(t, "duplicate-password-secret-marker")
		_ = provisionAndOpenTestRuntime(t, ctx, database, config)
		credentials, err := readCredentialRows(ctx, database)
		if err != nil || len(credentials) != 1 {
			_ = database.Close()
			t.Fatalf("read duplicate setup credential: %v/%+v", err, credentials)
		}
		if err := database.Atomic(ctx, func(session db.Session) error {
			_, err := insertCredential(ctx, session, credentials[0])
			return err
		}); err != nil {
			_ = database.Close()
			t.Fatalf("insert duplicate credential: %v", err)
		}
		if err := database.Close(); err != nil {
			t.Fatalf("close duplicate setup database: %v", err)
		}
		reopened := openSessionStoreBackend(t, ctx, dataSourceName)
		defer func() { _ = reopened.Close() }()
		observed := &observedRuntimeBackend{Backend: reopened}
		if err := ProvisionOperator(ctx, observed, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeCardinality, Field: "credential"}) {
			t.Fatalf("ProvisionOperator(duplicate credential) = %#v", err)
		}
		observed.resetObservation()
		runtime, err := OpenExisting(ctx, observed, runtimeTestConfig(t, "unused-restart-password").runtimeConfig(t))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeCardinality, Field: "credential"}) {
			t.Fatalf("OpenExisting(duplicate credential) = (%v,%#v)", runtime, err)
		}
		if observed.atomicCalls.Load() != 1 {
			t.Fatalf("duplicate credential invoked %d coordinated inspections, want 1", observed.atomicCalls.Load())
		}
	})

	t.Run("dependent row without credential", func(t *testing.T) {
		database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "orphan.sqlite3"))+"?mode=rwc")
		defer func() { _ = database.Close() }()
		explicitlyMigrateSystemState(t, ctx, database)
		if _, err := database.Insert(ctx, runtimeAuditInsertPlan("orphan")); err != nil {
			t.Fatalf("insert orphan audit row: %v", err)
		}
		observed := &observedRuntimeBackend{Backend: database}
		config := runtimeTestConfig(t, "orphan-password-secret-marker")
		if err := ProvisionOperator(ctx, observed, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
			t.Fatalf("ProvisionOperator(orphan audit state) = %#v", err)
		}
		observed.resetObservation()
		runtime, err := OpenExisting(ctx, observed, config.runtimeConfig(t))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
			t.Fatalf("OpenExisting(orphan state) = (%v,%#v)", runtime, err)
		}
		if observed.atomicCalls.Load() != 1 {
			t.Fatalf("orphan state invoked %d coordinated inspections, want 1", observed.atomicCalls.Load())
		}
	})

	t.Run("dependent session row without credential", func(t *testing.T) {
		database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "orphan-session.sqlite3"))+"?mode=rwc")
		defer func() { _ = database.Close() }()
		explicitlyMigrateSystemState(t, ctx, database)
		config := runtimeTestConfig(t, "orphan-session-password-secret-marker")
		runtime := provisionAndOpenTestRuntime(t, ctx, database, config)
		base := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
		record := multiRuntimeSessionRecord(t, "Z", base, base, base.Add(time.Hour), base.Add(30*time.Minute))
		if created, err := runtime.SessionStore().Create(ctx, record); err != nil || !created {
			t.Fatalf("Create(orphan session fixture) = (%v, %v)", created, err)
		}
		credentials, err := readCredentialRows(ctx, database)
		if err != nil || len(credentials) != 1 {
			t.Fatalf("read credential before orphaning session = (%+v, %v)", credentials, err)
		}
		affected, err := database.Delete(ctx, query.NewDeletePlan(
			credentialTableName,
			credentialIDRef,
			query.Integer(credentials[0].id),
		))
		if err != nil || affected != 1 {
			t.Fatalf("delete sole credential = affected %d/error %v", affected, err)
		}
		observed := &observedRuntimeBackend{Backend: database}
		if err := ProvisionOperator(ctx, observed, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
			t.Fatalf("ProvisionOperator(orphan session state) = %#v", err)
		}
		observed.resetObservation()
		opened, err := OpenExisting(ctx, observed, config.runtimeConfig(t))
		if opened != nil || !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
			t.Fatalf("OpenExisting(orphan session state) = (%v, %#v)", opened, err)
		}
		if observed.atomicCalls.Load() != 1 || observed.insertCalls.Load() != 0 ||
			observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
			t.Fatalf(
				"orphan session open calls = atomic %d/insert %d/update %d/delete %d, want 1/0/0/0",
				observed.atomicCalls.Load(), observed.insertCalls.Load(), observed.updateCalls.Load(), observed.deleteCalls.Load(),
			)
		}
	})
}

func TestRuntimeRequiresExactMigrationAndNeverCreatesOrRepairsSchema(t *testing.T) {
	ctx := context.Background()
	config := runtimeTestConfig(t, "schema-password-secret-marker")

	t.Run("unmigrated", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "unmigrated.sqlite3"))+"?mode=rwc")
		defer func() { _ = backend.Close() }()
		if err := ProvisionOperator(ctx, backend, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatalf("ProvisionOperator(unmigrated) = %#v", err)
		}
		runtime, err := OpenExisting(ctx, backend, config.runtimeConfig(t))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatalf("OpenExisting(unmigrated) = (%v,%#v)", runtime, err)
		}
		history, historyErr := backend.ReadAppliedMigrations(ctx)
		if historyErr != nil || len(history) != 0 {
			t.Fatalf("unmigrated history after startup attempts = (%+v,%v)", history, historyErr)
		}
		assertRuntimeTableMissing(t, ctx, backend, credentialTableName, credentialFieldRefs)
	})

	t.Run("missing required table", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "missing.sqlite3"))+"?mode=rwc")
		defer func() { _ = backend.Close() }()
		explicitlyMigrateSystemState(t, ctx, backend)
		if _, err := backend.ExecContext(ctx, `DROP TABLE "godj_system_session"`); err != nil {
			t.Fatalf("drop required session table: %v", err)
		}
		if err := ProvisionOperator(ctx, backend, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName}) {
			t.Fatalf("ProvisionOperator(missing session table) = %#v", err)
		}
		runtime, err := OpenExisting(ctx, backend, config.runtimeConfig(t))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName}) {
			t.Fatalf("OpenExisting(missing session table) = (%v,%#v)", runtime, err)
		}
		assertRuntimeTableMissing(t, ctx, backend, sessionTableName, []query.FieldRef{
			systemRowIDField,
			sessionDigestField,
			sessionPayloadField,
		})
	})

	t.Run("unknown same-app migration", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "unknown.sqlite3"))+"?mode=rwc")
		defer func() { _ = backend.Close() }()
		extra := migrationdefinition.Source{
			SourceID: "systemstate/test-only-unknown-tail",
			Document: []byte(`{"format_version":1,"producer":{"name":"systemstate-test","version":"1"},"migration":{"app":"godj_system","name":"0002_unknown","dependencies":[{"app":"godj_system","name":"0001_initial"}],"operations":[]}}`),
		}
		loaded, _, err := migrationdefinition.Load(InitialDefinitionSource(), extra)
		if err != nil {
			t.Fatalf("load system definition with unknown tail: %v", err)
		}
		if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
			t.Fatalf("migrate system definition with unknown tail: %v", err)
		}
		if err := ProvisionOperator(ctx, backend, config.provisionConfig(t)); !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatalf("ProvisionOperator(unknown system migration) = %#v", err)
		}
		runtime, err := OpenExisting(ctx, backend, config.runtimeConfig(t))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatalf("OpenExisting(unknown system migration) = (%v,%#v)", runtime, err)
		}
	})
}

func TestProvisionOperatorFailureAndUnknownOutcomesAreNotRetriedAndFreshBackendReconciles(t *testing.T) {
	tests := []struct {
		name             string
		callbackFailure  error
		outcomeCode      string
		wantPhysicalRows int
	}{
		{
			name:             "callback rollback",
			callbackFailure:  errors.New("injected provision callback failure"),
			wantPhysicalRows: 0,
		},
		{
			name:             "commit outcome unknown",
			outcomeCode:      query.CodeCommitOutcomeUnknown,
			wantPhysicalRows: 1,
		},
		{
			name:             "transaction outcome unknown",
			outcomeCode:      query.CodeTransactionOutcomeUnknown,
			wantPhysicalRows: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			dataSourceName := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "provision-outcome.sqlite3")) + "?mode=rwc"
			database := openSessionStoreBackend(t, ctx, dataSourceName)
			t.Cleanup(func() { _ = database.Close() })
			explicitlyMigrateSystemState(t, ctx, database)
			backend := &provisionFaultRuntimeBackend{
				Backend:         database,
				callbackFailure: test.callbackFailure,
				outcomeCode:     test.outcomeCode,
			}
			config := runtimeTestConfig(t, "provision-outcome-password-secret-marker")

			err := ProvisionOperator(ctx, backend, config.provisionConfig(t))
			if err == nil || !errors.Is(err, &Error{Code: CodePersistence, Field: "credential"}) ||
				errors.Is(err, &Error{Code: CodeSchemaUnavailable}) {
				t.Fatalf("ProvisionOperator(outcome failure) = %#v", err)
			}
			if test.callbackFailure != nil && errors.Is(err, test.callbackFailure) {
				t.Fatalf("ProvisionOperator(callback failure) retained raw cause: %#v", err)
			}
			if test.outcomeCode != "" {
				var outcome *query.Error
				if !errors.As(err, &outcome) || outcome.Code != test.outcomeCode || outcome.Detail != "" || outcome.Cause != nil {
					t.Fatalf("ProvisionOperator(%s) lost reconciliation marker: %#v", test.outcomeCode, err)
				}
			}
			if got := backend.atomicCalls.Load(); got != 1 {
				t.Fatalf("Atomic calls = %d, want exactly 1", got)
			}
			if got := backend.callbackCalls.Load(); got != 1 {
				t.Fatalf("Atomic callback calls = %d, want exactly 1", got)
			}
			rows, readErr := readCredentialRows(ctx, database)
			if readErr != nil || len(rows) != test.wantPhysicalRows {
				t.Fatalf(
					"physical rows after failed outcome = (%d,%v), want (%d,nil)",
					len(rows), readErr, test.wantPhysicalRows,
				)
			}

			freshBackend := openSessionStoreBackend(t, ctx, dataSourceName)
			t.Cleanup(func() { _ = freshBackend.Close() })
			reopened, reopenErr := OpenExisting(ctx, freshBackend, config.runtimeConfig(t))
			if test.wantPhysicalRows == 0 {
				if reopened != nil || !errors.Is(reopenErr, &Error{Code: CodeCredentialAbsent, Field: "credential"}) {
					t.Fatalf("OpenExisting(fresh rollback reconciliation) = (%v,%#v)", reopened, reopenErr)
				}
				if err := ProvisionOperator(ctx, freshBackend, config.provisionConfig(t)); err != nil {
					t.Fatalf("ProvisionOperator(after confirmed rollback): %v", err)
				}
				reopened, reopenErr = OpenExisting(ctx, freshBackend, config.runtimeConfig(t))
			}
			if reopenErr != nil || reopened == nil {
				t.Fatalf("OpenExisting(fresh-backend reconciliation) = (%v,%v)", reopened, reopenErr)
			}
		})
	}
}

func TestRedactAtomicFailurePreservesCoordinationOwnershipAndOutcomeMarkers(t *testing.T) {
	tests := []struct {
		name        string
		cause       error
		wantCode    ErrorCode
		wantOutcome string
	}{
		{
			name:     "coordination acquisition failure",
			cause:    errors.New("backend coordination acquisition failed"),
			wantCode: CodePersistence,
		},
		{
			name: "commit outcome unknown",
			cause: &query.Error{
				Category: query.CategoryBackend,
				Code:     query.CodeCommitOutcomeUnknown,
			},
			wantCode:    CodePersistence,
			wantOutcome: query.CodeCommitOutcomeUnknown,
		},
		{
			name: "transaction outcome unknown",
			cause: &query.Error{
				Category: query.CategoryBackend,
				Code:     query.CodeTransactionOutcomeUnknown,
			},
			wantCode:    CodePersistence,
			wantOutcome: query.CodeTransactionOutcomeUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := redactAtomicFailure(test.cause)
			if !errors.Is(err, &Error{Code: test.wantCode, Field: "credential"}) ||
				errors.Is(err, &Error{Code: CodeSchemaUnavailable}) ||
				(test.wantOutcome == "" && errors.Is(err, test.cause)) {
				t.Fatalf("redactAtomicFailure() = %#v", err)
			}
			if test.wantOutcome != "" {
				var outcome *query.Error
				if !errors.As(err, &outcome) || outcome.Code != test.wantOutcome || outcome.Detail != "" || outcome.Cause != nil {
					t.Fatalf("redactAtomicFailure() outcome = %#v, want %q", outcome, test.wantOutcome)
				}
			}
		})
	}

	stateCause := &Error{Code: CodeCardinality, Field: "credential", Detail: "framework-owned cardinality failure"}
	stateSnapshot := snapshotOperatorError(stateCause)
	stateCause.Code = CodeCredentialAlreadyExists
	stateCause.Field = "backend-mutated-field"
	stateCause.Detail = "backend-mutated-detail"
	if err := redactOperatorAtomicFailure(stateCause, stateSnapshot); !errors.Is(err, &Error{Code: CodeCardinality, Field: "credential"}) ||
		err.(*Error).Cause != nil || err.(*Error).Detail != stateSnapshot.detail {
		t.Fatalf("redactOperatorAtomicFailure(trusted state error) = %#v", err)
	}
	const forgedMarker = "postgres://operator:password-secret-marker@example.invalid/private"
	forged := &Error{Code: CodeCredentialAlreadyExists, Field: forgedMarker, Detail: forgedMarker}
	if err := redactAtomicFailure(forged); !errors.Is(err, &Error{Code: CodePersistence, Field: "credential"}) ||
		errors.Is(err, &Error{Code: CodeCredentialAlreadyExists}) || err.(*Error).Cause != nil ||
		strings.Contains(err.Error(), forgedMarker) {
		t.Fatalf("redactAtomicFailure(forged state error) = %#v", err)
	}
	if err := redactAtomicFailure(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("redactAtomicFailure(context canceled) = %#v", err)
	}

	for _, outcomeCode := range []string{
		query.CodeCommitOutcomeUnknown,
		query.CodeTransactionOutcomeUnknown,
	} {
		t.Run("joined "+outcomeCode+" takes precedence", func(t *testing.T) {
			primary := &Error{
				Code:   CodeSchemaUnavailable,
				Field:  "credential",
				Detail: "test-only callback failure",
				Cause: &query.Error{
					Category: query.CategoryBackend,
					Code:     query.CodeMissingTable,
				},
			}
			unknown := &query.Error{
				Category: query.CategoryBackend,
				Code:     outcomeCode,
			}
			cause := errors.Join(primary, unknown)
			err := redactAtomicFailure(cause)
			var classified *Error
			var safeOutcome *query.Error
			if !errors.As(err, &classified) || classified.Code != CodePersistence || classified.Field != "credential" ||
				errors.Is(err, primary) || !errors.Is(err, unknown) || !errors.As(err, &safeOutcome) ||
				safeOutcome == nil || safeOutcome.Detail != "" || safeOutcome.Cause != nil {
				t.Fatalf("redactAtomicFailure(joined %s) = %#v", outcomeCode, err)
			}
		})
	}

	for _, cause := range []error{
		(*query.Error)(nil),
		(*Error)(nil),
	} {
		err := redactAtomicFailure(cause)
		var classified *Error
		if !errors.As(err, &classified) || classified == nil || classified.Code != CodePersistence ||
			classified.Field != "credential" {
			t.Fatalf("redactAtomicFailure(typed nil %T) = %#v", cause, err)
		}
	}
}

func TestRuntimeGateSerializesAtomicCallsAndValidatesBeforeBackend(t *testing.T) {
	ctx := context.Background()
	backend := &serialRuntimeBackend{}
	runtime := &Runtime{backend: backend}
	const calls = 16
	var wait sync.WaitGroup
	wait.Add(calls)
	errorsSeen := make(chan error, calls)
	for index := 0; index < calls; index++ {
		go func() {
			defer wait.Done()
			errorsSeen <- runtime.withAtomic(ctx, func(db.Session) error { return nil })
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("withAtomic(serial): %v", err)
		}
	}
	if backend.atomicCalls.Load() != calls || backend.maxActive.Load() != 1 {
		t.Fatalf("serialized Atomic calls = total %d/max active %d, want %d/1", backend.atomicCalls.Load(), backend.maxActive.Load(), calls)
	}

	before := backend.atomicCalls.Load()
	if err := runtime.withAtomic(nil, func(db.Session) error { return nil }); !errors.Is(err, &Error{Code: CodeInvalidInput, Field: "context"}) {
		t.Fatalf("withAtomic(nil context): %#v", err)
	}
	if err := runtime.withAtomic(ctx, nil); !errors.Is(err, &Error{Code: CodeInvalidInput, Field: "callback"}) {
		t.Fatalf("withAtomic(nil callback): %#v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := runtime.withAtomic(canceled, func(db.Session) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("withAtomic(canceled): %#v", err)
	}
	if backend.atomicCalls.Load() != before {
		t.Fatalf("invalid withAtomic calls reached backend: %d -> %d", before, backend.atomicCalls.Load())
	}
}

type runtimeTestConfiguration struct {
	Username       string
	Password       string
	PrincipalID    string
	Active         bool
	Permissions    []auth.Permission
	PasswordHasher auth.PasswordHasher
	MaxSessions    int
	AuditCapacity  int
}

func runtimeTestConfig(t *testing.T, password string) runtimeTestConfiguration {
	t.Helper()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatalf("auth.NewPBKDF2(): %v", err)
	}
	return runtimeTestConfiguration{
		Username:    "admin",
		Password:    password,
		PrincipalID: "article-development-admin",
		Active:      true,
		Permissions: []auth.Permission{
			mustPermission(t, "admin.site.access"),
			mustPermission(t, "article.article.view"),
			mustPermission(t, "article.article.add"),
			mustPermission(t, "article.article.change"),
			mustPermission(t, "article.article.delete"),
		},
		PasswordHasher: hasher,
		MaxSessions:    8,
		AuditCapacity:  16,
	}
}

func (config runtimeTestConfiguration) credentialPolicy(t *testing.T) CredentialPolicy {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          config.PrincipalID,
		Active:      config.Active,
		Permissions: append([]auth.Permission(nil), config.Permissions...),
	})
	if err != nil {
		t.Fatalf("auth.NewPrincipal(runtime test policy): %v", err)
	}
	return CredentialPolicy{Principal: principal, PasswordHasher: config.PasswordHasher}
}

func (config runtimeTestConfiguration) provisionConfig(t *testing.T) ProvisionOperatorConfig {
	t.Helper()
	return ProvisionOperatorConfig{
		Username:         config.Username,
		Password:         config.Password,
		CredentialPolicy: config.credentialPolicy(t),
	}
}

func (config runtimeTestConfiguration) runtimeConfig(t *testing.T) RuntimeConfig {
	t.Helper()
	return RuntimeConfig{
		CredentialPolicy: config.credentialPolicy(t),
		MaxSessions:      config.MaxSessions,
		AuditCapacity:    config.AuditCapacity,
	}
}

func provisionAndOpenTestRuntime(
	t *testing.T,
	ctx context.Context,
	backend Backend,
	config runtimeTestConfiguration,
) *Runtime {
	t.Helper()
	if err := ProvisionOperator(ctx, backend, config.provisionConfig(t)); err != nil &&
		!errors.Is(err, &Error{Code: CodeCredentialAlreadyExists}) {
		t.Fatalf("systemstate.ProvisionOperator(): %v", err)
	}
	runtime, err := OpenExisting(ctx, backend, config.runtimeConfig(t))
	if err != nil {
		t.Fatalf("systemstate.OpenExisting(): %v", err)
	}
	return runtime
}

func runtimeAuditInsertPlan(displayLabel string) query.InsertPlan {
	return query.NewInsertPlanReturningKey(
		auditTableName,
		[]query.Assignment{
			query.NewAssignment(auditActorIDRef, query.String("article-development-admin")),
			query.NewAssignment(auditModelRef, query.String("article.article")),
			query.NewAssignment(auditObjectIDRef, query.String("42")),
			query.NewAssignment(auditActionRef, query.String("add")),
			query.NewAssignment(auditChangedFieldsRef, query.String("v1.AAA")),
			query.NewAssignment(auditDisplayLabelRef, query.String(displayLabel)),
		},
		auditIDRef,
	)
}

func assertRuntimeTableMissing(t *testing.T, ctx context.Context, backend *sqlite.Backend, table string, fields []query.FieldRef) {
	t.Helper()
	rows, err := backend.Query(ctx, query.NewPlan(table, fields))
	if rows != nil {
		_ = rows.Close()
	}
	if !errors.Is(err, &query.Error{Code: query.CodeMissingTable}) {
		t.Fatalf("query missing table %q error = %#v", table, err)
	}
}

type observedRuntimeBackend struct {
	*sqlite.Backend
	atomicCalls atomic.Int64
	insertCalls atomic.Int64
	updateCalls atomic.Int64
	deleteCalls atomic.Int64
}

type coordinationDepthRuntimeBackend struct {
	*sqlite.Backend
	active  atomic.Int64
	maximum atomic.Int64
}

func (backend *coordinationDepthRuntimeBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	active := backend.active.Add(1)
	for {
		maximum := backend.maximum.Load()
		if active <= maximum || backend.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	defer backend.active.Add(-1)
	return backend.Backend.CoordinatedAtomic(ctx, callback)
}

type coordinationCheckingHasher struct {
	auth.PasswordHasher
	active           *atomic.Int64
	hashCalls        atomic.Int64
	verifyCalls      atomic.Int64
	validateCalls    atomic.Int64
	activeViolations atomic.Int64
}

func (hasher *coordinationCheckingHasher) Hash(ctx context.Context, password string) (string, error) {
	hasher.hashCalls.Add(1)
	hasher.observeActiveCoordination()
	return hasher.PasswordHasher.Hash(ctx, password)
}

func (hasher *coordinationCheckingHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	hasher.verifyCalls.Add(1)
	hasher.observeActiveCoordination()
	return hasher.PasswordHasher.Verify(ctx, password, encoded)
}

func (hasher *coordinationCheckingHasher) ValidateEncoded(encoded string) error {
	hasher.validateCalls.Add(1)
	hasher.observeActiveCoordination()
	return hasher.PasswordHasher.ValidateEncoded(encoded)
}

func (hasher *coordinationCheckingHasher) observeActiveCoordination() {
	if hasher.active != nil && hasher.active.Load() != 0 {
		hasher.activeViolations.Add(1)
	}
}

type credentialRemovedBeforeCoordinationBackend struct {
	*sqlite.Backend
	credentialID     int64
	removeOnce       sync.Once
	removeErr        error
	coordinatedCalls atomic.Int64
	callbackCalls    atomic.Int64
}

func (backend *credentialRemovedBeforeCoordinationBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.removeOnce.Do(func() {
		backend.removeErr = backend.Backend.Atomic(ctx, func(session db.Session) error {
			affected, err := session.Delete(ctx, query.NewDeletePlan(
				credentialTableName,
				credentialIDRef,
				query.Integer(backend.credentialID),
			))
			if err != nil {
				return err
			}
			if affected != 1 {
				return fmt.Errorf("test credential removal affected %d rows, want 1", affected)
			}
			return nil
		})
	})
	if backend.removeErr != nil {
		return backend.removeErr
	}
	backend.coordinatedCalls.Add(1)
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		backend.callbackCalls.Add(1)
		return callback(session)
	})
}

type provisionFaultRuntimeBackend struct {
	*sqlite.Backend
	callbackFailure error
	outcomeCode     string
	atomicCalls     atomic.Int64
	callbackCalls   atomic.Int64
}

func (backend *provisionFaultRuntimeBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.atomicCalls.Add(1)
	if backend.callbackFailure != nil {
		return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
			backend.callbackCalls.Add(1)
			if err := callback(session); err != nil {
				return err
			}
			return backend.callbackFailure
		})
	}
	if err := backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		backend.callbackCalls.Add(1)
		return callback(session)
	}); err != nil {
		return err
	}
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     backend.outcomeCode,
		Detail:   "injected provision transaction outcome unknown",
	}
}

func (backend *observedRuntimeBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	return backend.Backend.Atomic(ctx, func(session db.Session) error {
		return callback(&observedRuntimeSession{Session: session, backend: backend})
	})
}

func (backend *observedRuntimeBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.atomicCalls.Add(1)
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		return callback(&observedRuntimeSession{Session: session, backend: backend})
	})
}

func (backend *observedRuntimeBackend) resetObservation() {
	backend.atomicCalls.Store(0)
	backend.insertCalls.Store(0)
	backend.updateCalls.Store(0)
	backend.deleteCalls.Store(0)
}

type observedRuntimeSession struct {
	db.Session
	backend *observedRuntimeBackend
}

func (session *observedRuntimeSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.backend.insertCalls.Add(1)
	return session.Session.Insert(ctx, plan)
}

func (session *observedRuntimeSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.backend.updateCalls.Add(1)
	return session.Session.Update(ctx, plan)
}

func (session *observedRuntimeSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session.backend.deleteCalls.Add(1)
	return session.Session.Delete(ctx, plan)
}

type serialRuntimeBackend struct {
	atomicCalls atomic.Int64
	active      atomic.Int64
	maxActive   atomic.Int64
}

func (*serialRuntimeBackend) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unused query")
}

func (*serialRuntimeBackend) Insert(context.Context, query.InsertPlan) (int64, error) { return 0, nil }
func (*serialRuntimeBackend) Update(context.Context, query.UpdatePlan) (int64, error) { return 0, nil }
func (*serialRuntimeBackend) Delete(context.Context, query.DeletePlan) (int64, error) { return 0, nil }
func (*serialRuntimeBackend) ReadAppliedMigrations(context.Context) ([]migrationbackend.AppliedMigration, error) {
	return nil, nil
}

func (backend *serialRuntimeBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	return backend.runAtomic(ctx, callback)
}

func (backend *serialRuntimeBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	return backend.runAtomic(ctx, callback)
}

func (backend *serialRuntimeBackend) runAtomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	active := backend.active.Add(1)
	for {
		maximum := backend.maxActive.Load()
		if active <= maximum || backend.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	defer backend.active.Add(-1)
	time.Sleep(time.Millisecond)
	return callback(serialRuntimeSession{})
}

type serialRuntimeSession struct{}

func (serialRuntimeSession) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("unused query")
}
func (serialRuntimeSession) Insert(context.Context, query.InsertPlan) (int64, error) { return 1, nil }
func (serialRuntimeSession) Update(context.Context, query.UpdatePlan) (int64, error) { return 1, nil }
func (serialRuntimeSession) Delete(context.Context, query.DeletePlan) (int64, error) { return 1, nil }
