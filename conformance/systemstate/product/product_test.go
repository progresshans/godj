package product

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

const (
	postgresURLEnv      = "GODJ_TEST_POSTGRES_URL"
	postgresRequiredEnv = "GODJ_REQUIRE_POSTGRES"
)

var errInjectedAuditRollback = errors.New("injected product audit rollback")

func TestSystemStateSQLiteProductSentinel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	databasePath := filepath.Join(t.TempDir(), "system-state-product.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc"
	runSystemStateProductSentinel(t, ctx, func(ctx context.Context) (productBackend, error) {
		return sqlite.Open(ctx, dataSourceName)
	})
}

func TestSystemStatePostgresProductSentinel(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv(postgresURLEnv))
	if databaseURL == "" {
		if os.Getenv(postgresRequiredEnv) == "1" {
			t.Fatalf("%s=1 requires %s", postgresRequiredEnv, postgresURLEnv)
		}
		t.Skip("GODJ_TEST_POSTGRES_URL is not configured; system-state PostgreSQL product sentinel was not run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	adminConnection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect system-state PostgreSQL product database: %s", redactedPostgresError(databaseURL, err))
	}
	schema := fmt.Sprintf("godj_systemstate_product_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminConnection.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = adminConnection.Close(ctx)
		t.Fatalf("create isolated system-state PostgreSQL schema: %s", redactedPostgresError(databaseURL, err))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := adminConnection.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated system-state PostgreSQL schema: %s", redactedPostgresError(databaseURL, err))
		}
		if err := adminConnection.Close(cleanupCtx); err != nil {
			t.Errorf("close system-state PostgreSQL admin connection: %s", redactedPostgresError(databaseURL, err))
		}
	})

	runSystemStateProductSentinel(t, ctx, func(ctx context.Context) (productBackend, error) {
		backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
		if err != nil {
			return nil, errors.New(redactedPostgresError(databaseURL, err))
		}
		return backend, nil
	})
}

type productBackend interface {
	systemstate.Backend
	db.Atomic
	migrationbackend.RevisionFencedBackend
	Close() error
}

type backendFactory func(context.Context) (productBackend, error)

func runSystemStateProductSentinel(t *testing.T, ctx context.Context, open backendFactory) {
	t.Helper()

	firstRaw, err := open(ctx)
	if err != nil {
		t.Fatalf("open first product backend: %v", err)
	}
	firstOpen := true
	defer func() {
		if firstOpen {
			_ = firstRaw.Close()
		}
	}()
	first := &observedBackend{productBackend: firstRaw}
	firstState, err := (migrations.Executor{Backend: first}).Migrate(
		ctx,
		loadProductDefinitions(t),
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		t.Fatalf("explicitly migrate Article and system-state definitions: %v", err)
	}
	if got := first.migrationTransactions.Load(); got != 2 {
		t.Fatalf("first migration transaction count = %d, want 2", got)
	}
	assertProductState(t, firstState)
	assertProductHistory(t, ctx, first)

	firstConfig := productBootstrapConfig(t)
	firstRuntime, err := systemstate.Open(ctx, first, firstConfig)
	if err != nil {
		t.Fatalf("bootstrap first system-state Runtime: %v", err)
	}
	if got := first.atomicCalls.Load(); got != 1 {
		t.Fatalf("first Runtime bootstrap atomic calls = %d, want 1", got)
	}
	principal, err := firstRuntime.Authenticator().Authenticate(ctx, firstConfig.Username, firstConfig.Password)
	if err != nil || principal.ID() != firstConfig.PrincipalID || !principal.Authenticated() {
		t.Fatalf("authenticate first durable credential = (%v, %v)", principal, err)
	}

	createdAt := time.Now().UTC().Truncate(time.Second)
	firstManager, err := sessions.NewManager(firstRuntime.SessionStore(), sessions.Config{
		AbsoluteLifetime: 24 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Limits:           firstConfig.SessionLimits,
		Clock:            func() time.Time { return createdAt },
		Random:           bytes.NewReader(bytes.Repeat([]byte{0x5a}, 32)),
	})
	if err != nil {
		t.Fatalf("construct first durable session manager: %v", err)
	}
	createdSession, err := firstManager.Create(ctx, map[string]string{
		"principal_id": firstConfig.PrincipalID,
		"scope":        "system-state-product",
	})
	if err != nil {
		t.Fatalf("create durable product session: %v", err)
	}
	if err := firstRaw.Close(); err != nil {
		t.Fatalf("close first product backend: %v", err)
	}
	firstOpen = false

	secondRaw, err := open(ctx)
	if err != nil {
		t.Fatalf("reopen product backend: %v", err)
	}
	secondOpen := true
	defer func() {
		if secondOpen {
			_ = secondRaw.Close()
		}
	}()
	second := &observedBackend{productBackend: secondRaw}
	secondState, err := (migrations.Executor{Backend: second}).Migrate(
		ctx,
		loadProductDefinitions(t),
		migrations.LatestLifecycleRequest(),
	)
	if err != nil {
		t.Fatalf("repeat explicit migration after reopen: %v", err)
	}
	if got := second.migrationTransactions.Load(); got != 0 {
		t.Fatalf("reopened no-op migration transactions = %d, want 0", got)
	}
	if !firstState.Equal(secondState) {
		t.Fatal("reopened no-op migration state differs from the first state")
	}
	assertProductState(t, secondState)
	assertProductHistory(t, ctx, second)

	secondConfig := productBootstrapConfig(t)
	secondRuntime, err := systemstate.Open(ctx, second, secondConfig)
	if err != nil {
		t.Fatalf("reopen system-state Runtime: %v", err)
	}
	if got := second.atomicCalls.Load(); got != 1 {
		t.Fatalf("identical Runtime reopen atomic calls = %d, want 1 final inspection", got)
	}
	restartedPrincipal, err := secondRuntime.Authenticator().Authenticate(ctx, secondConfig.Username, secondConfig.Password)
	if err != nil || restartedPrincipal.ID() != secondConfig.PrincipalID || !restartedPrincipal.Authenticated() {
		t.Fatalf("authenticate reopened durable credential = (%v, %v)", restartedPrincipal, err)
	}

	loadAt := createdAt.Add(5 * time.Minute)
	secondManager, err := sessions.NewManager(secondRuntime.SessionStore(), sessions.Config{
		AbsoluteLifetime: 24 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Limits:           secondConfig.SessionLimits,
		Clock:            func() time.Time { return loadAt },
		Random:           bytes.NewReader(bytes.Repeat([]byte{0x6b}, 32)),
	})
	if err != nil {
		t.Fatalf("construct reopened durable session manager: %v", err)
	}
	loadedSession, found, err := secondManager.Load(ctx, createdSession.ID())
	if err != nil || !found || loadedSession.ID() != createdSession.ID() {
		t.Fatalf("load durable session after backend reopen = (%v, %t, %v)", loadedSession, found, err)
	}
	if principalID, present := loadedSession.Value("principal_id"); !present || principalID != secondConfig.PrincipalID {
		t.Fatalf("reopened durable session principal = %q, present=%t", principalID, present)
	}

	rollbackStore := &rollbackAuditStore{delegate: secondRuntime, failure: errInjectedAuditRollback}
	rollbackService, err := adminapp.NewDurableService(secondRuntime, rollbackStore)
	if err != nil {
		t.Fatalf("construct rollback Article audit service: %v", err)
	}
	if _, err := rollbackService.Create(ctx, secondConfig.PrincipalID, adminapp.Input{Title: "Rolled back Article"}); !errors.Is(err, errInjectedAuditRollback) {
		t.Fatalf("Article create with injected audit failure = %v, want rollback sentinel", err)
	}
	if rollbackStore.objectID <= 0 {
		t.Fatal("rollback audit store did not observe the Article object identity")
	}
	repository, err := adminapp.NewRepository(secondRuntime)
	if err != nil {
		t.Fatalf("construct Article product repository: %v", err)
	}
	page, err := repository.List(ctx, adminapp.ListOptions{})
	if err != nil || page.Total != 0 || len(page.Articles) != 0 {
		t.Fatalf("Article rows after transactional audit rollback = (%#v, %v)", page, err)
	}
	rolledBackHistory, err := secondRuntime.AuditHistory(ctx, "godj_conformance.article", rollbackStore.objectID, 10)
	if err != nil || len(rolledBackHistory) != 0 {
		t.Fatalf("audit rows after transactional rollback = (%#v, %v)", rolledBackHistory, err)
	}

	commitService, err := adminapp.NewDurableService(secondRuntime, secondRuntime)
	if err != nil {
		t.Fatalf("construct committed Article audit service: %v", err)
	}
	committedArticle, err := commitService.Create(ctx, secondConfig.PrincipalID, adminapp.Input{Title: "Committed Article"})
	if err != nil || committedArticle.ID <= 0 {
		t.Fatalf("commit Article and audit together = (%#v, %v)", committedArticle, err)
	}
	page, err = repository.List(ctx, adminapp.ListOptions{})
	if err != nil || page.Total != 1 || len(page.Articles) != 1 || page.Articles[0].ID != committedArticle.ID {
		t.Fatalf("committed Article rows = (%#v, %v)", page, err)
	}
	committedHistory, err := commitService.HistoryLimited(ctx, committedArticle.ID, 10)
	if err != nil || len(committedHistory) != 1 || committedHistory[0].ObjectID != committedArticle.ID ||
		committedHistory[0].Action != admin.ActionAdd || committedHistory[0].ActorID != secondConfig.PrincipalID {
		t.Fatalf("committed durable audit history = (%#v, %v)", committedHistory, err)
	}
	assertProductHistory(t, ctx, second)

	if err := secondRaw.Close(); err != nil {
		t.Fatalf("close reopened product backend: %v", err)
	}
	secondOpen = false
}

func loadProductDefinitions(t *testing.T) migrations.LoadedDefinitionSet {
	t.Helper()
	repository := productRepositoryRoot(t)
	document, err := os.ReadFile(filepath.Join(repository, "examples", "article", "testdata", "postgres", "0001_initial.godj.json"))
	if err != nil {
		t.Fatalf("read Article product definition: %v", err)
	}
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "examples/article/testdata/postgres/0001_initial.godj.json",
			Document: document,
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatalf("load Article and system-state definitions: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 4 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("combined product definition load report = %+v", report)
	}
	return loaded
}

func productRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("cannot locate repository root from product test working directory")
		}
		directory = parent
	}
}

func productBootstrapConfig(t *testing.T) systemstate.BootstrapConfig {
	t.Helper()
	permission, err := auth.NewPermission("article.manage")
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 10_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x42}, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return systemstate.BootstrapConfig{
		Username:       "system-product-admin",
		Password:       "system-product-password-marker",
		PrincipalID:    "system-product-principal",
		Active:         true,
		Permissions:    []auth.Permission{permission},
		PasswordHasher: hasher,
		SessionLimits:  sessions.DefaultLimits(),
		MaxSessions:    8,
		AuditCapacity:  16,
	}
}

func assertProductState(t *testing.T, state migrations.ProjectState) {
	t.Helper()
	for _, app := range []string{"godj_conformance", systemstate.InitialMigrationKey().App} {
		if _, exists := state.Schema(app); !exists {
			t.Fatalf("combined migrated state is missing app %q", app)
		}
	}
}

func assertProductHistory(t *testing.T, ctx context.Context, backend migrationbackend.AppliedMigrationReader) {
	t.Helper()
	history, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		t.Fatalf("read combined product migration history: %v", err)
	}
	want := map[migrations.MigrationKey]bool{
		{App: "godj_conformance", Name: "0001_initial"}: true,
		systemstate.InitialMigrationKey():               true,
	}
	if len(history) != len(want) {
		t.Fatalf("combined product migration history = %+v, want exactly two entries", history)
	}
	for _, applied := range history {
		key := migrations.MigrationKey{App: applied.App, Name: applied.Name}
		if !want[key] {
			t.Fatalf("combined product migration history contains unexpected key %+v", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("combined product migration history is missing %+v", want)
	}
}

type observedBackend struct {
	productBackend
	migrationTransactions atomic.Int64
	atomicCalls           atomic.Int64
}

func (backend *observedBackend) OpenRevisionFencedSession(
	ctx context.Context,
) (migrationbackend.RevisionFencedSession, error) {
	session, err := backend.productBackend.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	return &observedMigrationSession{
		RevisionFencedSession: session,
		transactions:          &backend.migrationTransactions,
	}, nil
}

func (backend *observedBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.atomicCalls.Add(1)
	return backend.productBackend.Atomic(ctx, callback)
}

func (backend *observedBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	backend.atomicCalls.Add(1)
	return backend.productBackend.CoordinatedAtomic(ctx, callback)
}

type observedMigrationSession struct {
	migrationbackend.RevisionFencedSession
	transactions *atomic.Int64
}

func (session *observedMigrationSession) BeginMigration(
	ctx context.Context,
	transition migrationbackend.HistoryTransition,
	intent migrationbackend.MigrationIntent,
) (migrationbackend.RevisionFencedTransaction, error) {
	session.transactions.Add(1)
	return session.RevisionFencedSession.BeginMigration(ctx, transition, intent)
}

type rollbackAuditStore struct {
	delegate *systemstate.Runtime
	failure  error
	objectID int64
}

func (store *rollbackAuditStore) AppendAudit(
	ctx context.Context,
	session db.Session,
	event admin.PreparedEvent,
) error {
	store.objectID = event.ObjectID()
	if err := store.delegate.AppendAudit(ctx, session, event); err != nil {
		return err
	}
	return store.failure
}

func (store *rollbackAuditStore) AuditHistory(
	ctx context.Context,
	model string,
	objectID int64,
	limit int,
) ([]admin.AuditEntry, error) {
	return store.delegate.AuditHistory(ctx, model, objectID, limit)
}

func redactedPostgresError(databaseURL string, err error) string {
	if err == nil {
		return "<nil>"
	}
	message := strings.ReplaceAll(err.Error(), databaseURL, "<redacted-postgres-url>")
	if config, parseErr := pgx.ParseConfig(databaseURL); parseErr == nil && config.Password != "" {
		message = strings.ReplaceAll(message, config.Password, "<redacted-postgres-password>")
	}
	return message
}
