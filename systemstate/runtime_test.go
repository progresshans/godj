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

func TestRuntimeExplicitBootstrapRestartAndDatabaseInterfaces(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "runtime.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	firstDatabase := openSessionStoreBackend(t, ctx, dataSourceName)
	explicitlyMigrateSystemState(t, ctx, firstDatabase)
	firstBackend := &observedRuntimeBackend{Backend: firstDatabase}
	config := runtimeTestConfig(t, "bootstrap-password-secret-marker")

	firstRuntime, err := Open(ctx, firstBackend, config)
	if err != nil {
		_ = firstDatabase.Close()
		t.Fatalf("Open(first bootstrap): %v", err)
	}
	if firstBackend.atomicCalls.Load() != 1 || firstBackend.insertCalls.Load() != 1 {
		_ = firstDatabase.Close()
		t.Fatalf("bootstrap calls = atomic %d/insert %d, want 1/1", firstBackend.atomicCalls.Load(), firstBackend.insertCalls.Load())
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
	secondConfig := runtimeTestConfig(t, config.Password)
	secondRuntime, err := Open(ctx, secondBackend, secondConfig)
	if err != nil {
		t.Fatalf("Open(identical restart): %v", err)
	}
	if secondBackend.atomicCalls.Load() != 0 || secondBackend.insertCalls.Load() != 0 {
		t.Fatalf("identical restart calls = atomic %d/insert %d, want zero", secondBackend.atomicCalls.Load(), secondBackend.insertCalls.Load())
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

func TestRuntimeIdenticalRestartRejectsEveryCredentialMismatchWithoutWriting(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "mismatch.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	setup := openSessionStoreBackend(t, ctx, dataSourceName)
	explicitlyMigrateSystemState(t, ctx, setup)
	baseline := runtimeTestConfig(t, "correct-password-secret-marker")
	if _, err := Open(ctx, setup, baseline); err != nil {
		_ = setup.Close()
		t.Fatalf("Open(setup): %v", err)
	}
	if err := setup.Close(); err != nil {
		t.Fatalf("close mismatch setup: %v", err)
	}

	tests := []struct {
		name string
		edit func(*BootstrapConfig)
	}{
		{name: "username", edit: func(config *BootstrapConfig) { config.Username = "other-admin" }},
		{name: "password", edit: func(config *BootstrapConfig) { config.Password = "wrong-password-secret-marker" }},
		{name: "principal", edit: func(config *BootstrapConfig) { config.PrincipalID = "other-principal" }},
		{name: "active", edit: func(config *BootstrapConfig) { config.Active = false }},
		{name: "permission order", edit: func(config *BootstrapConfig) {
			config.Permissions[0], config.Permissions[1] = config.Permissions[1], config.Permissions[0]
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openSessionStoreBackend(t, ctx, dataSourceName)
			defer func() { _ = database.Close() }()
			observed := &observedRuntimeBackend{Backend: database}
			config := runtimeTestConfig(t, baseline.Password)
			test.edit(&config)
			runtime, err := Open(ctx, observed, config)
			if runtime != nil || !errors.Is(err, &Error{Code: CodeCredentialMismatch}) {
				t.Fatalf("Open(mismatch) = (%v,%#v)", runtime, err)
			}
			if observed.atomicCalls.Load() != 0 || observed.insertCalls.Load() != 0 {
				t.Fatalf("mismatch wrote state: atomic %d/insert %d", observed.atomicCalls.Load(), observed.insertCalls.Load())
			}
			for _, secret := range []string{baseline.Password, config.Password} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("mismatch error leaked secret: %v", err)
				}
			}
		})
	}

	finalDatabase := openSessionStoreBackend(t, ctx, dataSourceName)
	t.Cleanup(func() { _ = finalDatabase.Close() })
	finalObserved := &observedRuntimeBackend{Backend: finalDatabase}
	if _, err := Open(ctx, finalObserved, runtimeTestConfig(t, baseline.Password)); err != nil {
		t.Fatalf("Open(correct after mismatches): %v", err)
	}
	if finalObserved.atomicCalls.Load() != 0 {
		t.Fatalf("correct final restart atomic calls = %d, want zero", finalObserved.atomicCalls.Load())
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
		if _, err := Open(ctx, database, config); err != nil {
			_ = database.Close()
			t.Fatalf("Open(corrupt setup): %v", err)
		}
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
		runtime, err := Open(ctx, &observedRuntimeBackend{Backend: reopened}, runtimeTestConfig(t, config.Password))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState}) {
			t.Fatalf("Open(corrupt permission) = (%v,%#v)", runtime, err)
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
		if _, err := Open(ctx, database, config); err != nil {
			_ = database.Close()
			t.Fatalf("Open(duplicate setup): %v", err)
		}
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
		runtime, err := Open(ctx, observed, runtimeTestConfig(t, config.Password))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeCardinality, Field: "credential"}) {
			t.Fatalf("Open(duplicate credential) = (%v,%#v)", runtime, err)
		}
		if observed.atomicCalls.Load() != 0 {
			t.Fatalf("duplicate credential invoked %d transactions", observed.atomicCalls.Load())
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
		runtime, err := Open(ctx, observed, runtimeTestConfig(t, "orphan-password-secret-marker"))
		if runtime != nil || !errors.Is(err, &Error{Code: CodeCorruptState, Field: "credential"}) {
			t.Fatalf("Open(orphan state) = (%v,%#v)", runtime, err)
		}
		if observed.atomicCalls.Load() != 0 {
			t.Fatalf("orphan state invoked %d transactions", observed.atomicCalls.Load())
		}
	})
}

func TestRuntimeRequiresExactMigrationAndNeverCreatesOrRepairsSchema(t *testing.T) {
	ctx := context.Background()
	config := runtimeTestConfig(t, "schema-password-secret-marker")

	t.Run("unmigrated", func(t *testing.T) {
		backend := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "unmigrated.sqlite3"))+"?mode=rwc")
		defer func() { _ = backend.Close() }()
		runtime, err := Open(ctx, backend, config)
		if runtime != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatalf("Open(unmigrated) = (%v,%#v)", runtime, err)
		}
		history, historyErr := backend.ReadAppliedMigrations(ctx)
		if historyErr != nil || len(history) != 0 {
			t.Fatalf("unmigrated history after Open = (%+v,%v)", history, historyErr)
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
		runtime, err := Open(ctx, backend, config)
		if runtime != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: sessionTableName}) {
			t.Fatalf("Open(missing session table) = (%v,%#v)", runtime, err)
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
		runtime, err := Open(ctx, backend, config)
		if runtime != nil || !errors.Is(err, &Error{Code: CodeSchemaUnavailable, Field: "migration_history"}) {
			t.Fatalf("Open(unknown system migration) = (%v,%#v)", runtime, err)
		}
	})
}

func TestRuntimeBootstrapCommitUnknownIsNotRetriedOrMisclassifiedAsSchemaMissing(t *testing.T) {
	ctx := context.Background()
	database := openSessionStoreBackend(t, ctx, "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "bootstrap-unknown.sqlite3"))+"?mode=rwc")
	t.Cleanup(func() { _ = database.Close() })
	explicitlyMigrateSystemState(t, ctx, database)
	backend := &bootstrapUnknownRuntimeBackend{Backend: database}
	config := runtimeTestConfig(t, "bootstrap-unknown-password-secret-marker")

	runtime, err := Open(ctx, backend, config)
	if runtime != nil || err == nil {
		t.Fatalf("Open(commit unknown) = (%v,%v), want nil/error", runtime, err)
	}
	if !errors.Is(err, &Error{Code: CodePersistence, Field: "credential"}) ||
		errors.Is(err, &Error{Code: CodeSchemaUnavailable}) {
		t.Fatalf("Open(commit unknown) classification = %#v", err)
	}
	var outcome *query.Error
	if !errors.As(err, &outcome) || outcome.Code != query.CodeCommitOutcomeUnknown {
		t.Fatalf("Open(commit unknown) lost reconciliation marker: %#v", err)
	}
	if got := backend.atomicCalls.Load(); got != 1 {
		t.Fatalf("Atomic calls = %d, want exactly 1", got)
	}
	if got := backend.callbackCalls.Load(); got != 1 {
		t.Fatalf("Atomic callback calls = %d, want exactly 1", got)
	}
	rows, readErr := readCredentialRows(ctx, database)
	if readErr != nil || len(rows) != 1 {
		t.Fatalf("physical rows after commit unknown = (%d,%v), want one reconciliation row", len(rows), readErr)
	}
	if reopened, reopenErr := Open(ctx, database, config); reopenErr != nil || reopened == nil {
		t.Fatalf("Open(reconcile by restart) = (%v,%v)", reopened, reopenErr)
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

func runtimeTestConfig(t *testing.T, password string) BootstrapConfig {
	t.Helper()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatalf("auth.NewPBKDF2(): %v", err)
	}
	return BootstrapConfig{
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

type bootstrapUnknownRuntimeBackend struct {
	*sqlite.Backend
	atomicCalls   atomic.Int64
	callbackCalls atomic.Int64
}

func (backend *bootstrapUnknownRuntimeBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	if err := backend.Backend.Atomic(ctx, func(session db.Session) error {
		backend.callbackCalls.Add(1)
		return callback(session)
	}); err != nil {
		return err
	}
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "injected bootstrap commit outcome unknown",
	}
}

func (backend *observedRuntimeBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	return backend.Backend.Atomic(ctx, func(session db.Session) error {
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
