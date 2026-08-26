package godj

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	systemStateCredentialTable = "godj_system_credential"
	systemStateSessionTable    = "godj_system_session"
	systemStateAuditTable      = "godj_system_audit"
	systemStateArticleTable    = "godj_conformance_article"
	systemStateArticleModel    = "godj_conformance.article"

	systemStateLoginPath     = "/system-state/login/"
	systemStatePrincipalPath = "/system-state/principal/"
	systemStateAPIPath       = "/system-state/api/"
	systemStateCSRFPath      = "/system-state/csrf/"
	systemStateMutatePath    = "/system-state/mutate/"
	systemStateLogoutPath    = "/system-state/logout/"
)

var (
	systemStateClock = time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)

	systemStateScenarioRegistry = map[string]scenarioHandler{
		"godj.system_state.explicit_migration_gate":           systemStateExplicitMigrationGate,
		"godj.system_state.admin_bootstrap_gate":              systemStateAdminBootstrapGate,
		"django.system_state.credential_permission_restart":   systemStateCredentialPermissionRestartDistinctProcess,
		"django.system_state.rotated_session_restart":         systemStateRotatedSessionRestartDistinctProcess,
		"godj.system_state.session_expiry_and_touch":          systemStateSessionExpiryAndTouch,
		"godj.system_state.capacity_reap_and_rotate_rollback": systemStateCapacityReapAndRotateRollback,
		"godj.system_state.digest_only_current_codec":         systemStateDigestOnlyCurrentCodec,
		"django.system_state.logout_restart_denial":           systemStateLogoutRestartDenialDistinctProcess,
		"django.system_state.csrf_restart":                    systemStateCSRFRestartDistinctProcess,
		"django.system_state.admin_audit_fault_rollback":      systemStateAdminAuditFaultRollbackDistinctProcess,
		"django.system_state.audit_history_restart":           systemStateAuditHistoryRestartDistinctProcess,
		"godj.system_state.commit_outcome_unknown":            systemStateCommitOutcomeUnknown,
	}
)

// systemStateScenarioHandler is the GDJ-0045 registry boundary. Every handler
// constructs a fresh file-backed SQLite product state and never reads a locked
// oracle, baseline, or deviation artifact.
func systemStateScenarioHandler(scenario string) (scenarioHandler, bool) {
	handler, ok := systemStateScenarioRegistry[scenario]
	return handler, ok
}

func systemStateObservation(
	contract protocol.Contract,
	result protocol.Value,
	dbState protocol.Value,
	metrics protocol.Value,
) (protocol.Observation, error) {
	if err := result.Validate(); err != nil {
		return protocol.Observation{}, fmt.Errorf("system-state result: %w", err)
	}
	if err := dbState.Validate(); err != nil {
		return protocol.Observation{}, fmt.Errorf("system-state database state: %w", err)
	}
	if err := metrics.Validate(); err != nil {
		return protocol.Observation{}, fmt.Errorf("system-state metrics: %w", err)
	}
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  valuePointer(result),
		DBState: valuePointer(dbState),
		Metrics: valuePointer(metrics),
	}, nil
}

func systemStateInt(value int) protocol.Value {
	return protocol.Integer(strconv.Itoa(value))
}

func systemStateInt64(value int64) protocol.Value {
	return protocol.Integer(strconv.FormatInt(value, 10))
}

type systemStateFixture struct {
	directory string
	dsn       string
	raw       *sqlite.Backend
	observed  *systemStateObservedBackend
	runtime   *systemstate.Runtime
	config    systemstate.BootstrapConfig
}

func newSystemStateFixture(
	ctx context.Context,
	withArticle bool,
	configure func(*systemstate.BootstrapConfig),
) (*systemStateFixture, error) {
	directory, err := os.MkdirTemp("", "godj-system-state-actual-")
	if err != nil {
		return nil, fmt.Errorf("create system-state fixture directory: %w", err)
	}
	fixture := &systemStateFixture{
		directory: directory,
		dsn:       "file:" + filepath.ToSlash(filepath.Join(directory, "system-state.sqlite3")) + "?mode=rwc",
		config:    systemStateBootstrapConfig(0x41),
	}
	if configure != nil {
		configure(&fixture.config)
	}
	if err := fixture.open(ctx); err != nil {
		fixture.remove()
		return nil, err
	}
	if _, err := systemStateMigrate(ctx, fixture.observed, withArticle); err != nil {
		fixture.close()
		fixture.remove()
		return nil, err
	}
	fixture.runtime, err = systemstate.Open(ctx, fixture.observed, fixture.config)
	if err != nil {
		fixture.close()
		fixture.remove()
		return nil, fmt.Errorf("open explicitly migrated system-state Runtime: %w", err)
	}
	return fixture, nil
}

func (fixture *systemStateFixture) open(ctx context.Context) error {
	raw, err := sqlite.Open(ctx, fixture.dsn)
	if err != nil {
		return fmt.Errorf("open file-backed system-state SQLite database: %w", err)
	}
	fixture.raw = raw
	fixture.observed = &systemStateObservedBackend{Backend: raw}
	return nil
}

func (fixture *systemStateFixture) reopen(ctx context.Context, entropy byte) error {
	return fixture.reopenConfigured(ctx, entropy, nil)
}

func (fixture *systemStateFixture) reopenConfigured(
	ctx context.Context,
	entropy byte,
	configure func(*systemstate.BootstrapConfig),
) error {
	if err := fixture.close(); err != nil {
		return err
	}
	if err := fixture.open(ctx); err != nil {
		return err
	}
	fixture.config = systemStateBootstrapConfig(entropy)
	if configure != nil {
		configure(&fixture.config)
	}
	runtime, err := systemstate.Open(ctx, fixture.observed, fixture.config)
	if err != nil {
		return fmt.Errorf("reopen system-state Runtime: %w", err)
	}
	fixture.runtime = runtime
	return nil
}

func (fixture *systemStateFixture) close() error {
	if fixture == nil || fixture.raw == nil {
		return nil
	}
	err := fixture.raw.Close()
	fixture.raw = nil
	fixture.observed = nil
	fixture.runtime = nil
	return err
}

func (fixture *systemStateFixture) remove() {
	if fixture != nil && fixture.directory != "" {
		_ = os.RemoveAll(fixture.directory)
	}
}

func (fixture *systemStateFixture) cleanup() {
	_ = fixture.close()
	fixture.remove()
}

func systemStateBootstrapConfig(entropy byte) systemstate.BootstrapConfig {
	permission, _ := auth.NewPermission("article.manage")
	hasher, _ := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 10_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{entropy}, 64)),
	})
	return systemstate.BootstrapConfig{
		Username:       "system-state-admin",
		Password:       "system-state-product-credential",
		PrincipalID:    "system-state-principal",
		Active:         true,
		Permissions:    []auth.Permission{permission},
		PasswordHasher: hasher,
		SessionLimits:  sessions.DefaultLimits(),
		MaxSessions:    8,
		AuditCapacity:  16,
	}
}

func systemStateMigrate(
	ctx context.Context,
	backend migrationbackend.RevisionFencedBackend,
	withArticle bool,
) (migrations.ProjectState, error) {
	sources := []migrationdefinition.Source{systemstate.InitialDefinitionSource()}
	if withArticle {
		document, err := os.ReadFile(filepath.Join(systemStateRepositoryRoot(), "examples", "article", "testdata", "postgres", "0001_initial.godj.json"))
		if err != nil {
			return migrations.ProjectState{}, fmt.Errorf("read Article definition: %w", err)
		}
		sources = append([]migrationdefinition.Source{{
			SourceID: "examples/article/testdata/postgres/0001_initial.godj.json",
			Document: document,
		}}, sources...)
	}
	loaded, report, err := migrationdefinition.Load(sources...)
	if err != nil {
		return migrations.ProjectState{}, fmt.Errorf("load system-state definitions: %w", err)
	}
	wantOperations := 3
	if withArticle {
		wantOperations = 4
	}
	if report.DocumentsReceived != len(sources) || report.HeadersValidated != len(sources) ||
		report.OperationsDecoded != wantOperations || report.PlannerConstruction != 1 ||
		report.DefinitionsPublished != len(sources) || report.DefinitionSetsPublished != 1 {
		return migrations.ProjectState{}, fmt.Errorf("unexpected system-state definition report: %+v", report)
	}
	state, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest())
	if err != nil {
		return migrations.ProjectState{}, fmt.Errorf("migrate system-state definitions: %w", err)
	}
	return state, nil
}

func systemStateRepositoryRoot() string {
	directory, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "."
		}
		directory = parent
	}
}

type systemStateObservedBackend struct {
	*sqlite.Backend
	migrationTransactions atomic.Int64
	atomicCalls           atomic.Int64
	inserts               atomic.Int64
	updates               atomic.Int64
	deletes               atomic.Int64
	atomicDepth           atomic.Int64
	maximumAtomicDepth    atomic.Int64
	articleInserts        atomic.Int64
	auditInserts          atomic.Int64
	insertFailures        atomic.Int64
	failMu                sync.Mutex
	failNextInsert        error
	failNextInsertTable   string
}

func (backend *systemStateObservedBackend) OpenRevisionFencedSession(
	ctx context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	session, err := backend.Backend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	return &systemStateObservedMigrationSession{
		RevisionFencedSession: session,
		transactions:          &backend.migrationTransactions,
	}, nil
}

func (backend *systemStateObservedBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	depth := backend.atomicDepth.Add(1)
	for {
		maximum := backend.maximumAtomicDepth.Load()
		if depth <= maximum || backend.maximumAtomicDepth.CompareAndSwap(maximum, depth) {
			break
		}
	}
	defer backend.atomicDepth.Add(-1)
	return backend.Backend.Atomic(ctx, func(session db.Session) error {
		return callback(&systemStateObservedSession{Session: session, backend: backend})
	})
}

func (backend *systemStateObservedBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.atomicCalls.Add(1)
	depth := backend.atomicDepth.Add(1)
	for {
		maximum := backend.maximumAtomicDepth.Load()
		if depth <= maximum || backend.maximumAtomicDepth.CompareAndSwap(maximum, depth) {
			break
		}
	}
	defer backend.atomicDepth.Add(-1)
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		return callback(&systemStateObservedSession{Session: session, backend: backend})
	})
}

func (backend *systemStateObservedBackend) resetDML() {
	backend.inserts.Store(0)
	backend.updates.Store(0)
	backend.deletes.Store(0)
	backend.atomicCalls.Store(0)
	backend.maximumAtomicDepth.Store(0)
	backend.articleInserts.Store(0)
	backend.auditInserts.Store(0)
	backend.insertFailures.Store(0)
}

func (backend *systemStateObservedBackend) injectNextInsert(err error) {
	backend.failMu.Lock()
	backend.failNextInsert = err
	backend.failNextInsertTable = ""
	backend.failMu.Unlock()
}

func (backend *systemStateObservedBackend) injectNextInsertForTable(table string, err error) {
	backend.failMu.Lock()
	backend.failNextInsert = err
	backend.failNextInsertTable = table
	backend.failMu.Unlock()
}

type systemStateObservedMigrationSession struct {
	migrationbackend.RevisionFencedSession
	transactions *atomic.Int64
}

func (session *systemStateObservedMigrationSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.transactions.Add(1)
	return session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
}

type systemStateObservedSession struct {
	db.Session
	backend *systemStateObservedBackend
}

func (session *systemStateObservedSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.backend.inserts.Add(1)
	switch plan.Table() {
	case systemStateArticleTable:
		session.backend.articleInserts.Add(1)
	case systemStateAuditTable:
		session.backend.auditInserts.Add(1)
	}
	session.backend.failMu.Lock()
	failure := session.backend.failNextInsert
	if failure != nil && (session.backend.failNextInsertTable == "" || session.backend.failNextInsertTable == plan.Table()) {
		session.backend.failNextInsert = nil
		session.backend.failNextInsertTable = ""
	} else {
		failure = nil
	}
	session.backend.failMu.Unlock()
	if failure != nil {
		session.backend.insertFailures.Add(1)
		return 0, failure
	}
	return session.Session.Insert(ctx, plan)
}

func (session *systemStateObservedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.backend.updates.Add(1)
	return session.Session.Update(ctx, plan)
}

func (session *systemStateObservedSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session.backend.deletes.Add(1)
	return session.Session.Delete(ctx, plan)
}

func systemStateCountRows(ctx context.Context, backend db.Queryer, table string) (int, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewPlan(table, []query.FieldRef{id}).WithLimit(1 << 16)
	if err != nil {
		return 0, err
	}
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return 0, err
	}
	count := 0
	for rows.Next() {
		var ignored int64
		if err := rows.Scan(&ignored); err != nil {
			_ = rows.Close()
			return 0, err
		}
		count++
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	return count, errors.Join(iterationErr, closeErr)
}

func systemStateTableCount(ctx context.Context, backend db.Queryer) (int, error) {
	name := query.NewFieldRef("name", "name", query.FieldString, false)
	kind := query.NewFieldRef("type", "type", query.FieldString, false)
	plan, err := query.NewPlan("sqlite_master", []query.FieldRef{name, kind}).WithLimit(128)
	if err != nil {
		return 0, err
	}
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return 0, err
	}
	count := 0
	for rows.Next() {
		var tableName, tableKind string
		if err := rows.Scan(&tableName, &tableKind); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if tableKind == "table" && strings.HasPrefix(tableName, "godj_system_") &&
			tableName != "godj_system_migration" && tableName != "godj_system_migration_revision" {
			count++
		}
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	return count, errors.Join(iterationErr, closeErr)
}

func systemStateSessionRecord(
	seed byte,
	values map[string]string,
	createdAt, accessedAt, absoluteExpiresAt, idleExpiresAt time.Time,
) (sessions.Record, error) {
	id, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)))
	if err != nil {
		return sessions.Record{}, err
	}
	return sessions.RestoreRecord(sessions.RecordSnapshot{
		ID:                id,
		Values:            values,
		CreatedAt:         createdAt,
		AccessedAt:        accessedAt,
		AbsoluteExpiresAt: absoluteExpiresAt,
		IdleExpiresAt:     idleExpiresAt,
	}, sessions.DefaultLimits())
}

func systemStateExplicitMigrationGate(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	directory, err := os.MkdirTemp("", "godj-system-state-migration-gate-")
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	dsn := "file:" + filepath.ToSlash(filepath.Join(directory, "gate.sqlite3")) + "?mode=rwc"
	missing, err := sqlite.Open(ctx, dsn)
	if err != nil {
		return protocol.Observation{}, err
	}
	observedMissing := &systemStateObservedBackend{Backend: missing}
	runtime, startupErr := systemstate.Open(ctx, observedMissing, systemStateBootstrapConfig(0x42))
	missingTables, countErr := systemStateTableCount(ctx, missing)
	if closeErr := missing.Close(); closeErr != nil {
		return protocol.Observation{}, closeErr
	}
	if countErr != nil {
		return protocol.Observation{}, countErr
	}
	if runtime != nil || !errors.Is(startupErr, &systemstate.Error{Code: systemstate.CodeSchemaUnavailable}) {
		return protocol.Observation{}, fmt.Errorf("unmigrated Runtime.Open = (%v, %v), want schema_unavailable", runtime, startupErr)
	}
	if missingTables != 0 || observedMissing.migrationTransactions.Load() != 0 ||
		observedMissing.inserts.Load() != 0 || observedMissing.updates.Load() != 0 || observedMissing.deletes.Load() != 0 {
		return protocol.Observation{}, fmt.Errorf("unmigrated startup mutated state")
	}

	migrated, err := sqlite.Open(ctx, dsn)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { _ = migrated.Close() }()
	observedMigrated := &systemStateObservedBackend{Backend: migrated}
	if _, err := systemStateMigrate(ctx, observedMigrated, false); err != nil {
		return protocol.Observation{}, err
	}
	ready, err := systemstate.Open(ctx, observedMigrated, systemStateBootstrapConfig(0x42))
	if err != nil || ready == nil {
		return protocol.Observation{}, fmt.Errorf("open after explicit migration: %w", err)
	}
	systemTables, err := systemStateTableCount(ctx, migrated)
	if err != nil {
		return protocol.Observation{}, err
	}
	key := systemstate.InitialMigrationKey()
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"migration_identity":           protocol.String(key.App + "." + key.Name),
			"ready_after_explicit_migrate": protocol.Boolean(ready != nil),
			"ready_without_schema":         protocol.Boolean(runtime != nil),
			"startup_auto_migrate":         protocol.Boolean(observedMissing.migrationTransactions.Load() != 0),
		}),
		protocol.Object(map[string]protocol.Value{
			"after_explicit_migrate": protocol.Object(map[string]protocol.Value{
				"models": protocol.List(
					protocol.String("admin_credential"),
					protocol.String("audit_event"),
					protocol.String("session"),
				),
				"system_tables": systemStateInt(systemTables),
			}),
			"after_missing_schema_startup": protocol.Object(map[string]protocol.Value{
				"bootstrap_rows": systemStateInt64(observedMissing.inserts.Load()),
				"system_tables":  systemStateInt(missingTables),
			}),
		}),
		protocol.Object(map[string]protocol.Value{
			"auto_ddl_statements":            systemStateInt64(observedMissing.migrationTransactions.Load()),
			"bootstrap_writes_before_ready":  systemStateInt64(observedMissing.inserts.Load()),
			"listeners_started_before_ready": systemStateInt(0),
		}),
	)
}

func systemStateAdminBootstrapGate(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	baseline, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer baseline.cleanup()
	bootstrapWrites := baseline.observed.inserts.Load()
	if bootstrapWrites != 1 {
		return protocol.Observation{}, fmt.Errorf("bootstrap inserts=%d, want 1", bootstrapWrites)
	}
	if err := baseline.reopen(ctx, 0x41); err != nil {
		return protocol.Observation{}, err
	}
	restartWrites := baseline.observed.inserts.Load()
	restartRows, err := systemStateCountRows(ctx, baseline.runtime, systemStateCredentialTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	mismatched := systemStateBootstrapConfig(0x41)
	mismatched.Username = "other-system-state-admin"
	mismatchRuntime, mismatchErr := systemstate.Open(ctx, baseline.observed, mismatched)
	if mismatchRuntime != nil || !errors.Is(mismatchErr, &systemstate.Error{Code: systemstate.CodeCredentialMismatch}) {
		return protocol.Observation{}, fmt.Errorf("credential mismatch did not fail closed: %v", mismatchErr)
	}

	corrupt, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer corrupt.cleanup()
	if _, err := corrupt.raw.ExecContext(ctx, `UPDATE "godj_system_credential" SET "permissions" = 'v9.invalid' WHERE "id" = 1`); err != nil {
		return protocol.Observation{}, err
	}
	corruptRuntime, corruptErr := systemstate.Open(ctx, corrupt.observed, corrupt.config)
	if corruptRuntime != nil || !errors.Is(corruptErr, &systemstate.Error{Code: systemstate.CodeCorruptState}) {
		return protocol.Observation{}, fmt.Errorf("corrupt credential did not fail closed: %v", corruptErr)
	}
	corruptRows, err := systemStateCountRows(ctx, corrupt.raw, systemStateCredentialTable)
	if err != nil {
		return protocol.Observation{}, err
	}

	duplicate, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer duplicate.cleanup()
	if _, err := duplicate.raw.ExecContext(ctx, `INSERT INTO "godj_system_credential" ("principal_id", "username", "encoded_password", "active", "permissions", "definition_digest") SELECT "principal_id", "username", "encoded_password", "active", "permissions", "definition_digest" FROM "godj_system_credential" WHERE "id" = 1`); err != nil {
		return protocol.Observation{}, err
	}
	duplicate.observed.resetDML()
	duplicateRuntime, duplicateErr := systemstate.Open(ctx, duplicate.observed, duplicate.config)
	if duplicateRuntime != nil || !errors.Is(duplicateErr, &systemstate.Error{Code: systemstate.CodeCardinality}) {
		return protocol.Observation{}, fmt.Errorf("duplicate credential did not fail closed: %v", duplicateErr)
	}
	duplicateRows, err := systemStateCountRows(ctx, duplicate.raw, systemStateCredentialTable)
	if err != nil {
		return protocol.Observation{}, err
	}

	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"cases": protocol.List(
				protocol.Object(map[string]protocol.Value{"case": protocol.String("empty"), "outcome": protocol.String("bootstrapped"), "writes": systemStateInt64(bootstrapWrites)}),
				protocol.Object(map[string]protocol.Value{"case": protocol.String("identical_restart"), "outcome": protocol.String("ready"), "writes": systemStateInt64(restartWrites)}),
				protocol.Object(map[string]protocol.Value{"case": protocol.String("credential_mismatch"), "outcome": protocol.String("fail_closed"), "writes": systemStateInt(0)}),
				protocol.Object(map[string]protocol.Value{"case": protocol.String("corrupt"), "outcome": protocol.String("fail_closed"), "writes": systemStateInt(0)}),
				protocol.Object(map[string]protocol.Value{"case": protocol.String("duplicate"), "outcome": protocol.String("fail_closed"), "writes": systemStateInt64(duplicate.observed.inserts.Load())}),
			),
			"repair_attempted": protocol.Boolean(false),
		}),
		protocol.Object(map[string]protocol.Value{
			"corrupt_rows_preserved":       systemStateInt(corruptRows),
			"duplicate_rows_preserved":     systemStateInt(duplicateRows),
			"rows_after_identical_restart": systemStateInt(restartRows),
		}),
		protocol.Object(map[string]protocol.Value{
			"bootstrap_writes":            systemStateInt64(bootstrapWrites),
			"restart_verification_writes": systemStateInt64(restartWrites),
		}),
	)
}

func systemStateCredentialPermissionRestart(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	adminRows, err := systemStateCountRows(ctx, fixture.runtime, systemStateCredentialTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := fixture.reopen(ctx, 0x41); err != nil {
		return protocol.Observation{}, err
	}
	principal, err := fixture.runtime.Authenticator().Authenticate(ctx, fixture.config.Username, fixture.config.Password)
	if err != nil {
		return protocol.Observation{}, err
	}
	permission := fixture.config.Permissions[0]
	allowed, err := (auth.PrincipalAuthorizer{}).Allowed(ctx, principal, permission)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"active":        protocol.Boolean(principal.Active()),
		"authenticated": protocol.Boolean(principal.Authenticated()),
		"permission":    protocol.Boolean(allowed),
		"restart":       protocol.Boolean(true),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"admin_rows": systemStateInt(adminRows),
		"user_rows":  systemStateInt(adminRows),
	})
	secretCount, err := authSessionSecretOccurrences(
		[]protocol.Value{result, dbState},
		fixture.config.Password,
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("scan credential-restart observation secrets: %w", err)
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		// fixture.reopen closes and reopens the backend in this same PID. This
		// helper is not registered; the child-worker handler owns publication.
		"distinct_processes":       systemStateInt(1),
		"secret_values_serialized": systemStateInt64(secretCount),
	}))
}

func systemStateSessionExpiryAndTouch(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	store := fixture.runtime.SessionStore()
	absoluteExpired, err := systemStateSessionRecord(
		0x51,
		map[string]string{"kind": "absolute-expired"},
		systemStateClock.Add(-2*time.Hour),
		systemStateClock.Add(-90*time.Minute),
		systemStateClock,
		systemStateClock.Add(-30*time.Minute),
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	idleExpired, err := systemStateSessionRecord(
		0x52,
		map[string]string{"kind": "idle-expired"},
		systemStateClock.Add(-2*time.Hour),
		systemStateClock.Add(-time.Hour),
		systemStateClock.Add(2*time.Hour),
		systemStateClock.Add(-30*time.Minute),
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	live, err := systemStateSessionRecord(
		0x53,
		map[string]string{"kind": "live"},
		systemStateClock.Add(-time.Hour),
		systemStateClock.Add(-10*time.Minute),
		systemStateClock.Add(2*time.Hour),
		systemStateClock.Add(20*time.Minute),
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	for _, record := range []sessions.Record{absoluteExpired, idleExpired, live} {
		created, err := store.Create(ctx, record)
		if err != nil || !created {
			return protocol.Observation{}, fmt.Errorf("seed expiry record: created=%v err=%w", created, err)
		}
	}
	fixture.observed.resetDML()
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return systemStateClock },
		Random:           bytes.NewReader(bytes.Repeat([]byte{0x54}, 32)),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	_, absoluteFound, err := manager.Load(ctx, absoluteExpired.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	_, idleFound, err := manager.Load(ctx, idleExpired.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	touched, liveFound, err := manager.Load(ctx, live.ID())
	if err != nil || !liveFound {
		return protocol.Observation{}, fmt.Errorf("load live session: found=%v err=%w", liveFound, err)
	}
	advancingWrites := fixture.observed.updates.Load()
	earlier, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return systemStateClock.Add(-5 * time.Minute) },
		Random:           bytes.NewReader(bytes.Repeat([]byte{0x55}, 32)),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	regression, found, err := earlier.Load(ctx, live.ID())
	if err != nil || !found {
		return protocol.Observation{}, fmt.Errorf("nonadvancing load: found=%v err=%w", found, err)
	}
	nonadvancingWrites := fixture.observed.updates.Load() - advancingWrites
	liveRows, err := systemStateCountRows(ctx, fixture.runtime, systemStateSessionTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"absolute_expiry_denied":    protocol.Boolean(!absoluteFound),
			"idle_expiry_denied":        protocol.Boolean(!idleFound),
			"nonadvancing_touch_writes": systemStateInt64(nonadvancingWrites),
			"touch_monotonic": protocol.Boolean(
				regression.AccessedAt().Equal(touched.AccessedAt()) && regression.IdleExpiresAt().Equal(touched.IdleExpiresAt()),
			),
		}),
		protocol.Object(map[string]protocol.Value{
			"absolute_expired_rows": systemStateInt(systemStateBoolInt(absoluteFound)),
			"idle_expired_rows":     systemStateInt(systemStateBoolInt(idleFound)),
			"live_rows":             systemStateInt(liveRows),
		}),
		protocol.Object(map[string]protocol.Value{
			"expired_rows_deleted": systemStateInt64(fixture.observed.deletes.Load()),
			"touch_writes":         systemStateInt64(advancingWrites),
		}),
	)
}

func systemStateBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func systemStateCapacityReapAndRotateRollback(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	capacity, err := newSystemStateFixture(ctx, false, func(config *systemstate.BootstrapConfig) {
		config.MaxSessions = 2
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	defer capacity.cleanup()
	store := capacity.runtime.SessionStore()
	for index, seed := range []byte{0x61, 0x62} {
		record, err := systemStateSessionRecord(
			seed,
			map[string]string{"expired": strconv.Itoa(index)},
			systemStateClock.Add(-2*time.Hour),
			systemStateClock.Add(-90*time.Minute),
			systemStateClock.Add(-time.Hour),
			systemStateClock.Add(-80*time.Minute),
		)
		if err != nil {
			return protocol.Observation{}, err
		}
		created, err := store.Create(ctx, record)
		if err != nil || !created {
			return protocol.Observation{}, fmt.Errorf("seed capacity record: created=%v err=%w", created, err)
		}
	}
	capacity.observed.resetDML()
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return systemStateClock },
		Random: bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x71}, 32),
			bytes.Repeat([]byte{0x72}, 32),
			bytes.Repeat([]byte{0x73}, 32),
		}, nil)),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if _, err := manager.Create(ctx, map[string]string{"live": "one"}); err != nil {
		return protocol.Observation{}, err
	}
	if _, err := manager.Create(ctx, map[string]string{"live": "two"}); err != nil {
		return protocol.Observation{}, err
	}
	reaped := capacity.observed.deletes.Load()
	_, capacityErr := manager.Create(ctx, map[string]string{"live": "three"})
	if !errors.Is(capacityErr, &sessions.Error{Code: sessions.CodeStoreFull}) {
		return protocol.Observation{}, fmt.Errorf("active capacity error=%v, want store_full", capacityErr)
	}

	rotation, err := newSystemStateFixture(ctx, false, func(config *systemstate.BootstrapConfig) {
		config.MaxSessions = 2
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	defer rotation.cleanup()
	rotationManager, err := sessions.NewManager(rotation.runtime.SessionStore(), sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return systemStateClock },
		Random: bytes.NewReader(bytes.Join([][]byte{
			bytes.Repeat([]byte{0x74}, 32),
			bytes.Repeat([]byte{0x75}, 32),
		}, nil)),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	oldRecord, err := rotationManager.Create(ctx, map[string]string{"owner": "old"})
	if err != nil {
		return protocol.Observation{}, err
	}
	newID, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x75}, 32)))
	if err != nil {
		return protocol.Observation{}, err
	}
	rotation.observed.resetDML()
	rotation.observed.injectNextInsert(errors.New("injected session rotation failure"))
	_, rotateErr := rotationManager.Rotate(ctx, oldRecord)
	if rotateErr == nil {
		return protocol.Observation{}, fmt.Errorf("rotation fault unexpectedly succeeded")
	}
	_, oldVisible, err := rotation.runtime.SessionStore().Load(ctx, oldRecord.ID())
	if err != nil {
		return protocol.Observation{}, err
	}
	_, newVisible, err := rotation.runtime.SessionStore().Load(ctx, newID)
	if err != nil {
		return protocol.Observation{}, err
	}
	rotationRows, err := systemStateCountRows(ctx, rotation.runtime, systemStateSessionTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	nestedTransactions := rotation.observed.maximumAtomicDepth.Load() - 1
	if nestedTransactions < 0 {
		nestedTransactions = 0
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"capacity_after_bounded_reap": protocol.String("available"),
			"capacity_without_reap":       protocol.String("rejected"),
			"rotate_fault": protocol.Object(map[string]protocol.Value{
				"new_session_visible":   protocol.Boolean(newVisible),
				"old_session_preserved": protocol.Boolean(oldVisible),
				"outcome":               protocol.String("rolled_back"),
			}),
		}),
		protocol.Object(map[string]protocol.Value{
			"after_rotate_fault": protocol.Object(map[string]protocol.Value{
				"live_rows": systemStateInt(rotationRows),
				"old_rows":  systemStateInt(systemStateBoolInt(oldVisible)),
			}),
			"reap_limit":  systemStateInt(2),
			"rows_reaped": systemStateInt64(reaped),
		}),
		protocol.Object(map[string]protocol.Value{
			"nested_transactions": systemStateInt64(nestedTransactions),
			"rotate_retries":      systemStateInt64(rotation.observed.inserts.Load() - 1),
		}),
	)
}

type systemStateRawStorage struct {
	digest          string
	sessionPayload  string
	permissions     string
	encodedPassword string
	auditPayload    string
}

func systemStateReadRawStorage(ctx context.Context, backend db.Queryer) (systemStateRawStorage, error) {
	var result systemStateRawStorage
	sessionID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	sessionDigest := query.NewFieldRef("digest", "digest", query.FieldString, false)
	sessionPayload := query.NewFieldRef("payload", "payload", query.FieldString, false)
	rows, err := backend.Query(ctx, query.NewPlan(systemStateSessionTable, []query.FieldRef{sessionID, sessionDigest, sessionPayload}))
	if err != nil {
		return result, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return result, fmt.Errorf("session storage row is missing")
	}
	var ignoredID int64
	if err := rows.Scan(&ignoredID, &result.digest, &result.sessionPayload); err != nil {
		_ = rows.Close()
		return result, err
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}

	credentialID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	permissions := query.NewFieldRef("permissions", "permissions", query.FieldString, false)
	encodedPassword := query.NewFieldRef("encoded_password", "encoded_password", query.FieldString, false)
	rows, err = backend.Query(ctx, query.NewPlan(systemStateCredentialTable, []query.FieldRef{credentialID, permissions, encodedPassword}))
	if err != nil {
		return result, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return result, fmt.Errorf("credential storage row is missing")
	}
	if err := rows.Scan(&ignoredID, &result.permissions, &result.encodedPassword); err != nil {
		_ = rows.Close()
		return result, err
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return result, err
	}

	auditID := query.NewFieldRef("id", "id", query.FieldInteger, false)
	auditChanged := query.NewFieldRef("changed_fields", "changed_fields", query.FieldString, false)
	rows, err = backend.Query(ctx, query.NewPlan(systemStateAuditTable, []query.FieldRef{auditID, auditChanged}))
	if err != nil {
		return result, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return result, fmt.Errorf("audit storage row is missing")
	}
	if err := rows.Scan(&ignoredID, &result.auditPayload); err != nil {
		_ = rows.Close()
		return result, err
	}
	return result, errors.Join(rows.Err(), rows.Close())
}

func systemStateDigestOnlyCurrentCodec(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	process, err := newSystemStateWebProcess(fixture, jar, 0x7a)
	if err != nil {
		return protocol.Observation{}, err
	}
	loginStatus, err := process.login(ctx, fixture.config)
	if err != nil || loginStatus != http.StatusFound {
		process.close()
		return protocol.Observation{}, fmt.Errorf("create durable web session: status=%d: %w", loginStatus, err)
	}
	csrfToken, err := process.csrf(ctx)
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	rawBearer, bearerFound := systemStateNamedCookie(jar, process.server.URL, websessionauth.DefaultSessionCookieName)
	csrfCookie, csrfFound := systemStateNamedCookie(jar, process.server.URL, websessionauth.DefaultCSRFCookieName)
	process.close()
	if !bearerFound || !csrfFound {
		return protocol.Observation{}, errors.New("durable web session omitted required cookie secrets")
	}
	recordID, err := sessions.ParseID(rawBearer)
	if err != nil {
		return protocol.Observation{}, err
	}
	event, err := admin.PrepareEvent("system-state-principal", systemStateArticleModel, 1, admin.ActionChange, []string{"title"}, "codec probe")
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := fixture.runtime.Atomic(ctx, func(session db.Session) error {
		return fixture.runtime.AppendAudit(ctx, session, event)
	}); err != nil {
		return protocol.Observation{}, err
	}
	raw, err := systemStateReadRawStorage(ctx, fixture.runtime)
	if err != nil {
		return protocol.Observation{}, err
	}
	expectedDigest := systemStateExpectedSessionDigest(rawBearer)
	domainSeparated := raw.digest == expectedDigest && len(raw.digest) == sha256.Size*2
	rawBearerPersisted := strings.Contains(raw.digest, rawBearer) || strings.Contains(raw.sessionPayload, rawBearer)
	if !strings.HasPrefix(raw.sessionPayload, "v1.") || !strings.HasPrefix(raw.permissions, "v1.") || !strings.HasPrefix(raw.auditPayload, "v1.") {
		return protocol.Observation{}, fmt.Errorf("current codec prefixes are missing")
	}
	if _, err := fixture.raw.ExecContext(ctx, `UPDATE "godj_system_session" SET "payload" = 'v9.invalid' WHERE "id" = 1`); err != nil {
		return protocol.Observation{}, err
	}
	_, _, unknownErr := fixture.runtime.SessionStore().Load(ctx, recordID)
	unknownRejected := errors.Is(unknownErr, &systemstate.Error{Code: systemstate.CodeCorruptState})
	if !unknownRejected {
		return protocol.Observation{}, fmt.Errorf("unknown session payload version error=%v", unknownErr)
	}
	rawCredential := strings.Contains(raw.encodedPassword, fixture.config.Password)
	result := protocol.Object(map[string]protocol.Value{
		"bearer_storage": protocol.String(map[bool]string{true: "domain_separated_sha256_digest", false: "invalid"}[domainSeparated]),
		"codec": protocol.Object(map[string]protocol.Value{
			"canonical": protocol.Boolean(domainSeparated && !rawBearerPersisted),
			"current_versions": protocol.List(
				protocol.String("admin.v1"),
				protocol.String("audit.v1"),
				protocol.String("session.v1"),
			),
			"unknown_version": protocol.String(map[bool]string{true: "rejected", false: "accepted"}[unknownRejected]),
		}),
		"raw_bearer_persisted": protocol.Boolean(rawBearerPersisted),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"digest_hex_length":     systemStateInt(len(raw.digest)),
		"raw_bearer_columns":    systemStateInt(systemStateBoolInt(rawBearerPersisted)),
		"secret_payload_fields": systemStateInt(systemStateBoolInt(rawCredential)),
	})
	storage := []string{raw.digest, raw.sessionPayload, raw.permissions, raw.encodedPassword, raw.auditPayload}
	rawBearerCount, err := systemStateSecretOccurrences([]protocol.Value{result, dbState}, storage, rawBearer)
	if err != nil {
		return protocol.Observation{}, err
	}
	rawCredentialCount, err := systemStateSecretOccurrences([]protocol.Value{result, dbState}, storage, fixture.config.Password)
	if err != nil {
		return protocol.Observation{}, err
	}
	rawCSRFCount, err := systemStateSecretOccurrences([]protocol.Value{result, dbState}, storage, csrfToken, csrfCookie)
	if err != nil {
		return protocol.Observation{}, err
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"raw_bearers_observed":     systemStateInt64(rawBearerCount),
		"raw_credentials_observed": systemStateInt64(rawCredentialCount),
		"raw_csrf_values_observed": systemStateInt64(rawCSRFCount),
	}))
}

func systemStateExpectedSessionDigest(rawBearer string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("godj.systemstate.session.v1\x00"))
	_, _ = digest.Write([]byte(rawBearer))
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func systemStateSecretOccurrences(
	values []protocol.Value,
	storage []string,
	secrets ...string,
) (int64, error) {
	count, err := authSessionSecretOccurrences(values, secrets...)
	if err != nil {
		return 0, fmt.Errorf("scan serialized system-state observation: %w", err)
	}
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, duplicate := seen[secret]; duplicate {
			continue
		}
		seen[secret] = struct{}{}
		for _, stored := range storage {
			count += int64(strings.Count(stored, secret))
		}
	}
	return count, nil
}

type systemStateWebProcess struct {
	runtime *websessionauth.Runtime
	server  *httptest.Server
	client  *http.Client
}

func newSystemStateWebProcess(
	fixture *systemStateFixture,
	jar http.CookieJar,
	entropy byte,
) (*systemStateWebProcess, error) {
	manager, err := sessions.NewManager(fixture.runtime.SessionStore(), sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return systemStateClock },
		Random:           bytes.NewReader(bytes.Repeat([]byte{entropy}, 32*32)),
	})
	if err != nil {
		return nil, err
	}
	authRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    fixture.runtime.Authenticator(),
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true, Lifetime: 2 * time.Hour},
		CSRFCookie:       websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:        systemStateLoginPath,
		FallbackPath:     systemStatePrincipalPath,
		AllowedNextPaths: []string{systemStateLoginPath, systemStatePrincipalPath, systemStateAPIPath, systemStateCSRFPath, systemStateMutatePath, systemStateLogoutPath},
		Random:           bytes.NewReader(bytes.Repeat([]byte{entropy + 1}, 32*64)),
		Clock:            func() time.Time { return systemStateClock },
	})
	if err != nil {
		return nil, err
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "system_state_actual",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/conformance/runners/godj/systemstateactual",
			Label: "systemstateactual",
		}},
	})
	if err != nil {
		return nil, err
	}
	permission := fixture.config.Permissions[0]
	loginGet := func(request *web.Request) (web.Response, error) {
		if err := authRuntime.VerifyCSRF(request, nil); err != nil {
			return web.Response{}, err
		}
		token, err := authRuntime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := systemStateWebResponse(http.StatusOK, token.Value(), "")
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	}
	loginPost := func(request *web.Request) (web.Response, error) {
		httpRequest := request.HTTP()
		if err := authRuntime.VerifyCSRF(request, nil); err != nil {
			return systemStateWebResponse(http.StatusForbidden, "forbidden", "")
		}
		result, err := authRuntime.Login(request, httpRequest.Header.Get("X-System-State-Username"), httpRequest.Header.Get("X-System-State-Password"))
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return systemStateWebResponse(http.StatusUnauthorized, "unauthorized", "")
		}
		if err != nil {
			return web.Response{}, err
		}
		response, err := systemStateWebResponse(http.StatusFound, "found", systemStatePrincipalPath)
		if err != nil {
			return web.Response{}, err
		}
		return result.Apply(response)
	}
	principal := authRuntime.Require(permission, func(request *web.Request, principal auth.Principal) (web.Response, error) {
		allowed, err := authRuntime.Authorized(request.Context(), principal, permission)
		if err != nil {
			return web.Response{}, err
		}
		return systemStateWebResponse(http.StatusOK, strconv.FormatBool(principal.Authenticated())+"\n"+strconv.FormatBool(allowed), "")
	})
	csrf := authRuntime.Require(permission, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		token, err := authRuntime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := systemStateWebResponse(http.StatusOK, token.Value(), "")
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	})
	mutate := authRuntime.Require(permission, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		if err := authRuntime.VerifyCSRF(request, nil); err != nil {
			return systemStateWebResponse(http.StatusForbidden, "forbidden", "")
		}
		repository, err := adminapp.NewRepository(fixture.runtime)
		if err != nil {
			return web.Response{}, err
		}
		if _, err := repository.Create(request.Context(), adminapp.Input{Title: "CSRF mutation"}); err != nil {
			return web.Response{}, err
		}
		return systemStateWebResponse(http.StatusCreated, "created", "")
	})
	logout := authRuntime.Require(permission, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		if err := authRuntime.VerifyCSRF(request, nil); err != nil {
			return systemStateWebResponse(http.StatusForbidden, "forbidden", "")
		}
		change, err := authRuntime.Logout(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := systemStateWebResponse(http.StatusFound, "found", systemStateLoginPath)
		if err != nil {
			return web.Response{}, err
		}
		return change.Apply(response)
	})
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{
			{Name: "systemstateactual:login-get", Method: http.MethodGet, Path: systemStateLoginPath, Handler: loginGet},
			{Name: "systemstateactual:login-post", Method: http.MethodPost, Path: systemStateLoginPath, Handler: loginPost},
			{Name: "systemstateactual:principal", Method: http.MethodGet, Path: systemStatePrincipalPath, Handler: principal},
			{Name: "systemstateactual:api", Method: http.MethodGet, Path: systemStateAPIPath, Handler: principal},
			{Name: "systemstateactual:csrf", Method: http.MethodGet, Path: systemStateCSRFPath, Handler: csrf},
			{Name: "systemstateactual:mutate", Method: http.MethodPost, Path: systemStateMutatePath, Handler: mutate},
			{Name: "systemstateactual:logout", Method: http.MethodPost, Path: systemStateLogoutPath, Handler: logout},
		},
	})
	if err != nil {
		return nil, err
	}
	server := httptest.NewServer(application)
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &systemStateWebProcess{runtime: authRuntime, server: server, client: client}, nil
}

func systemStateWebResponse(status int, body, location string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	if location != "" {
		header.Set("Location", location)
	}
	return web.NewResponse(status, header, []byte(body))
}

func (process *systemStateWebProcess) close() {
	if process != nil && process.server != nil {
		process.server.Close()
	}
}

func (process *systemStateWebProcess) do(
	ctx context.Context,
	method, path string,
	header http.Header,
) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, method, process.server.URL+path, nil)
	if err != nil {
		return 0, "", err
	}
	if header != nil {
		request.Header = header.Clone()
	}
	response, err := process.client.Do(request)
	if err != nil {
		return 0, "", err
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	return response.StatusCode, string(body), errors.Join(readErr, closeErr)
}

func (process *systemStateWebProcess) login(ctx context.Context, config systemstate.BootstrapConfig) (int, error) {
	status, token, err := process.do(ctx, http.MethodGet, systemStateLoginPath, nil)
	if err != nil || status != http.StatusOK {
		return status, fmt.Errorf("issue login CSRF: status=%d: %w", status, err)
	}
	header := make(http.Header)
	header.Set(websessionauth.DefaultCSRFHeader, token)
	header.Set("X-System-State-Username", config.Username)
	header.Set("X-System-State-Password", config.Password)
	status, _, err = process.do(ctx, http.MethodPost, systemStateLoginPath, header)
	return status, err
}

func (process *systemStateWebProcess) csrf(ctx context.Context) (string, error) {
	status, token, err := process.do(ctx, http.MethodGet, systemStateCSRFPath, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("issue authenticated CSRF status=%d", status)
	}
	return token, nil
}

func (process *systemStateWebProcess) mutate(ctx context.Context, token string) (int, error) {
	header := make(http.Header)
	header.Set(websessionauth.DefaultCSRFHeader, token)
	status, _, err := process.do(ctx, http.MethodPost, systemStateMutatePath, header)
	return status, err
}

func systemStateNamedCookie(jar http.CookieJar, rawURL, name string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	for _, cookie := range jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value, true
		}
	}
	return "", false
}

func systemStateCookieSnapshot(jar http.CookieJar, rawURL string) ([]*http.Cookie, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	cookies := jar.Cookies(parsed)
	result := make([]*http.Cookie, len(cookies))
	for index, cookie := range cookies {
		clone := *cookie
		result[index] = &clone
	}
	return result, nil
}

type systemStateAdminProcess struct {
	server *httptest.Server
	client *http.Client
}

func systemStateConfigureArticleAdmin(config *systemstate.BootstrapConfig) {
	config.Permissions = []auth.Permission{
		admin.DefaultAccessPermission,
		adminapp.ArticleViewPermission,
		adminapp.ArticleAddPermission,
		adminapp.ArticleChangePermission,
		adminapp.ArticleDeletePermission,
	}
}

func newSystemStateAdminProcess(
	fixture *systemStateFixture,
	jar http.CookieJar,
	entropy byte,
) (*systemStateAdminProcess, error) {
	service, err := adminapp.NewDurableService(fixture.runtime, fixture.runtime)
	if err != nil {
		return nil, err
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "system_state_admin_actual",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: "godj_conformance",
		}},
	})
	if err != nil {
		return nil, err
	}
	builder := admin.NewBuilder(configured.Apps())
	if err := adminapp.RegisterArticle(builder, service); err != nil {
		return nil, err
	}
	registry, err := builder.Build()
	if err != nil {
		return nil, err
	}
	manager, err := sessions.NewManager(fixture.runtime.SessionStore(), sessions.Config{
		AbsoluteLifetime: 2 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return systemStateClock },
		Random:           bytes.NewReader(bytes.Repeat([]byte{entropy}, 32*32)),
	})
	if err != nil {
		return nil, err
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, "/admin")
	if err != nil {
		return nil, err
	}
	authRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions:         manager,
		Authenticator:    fixture.runtime.Authenticator(),
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true, Lifetime: 2 * time.Hour},
		CSRFCookie:       websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:        "/admin/login/",
		FallbackPath:     "/admin/",
		AllowedNextPaths: allowedNext,
		Random:           bytes.NewReader(bytes.Repeat([]byte{entropy + 1}, 32*64)),
		Clock:            func() time.Time { return systemStateClock },
	})
	if err != nil {
		return nil, err
	}
	site, err := admin.NewSite(admin.SiteConfig{
		Apps:      configured.Apps(),
		Namespace: "godj_conformance",
		BasePath:  "/admin",
		Registry:  registry,
		Auth:      authRuntime,
		Random:    bytes.NewReader(bytes.Repeat([]byte{entropy + 2}, 32)),
	})
	if err != nil {
		return nil, err
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		return nil, err
	}
	server := httptest.NewServer(application)
	client := server.Client()
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &systemStateAdminProcess{server: server, client: client}, nil
}

func (process *systemStateAdminProcess) close() {
	if process != nil && process.server != nil {
		process.server.Close()
	}
}

func (process *systemStateAdminProcess) request(
	ctx context.Context,
	method, path string,
	values url.Values,
) (int, http.Header, string, error) {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, process.server.URL+path, body)
	if err != nil {
		return 0, nil, "", err
	}
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := process.client.Do(request)
	if err != nil {
		return 0, nil, "", err
	}
	payload, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	return response.StatusCode, response.Header.Clone(), string(payload), errors.Join(readErr, closeErr)
}

func (process *systemStateAdminProcess) login(
	ctx context.Context,
	config systemstate.BootstrapConfig,
) (int, error) {
	status, _, body, err := process.request(ctx, http.MethodGet, "/admin/login/?next=/admin/articles/", nil)
	if err != nil || status != http.StatusOK {
		return status, fmt.Errorf("load system-state Admin login: status=%d: %w", status, err)
	}
	token, err := systemStateHTMLCSRFToken(body)
	if err != nil {
		return 0, err
	}
	status, _, _, err = process.request(ctx, http.MethodPost, "/admin/login/", url.Values{
		"csrfmiddlewaretoken": {token},
		"username":            {config.Username},
		"password":            {config.Password},
		"next":                {"/admin/articles/"},
	})
	return status, err
}

func (process *systemStateAdminProcess) formToken(ctx context.Context, path string) (string, error) {
	status, _, body, err := process.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("load system-state Admin form: status=%d", status)
	}
	return systemStateHTMLCSRFToken(body)
}

func systemStateHTMLCSRFToken(body string) (string, error) {
	const marker = `name="csrfmiddlewaretoken" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		return "", errors.New("system-state Admin form omitted CSRF token")
	}
	remaining := body[start+len(marker):]
	end := strings.IndexByte(remaining, '"')
	if end <= 0 {
		return "", errors.New("system-state Admin form published malformed CSRF token")
	}
	return remaining[:end], nil
}

func systemStateRotatedSessionRestart(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	loginProcess, err := newSystemStateWebProcess(fixture, jar, 0x81)
	if err != nil {
		return protocol.Observation{}, err
	}
	if status, err := loginProcess.login(ctx, fixture.config); err != nil || status != http.StatusFound {
		loginProcess.close()
		return protocol.Observation{}, fmt.Errorf("initial login status=%d: %w", status, err)
	}
	firstCookie, firstFound := systemStateNamedCookie(jar, loginProcess.server.URL, websessionauth.DefaultSessionCookieName)
	loginStatus, err := loginProcess.login(ctx, fixture.config)
	if err != nil {
		loginProcess.close()
		return protocol.Observation{}, err
	}
	rotatedCookie, rotatedFound := systemStateNamedCookie(jar, loginProcess.server.URL, websessionauth.DefaultSessionCookieName)
	loginProcess.close()
	if !firstFound || !rotatedFound {
		return protocol.Observation{}, fmt.Errorf("login did not publish a session cookie")
	}
	oldID, err := sessions.ParseID(firstCookie)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, oldPresent, err := fixture.runtime.SessionStore().Load(ctx, oldID)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := fixture.reopen(ctx, 0x41); err != nil {
		return protocol.Observation{}, err
	}
	restarted, err := newSystemStateWebProcess(fixture, jar, 0x82)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer restarted.close()
	beforeProbe, _ := systemStateNamedCookie(jar, restarted.server.URL, websessionauth.DefaultSessionCookieName)
	adminStatus, body, err := restarted.do(ctx, http.MethodGet, systemStatePrincipalPath, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	apiStatus, _, err := restarted.do(ctx, http.MethodGet, systemStateAPIPath, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	afterProbe, _ := systemStateNamedCookie(jar, restarted.server.URL, websessionauth.DefaultSessionCookieName)
	parts := strings.Split(body, "\n")
	authenticated := len(parts) == 2 && parts[0] == "true"
	permission := len(parts) == 2 && parts[1] == "true"
	sessionRows, err := systemStateCountRows(ctx, fixture.runtime, systemStateSessionTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"admin_status":        systemStateInt(adminStatus),
			"api_status":          systemStateInt(apiStatus),
			"authenticated":       protocol.Boolean(authenticated),
			"login_status":        systemStateInt(loginStatus),
			"old_session_removed": protocol.Boolean(!oldPresent),
			"permission":          protocol.Boolean(permission),
			"rotated":             protocol.Boolean(firstCookie != rotatedCookie),
			"same_cookie_handoff": protocol.Boolean(beforeProbe == afterProbe && beforeProbe == rotatedCookie),
		}),
		protocol.Object(map[string]protocol.Value{
			"session_rows_after_restart": systemStateInt(sessionRows),
		}),
		protocol.Object(map[string]protocol.Value{
			// Login, rotation and fixture.reopen all execute in this PID. The
			// helper is not registered; the child-worker handler owns publication.
			"distinct_processes":       systemStateInt(1),
			"session_rows_after_login": systemStateInt(1),
		}),
	)
}

func systemStateLogoutRestartDenial(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	loginProcess, err := newSystemStateWebProcess(fixture, jar, 0x91)
	if err != nil {
		return protocol.Observation{}, err
	}
	if status, err := loginProcess.login(ctx, fixture.config); err != nil || status != http.StatusFound {
		loginProcess.close()
		return protocol.Observation{}, fmt.Errorf("logout scenario login status=%d: %w", status, err)
	}
	oldCookie, found := systemStateNamedCookie(jar, loginProcess.server.URL, websessionauth.DefaultSessionCookieName)
	if !found {
		loginProcess.close()
		return protocol.Observation{}, errors.New("logout scenario login omitted session cookie")
	}
	oldID, err := sessions.ParseID(oldCookie)
	if err != nil {
		loginProcess.close()
		return protocol.Observation{}, err
	}
	csrfToken, err := loginProcess.csrf(ctx)
	if err != nil {
		loginProcess.close()
		return protocol.Observation{}, err
	}
	header := make(http.Header)
	header.Set(websessionauth.DefaultCSRFHeader, csrfToken)
	logoutStatus, _, err := loginProcess.do(ctx, http.MethodPost, systemStateLogoutPath, header)
	loginProcess.close()
	if err != nil || logoutStatus != http.StatusFound {
		return protocol.Observation{}, fmt.Errorf("logout status=%d: %w", logoutStatus, err)
	}
	_, oldPresentAfterLogout, err := fixture.runtime.SessionStore().Load(ctx, oldID)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := fixture.reopen(ctx, 0x41); err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.resetDML()
	copiedJar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	restarted, err := newSystemStateWebProcess(fixture, copiedJar, 0x92)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer restarted.close()
	parsed, err := url.Parse(restarted.server.URL)
	if err != nil {
		return protocol.Observation{}, err
	}
	copiedJar.SetCookies(parsed, []*http.Cookie{{
		Name:     websessionauth.DefaultSessionCookieName,
		Value:    oldCookie,
		Path:     "/",
		HttpOnly: true,
	}})
	adminStatus, adminBody, err := restarted.do(ctx, http.MethodGet, systemStatePrincipalPath, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	apiStatus, _, err := restarted.do(ctx, http.MethodGet, systemStateAPIPath, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, resurrected, err := fixture.runtime.SessionStore().Load(ctx, oldID)
	if err != nil {
		return protocol.Observation{}, err
	}
	sessionRows, err := systemStateCountRows(ctx, fixture.runtime, systemStateSessionTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldAuthenticated := adminStatus == http.StatusOK && strings.HasPrefix(adminBody, "true\n")
	resurrectionWrites := fixture.observed.inserts.Load() + fixture.observed.updates.Load()
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"admin_status":             systemStateInt(adminStatus),
			"api_status":               systemStateInt(apiStatus),
			"old_cookie_authenticated": protocol.Boolean(oldAuthenticated),
			"old_session_removed":      protocol.Boolean(!oldPresentAfterLogout),
			"resurrected":              protocol.Boolean(resurrected),
		}),
		protocol.Object(map[string]protocol.Value{
			"session_rows_after_logout": systemStateInt(sessionRows),
		}),
		protocol.Object(map[string]protocol.Value{
			"distinct_processes":  systemStateInt(1),
			"resurrection_writes": systemStateInt64(resurrectionWrites),
		}),
	)
}

func systemStateCSRFRestart(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, true, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	beforeRestart, err := newSystemStateWebProcess(fixture, jar, 0xa1)
	if err != nil {
		return protocol.Observation{}, err
	}
	if status, err := beforeRestart.login(ctx, fixture.config); err != nil || status != http.StatusFound {
		beforeRestart.close()
		return protocol.Observation{}, fmt.Errorf("CSRF scenario login status=%d: %w", status, err)
	}
	preRestartToken, err := beforeRestart.csrf(ctx)
	if err != nil {
		beforeRestart.close()
		return protocol.Observation{}, err
	}
	sessionCookie, sessionFound := systemStateNamedCookie(jar, beforeRestart.server.URL, websessionauth.DefaultSessionCookieName)
	csrfCookie, csrfFound := systemStateNamedCookie(jar, beforeRestart.server.URL, websessionauth.DefaultCSRFCookieName)
	if !sessionFound || !csrfFound {
		beforeRestart.close()
		return protocol.Observation{}, errors.New("CSRF scenario omitted required cookies")
	}
	setupBefore, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		beforeRestart.close()
		return protocol.Observation{}, err
	}
	setupStatus, err := beforeRestart.mutate(ctx, preRestartToken)
	if err != nil {
		beforeRestart.close()
		return protocol.Observation{}, err
	}
	setupAfter, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	beforeRestart.close()
	if err != nil {
		return protocol.Observation{}, err
	}
	if setupStatus != http.StatusCreated {
		return protocol.Observation{}, fmt.Errorf("pre-restart CSRF setup status=%d", setupStatus)
	}
	if err := fixture.reopen(ctx, 0x41); err != nil {
		return protocol.Observation{}, err
	}
	afterRestart, err := newSystemStateWebProcess(fixture, jar, 0xa2)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer afterRestart.close()
	carriedSession, _ := systemStateNamedCookie(jar, afterRestart.server.URL, websessionauth.DefaultSessionCookieName)
	carriedCSRF, _ := systemStateNamedCookie(jar, afterRestart.server.URL, websessionauth.DefaultCSRFCookieName)
	staleBefore, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	staleStatus, err := afterRestart.mutate(ctx, preRestartToken)
	if err != nil {
		return protocol.Observation{}, err
	}
	staleAfter, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	freshToken, err := afterRestart.csrf(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	freshSession, _ := systemStateNamedCookie(jar, afterRestart.server.URL, websessionauth.DefaultSessionCookieName)
	freshCSRF, _ := systemStateNamedCookie(jar, afterRestart.server.URL, websessionauth.DefaultCSRFCookieName)
	freshBefore, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	freshStatus, err := afterRestart.mutate(ctx, freshToken)
	if err != nil {
		return protocol.Observation{}, err
	}
	freshAfter, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	staleDelta := staleAfter - staleBefore
	freshDelta := freshAfter - freshBefore
	setupDelta := setupAfter - setupBefore
	result := protocol.Object(map[string]protocol.Value{
		"fresh": protocol.Object(map[string]protocol.Value{
			"accepted": protocol.Boolean(freshStatus == http.StatusCreated),
			"status":   systemStateInt(freshStatus),
		}),
		"pre_restart": protocol.Object(map[string]protocol.Value{
			"accepted": protocol.Boolean(staleStatus == http.StatusCreated),
			"status":   systemStateInt(staleStatus),
		}),
		"same_cookie_handoff": protocol.Boolean(
			sessionCookie == carriedSession && sessionCookie == freshSession &&
				csrfCookie == carriedCSRF && csrfCookie == freshCSRF,
		),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"fresh":       protocol.Object(map[string]protocol.Value{"article_delta": systemStateInt(freshDelta)}),
		"pre_restart": protocol.Object(map[string]protocol.Value{"article_delta": systemStateInt(staleDelta)}),
	})
	secretCount, err := authSessionSecretOccurrences(
		[]protocol.Value{result, dbState},
		fixture.config.Password,
		sessionCookie,
		csrfCookie,
		preRestartToken,
		freshToken,
	)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("scan CSRF-restart observation secrets: %w", err)
	}
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"distinct_processes":          systemStateInt(1),
		"fresh_mutations":             systemStateInt(freshDelta),
		"pre_restart_mutations":       systemStateInt(staleDelta),
		"pre_restart_setup_mutations": systemStateInt(setupDelta),
		"secret_values_serialized":    systemStateInt64(secretCount),
	}))
}

func systemStateAdminAuditFaultRollback(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, true, systemStateConfigureArticleAdmin)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	process, err := newSystemStateAdminProcess(fixture, jar, 0xb1)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer process.close()
	if status, err := process.login(ctx, fixture.config); err != nil || status != http.StatusFound {
		return protocol.Observation{}, fmt.Errorf("audit-fault Admin login status=%d: %w", status, err)
	}
	articleBefore, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	auditBefore, err := systemStateCountRows(ctx, fixture.runtime, systemStateAuditTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	token, err := process.formToken(ctx, "/admin/articles/add/")
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.resetDML()
	fixture.observed.injectNextInsertForTable(systemStateAuditTable, errors.New("injected audit append failure"))
	status, _, _, err := process.request(ctx, http.MethodPost, "/admin/articles/add/", url.Values{
		"csrfmiddlewaretoken": {token},
		"title":               {"Rollback"},
		"summary":             {"Rollback"},
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	articleAfter, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	auditAfter, err := systemStateCountRows(ctx, fixture.runtime, systemStateAuditTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	faults := fixture.observed.insertFailures.Load()
	if faults != 1 {
		return protocol.Observation{}, fmt.Errorf("audit fault injections observed=%d, want 1", faults)
	}
	articleDelta := articleAfter - articleBefore
	auditDelta := auditAfter - auditBefore
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"article_rolled_back": protocol.Boolean(articleDelta == 0),
			"audit_rolled_back":   protocol.Boolean(auditDelta == 0),
			"status":              systemStateInt(status),
		}),
		protocol.Object(map[string]protocol.Value{
			"article_delta": systemStateInt(articleDelta),
			"audit_delta":   systemStateInt(auditDelta),
		}),
		protocol.Object(map[string]protocol.Value{
			"distinct_processes": systemStateInt(1),
			"faults_injected":    systemStateInt64(faults),
		}),
	)
}

func systemStateAuditHistoryRestart(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, true, systemStateConfigureArticleAdmin)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	process, err := newSystemStateAdminProcess(fixture, jar, 0xc1)
	if err != nil {
		return protocol.Observation{}, err
	}
	if status, err := process.login(ctx, fixture.config); err != nil || status != http.StatusFound {
		process.close()
		return protocol.Observation{}, fmt.Errorf("history Admin login status=%d: %w", status, err)
	}
	addToken, err := process.formToken(ctx, "/admin/articles/add/")
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	addStatus, _, _, err := process.request(ctx, http.MethodPost, "/admin/articles/add/", url.Values{
		"csrfmiddlewaretoken": {addToken},
		"title":               {"Lifecycle"},
		"summary":             {"Initial"},
	})
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	repository, err := adminapp.NewRepository(fixture.runtime)
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	page, err := repository.List(ctx, adminapp.ListOptions{})
	if err != nil || page.Total != 1 || len(page.Articles) != 1 {
		process.close()
		return protocol.Observation{}, fmt.Errorf("history created Article inventory total=%d rows=%d: %w", page.Total, len(page.Articles), err)
	}
	articleID := page.Articles[0].ID
	changePath := "/admin/articles/change/?id=" + strconv.FormatInt(articleID, 10)
	changeToken, err := process.formToken(ctx, changePath)
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	changeStatus, _, _, err := process.request(ctx, http.MethodPost, changePath, url.Values{
		"csrfmiddlewaretoken": {changeToken},
		"title":               {"Lifecycle"},
		"published":           {"on"},
		"summary":             {"Changed"},
	})
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	deletePath := "/admin/articles/delete/?id=" + strconv.FormatInt(articleID, 10)
	deleteToken, err := process.formToken(ctx, deletePath)
	if err != nil {
		process.close()
		return protocol.Observation{}, err
	}
	deleteStatus, _, _, err := process.request(ctx, http.MethodPost, deletePath, url.Values{
		"csrfmiddlewaretoken": {deleteToken},
		"confirm":             {"yes"},
	})
	process.close()
	if err != nil {
		return protocol.Observation{}, err
	}
	writeStatuses := []int{addStatus, changeStatus, deleteStatus}
	for _, status := range writeStatuses {
		if status != http.StatusFound {
			return protocol.Observation{}, fmt.Errorf("history Admin write status=%d, want redirect", status)
		}
	}
	articleRows, err := systemStateCountRows(ctx, fixture.runtime, systemStateArticleTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	auditRows, err := systemStateCountRows(ctx, fixture.runtime, systemStateAuditTable)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := fixture.reopenConfigured(ctx, 0x41, systemStateConfigureArticleAdmin); err != nil {
		return protocol.Observation{}, err
	}
	restartedService, err := adminapp.NewDurableService(fixture.runtime, fixture.runtime)
	if err != nil {
		return protocol.Observation{}, err
	}
	allEvents, err := restartedService.HistoryLimited(ctx, articleID, 3)
	if err != nil {
		return protocol.Observation{}, err
	}
	newest, err := restartedService.HistoryLimited(ctx, articleID, 2)
	if err != nil {
		return protocol.Observation{}, err
	}
	strictlyIncreasing := systemStateAuditStrictlyIncreasing(allEvents)
	acceptsNonContiguous, err := systemStateAuditAcceptsNonContiguous(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	if !acceptsNonContiguous {
		return protocol.Observation{}, errors.New("durable audit history rejected a non-contiguous sequence")
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"all_events":          protocol.List(systemStateAuditValues(allEvents)...),
			"contiguous_required": protocol.Boolean(!acceptsNonContiguous),
			"newest_bounded":      protocol.List(systemStateAuditValues(newest)...),
			"strictly_increasing": protocol.Boolean(strictlyIncreasing),
			"survived_restart":    protocol.Boolean(len(allEvents) == 3),
		}),
		protocol.Object(map[string]protocol.Value{
			"article_rows": systemStateInt(articleRows),
			"audit_rows":   systemStateInt(auditRows),
		}),
		protocol.Object(map[string]protocol.Value{
			"distinct_processes": systemStateInt(1),
			"history_limit":      systemStateInt(2),
			"write_statuses": protocol.List(
				systemStateInt(addStatus),
				systemStateInt(changeStatus),
				systemStateInt(deleteStatus),
			),
		}),
	)
}

func systemStateAuditValues(entries []admin.AuditEntry) []protocol.Value {
	values := make([]protocol.Value, len(entries))
	for index, entry := range entries {
		values[index] = protocol.Object(map[string]protocol.Value{
			"action":   protocol.String(string(entry.Action)),
			"sequence": systemStateInt64(int64(entry.Sequence)),
		})
	}
	return values
}

func systemStateAuditStrictlyIncreasing(entries []admin.AuditEntry) bool {
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Sequence >= entries[index].Sequence {
			return false
		}
	}
	return true
}

func systemStateAuditContiguous(entries []admin.AuditEntry) bool {
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Sequence+1 != entries[index].Sequence {
			return false
		}
	}
	return true
}

func systemStateAuditAcceptsNonContiguous(ctx context.Context) (bool, error) {
	fixture, err := newSystemStateFixture(ctx, false, nil)
	if err != nil {
		return false, err
	}
	defer fixture.cleanup()
	events := make([]admin.PreparedEvent, 0, 3)
	for _, seed := range []struct {
		objectID int64
		action   admin.Action
	}{
		{objectID: 7, action: admin.ActionAdd},
		{objectID: 8, action: admin.ActionAdd},
		{objectID: 7, action: admin.ActionChange},
	} {
		event, err := admin.PrepareEvent(
			fixture.config.PrincipalID,
			systemStateArticleModel,
			seed.objectID,
			seed.action,
			nil,
			"Gap probe",
		)
		if err != nil {
			return false, err
		}
		events = append(events, event)
	}
	for _, event := range events {
		if err := fixture.runtime.Atomic(ctx, func(session db.Session) error {
			return fixture.runtime.AppendAudit(ctx, session, event)
		}); err != nil {
			return false, err
		}
	}
	history, err := fixture.runtime.AuditHistory(ctx, systemStateArticleModel, 7, 3)
	if err != nil {
		return false, err
	}
	return len(history) == 2 && systemStateAuditStrictlyIncreasing(history) && !systemStateAuditContiguous(history), nil
}

type systemStateCommitUnknownBackend struct {
	*systemStateObservedBackend
	atomicCalls        atomic.Int64
	postUnknownAtomics atomic.Int64
	postUnknownQueries atomic.Int64
	injectUnknown      atomic.Bool
	returnedUnknown    atomic.Bool
}

func (backend *systemStateCommitUnknownBackend) Atomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.atomicCalls.Add(1)
	if backend.returnedUnknown.Load() {
		backend.postUnknownAtomics.Add(1)
	}
	if err := backend.systemStateObservedBackend.Atomic(ctx, callback); err != nil {
		return err
	}
	if !backend.injectUnknown.Load() {
		return nil
	}
	backend.returnedUnknown.Store(true)
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "injected system-state commit outcome unknown",
	}
}

func (backend *systemStateCommitUnknownBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.atomicCalls.Add(1)
	if backend.returnedUnknown.Load() {
		backend.postUnknownAtomics.Add(1)
	}
	if err := backend.systemStateObservedBackend.CoordinatedAtomic(ctx, callback); err != nil {
		return err
	}
	if !backend.injectUnknown.Load() {
		return nil
	}
	backend.returnedUnknown.Store(true)
	return &query.Error{
		Category: query.CategoryBackend,
		Code:     query.CodeCommitOutcomeUnknown,
		Detail:   "injected system-state commit outcome unknown",
	}
}

func (backend *systemStateCommitUnknownBackend) Query(
	ctx context.Context,
	plan query.Plan,
) (db.Rows, error) {
	if backend.returnedUnknown.Load() {
		backend.postUnknownQueries.Add(1)
	}
	return backend.systemStateObservedBackend.Query(ctx, plan)
}

func systemStateCommitOutcomeUnknown(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	fixture, err := newSystemStateFixture(ctx, true, systemStateConfigureArticleAdmin)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer fixture.cleanup()
	unknownBackend := &systemStateCommitUnknownBackend{
		systemStateObservedBackend: fixture.observed,
	}
	unknownRuntime, err := systemstate.Open(ctx, unknownBackend, fixture.config)
	if err != nil {
		return protocol.Observation{}, err
	}
	unknownBackend.atomicCalls.Store(0)
	unknownBackend.injectUnknown.Store(true)
	service, err := adminapp.NewDurableService(unknownRuntime, unknownRuntime)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.resetDML()
	_, mutationErr := service.Create(ctx, fixture.config.PrincipalID, adminapp.Input{Title: "Outcome unknown"})
	var outcome *query.Error
	classifiedUnknown := errors.As(mutationErr, &outcome) && outcome.Code == query.CodeCommitOutcomeUnknown
	if !classifiedUnknown {
		return protocol.Observation{}, fmt.Errorf("commit-unknown mutation error has wrong classification: %w", mutationErr)
	}
	if articleInserts, auditInserts := fixture.observed.articleInserts.Load(), fixture.observed.auditInserts.Load(); articleInserts != 1 || auditInserts != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"commit-unknown transactional inserts article=%d audit=%d, want 1/1",
			articleInserts,
			auditInserts,
		)
	}
	atomicCalls := unknownBackend.atomicCalls.Load()
	automaticRetries := atomicCalls - 1
	if automaticRetries < 0 {
		automaticRetries = 0
	}
	// One audit insert belongs to the transaction whose commit outcome is
	// unknown. Any additional insert would be a forbidden synthetic/retry
	// publication after the caller lost a verified commit boundary.
	syntheticAuditRows := fixture.observed.auditInserts.Load() - 1
	if syntheticAuditRows < 0 {
		syntheticAuditRows = 0
	}
	if postUnknownQueries := unknownBackend.postUnknownQueries.Load(); postUnknownQueries != 0 {
		return protocol.Observation{}, fmt.Errorf("commit-unknown path performed %d reconciliation reads", postUnknownQueries)
	}
	return systemStateObservation(
		contract,
		protocol.Object(map[string]protocol.Value{
			"outcome":                 protocol.String(string(outcome.Code)),
			"reconciliation_required": protocol.Boolean(classifiedUnknown),
			"reported_success":        protocol.Boolean(mutationErr == nil),
			"verified_commit":         protocol.Boolean(false),
		}),
		protocol.Object(map[string]protocol.Value{
			"article_state":        protocol.String("unknown"),
			"audit_state":          protocol.String("unknown"),
			"synthetic_audit_rows": systemStateInt64(syntheticAuditRows),
		}),
		protocol.Object(map[string]protocol.Value{
			"automatic_retries":      systemStateInt64(automaticRetries),
			"rollback_after_unknown": systemStateInt64(unknownBackend.postUnknownAtomics.Load()),
		}),
	)
}
