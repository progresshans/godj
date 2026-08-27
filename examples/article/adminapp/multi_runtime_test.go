package adminapp_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

func TestTwoRuntimeSQLiteArticleAuditMutationUsesDatabaseFenceAndGlobalPrune(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	databasePath := filepath.Join(t.TempDir(), "article-admin.sqlite3")
	dataSourceName := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc&_busy_timeout=5000"
	holderRaw := openArticleMultiRuntimeBackend(t, ctx, dataSourceName)
	contenderRaw := openArticleMultiRuntimeBackend(t, ctx, dataSourceName)
	migrateArticleSystemState(t, ctx, holderRaw)

	holderBackend := &articleMultiRuntimeBackend{Backend: holderRaw, holder: true}
	contenderBackend := &articleMultiRuntimeBackend{Backend: contenderRaw}
	config := articleMultiRuntimeConfig(t, 3)
	holderRuntime, err := systemstate.Open(ctx, holderBackend, config)
	if err != nil {
		t.Fatalf("Open(holder Runtime): %v", err)
	}
	contenderRuntime, err := systemstate.Open(ctx, contenderBackend, config)
	if err != nil {
		t.Fatalf("Open(contender Runtime): %v", err)
	}
	holderService, err := adminapp.NewDurableService(holderRuntime, holderRuntime)
	if err != nil {
		t.Fatalf("NewDurableService(holder): %v", err)
	}
	contenderService, err := adminapp.NewDurableService(contenderRuntime, contenderRuntime)
	if err != nil {
		t.Fatalf("NewDurableService(contender): %v", err)
	}

	first, err := holderService.Create(ctx, config.PrincipalID, adminapp.Input{Title: "Seed one"})
	if err != nil {
		t.Fatalf("Create(first seed): %v", err)
	}
	second, err := holderService.Create(ctx, config.PrincipalID, adminapp.Input{Title: "Seed two"})
	if err != nil {
		t.Fatalf("Create(second seed): %v", err)
	}

	barrier := newArticleCoordinationBarrier()
	holderBackend.barrier = barrier
	contenderBackend.barrier = barrier
	defer barrier.release()

	holderResult := make(chan articleCreateResult, 1)
	contenderResult := make(chan articleCreateResult, 1)
	go func() {
		article, err := holderService.Create(ctx, config.PrincipalID, adminapp.Input{Title: "Holder"})
		holderResult <- articleCreateResult{article: article, err: err}
	}()
	waitArticleSignal(t, barrier.holderEntered, "holder coordinated callback")
	go func() {
		article, err := contenderService.Create(ctx, config.PrincipalID, adminapp.Input{Title: "Contender"})
		contenderResult <- articleCreateResult{article: article, err: err}
	}()
	waitArticleSignal(t, barrier.contenderAttempted, "contender coordinated attempt")
	select {
	case <-barrier.contenderEntered:
		t.Fatal("contender Article callback entered before holder transaction committed")
	case <-time.After(100 * time.Millisecond):
	}
	barrier.release()

	holderCreated := waitArticleCreateResult(t, holderResult)
	contenderCreated := waitArticleCreateResult(t, contenderResult)
	holderBackend.barrier = nil
	contenderBackend.barrier = nil
	if holderCreated.err != nil || holderCreated.article.ID <= second.ID {
		t.Fatalf("holder Create() = (%#v, %v)", holderCreated.article, holderCreated.err)
	}
	if contenderCreated.err != nil || contenderCreated.article.ID <= holderCreated.article.ID {
		t.Fatalf("contender Create() = (%#v, %v)", contenderCreated.article, contenderCreated.err)
	}
	if got := barrier.holderCallbacks.Load(); got != 1 {
		t.Fatalf("holder coordinated callbacks = %d, want 1", got)
	}
	if got := barrier.contenderAttempts.Load(); got != 1 {
		t.Fatalf("contender coordinated attempts = %d, want 1", got)
	}
	if got := barrier.contenderCallbacks.Load(); got != 1 {
		t.Fatalf("contender coordinated callbacks = %d, want 1", got)
	}

	wantAudit := []articleMultiRuntimeAuditRow{
		{sequence: 2, objectID: second.ID, action: admin.ActionAdd},
		{sequence: 3, objectID: holderCreated.article.ID, action: admin.ActionAdd},
		{sequence: 4, objectID: contenderCreated.article.ID, action: admin.ActionAdd},
	}
	assertArticleMultiRuntimeAudit(t, ctx, holderRaw, wantAudit)
	pruned, err := contenderService.HistoryLimited(ctx, first.ID, 3)
	if err != nil || len(pruned) != 0 {
		t.Fatalf("HistoryLimited(pruned first seed) = (%#v, %v), want empty/nil", pruned, err)
	}

	rollbackFailure := errors.New("injected multi-runtime audit callback failure")
	failingAudit := &articleFailingAuditStore{delegate: holderRuntime, failure: rollbackFailure}
	failingService, err := adminapp.NewDurableService(holderRuntime, failingAudit)
	if err != nil {
		t.Fatalf("NewDurableService(failing audit): %v", err)
	}
	if _, err := failingService.Create(ctx, config.PrincipalID, adminapp.Input{Title: "Rolled back"}); !errors.Is(err, rollbackFailure) {
		t.Fatalf("Create(audit callback failure) error = %v, want injected failure", err)
	}
	if failingAudit.calls != 1 {
		t.Fatalf("failing audit calls = %d, want 1", failingAudit.calls)
	}
	assertArticleMultiRuntimeAudit(t, ctx, contenderRaw, wantAudit)
	page, err := contenderService.List(ctx, adminapp.ListOptions{Limit: 10})
	if err != nil || page.Total != 4 || len(page.Articles) != 4 {
		t.Fatalf("List(after callback rollback) = (%#v, %v), want four committed Articles", page, err)
	}
	for _, article := range page.Articles {
		if article.Title == "Rolled back" {
			t.Fatalf("callback failure committed Article %#v", article)
		}
	}
}

type articleMultiRuntimeBackend struct {
	*sqlite.Backend
	holder  bool
	barrier *articleCoordinationBarrier
}

func (backend *articleMultiRuntimeBackend) CoordinatedAtomic(
	ctx context.Context,
	callback func(db.Session) error,
) error {
	barrier := backend.barrier
	if barrier == nil {
		return backend.Backend.CoordinatedAtomic(ctx, callback)
	}
	if backend.holder {
		return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
			barrier.holderCallbacks.Add(1)
			barrier.holderEnteredOnce.Do(func() { close(barrier.holderEntered) })
			select {
			case <-barrier.releaseHolder:
			case <-ctx.Done():
				return ctx.Err()
			}
			return callback(session)
		})
	}
	barrier.contenderAttempts.Add(1)
	barrier.contenderAttemptedOnce.Do(func() { close(barrier.contenderAttempted) })
	return backend.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		barrier.contenderCallbacks.Add(1)
		barrier.contenderEnteredOnce.Do(func() { close(barrier.contenderEntered) })
		return callback(session)
	})
}

type articleCoordinationBarrier struct {
	holderEntered      chan struct{}
	releaseHolder      chan struct{}
	contenderAttempted chan struct{}
	contenderEntered   chan struct{}

	holderEnteredOnce      sync.Once
	releaseHolderOnce      sync.Once
	contenderAttemptedOnce sync.Once
	contenderEnteredOnce   sync.Once
	holderCallbacks        atomic.Int64
	contenderAttempts      atomic.Int64
	contenderCallbacks     atomic.Int64
}

func newArticleCoordinationBarrier() *articleCoordinationBarrier {
	return &articleCoordinationBarrier{
		holderEntered:      make(chan struct{}),
		releaseHolder:      make(chan struct{}),
		contenderAttempted: make(chan struct{}),
		contenderEntered:   make(chan struct{}),
	}
}

func (barrier *articleCoordinationBarrier) release() {
	barrier.releaseHolderOnce.Do(func() { close(barrier.releaseHolder) })
}

type articleCreateResult struct {
	article adminapp.Article
	err     error
}

func waitArticleCreateResult(t *testing.T, results <-chan articleCreateResult) articleCreateResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for Article mutation")
		return articleCreateResult{}
	}
}

func waitArticleSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}

type articleFailingAuditStore struct {
	delegate *systemstate.Runtime
	failure  error
	calls    int
}

func (store *articleFailingAuditStore) AppendAudit(
	ctx context.Context,
	session db.Session,
	event admin.PreparedEvent,
) error {
	store.calls++
	if err := store.delegate.AppendAudit(ctx, session, event); err != nil {
		return err
	}
	return store.failure
}

func (store *articleFailingAuditStore) AuditHistory(
	ctx context.Context,
	model string,
	objectID int64,
	limit int,
) ([]admin.AuditEntry, error) {
	return store.delegate.AuditHistory(ctx, model, objectID, limit)
}

type articleMultiRuntimeAuditRow struct {
	sequence int64
	objectID int64
	action   admin.Action
}

var (
	articleMultiRuntimeAuditID = query.NewFieldRef("id", "id", query.FieldInteger, false)
	articleMultiRuntimeObject  = query.NewFieldRef("object_id", "object_id", query.FieldString, false)
	articleMultiRuntimeAction  = query.NewFieldRef("action", "action", query.FieldString, false)
)

func assertArticleMultiRuntimeAudit(
	t *testing.T,
	ctx context.Context,
	backend db.Queryer,
	want []articleMultiRuntimeAuditRow,
) {
	t.Helper()
	plan := query.NewPlan("godj_system_audit", []query.FieldRef{
		articleMultiRuntimeAuditID,
		articleMultiRuntimeObject,
		articleMultiRuntimeAction,
	}).WithOrderings(query.NewOrdering(articleMultiRuntimeAuditID, query.Ascending))
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		t.Fatalf("query global audit rows: %v", err)
	}
	if rows == nil {
		t.Fatal("query global audit rows returned nil rows")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close global audit rows: %v", err)
		}
	}()
	got := make([]articleMultiRuntimeAuditRow, 0, len(want))
	for rows.Next() {
		var row articleMultiRuntimeAuditRow
		var objectID, action string
		if err := rows.Scan(&row.sequence, &objectID, &action); err != nil {
			t.Fatalf("scan global audit row: %v", err)
		}
		row.objectID, err = strconv.ParseInt(objectID, 10, 64)
		if err != nil {
			t.Fatalf("parse global audit object id %q: %v", objectID, err)
		}
		row.action = admin.Action(action)
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate global audit rows: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("global audit rows = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("global audit row %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func openArticleMultiRuntimeBackend(t *testing.T, ctx context.Context, dataSourceName string) *sqlite.Backend {
	t.Helper()
	backend, err := sqlite.Open(ctx, dataSourceName)
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dataSourceName, err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close Article multi-runtime backend: %v", err)
		}
	})
	return backend
}

func migrateArticleSystemState(t *testing.T, ctx context.Context, backend *sqlite.Backend) {
	t.Helper()
	repository := articleMultiRuntimeRepositoryRoot(t)
	document, err := os.ReadFile(filepath.Join(
		repository,
		"examples",
		"article",
		"migrations",
		"0001_initial.godj.json",
	))
	if err != nil {
		t.Fatalf("read Article definition: %v", err)
	}
	loaded, _, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "examples/article/migrations/0001_initial.godj.json",
			Document: document,
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatalf("load Article and system-state definitions: %v", err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(
		ctx,
		loaded,
		migrations.LatestLifecycleRequest(),
	); err != nil {
		t.Fatalf("migrate Article and system-state definitions: %v", err)
	}
}

func articleMultiRuntimeRepositoryRoot(t *testing.T) string {
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
			t.Fatal("cannot locate repository root from Article multi-runtime test")
		}
		directory = parent
	}
}

func articleMultiRuntimeConfig(t *testing.T, auditCapacity int) systemstate.BootstrapConfig {
	t.Helper()
	permission, err := auth.NewPermission("article.manage")
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{
		Iterations: 10_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x58}, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return systemstate.BootstrapConfig{
		Username:       "article-multi-runtime-admin",
		Password:       "article-multi-runtime-password-marker",
		PrincipalID:    "article-multi-runtime-principal",
		Active:         true,
		Permissions:    []auth.Permission{permission},
		PasswordHasher: hasher,
		SessionLimits:  sessions.DefaultLimits(),
		MaxSessions:    8,
		AuditCapacity:  auditCapacity,
	}
}
