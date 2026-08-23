//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/progresshans/godj/db/sqlite"
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
)

const (
	articleSQLiteDatabaseEnv = "GODJ_ARTICLE_SQLITE_DATABASE"
	articlePostgresURLEnv    = "GODJ_ARTICLE_POSTGRES_URL"
	articlePostgresSchemaEnv = "GODJ_ARTICLE_POSTGRES_SCHEMA"
	articleReadinessPrefix   = "article site listening on http://"
	articleListPath          = "/articles/"
	runserverHarnessModeEnv  = "GODJ_RUNSERVERPRODUCT_HELPER_MODE"
	runserverHarnessReadyEnv = "GODJ_RUNSERVERPRODUCT_HELPER_READY"
	runserverGoAuditModeEnv  = "GODJ_RUNSERVERPRODUCT_GO_AUDIT_MODE"
	runserverGoAuditRealEnv  = "GODJ_RUNSERVERPRODUCT_GO_AUDIT_REAL"
	runserverGoAuditLogEnv   = "GODJ_RUNSERVERPRODUCT_GO_AUDIT_LOG"
)

func TestMain(m *testing.M) {
	if os.Getenv(runserverGoAuditModeEnv) == "1" {
		os.Exit(runAuditedGoCommand())
	}
	os.Exit(m.Run())
}

func runAuditedGoCommand() int {
	realGo := os.Getenv(runserverGoAuditRealEnv)
	logPath := os.Getenv(runserverGoAuditLogEnv)
	if realGo == "" || logPath == "" {
		return 95
	}
	payload, err := json.Marshal(os.Args[1:])
	if err != nil {
		return 96
	}
	payload = append(payload, '\n')
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 97
	}
	_, writeErr := logFile.Write(payload)
	closeErr := logFile.Close()
	if errors.Join(writeErr, closeErr) != nil {
		return 98
	}
	if err := syscall.Exec(realGo, append([]string{realGo}, os.Args[1:]...), os.Environ()); err != nil {
		return 99
	}
	return 0
}

func TestRunserverProductCleanupHelper(t *testing.T) {
	mode := os.Getenv(runserverHarnessModeEnv)
	if mode == "" {
		return
	}
	signal.Ignore(os.Interrupt)
	switch mode {
	case "supervisor":
		environment := environmentMap(os.Environ())
		environment[runserverHarnessModeEnv] = "descendant"
		child := exec.Command(os.Args[0], "-test.run=^TestRunserverProductCleanupHelper$")
		child.Env = sortedRunserverEnvironment(environment)
		child.Stdout = io.Discard
		child.Stderr = io.Discard
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := child.Start(); err != nil {
			os.Exit(91)
		}
		if err := os.WriteFile(os.Getenv(runserverHarnessReadyEnv), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_ = child.Process.Kill()
			os.Exit(92)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "descendant":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(93)
	}
}

func TestRunserverHarnessForcedCleanupIncludesSeparateDescendantGroup(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	environment := environmentMap(os.Environ())
	environment[runserverHarnessModeEnv] = "supervisor"
	environment[runserverHarnessReadyEnv] = ready
	command := exec.Command(os.Args[0], "-test.run=^TestRunserverProductCleanupHelper$")
	command.Env = sortedRunserverEnvironment(environment)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	finished := false
	knownProcessGroups := []int{command.Process.Pid}
	defer func() {
		if !finished {
			_ = interruptAndWaitRunserver(command, waited, time.Second, knownProcessGroups...)
		}
	}()
	descendantPID := waitForRunserverHarnessPID(t, ready)
	knownProcessGroups = append(knownProcessGroups, descendantPID)
	result := interruptAndWaitRunserver(command, waited, 50*time.Millisecond, knownProcessGroups...)
	finished = true
	if !result.Forced || result.SignalError != nil || result.DiscoveryError != nil || result.AbsenceError != nil || result.WaitError == nil {
		t.Fatalf("forced harness cleanup = %+v", result)
	}
	if !containsRunserverProcessGroup(result.ProcessGroups, descendantPID) || !containsRunserverProcessGroup(result.ProcessGroups, command.Process.Pid) {
		t.Fatalf("forced harness process groups = %v, want global %d and descendant %d", result.ProcessGroups, command.Process.Pid, descendantPID)
	}
}

func TestGlobalRunserverArticleSQLiteDevelopmentLoop(t *testing.T) {
	repository := runserverRepositoryRoot(t)
	articleRoot := filepath.Join(repository, "examples", "article")
	descriptor := filepath.Join(articleRoot, "godj.toml")
	databasePath := filepath.Join(t.TempDir(), "article.sqlite3")
	prepareRunserverArticleDatabase(t, repository, databasePath)
	globalBinary := buildGlobalGodj(t, repository)

	workspaceBase := filepath.Join(t.TempDir(), "runserver-workspaces")
	if err := os.Mkdir(workspaceBase, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := runserverEnvironment(t, databasePath, workspaceBase)
	environment, goAuditLog := runserverGoAuditEnvironment(t, environment)
	before := snapshotRunserverProjectTree(t, articleRoot)
	address := ""

	for attempt := 1; attempt <= 2; attempt++ {
		t.Run("start-stop-"+strconv.Itoa(attempt), func(t *testing.T) {
			address = reserveRunserverLoopbackAddress(t, address)
			body := runGlobalArticleServerOnce(t, globalBinary, repository, descriptor, address, environment)
			assertAdvancedArticleResponse(t, body)
			assertRunserverWorkspaceEmpty(t, workspaceBase)
			verifyRunserverArticleDatabase(t, databasePath)
			after := snapshotRunserverProjectTree(t, articleRoot)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("global runserver changed the Article project tree\nbefore=%v\nafter=%v", before, after)
			}
			wantPackages := make([]string, 0, attempt*2)
			for index := 0; index < attempt; index++ {
				wantPackages = append(wantPackages, "./cmd/projectrunner", "./cmd/site")
			}
			assertRunserverGoBuildAudit(t, goAuditLog, wantPackages)
		})
	}
}

func TestGlobalRunserverRejectsStaleCopiedArticleBeforeRuntime(t *testing.T) {
	repository := runserverRepositoryRoot(t)
	globalBinary := buildGlobalGodj(t, repository)
	base := prepareCopiedRunserverArticleProject(t, repository)
	address := reserveRunserverLoopbackAddress(t, "")
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "missing-generated-file",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "models", "zz_godj_generated.go")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "modified-manifest-file-mismatch",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, "models", "zz_godj_generated.go")
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := io.WriteString(file, "\n// stale copied Article candidate\n")
				closeErr := file.Close()
				if err := errors.Join(writeErr, closeErr); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "interrupted-publication-transaction",
			mutate: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, ".godj", "transactions", "interrupted-sentinel")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("incomplete"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projectRoot := filepath.Join(t.TempDir(), "article")
			copyRunserverTree(t, base, projectRoot)
			test.mutate(t, projectRoot)
			databasePath := filepath.Join(t.TempDir(), "must-not-exist.sqlite3")
			workspaceBase := filepath.Join(t.TempDir(), "runserver-workspaces")
			if err := os.Mkdir(workspaceBase, 0o700); err != nil {
				t.Fatal(err)
			}
			environment := runserverEnvironment(t, databasePath, workspaceBase)
			environment, goAuditLog := runserverGoAuditEnvironment(t, environment)
			before := snapshotRunserverProjectTree(t, projectRoot)
			stdout, stderr, exit := runStaleCopiedArticle(t, globalBinary, repository, filepath.Join(projectRoot, "godj.toml"), address, environment)
			if exit != 1 || stdout != "" || stderr != "project_runserver_generation_error/generated_bundle_stale\n" {
				t.Fatalf("stale copied Article exit/stdout/stderr = %d/%q/%q", exit, stdout, stderr)
			}
			if _, err := os.Lstat(databasePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale copied Article created runtime database: %v", err)
			}
			assertRunserverWorkspaceEmpty(t, workspaceBase)
			after := snapshotRunserverProjectTree(t, projectRoot)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("stale runserver changed copied Article project\nbefore=%v\nafter=%v", before, after)
			}
			assertRunserverGoBuildAudit(t, goAuditLog, []string{"./cmd/projectrunner"})
		})
	}
}

func runStaleCopiedArticle(
	t *testing.T,
	globalBinary, repository, descriptor, address string,
	environment []string,
) (string, string, int) {
	t.Helper()
	stdout := &synchronizedOutput{}
	stderr := &synchronizedOutput{}
	command := exec.Command(globalBinary, "runserver", "--project", descriptor, "--addr", address)
	command.Dir = repository
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	select {
	case waitErr := <-waited:
		if absenceErr := waitForRunserverProcessGroupsAbsent([]int{command.Process.Pid}, 2*time.Second); absenceErr != nil {
			t.Fatal(absenceErr)
		}
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatalf("stale copied Article wait error = %v, want exit error", waitErr)
		}
		return stdout.String(), stderr.String(), exitError.ExitCode()
	case <-timer.C:
		cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second)
		t.Fatalf("stale copied Article timed out: cleanup=%+v stdout=%q stderr=%q", cleanup, stdout.String(), stderr.String())
		return "", "", -1
	}
}

func prepareCopiedRunserverArticleProject(t *testing.T, repository string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "article-base")
	copyRunserverTree(t, filepath.Join(repository, "examples", "article"), root)
	rootModule, err := os.ReadFile(filepath.Join(repository, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	const moduleDeclaration = "module github.com/progresshans/godj\n"
	if !strings.HasPrefix(string(rootModule), moduleDeclaration) || strings.Count(string(rootModule), moduleDeclaration) != 1 {
		t.Fatal("repository go.mod has an unexpected module declaration")
	}
	document := strings.Replace(string(rootModule), moduleDeclaration, "module example.com/godj-runserver-fixture\n", 1)
	document += fmt.Sprintf(`
require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, strconv.Quote(filepath.ToSlash(repository)))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(repository, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), sums, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyRunserverTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("copy Article project: unsupported file %s", relative)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copy Article project: %v", err)
	}
}

func prepareRunserverArticleDatabase(t *testing.T, repository, databasePath string) {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(repository, "examples", "article", "testdata", "postgres", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, report, err := migrationdefinition.Load(migrationdefinition.Source{
		SourceID: "examples/article/testdata/postgres/0001_initial.godj.json",
		Document: document,
	})
	if err != nil {
		t.Fatalf("load Article initial migration: %v", err)
	}
	if report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Article initial migration load report = %+v", report)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open external Article SQLite database: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = backend.Close()
		}
	}()
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("migrate external Article SQLite database: %v", err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, 'Go Launch', TRUE, NULL),
  (2, 'Rust Notes', TRUE, 'A Go summary'),
  (3, 'Go Draft', TRUE, 'Release candidate'),
  (4, 'Go Hidden', FALSE, NULL),
  (5, '100% Coverage', TRUE, 'under_score'),
  (6, '1000 Coverage', TRUE, 'underXscore'),
  (7, 'Other', TRUE, ''),
  (8, 'Go Mirror', TRUE, 'Go Mirror'),
  (9, 'Go Split', TRUE, 'Elsewhere')`); err != nil {
		t.Fatalf("seed external Article SQLite database: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close prepared Article SQLite database: %v", err)
	}
	closed = true
	info, err := os.Stat(databasePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("prepared Article SQLite database = %+v, error=%v", info, err)
	}
}

func buildGlobalGodj(t *testing.T, repository string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "godj")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-mod=readonly", "-o", binary, "./cmd/godj")
	command.Dir = repository
	command.Env = offlineRunserverEnvironment(os.Environ())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build global godj: %v\n%s", err, output)
	}
	return binary
}

func runGlobalArticleServerOnce(
	t *testing.T,
	globalBinary, repository, descriptor, expectedAddress string,
	environment []string,
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
		t.Fatalf("start global runserver: %v", err)
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
		if err := validateArticleReadinessAddress(address); err != nil {
			t.Fatalf("invalid Article readiness address %q: %v", address, err)
		}
		if address != expectedAddress {
			t.Fatalf("Article readiness address = %q, want reserved %q", address, expectedAddress)
		}
		groups, err := runserverOwnedProcessGroups(command.Process.Pid)
		knownProcessGroups = groups
		if err != nil || len(groups) < 2 {
			t.Fatalf("capture global/runtime process groups = %v, error=%v", groups, err)
		}
	case waitErr := <-waited:
		finished = true
		t.Fatalf("global runserver exited before readiness: %v; stdout=%q stderr=%q", waitErr, stdout.String(), stderr.String())
	case <-readyTimer.C:
		cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
		finished = true
		t.Fatalf("global runserver readiness timed out: cleanup=%+v stdout=%q stderr=%q", cleanup, stdout.String(), stderr.String())
	}

	body, requestErr := requestAdvancedArticlePage(address)
	if requestErr != nil {
		cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
		finished = true
		t.Fatalf("request global Article runtime: %v; cleanup=%+v stdout=%q stderr=%q", requestErr, cleanup, stdout.String(), stderr.String())
	}
	cleanup := interruptAndWaitRunserver(command, waited, 20*time.Second, knownProcessGroups...)
	finished = true
	if cleanup.failed() || len(cleanup.ProcessGroups) < 2 {
		t.Fatalf("clean global runserver interrupt: cleanup=%+v stdout=%q stderr=%q", cleanup, stdout.String(), stderr.String())
	}
	if stderr.String() != "" {
		t.Fatalf("global runserver stderr = %q, want empty", stderr.String())
	}
	readinessLine := articleReadinessPrefix + address + "\n"
	if output := stdout.String(); strings.Count(output, readinessLine) != 1 || output != readinessLine {
		t.Fatalf("global runserver stdout = %q, want exact %q", output, readinessLine)
	}
	return body
}

func reserveRunserverLoopbackAddress(t *testing.T, previous string) string {
	t.Helper()
	candidate := previous
	if candidate == "" {
		candidate = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp4", candidate)
	if err != nil {
		t.Fatalf("reserve runserver loopback address %q: %v", candidate, err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release reserved runserver loopback address %q: %v", address, err)
	}
	if previous != "" && address != previous {
		t.Fatalf("re-reserved runserver address = %q, want %q", address, previous)
	}
	if err := validateArticleReadinessAddress(address); err != nil {
		t.Fatalf("reserved runserver address %q: %v", address, err)
	}
	return address
}

func requestAdvancedArticlePage(address string) (string, error) {
	values := url.Values{
		"max_id":                {"9"},
		"min_id":                {"8"},
		"q":                     {"go"},
		"title_matches_summary": {"true"},
	}
	requestURL := "http://" + address + articleListPath + "?" + values.Encode()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get(requestURL)
		if err != nil {
			if time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return "", err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return "", errors.Join(readErr, closeErr)
		}
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("GET %s status = %d, body=%q", requestURL, response.StatusCode, body)
		}
		if response.Header.Get("Content-Type") != "text/html; charset=utf-8" {
			return "", fmt.Errorf("GET %s content type = %q", requestURL, response.Header.Get("Content-Type"))
		}
		return string(body), nil
	}
}

func assertAdvancedArticleResponse(t *testing.T, body string) {
	t.Helper()
	for _, present := range []string{
		`data-article-id="8"`,
		`Go Mirror`,
		`id="article-report" data-matching-count="1" data-latest-id="8"`,
		`id="article-pagination" data-offset="0" data-limit="20" data-page-count="1"`,
	} {
		if !strings.Contains(body, present) {
			t.Errorf("advanced Article response is missing %q: %q", present, body)
		}
	}
	for _, absent := range []string{`data-article-id="7"`, `data-article-id="9"`, `Go Split`} {
		if strings.Contains(body, absent) {
			t.Errorf("advanced Article response unexpectedly contains %q: %q", absent, body)
		}
	}
}

func verifyRunserverArticleDatabase(t *testing.T, databasePath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	backend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen durable Article SQLite database: %v", err)
	}
	history, historyErr := backend.ReadAppliedMigrations(ctx)
	articles, articleErr := articlemodels.ArticleObjects.Using(backend).
		OrderBy(articlemodels.ArticleFields.ID.Asc()).
		All(ctx)
	closeErr := backend.Close()
	if err := errors.Join(historyErr, articleErr, closeErr); err != nil {
		t.Fatalf("inspect durable Article SQLite database: %v", err)
	}
	if len(history) != 1 || history[0].App != "godj_conformance" || history[0].Name != "0001_initial" {
		t.Fatalf("durable Article migration history = %+v", history)
	}
	expected := []struct {
		id        int64
		title     string
		published bool
		summary   *string
	}{
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
	if len(articles) != len(expected) {
		t.Fatalf("durable Article rows = %+v", articles)
	}
	for index, want := range expected {
		article := articles[index]
		if article.ID != want.id || article.Title != want.title || article.Published != want.published || !runserverOptionalStringEqual(article.Summary, want.summary) {
			t.Fatalf("durable Article row %d = %+v, want %+v", index, article, want)
		}
	}
}

func runserverString(value string) *string {
	return &value
}

func runserverOptionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type runserverCleanupResult struct {
	WaitError      error
	SignalError    error
	DiscoveryError error
	AbsenceError   error
	Forced         bool
	ProcessGroups  []int
}

func (result runserverCleanupResult) failed() bool {
	return result.WaitError != nil || result.SignalError != nil || result.DiscoveryError != nil || result.AbsenceError != nil || result.Forced
}

func interruptAndWaitRunserver(command *exec.Cmd, waited <-chan error, timeout time.Duration, knownGroups ...int) runserverCleanupResult {
	result := runserverCleanupResult{}
	groups, discoveryErr := runserverOwnedProcessGroups(command.Process.Pid)
	result.ProcessGroups = mergeRunserverProcessGroups(knownGroups, groups)
	result.DiscoveryError = discoveryErr
	result.SignalError = signalRunserverTestProcessGroup(command.Process.Pid, syscall.SIGINT)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case waitErr := <-waited:
		result.WaitError = waitErr
	case <-timer.C:
		result.Forced = true
		refreshed, refreshErr := runserverOwnedProcessGroups(command.Process.Pid)
		result.ProcessGroups = mergeRunserverProcessGroups(result.ProcessGroups, refreshed)
		result.DiscoveryError = errors.Join(result.DiscoveryError, refreshErr)
		killErr := killRunserverTestProcessGroups(result.ProcessGroups, command.Process.Pid)
		result.WaitError = errors.Join(killErr, boundedRunserverWait(waited, 3*time.Second))
	}
	result.AbsenceError = waitForRunserverProcessGroupsAbsent(result.ProcessGroups, 2*time.Second)
	if result.AbsenceError != nil && !result.Forced {
		result.Forced = true
		killErr := killRunserverTestProcessGroups(result.ProcessGroups, command.Process.Pid)
		result.AbsenceError = errors.Join(result.AbsenceError, killErr, waitForRunserverProcessGroupsAbsent(result.ProcessGroups, 2*time.Second))
	}
	return result
}

func boundedRunserverWait(waited <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case waitErr := <-waited:
		return waitErr
	case <-timer.C:
		return errors.New("runserver process Wait remained blocked after forced cleanup")
	}
}

func runserverOwnedProcessGroups(rootPID int) ([]int, error) {
	output, err := exec.Command("ps", "-Ao", "pid=,ppid=,pgid=").Output()
	if err != nil {
		return []int{rootPID}, fmt.Errorf("inspect runserver process tree: %w", err)
	}
	type process struct {
		pid  int
		ppid int
		pgid int
	}
	var processes []process
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return []int{rootPID}, fmt.Errorf("inspect runserver process tree: invalid ps row")
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		pgid, pgidErr := strconv.Atoi(fields[2])
		if errors.Join(pidErr, ppidErr, pgidErr) != nil {
			return []int{rootPID}, fmt.Errorf("inspect runserver process tree: invalid ps identifier")
		}
		processes = append(processes, process{pid: pid, ppid: ppid, pgid: pgid})
	}
	descendants := map[int]struct{}{rootPID: {}}
	for changed := true; changed; {
		changed = false
		for _, candidate := range processes {
			if _, parentOwned := descendants[candidate.ppid]; !parentOwned {
				continue
			}
			if _, alreadyOwned := descendants[candidate.pid]; alreadyOwned {
				continue
			}
			descendants[candidate.pid] = struct{}{}
			changed = true
		}
	}
	groups := map[int]struct{}{rootPID: {}}
	for _, candidate := range processes {
		if _, owned := descendants[candidate.pid]; owned {
			groups[candidate.pgid] = struct{}{}
		}
	}
	result := make([]int, 0, len(groups))
	for group := range groups {
		if group <= 1 || group == syscall.Getpgrp() {
			return []int{rootPID}, fmt.Errorf("inspect runserver process tree: unsafe process group")
		}
		result = append(result, group)
	}
	sort.Ints(result)
	return result, nil
}

func mergeRunserverProcessGroups(left, right []int) []int {
	unique := make(map[int]struct{}, len(left)+len(right))
	for _, group := range append(append([]int(nil), left...), right...) {
		unique[group] = struct{}{}
	}
	result := make([]int, 0, len(unique))
	for group := range unique {
		result = append(result, group)
	}
	sort.Ints(result)
	return result
}

func containsRunserverProcessGroup(groups []int, target int) bool {
	for _, group := range groups {
		if group == target {
			return true
		}
	}
	return false
}

func waitForRunserverHarnessPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(contents)))
			if parseErr != nil || pid <= 1 {
				t.Fatalf("parse runserver harness PID %q: %v", contents, parseErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("runserver harness did not publish child PID at %s", path)
	return 0
}

func signalRunserverTestProcessGroup(group int, signal syscall.Signal) error {
	if group <= 1 || group == syscall.Getpgrp() {
		return errors.New("refuse to signal unsafe runserver process group")
	}
	err := syscall.Kill(-group, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func killRunserverTestProcessGroups(groups []int, rootGroup int) error {
	var result error
	for index := len(groups) - 1; index >= 0; index-- {
		if groups[index] == rootGroup {
			continue
		}
		result = errors.Join(result, signalRunserverTestProcessGroup(groups[index], syscall.SIGKILL))
	}
	return errors.Join(result, signalRunserverTestProcessGroup(rootGroup, syscall.SIGKILL))
}

func waitForRunserverProcessGroupsAbsent(groups []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var remaining []int
		for _, group := range groups {
			err := syscall.Kill(-group, 0)
			if err == nil || errors.Is(err, syscall.EPERM) {
				remaining = append(remaining, group)
			}
		}
		if len(remaining) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("runserver process groups remain: %v", remaining)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type synchronizedOutput struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (output *synchronizedOutput) Write(payload []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.Write(payload)
}

func (output *synchronizedOutput) String() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.String()
}

type readinessOutput struct {
	synchronizedOutput
	ready   chan string
	once    sync.Once
	scanned int
}

func newReadinessOutput() *readinessOutput {
	return &readinessOutput{ready: make(chan string, 1)}
}

func (output *readinessOutput) Write(payload []byte) (int, error) {
	output.mutex.Lock()
	written, err := output.buffer.Write(payload)
	var addresses []string
	contents := output.buffer.Bytes()
	for output.scanned < len(contents) {
		relativeEnd := bytes.IndexByte(contents[output.scanned:], '\n')
		if relativeEnd < 0 {
			break
		}
		end := output.scanned + relativeEnd
		line := string(contents[output.scanned:end])
		output.scanned = end + 1
		if strings.HasPrefix(line, articleReadinessPrefix) {
			addresses = append(addresses, strings.TrimPrefix(line, articleReadinessPrefix))
		}
	}
	output.mutex.Unlock()
	if err != nil {
		return written, err
	}
	for _, address := range addresses {
		output.once.Do(func() {
			output.ready <- address
		})
	}
	return written, nil
}

func TestReadinessOutputWaitsForCompleteLine(t *testing.T) {
	output := newReadinessOutput()
	first := articleReadinessPrefix + "127.0."
	if written, err := output.Write([]byte(first)); err != nil || written != len(first) {
		t.Fatalf("first readiness write = %d, %v", written, err)
	}
	select {
	case address := <-output.ready:
		t.Fatalf("partial readiness published %q", address)
	default:
	}
	second := "0.1:8123\n"
	if written, err := output.Write([]byte(second)); err != nil || written != len(second) {
		t.Fatalf("second readiness write = %d, %v", written, err)
	}
	select {
	case address := <-output.ready:
		if address != "127.0.0.1:8123" {
			t.Fatalf("complete readiness = %q", address)
		}
	default:
		t.Fatal("complete readiness was not published")
	}
}

func validateArticleReadinessAddress(address string) error {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("address is not an IPv4 loopback host and port")
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("address port is outside the allocated TCP range")
	}
	return nil
}

func runserverEnvironment(t *testing.T, databasePath, workspaceBase string) []string {
	t.Helper()
	values := environmentMap(os.Environ())
	values[articleSQLiteDatabaseEnv] = databasePath
	delete(values, articlePostgresURLEnv)
	delete(values, articlePostgresSchemaEnv)
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

func offlineRunserverEnvironment(input []string) []string {
	values := environmentMap(input)
	values["GOWORK"] = "off"
	values["GOTOOLCHAIN"] = "local"
	values["GOENV"] = "off"
	values["GOFLAGS"] = ""
	values["GOCACHEPROG"] = ""
	values["GOPROXY"] = "off"
	return sortedRunserverEnvironment(values)
}

func runserverGoAuditEnvironment(t *testing.T, input []string) ([]string, string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapperDirectory := t.TempDir()
	wrapperPath := filepath.Join(wrapperDirectory, "go")
	if err := os.Symlink(executable, wrapperPath); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "go-build-audit.jsonl")
	values := environmentMap(input)
	if existingPath := values["PATH"]; existingPath == "" {
		values["PATH"] = wrapperDirectory
	} else {
		values["PATH"] = wrapperDirectory + string(os.PathListSeparator) + existingPath
	}
	values[runserverGoAuditModeEnv] = "1"
	values[runserverGoAuditRealEnv] = realGo
	values[runserverGoAuditLogEnv] = logPath
	return sortedRunserverEnvironment(values), logPath
}

func assertRunserverGoBuildAudit(t *testing.T, logPath string, wantPackages []string) {
	t.Helper()
	payload, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read runserver Go build audit: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(payload), []byte{'\n'})
	if len(lines) != len(wantPackages) {
		t.Fatalf("runserver Go build audit calls = %d, want %d: %s", len(lines), len(wantPackages), payload)
	}
	for index, line := range lines {
		var arguments []string
		if err := json.Unmarshal(line, &arguments); err != nil {
			t.Fatalf("decode runserver Go build audit %d: %v", index, err)
		}
		if len(arguments) != 6 || arguments[0] != "build" || arguments[1] != "-buildvcs=false" || arguments[2] != "-mod=readonly" || arguments[3] != "-o" || !filepath.IsAbs(arguments[4]) || arguments[5] != wantPackages[index] {
			t.Fatalf("runserver Go build audit %d = %q, want exact readonly build for %q", index, arguments, wantPackages[index])
		}
	}
}

func environmentMap(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	return values
}

func sortedRunserverEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}

func assertRunserverWorkspaceEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		t.Fatalf("global runserver left private workspace residue: %v", names)
	}
}

type runserverProjectTreeEntry struct {
	Mode         os.FileMode
	Size         int64
	Device       uint64
	Inode        uint64
	ModTimeNS    int64
	ChangeTimeNS int64
	SHA256       [sha256.Size]byte
	LinkTarget   string
}

func snapshotRunserverProjectTree(t *testing.T, root string) map[string]runserverProjectTreeEntry {
	t.Helper()
	result := make(map[string]runserverProjectTreeEntry)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		device, inode, changeTime, err := runserverProjectIdentity(info)
		if err != nil {
			return fmt.Errorf("snapshot identity %s: %w", key, err)
		}
		snapshot := runserverProjectTreeEntry{
			Mode: info.Mode(), Size: info.Size(), Device: device, Inode: inode,
			ModTimeNS: info.ModTime().UnixNano(), ChangeTimeNS: changeTime,
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot.LinkTarget = target
			result[key] = snapshot
			return nil
		}
		if entry.IsDir() {
			result[key+"/"] = snapshot
			return nil
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot.SHA256 = sha256.Sum256(payload)
		result[key] = snapshot
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot Article project tree: %v", err)
	}
	return result
}

func runserverProjectIdentity(info os.FileInfo) (uint64, uint64, int64, error) {
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0, 0, 0, errors.New("file identity is unavailable")
	}
	device, deviceOK := reflectedRunserverUint(value.FieldByName("Dev"))
	inode, inodeOK := reflectedRunserverUint(value.FieldByName("Ino"))
	changeTime, timeOK := reflectedRunserverTimespec(value, "Ctimespec", "Ctim")
	if !deviceOK || !inodeOK || !timeOK {
		return 0, 0, 0, errors.New("file device, inode, or change time is unavailable")
	}
	return device, inode, changeTime, nil
}

func reflectedRunserverUint(value reflect.Value) (uint64, bool) {
	if !value.IsValid() {
		return 0, false
	}
	switch value.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return value.Uint(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		integer := value.Int()
		if integer < 0 {
			return 0, false
		}
		return uint64(integer), true
	default:
		return 0, false
	}
}

func reflectedRunserverTimespec(value reflect.Value, names ...string) (int64, bool) {
	for _, name := range names {
		field := value.FieldByName(name)
		if !field.IsValid() {
			continue
		}
		seconds := field.FieldByName("Sec")
		nanoseconds := field.FieldByName("Nsec")
		if seconds.IsValid() && nanoseconds.IsValid() && seconds.CanInt() && nanoseconds.CanInt() {
			return seconds.Int()*int64(time.Second) + nanoseconds.Int(), true
		}
	}
	return 0, false
}

func runserverRepositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(working, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root %s: %v", root, err)
	}
	return root
}
