//go:build darwin || linux

package runserverproduct_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

const (
	runserverPostgresTestURLEnv  = "GODJ_TEST_POSTGRES_URL"
	runserverPostgresRequiredEnv = "GODJ_REQUIRE_POSTGRES"
)

type runserverPostgresArticleFixture struct {
	id        int64
	title     string
	published bool
	summary   *string
}

func TestGlobalRunserverArticlePostgresDevelopmentLoop(t *testing.T) {
	databaseURL := runserverPostgresTestURL(t)
	repository := runserverRepositoryRoot(t)
	articleRoot := filepath.Join(repository, "examples", "article")
	descriptor := filepath.Join(articleRoot, "godj.toml")
	schema := createRunserverPostgresSchema(t, databaseURL)
	prepareRunserverArticlePostgresDatabase(t, repository, databaseURL, schema)
	globalBinary := buildGlobalGodj(t, repository)

	workspaceBase := filepath.Join(t.TempDir(), "runserver-workspaces")
	if err := os.Mkdir(workspaceBase, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := runserverPostgresEnvironment(t, databaseURL, schema, workspaceBase)
	environment, goAuditLog := runserverGoAuditEnvironment(t, environment)
	assertRunserverPostgresEnvironment(t, environment, databaseURL, schema)
	before := snapshotRunserverProjectTree(t, articleRoot)
	address := reserveRunserverLoopbackAddress(t, "")

	body := runGlobalPostgresArticleServerOnce(t, globalBinary, repository, descriptor, address, environment, databaseURL)
	assertAdvancedArticleResponse(t, body)
	assertRunserverWorkspaceEmpty(t, workspaceBase)
	verifyRunserverArticlePostgresDatabase(t, databaseURL, schema)
	after := snapshotRunserverProjectTree(t, articleRoot)
	if !runserverProjectTreesEqual(before, after) {
		t.Fatal("global PostgreSQL runserver changed the Article project tree")
	}
	assertRunserverGoBuildAudit(t, goAuditLog, []string{"./cmd/projectrunner", "./cmd/site"})
}

func runserverPostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(runserverPostgresTestURLEnv))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(runserverPostgresRequiredEnv) == "1" {
		t.Fatalf("%s=1 requires %s", runserverPostgresRequiredEnv, runserverPostgresTestURLEnv)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; runserver PostgreSQL product E2E was not run")
	return ""
}

func createRunserverPostgresSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect runserver PostgreSQL database: %v", runserverPostgresSafeError(err))
	}
	schema := fmt.Sprintf("godj_runserver_article_%d_%d", os.Getpid(), time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create isolated runserver PostgreSQL schema: %v", runserverPostgresSafeError(err))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop isolated runserver PostgreSQL schema: %v", runserverPostgresSafeError(err))
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close runserver PostgreSQL admin connection: %v", runserverPostgresSafeError(err))
		}
	})
	return schema
}

func prepareRunserverArticlePostgresDatabase(t *testing.T, repository, databaseURL, schema string) {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(repository, "examples", "article", "testdata", "postgres", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{
			SourceID: "examples/article/testdata/postgres/0001_initial.godj.json",
			Document: document,
		},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatalf("load Article and system PostgreSQL initial migrations: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 4 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Article and system PostgreSQL initial migration load report = %+v", report)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("open Article PostgreSQL migration backend: %v", runserverPostgresSafeError(err))
	}
	closed := false
	defer func() {
		if !closed {
			_ = backend.Close()
		}
	}()
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("migrate Article and system PostgreSQL database: %v", runserverPostgresSafeError(err))
	}
	for index, fixture := range runserverPostgresArticleFixtures() {
		input := articlemodels.NewArticleCreate(fixture.title).WithPublished(fixture.published)
		if fixture.summary == nil {
			input = input.WithSummaryNull()
		} else {
			input = input.WithSummary(*fixture.summary)
		}
		article, err := articlemodels.ArticleObjects.Create(ctx, backend, input)
		if err != nil {
			t.Fatalf("seed Article PostgreSQL row %d: %v", index, runserverPostgresSafeError(err))
		}
		if article.ID != fixture.id || article.Title != fixture.title || article.Published != fixture.published ||
			!runserverOptionalStringEqual(article.Summary, fixture.summary) {
			t.Fatalf("seeded Article PostgreSQL row %d did not match the exact fixture", index)
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close prepared Article PostgreSQL database: %v", runserverPostgresSafeError(err))
	}
	closed = true
}

func runGlobalPostgresArticleServerOnce(
	t *testing.T,
	globalBinary, repository, descriptor, expectedAddress string,
	environment []string,
	databaseURL string,
) string {
	t.Helper()
	stdout := newReadinessOutput()
	stderr := &synchronizedOutput{}
	command := exec.Command(
		globalBinary,
		"runserver",
		"--project", descriptor,
		"--addr", expectedAddress,
	)
	command.Dir = repository
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		t.Fatalf("start global PostgreSQL runserver: %v", err)
	}
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()
	finished := false
	var knownProcessGroups []int
	defer func() {
		if !finished {
			_ = interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
		}
	}()

	var address string
	readyTimer := time.NewTimer(2 * time.Minute)
	defer readyTimer.Stop()
	select {
	case address = <-stdout.ready:
		assertRunserverPostgresOutputSecretFree(t, databaseURL, stdout.String(), stderr.String())
		if err := validateArticleReadinessAddress(address); err != nil {
			t.Fatalf("invalid PostgreSQL Article readiness address %q: %v", address, err)
		}
		if address != expectedAddress {
			t.Fatalf("PostgreSQL Article readiness address = %q, want reserved %q", address, expectedAddress)
		}
		groups, err := runserverOwnedProcessGroups(command.Process.Pid)
		knownProcessGroups = groups
		if err != nil || len(groups) < 2 {
			t.Fatalf("capture global/PostgreSQL runtime process groups = %v, error=%v", groups, err)
		}
	case waitErr := <-waited:
		finished = true
		assertRunserverPostgresOutputSecretFree(t, databaseURL, stdout.String(), stderr.String())
		t.Fatalf("global PostgreSQL runserver exited before readiness: %v; stdout_bytes=%d stderr_bytes=%d", waitErr, len(stdout.String()), len(stderr.String()))
	case <-readyTimer.C:
		cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
		finished = true
		assertRunserverPostgresOutputSecretFree(t, databaseURL, stdout.String(), stderr.String())
		t.Fatalf("global PostgreSQL runserver readiness timed out: cleanup=%+v stdout_bytes=%d stderr_bytes=%d", cleanup, len(stdout.String()), len(stderr.String()))
	}

	body, requestErr := requestAdvancedArticlePage(address)
	if requestErr != nil {
		cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
		finished = true
		assertRunserverPostgresOutputSecretFree(t, databaseURL, stdout.String(), stderr.String())
		t.Fatalf("request global PostgreSQL Article runtime: %v; cleanup=%+v", requestErr, cleanup)
	}
	cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
	finished = true
	assertRunserverPostgresOutputSecretFree(t, databaseURL, stdout.String(), stderr.String())
	if cleanup.failed() || len(cleanup.ProcessGroups) < 2 {
		t.Fatalf("clean global PostgreSQL runserver interrupt: cleanup=%+v stdout_bytes=%d stderr_bytes=%d", cleanup, len(stdout.String()), len(stderr.String()))
	}
	if stderr.String() != "" {
		t.Fatalf("global PostgreSQL runserver stderr bytes = %d, want 0", len(stderr.String()))
	}
	readinessLine := articleReadinessPrefix + address + "\n"
	if output := stdout.String(); strings.Count(output, readinessLine) != 1 || output != readinessLine {
		t.Fatalf("global PostgreSQL runserver stdout bytes = %d, want exact readiness line bytes = %d", len(output), len(readinessLine))
	}
	return body
}

func runserverPostgresEnvironment(t *testing.T, databaseURL, schema, workspaceBase string) []string {
	t.Helper()
	values := environmentMap(os.Environ())
	delete(values, articleSQLiteDatabaseEnv)
	delete(values, runserverPostgresTestURLEnv)
	delete(values, runserverPostgresRequiredEnv)
	delete(values, articleAdminUsernameEnv)
	delete(values, articleAdminPasswordEnv)
	values[articlePostgresURLEnv] = databaseURL
	values[articlePostgresSchemaEnv] = schema
	values["TMPDIR"] = workspaceBase
	values["GOWORK"] = "off"
	values["GOTOOLCHAIN"] = "local"
	values["GOENV"] = "off"
	values["GOFLAGS"] = ""
	values["GOCACHEPROG"] = ""
	values["GOPROXY"] = "off"
	if strings.TrimSpace(values["HOME"]) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		values["HOME"] = home
	}
	return sortedRunserverEnvironment(values)
}

func assertRunserverPostgresEnvironment(t *testing.T, environment []string, databaseURL, schema string) {
	t.Helper()
	values := environmentMap(environment)
	if _, exists := values[articleSQLiteDatabaseEnv]; exists {
		t.Fatal("PostgreSQL Article runtime environment retained SQLite configuration")
	}
	if _, exists := values[runserverPostgresTestURLEnv]; exists {
		t.Fatal("PostgreSQL Article runtime environment retained test-only database URL")
	}
	if _, exists := values[runserverPostgresRequiredEnv]; exists {
		t.Fatal("PostgreSQL Article runtime environment retained test-only required sentinel")
	}
	if values[articlePostgresURLEnv] != databaseURL || values[articlePostgresSchemaEnv] != schema {
		t.Fatal("PostgreSQL Article runtime environment did not retain exact project-owned database configuration")
	}
}

func assertRunserverPostgresOutputSecretFree(t *testing.T, databaseURL, stdout, stderr string) {
	t.Helper()
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse runserver PostgreSQL test URL: PostgreSQL URL is invalid")
	}
	for _, output := range []string{stdout, stderr} {
		if strings.Contains(output, databaseURL) {
			t.Fatal("global PostgreSQL runserver output exposed the database URL")
		}
	}
	if connectionConfig.Password != "" && strings.Contains(stderr, connectionConfig.Password) {
		t.Fatal("global PostgreSQL runserver stderr exposed the database password")
	}
}

func verifyRunserverArticlePostgresDatabase(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatalf("reopen durable Article PostgreSQL database: %v", runserverPostgresSafeError(err))
	}
	history, historyErr := backend.ReadAppliedMigrations(ctx)
	articles, articleErr := articlemodels.ArticleObjects.Using(backend).
		OrderBy(articlemodels.ArticleFields.ID.Asc()).
		All(ctx)
	closeErr := backend.Close()
	if err := errors.Join(historyErr, articleErr, closeErr); err != nil {
		t.Fatalf("inspect durable Article PostgreSQL database: %v", runserverPostgresSafeError(err))
	}
	systemMigration := systemstate.InitialMigrationKey()
	if len(history) != 2 ||
		history[0].App != "godj_conformance" || history[0].Name != "0001_initial" ||
		history[1].App != systemMigration.App || history[1].Name != systemMigration.Name {
		t.Fatalf("durable Article PostgreSQL migration history = %+v", history)
	}
	expected := runserverPostgresArticleFixtures()
	if len(articles) != len(expected) {
		t.Fatalf("durable Article PostgreSQL rows = %d, want %d", len(articles), len(expected))
	}
	for index, want := range expected {
		article := articles[index]
		if article.ID != want.id || article.Title != want.title || article.Published != want.published || !runserverOptionalStringEqual(article.Summary, want.summary) {
			t.Fatalf("durable Article PostgreSQL row %d = %+v, want %+v", index, article, want)
		}
	}
}

func runserverPostgresArticleFixtures() []runserverPostgresArticleFixture {
	return []runserverPostgresArticleFixture{
		{id: 1, title: "Go Launch", published: true},
		{id: 2, title: "Rust Notes", published: true, summary: runserverString("A Go summary")},
		{id: 3, title: "Go Draft", published: true, summary: runserverString("Release candidate")},
		{id: 4, title: "Go Hidden"},
		{id: 5, title: "100% Coverage", published: true, summary: runserverString("under_score")},
		{id: 6, title: "1000 Coverage", published: true, summary: runserverString("underXscore")},
		{id: 7, title: "Other", published: true, summary: runserverString("")},
		{id: 8, title: "Go Mirror", published: true, summary: runserverString("Go Mirror")},
		{id: 9, title: "Go Split", published: true, summary: runserverString("Elsewhere")},
	}
}

func runserverPostgresSafeError(err error) error {
	if err == nil {
		return nil
	}
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL operation timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL operation was canceled")
	}
	return errors.New("PostgreSQL operation failed")
}

func runserverProjectTreesEqual(left, right map[string]runserverProjectTreeEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftEntry := range left {
		if rightEntry, exists := right[path]; !exists || rightEntry != leftEntry {
			return false
		}
	}
	return true
}
