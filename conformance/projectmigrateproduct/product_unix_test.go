//go:build darwin || linux

package projectmigrateproduct_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
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
	"github.com/progresshans/godj/migrations"
	migrationdefinition "github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/systemstate"
)

const (
	articleSQLiteDatabaseEnv = "GODJ_ARTICLE_SQLITE_DATABASE"
	articlePostgresURLEnv    = "GODJ_ARTICLE_POSTGRES_URL"
	articlePostgresSchemaEnv = "GODJ_ARTICLE_POSTGRES_SCHEMA"
	articleAdminUsernameEnv  = "GODJ_ARTICLE_ADMIN_USERNAME"
	articleAdminPasswordEnv  = "GODJ_ARTICLE_ADMIN_PASSWORD"
	articleReadinessPrefix   = "article site listening on http://"
	articleListPath          = "/articles/"
	maximumCommandOutput     = 64 << 10
	commandTimeout           = 3 * time.Minute
)

type migrateResult struct {
	SourceCount         int    `json:"source_count"`
	DefinitionCount     int    `json:"definition_count"`
	DefinitionSetDigest string `json:"definition_set_digest"`
}

type articleCatalogExpectation struct {
	Command            migrateResult
	History            []historyRow
	HistoryFingerprint [sha256.Size]byte
	DefinitionDigest   [sha256.Size]byte
}

type commandResult struct {
	Stdout          string
	Stderr          string
	ExitCode        int
	StdoutTruncated bool
	StderrTruncated bool
}

func TestGlobalMigrateArticleSQLiteProduct(t *testing.T) {
	repository := repositoryRoot(t)
	descriptor := filepath.Join(repository, "examples", "article", "godj.toml")
	globalBinary := buildGlobalGodj(t, repository)
	expected := expectedArticleCatalog(t, repository)

	t.Run("MIG-087_fresh_latest_and_MIG-089_fresh_process_noop", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "fresh-latest.sqlite3")
		workspaceBase := newWorkspaceBase(t)
		environment := articleEnvironment(t, databasePath, workspaceBase)

		first := runMigrate(t, globalBinary, repository, descriptor, environment)
		assertMigrateSuccess(t, first, expected, databasePath)
		assertWorkspaceEmpty(t, workspaceBase)
		assertLatestDatabase(t, databasePath, expected, "")

		before := digestFile(t, databasePath)
		second := runMigrate(t, globalBinary, repository, descriptor, environment)
		assertMigrateSuccess(t, second, expected, databasePath)
		assertWorkspaceEmpty(t, workspaceBase)
		after := digestFile(t, databasePath)
		if before != after {
			t.Fatalf("second fresh-process migrate changed fully-applied SQLite bytes: before=%x after=%x", before, after)
		}
		assertLatestDatabase(t, databasePath, expected, "")
	})

	t.Run("MIG-088_applied_prefix_tail", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "prefix-tail.sqlite3")
		prepareArticlePrefix(t, repository, databasePath)
		const sentinel = "durable prefix sentinel"
		insertArticle(t, databasePath, sentinel)
		assertPrefixDatabase(t, databasePath, sentinel)

		workspaceBase := newWorkspaceBase(t)
		result := runMigrate(t, globalBinary, repository, descriptor, articleEnvironment(t, databasePath, workspaceBase))
		assertMigrateSuccess(t, result, expected, databasePath)
		assertWorkspaceEmpty(t, workspaceBase)
		assertLatestDatabase(t, databasePath, expected, sentinel)
	})

	t.Run("MIG-096_partial_externally_held_lock_two_child_contention_and_fresh_reconciliation", func(t *testing.T) {
		// This is deliberately a partial black-box observation, not the full
		// MIG-096 two-child winner/fence proof. The external transaction makes
		// it deterministic that each real child reaches SQLite's fenced write
		// boundary and that both leave an unmodified database which a fresh
		// child can reconcile. It cannot prove that the two child transactions
		// overlap one another or select exactly one child winner, because the
		// product exposes no runner-entry handshake and this package must not
		// add a test hook to that product boundary. The published MIG-096
		// winner/fence proof is owned by the separate GoDj conformance runner's
		// actual-process fixture; this case remains a narrower product canary.
		databasePath := filepath.Join(t.TempDir(), "concurrent.sqlite3")
		databaseDSN := "file:" + filepath.ToSlash(databasePath) + "?mode=rwc&_busy_timeout=5000"
		workspaceBase := newWorkspaceBase(t)
		environment := articleEnvironment(t, databaseDSN, workspaceBase)
		releaseLock := holdSQLiteWriteLock(t, databaseDSN)
		lockReleased := false
		defer func() {
			if !lockReleased {
				releaseLock()
			}
		}()
		start := make(chan struct{})
		results := make(chan commandResult, 2)
		errorsFound := make(chan error, 2)
		for index := 0; index < 2; index++ {
			go func() {
				<-start
				result, err := executeBounded(globalBinary, repository, environment, "migrate", "--project", descriptor)
				results <- result
				errorsFound <- err
			}()
		}
		close(start)
		observed := make([]commandResult, 2)
		for index := range observed {
			observed[index] = <-results
			if err := <-errorsFound; err != nil {
				t.Fatalf("concurrent migrate %d: %v", index, err)
			}
		}

		for index, result := range observed {
			assertOutputSanitized(t, result, databasePath, databaseDSN)
			assertMigrateFailure(t, result, 3, "migration_transaction_error/history_revision_contended\n")
			t.Logf("partial MIG-096 observation: child %d reached the externally held revision-fenced SQLite write-lock boundary", index+1)
		}
		assertWorkspaceEmpty(t, workspaceBase)
		releaseLock()
		lockReleased = true
		assertNoMigrationMutation(t, databasePath)

		reconciled := runMigrate(t, globalBinary, repository, descriptor, environment)
		assertMigrateSuccess(t, reconciled, expected, databasePath, databaseDSN)
		assertWorkspaceEmpty(t, workspaceBase)
		assertLatestDatabase(t, databasePath, expected, "")
	})

	t.Run("MIG-097_backend_configuration_secret_boundary", func(t *testing.T) {
		t.Run("missing", func(t *testing.T) {
			workspaceBase := newWorkspaceBase(t)
			environment := articleEnvironmentWithoutDatabase(t, workspaceBase)
			result := runMigrate(t, globalBinary, repository, descriptor, environment)
			assertMigrateFailure(t, result, 3, "migration_backend_error/backend_open_failed\n")
			assertWorkspaceEmpty(t, workspaceBase)
		})

		t.Run("mutually-exclusive-secret-values", func(t *testing.T) {
			workspaceBase := newWorkspaceBase(t)
			secretRoot := t.TempDir()
			sqliteSecret := filepath.Join(secretRoot, "sqlite-secret-canary-4f8056.sqlite3")
			postgresSecret := "postgres://secret-user:secret-password-91a3@127.0.0.1:1/secret-database"
			schemaSecret := "secret_schema_b904"
			values := environmentMap(articleEnvironmentWithoutDatabase(t, workspaceBase))
			values[articleSQLiteDatabaseEnv] = sqliteSecret
			values[articlePostgresURLEnv] = postgresSecret
			values[articlePostgresSchemaEnv] = schemaSecret
			result := runMigrate(t, globalBinary, repository, descriptor, sortedEnvironment(values))
			assertMigrateFailure(t, result, 3, "migration_backend_error/backend_open_failed\n")
			assertOutputSanitized(t, result, sqliteSecret, postgresSecret, schemaSecret, "secret-user", "secret-password-91a3")
			if _, err := os.Lstat(sqliteSecret); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid mixed configuration opened SQLite secret path: %v", err)
			}
			assertWorkspaceEmpty(t, workspaceBase)
		})
	})

	t.Run("explicit_migrate_then_runserver_restart_and_no_implicit_pre_migrate", func(t *testing.T) {
		databasePath := filepath.Join(t.TempDir(), "explicit-lifecycle.sqlite3")
		workspaceBase := newWorkspaceBase(t)
		environment := articleEnvironment(t, databasePath, workspaceBase)

		address := reserveLoopbackAddress(t, "")
		assertGlobalArticleServerRejectsUnmigratedState(
			t,
			globalBinary,
			repository,
			descriptor,
			address,
			environment,
			databasePath,
		)
		assertWorkspaceEmpty(t, workspaceBase)
		assertNoMigrationMutation(t, databasePath)

		migration := runMigrate(t, globalBinary, repository, descriptor, environment)
		assertMigrateSuccess(t, migration, expected, databasePath)
		const sentinel = "explicit migrate restart sentinel"
		insertArticle(t, databasePath, sentinel)
		assertLatestDatabase(t, databasePath, expected, sentinel)

		for attempt := 1; attempt <= 2; attempt++ {
			status, body := runGlobalArticleServerOnce(t, globalBinary, repository, descriptor, address, environment)
			if status != http.StatusOK || !strings.Contains(body, sentinel) {
				t.Fatalf("post-migrate runserver attempt %d response = %d/%q", attempt, status, body)
			}
			assertWorkspaceEmpty(t, workspaceBase)
			assertLatestDatabase(t, databasePath, expected, sentinel)
			address = reserveLoopbackAddress(t, address)
		}
	})
}

func assertGlobalArticleServerRejectsUnmigratedState(
	t *testing.T,
	globalBinary, repository, descriptor, address string,
	environment []string,
	databasePath string,
) {
	t.Helper()
	result, err := executeBounded(
		globalBinary,
		repository,
		environment,
		"runserver", "--project", descriptor, "--addr", address,
	)
	if err != nil {
		t.Fatal(err)
	}
	assertOutputSanitized(t, result, databasePath)
	const wantStderr = "article site failed: article site application: system state: systemstate: schema_unavailable: migration_history: exact initial system migration is not applied\n" +
		"project_runserver_runtime_error/project_runtime_exited\n"
	if result.ExitCode != 3 || result.Stdout != "" || result.Stderr != wantStderr ||
		result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf(
			"unmigrated runserver result = exit:%d stdout:%q stderr:%q truncated:%v/%v, want 3/empty/%q",
			result.ExitCode,
			result.Stdout,
			result.Stderr,
			result.StdoutTruncated,
			result.StderrTruncated,
			wantStderr,
		)
	}
}

func expectedArticleCatalog(t *testing.T, repository string) articleCatalogExpectation {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(repository, "examples", "article", "migrations", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, report, err := migrationdefinition.Load(
		migrationdefinition.Source{SourceID: "migrations/0001_initial.godj.json", Document: document},
		systemstate.InitialDefinitionSource(),
	)
	if err != nil {
		t.Fatalf("load expected Article catalog: %v", err)
	}
	if report.DocumentsReceived != 2 || report.HeadersValidated != 2 || report.OperationsDecoded != 4 ||
		report.PlannerConstruction != 1 || report.DefinitionsPublished != 2 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("expected Article catalog report = %+v", report)
	}
	definitions := loaded.Definitions()
	history := make([]historyRow, len(definitions))
	for index := range definitions {
		key := definitions[index].Key()
		history[index] = historyRow{App: key.App, Name: key.Name}
	}
	sort.Slice(history, func(left, right int) bool {
		if history[left].App != history[right].App {
			return history[left].App < history[right].App
		}
		return history[left].Name < history[right].Name
	})
	digest := decodeSHA256Digest(t, loaded.Digest())
	return articleCatalogExpectation{
		Command: migrateResult{
			SourceCount:         len(loaded.Sources()),
			DefinitionCount:     len(definitions),
			DefinitionSetDigest: loaded.Digest(),
		},
		History:            history,
		HistoryFingerprint: fingerprintHistory(history),
		DefinitionDigest:   digest,
	}
}

func prepareArticlePrefix(t *testing.T, repository, databasePath string) {
	t.Helper()
	document, err := os.ReadFile(filepath.Join(repository, "examples", "article", "migrations", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal(err)
	}
	loaded, report, err := migrationdefinition.Load(migrationdefinition.Source{
		SourceID: "migrations/0001_initial.godj.json",
		Document: document,
	})
	if err != nil || report.DefinitionsPublished != 1 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("load Article prefix = report:%+v error:%v", report, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open prefix database: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = backend.Close()
		}
	}()
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatalf("apply Article durable prefix: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close Article prefix database: %v", err)
	}
	closed = true
}

func holdSQLiteWriteLock(t *testing.T, databaseDSN string) func() {
	t.Helper()
	database, err := sql.Open("sqlite", databaseDSN)
	if err != nil {
		t.Fatalf("open SQLite contention owner: %v", err)
	}
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := database.Conn(ctx)
	if err != nil {
		_ = database.Close()
		t.Fatalf("acquire SQLite contention owner connection: %v", err)
	}
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = connection.Close()
		_ = database.Close()
		t.Fatalf("acquire parent-held SQLite write lock: %v", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer releaseCancel()
			rollbackErr := error(nil)
			if _, err := connection.ExecContext(releaseCtx, "ROLLBACK"); err != nil {
				rollbackErr = fmt.Errorf("rollback parent-held SQLite write lock: %w", err)
			}
			closeErr := errors.Join(connection.Close(), database.Close())
			if err := errors.Join(rollbackErr, closeErr); err != nil {
				t.Errorf("release parent-held SQLite write lock: %v", err)
			}
		})
	}
}

func buildGlobalGodj(t *testing.T, repository string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "godj")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-mod=readonly", "-o", binary, "./cmd/godj")
	command.Dir = repository
	command.Env = offlineEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build global godj: %v\n%s", err, output)
	}
	return binary
}

func runMigrate(t *testing.T, globalBinary, repository, descriptor string, environment []string) commandResult {
	t.Helper()
	result, err := executeBounded(globalBinary, repository, environment, "migrate", "--project", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func executeBounded(binary, directory string, environment []string, arguments ...string) (commandResult, error) {
	stdout := &boundedOutput{maximum: maximumCommandOutput}
	stderr := &boundedOutput{maximum: maximumCommandOutput}
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		return commandResult{}, fmt.Errorf("start %s: %w", filepath.Base(binary), err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-timer.C:
		groups, discoveryErr := ownedProcessGroups(command.Process.Pid)
		killErr := killProcessGroups(groups, command.Process.Pid)
		waitErr = boundedWait(waited, 5*time.Second)
		absenceErr := waitForProcessGroupsAbsent(groups, 2*time.Second)
		return commandResult{}, errors.Join(errors.New("command timed out"), discoveryErr, killErr, waitErr, absenceErr)
	}
	result := commandResult{
		Stdout: stdout.String(), Stderr: stderr.String(),
		StdoutTruncated: stdout.Truncated(), StderrTruncated: stderr.Truncated(),
	}
	if waitErr == nil {
		result.ExitCode = 0
	} else {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			return commandResult{}, fmt.Errorf("wait for %s: %w", filepath.Base(binary), waitErr)
		}
		result.ExitCode = exitError.ExitCode()
	}
	if absenceErr := waitForProcessGroupsAbsent([]int{command.Process.Pid}, 2*time.Second); absenceErr != nil {
		return commandResult{}, absenceErr
	}
	return result, nil
}

func assertMigrateSuccess(t *testing.T, result commandResult, expected articleCatalogExpectation, sensitive ...string) {
	t.Helper()
	assertOutputSanitized(t, result, sensitive...)
	if result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf("migrate result = exit:%d stdout:%q stderr:%q truncated:%v/%v", result.ExitCode, result.Stdout, result.Stderr, result.StdoutTruncated, result.StderrTruncated)
	}
	want, err := json.Marshal(expected.Command)
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if result.Stdout != string(want) {
		t.Fatalf("migrate stdout = %q, want %q", result.Stdout, want)
	}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSuffix(result.Stdout, "\n")))
	decoder.DisallowUnknownFields()
	var parsed migrateResult
	if err := decoder.Decode(&parsed); err != nil {
		t.Fatalf("decode migrate result: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("migrate result has trailing JSON: %v", err)
	}
	if !reflect.DeepEqual(parsed, expected.Command) {
		t.Fatalf("migrate result = %+v, want %+v", parsed, expected.Command)
	}
	if digest := decodeSHA256Digest(t, parsed.DefinitionSetDigest); digest != expected.DefinitionDigest {
		t.Fatalf("migrate definition digest bytes = %x, want loaded two-source bytes %x", digest, expected.DefinitionDigest)
	}
}

func assertMigrateFailure(t *testing.T, result commandResult, exit int, stderr string) {
	t.Helper()
	if result.ExitCode != exit || result.Stdout != "" || result.Stderr != stderr || result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf("migrate failure = exit:%d stdout:%q stderr:%q truncated:%v/%v, want %d/empty/%q", result.ExitCode, result.Stdout, result.Stderr, result.StdoutTruncated, result.StderrTruncated, exit, stderr)
	}
}

func assertOutputSanitized(t *testing.T, result commandResult, sensitive ...string) {
	t.Helper()
	combined := result.Stdout + result.Stderr
	for _, value := range sensitive {
		if value != "" && strings.Contains(combined, value) {
			t.Fatal("command output exposed a sensitive value")
		}
	}
}

func newWorkspaceBase(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "private-workspaces")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func articleEnvironment(t *testing.T, databasePath, workspaceBase string) []string {
	t.Helper()
	values := environmentMap(articleEnvironmentWithoutDatabase(t, workspaceBase))
	values[articleSQLiteDatabaseEnv] = databasePath
	return sortedEnvironment(values)
}

func articleEnvironmentWithoutDatabase(t *testing.T, workspaceBase string) []string {
	t.Helper()
	values := environmentMap(os.Environ())
	delete(values, articleSQLiteDatabaseEnv)
	delete(values, articlePostgresURLEnv)
	delete(values, articlePostgresSchemaEnv)
	delete(values, articleAdminUsernameEnv)
	delete(values, articleAdminPasswordEnv)
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
	return sortedEnvironment(values)
}

func offlineEnvironment(input []string) []string {
	values := environmentMap(input)
	values["GOWORK"] = "off"
	values["GOTOOLCHAIN"] = "local"
	values["GOENV"] = "off"
	values["GOFLAGS"] = ""
	values["GOCACHEPROG"] = ""
	values["GOPROXY"] = "off"
	return sortedEnvironment(values)
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

func sortedEnvironment(values map[string]string) []string {
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

func assertWorkspaceEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		return
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	t.Fatalf("global command left private workspace residue: %v", names)
}

func digestFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(payload)
}

type historyRow struct {
	App  string
	Name string
}

type revisionRow struct {
	FormatVersion      int
	EpochBytes         int
	Revision           int64
	FingerprintBytes   int
	HistoryFingerprint [sha256.Size]byte
	AdditionalRowCount int
}

type columnSnapshot struct {
	Name    string
	Type    string
	NotNull int
	Primary int
}

type databaseSnapshot struct {
	Tables   []string
	History  []historyRow
	Revision revisionRow
	Columns  map[string][]columnSnapshot
}

func inspectDatabase(t *testing.T, databasePath string) databaseSnapshot {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close inspected SQLite database: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping inspected SQLite database: %v", err)
	}

	snapshot := databaseSnapshot{Columns: make(map[string][]columnSnapshot)}
	rows, err := database.QueryContext(ctx, `SELECT "name" FROM "sqlite_schema" WHERE "type" = 'table' AND "name" NOT LIKE 'sqlite_%' ORDER BY "name"`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		snapshot.Tables = append(snapshot.Tables, name)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	for _, table := range snapshot.Tables {
		columnRows, err := database.QueryContext(ctx, `PRAGMA table_info("`+table+`")`)
		if err != nil {
			t.Fatal(err)
		}
		for columnRows.Next() {
			var cid int
			var column columnSnapshot
			var defaultValue sql.NullString
			if err := columnRows.Scan(&cid, &column.Name, &column.Type, &column.NotNull, &defaultValue, &column.Primary); err != nil {
				_ = columnRows.Close()
				t.Fatal(err)
			}
			snapshot.Columns[table] = append(snapshot.Columns[table], column)
		}
		if err := errors.Join(columnRows.Err(), columnRows.Close()); err != nil {
			t.Fatal(err)
		}
	}
	if containsString(snapshot.Tables, "godj_migrations") {
		historyRows, err := database.QueryContext(ctx, `SELECT "app", "name" FROM "godj_migrations" ORDER BY "app", "name"`)
		if err != nil {
			t.Fatal(err)
		}
		for historyRows.Next() {
			var row historyRow
			if err := historyRows.Scan(&row.App, &row.Name); err != nil {
				_ = historyRows.Close()
				t.Fatal(err)
			}
			snapshot.History = append(snapshot.History, row)
		}
		if err := errors.Join(historyRows.Err(), historyRows.Close()); err != nil {
			t.Fatal(err)
		}
	}
	if containsString(snapshot.Tables, "godj_migration_revision") {
		var fingerprint []byte
		if err := database.QueryRowContext(ctx, `SELECT "format_version", length("epoch"), "revision", length("history_fingerprint") FROM "godj_migration_revision" WHERE "singleton" = 1`).Scan(
			&snapshot.Revision.FormatVersion,
			&snapshot.Revision.EpochBytes,
			&snapshot.Revision.Revision,
			&snapshot.Revision.FingerprintBytes,
		); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRowContext(ctx, `SELECT "history_fingerprint" FROM "godj_migration_revision" WHERE "singleton" = 1`).Scan(&fingerprint); err != nil {
			t.Fatal(err)
		}
		if len(fingerprint) != sha256.Size {
			t.Fatalf("migration history fingerprint bytes = %d, want %d", len(fingerprint), sha256.Size)
		}
		copy(snapshot.Revision.HistoryFingerprint[:], fingerprint)
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) - 1 FROM "godj_migration_revision"`).Scan(&snapshot.Revision.AdditionalRowCount); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func assertPrefixDatabase(t *testing.T, databasePath, sentinel string) {
	t.Helper()
	snapshot := inspectDatabase(t, databasePath)
	wantTables := []string{"godj_conformance_article", "godj_migration_revision", "godj_migrations"}
	if !reflect.DeepEqual(snapshot.Tables, wantTables) {
		t.Fatalf("prefix tables = %v, want %v", snapshot.Tables, wantTables)
	}
	if !reflect.DeepEqual(snapshot.History, []historyRow{{App: "godj_conformance", Name: "0001_initial"}}) {
		t.Fatalf("prefix history = %+v", snapshot.History)
	}
	assertRevision(t, snapshot.Revision, 1, snapshot.History, fingerprintHistory(snapshot.History))
	assertExpectedColumns(t, snapshot, wantTables)
	assertArticleSentinel(t, databasePath, sentinel)
}

func assertLatestDatabase(t *testing.T, databasePath string, expected articleCatalogExpectation, sentinel string) {
	t.Helper()
	snapshot := inspectDatabase(t, databasePath)
	wantTables := []string{
		"godj_conformance_article",
		"godj_migration_revision",
		"godj_migrations",
		"godj_system_audit",
		"godj_system_credential",
		"godj_system_session",
	}
	if !reflect.DeepEqual(snapshot.Tables, wantTables) {
		t.Fatalf("latest tables = %v, want %v", snapshot.Tables, wantTables)
	}
	wantHistory := expected.History
	if !reflect.DeepEqual(snapshot.History, wantHistory) {
		t.Fatalf("latest history = %+v, want %+v", snapshot.History, wantHistory)
	}
	assertRevision(t, snapshot.Revision, int64(len(wantHistory)), wantHistory, expected.HistoryFingerprint)
	assertExpectedColumns(t, snapshot, wantTables)
	assertArticleSentinel(t, databasePath, sentinel)
}

func assertNoMigrationMutation(t *testing.T, databasePath string) {
	t.Helper()
	snapshot := inspectDatabase(t, databasePath)
	if len(snapshot.Tables) != 0 || len(snapshot.History) != 0 || len(snapshot.Columns) != 0 || snapshot.Revision != (revisionRow{}) {
		t.Fatalf("runserver-before-migrate mutated schema/history: %+v", snapshot)
	}
}

func assertRevision(
	t *testing.T,
	got revisionRow,
	wantRevision int64,
	wantHistory []historyRow,
	wantFingerprint [sha256.Size]byte,
) {
	t.Helper()
	calculated := fingerprintHistory(wantHistory)
	if calculated != wantFingerprint {
		t.Fatalf("expected migration history fingerprint = %x, loaded-catalog fingerprint = %x", calculated, wantFingerprint)
	}
	want := revisionRow{
		FormatVersion:      1,
		EpochBytes:         16,
		Revision:           wantRevision,
		FingerprintBytes:   sha256.Size,
		HistoryFingerprint: wantFingerprint,
	}
	if got != want {
		t.Fatalf("migration revision = %+v, want %+v", got, want)
	}
}

func fingerprintHistory(records []historyRow) [sha256.Size]byte {
	canonical := append([]historyRow(nil), records...)
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].App != canonical[right].App {
			return canonical[left].App < canonical[right].App
		}
		return canonical[left].Name < canonical[right].Name
	})
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(canonical)))
	_, _ = hash.Write(length[:])
	for _, record := range canonical {
		for _, value := range []string{record.App, record.Name} {
			binary.BigEndian.PutUint64(length[:], uint64(len(value)))
			_, _ = hash.Write(length[:])
			_, _ = hash.Write([]byte(value))
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func decodeSHA256Digest(t *testing.T, digest string) [sha256.Size]byte {
	t.Helper()
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) {
		t.Fatalf("definition digest = %q, want sha256 domain", digest)
	}
	payload, err := hex.DecodeString(strings.TrimPrefix(digest, prefix))
	if err != nil || len(payload) != sha256.Size {
		t.Fatalf("decode definition digest %q: bytes=%d error=%v", digest, len(payload), err)
	}
	var result [sha256.Size]byte
	copy(result[:], payload)
	return result
}

func assertExpectedColumns(t *testing.T, snapshot databaseSnapshot, tables []string) {
	t.Helper()
	expected := expectedColumns()
	for _, table := range tables {
		want, exists := expected[table]
		if !exists {
			t.Fatalf("test has no column contract for table %q", table)
		}
		if !reflect.DeepEqual(snapshot.Columns[table], want) {
			t.Fatalf("columns for %s = %+v, want %+v", table, snapshot.Columns[table], want)
		}
	}
}

func expectedColumns() map[string][]columnSnapshot {
	return map[string][]columnSnapshot{
		"godj_conformance_article": {
			{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: "title", Type: "VARCHAR(200)", NotNull: 1},
			{Name: "published", Type: "BOOLEAN", NotNull: 1},
			{Name: "summary", Type: "VARCHAR(200)"},
		},
		"godj_migration_revision": {
			{Name: "singleton", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: "format_version", Type: "INTEGER", NotNull: 1},
			{Name: "epoch", Type: "BLOB", NotNull: 1},
			{Name: "revision", Type: "INTEGER", NotNull: 1},
			{Name: "history_fingerprint", Type: "BLOB", NotNull: 1},
		},
		"godj_migrations": {
			{Name: "app", Type: "VARCHAR(255)", NotNull: 1, Primary: 1},
			{Name: "name", Type: "VARCHAR(255)", NotNull: 1, Primary: 2},
		},
		"godj_system_credential": {
			{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: "principal_id", Type: "VARCHAR(128)", NotNull: 1},
			{Name: "username", Type: "VARCHAR(256)", NotNull: 1},
			{Name: "encoded_password", Type: "VARCHAR(2048)", NotNull: 1},
			{Name: "active", Type: "BOOLEAN", NotNull: 1},
			{Name: "permissions", Type: "VARCHAR(65536)", NotNull: 1},
			{Name: "definition_digest", Type: "VARCHAR(71)", NotNull: 1},
		},
		"godj_system_session": {
			{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: "digest", Type: "VARCHAR(64)", NotNull: 1},
			{Name: "payload", Type: "VARCHAR(32768)", NotNull: 1},
		},
		"godj_system_audit": {
			{Name: "id", Type: "INTEGER", NotNull: 1, Primary: 1},
			{Name: "actor_id", Type: "VARCHAR(128)", NotNull: 1},
			{Name: "model", Type: "VARCHAR(128)", NotNull: 1},
			{Name: "object_id", Type: "VARCHAR(64)", NotNull: 1},
			{Name: "action", Type: "VARCHAR(16)", NotNull: 1},
			{Name: "changed_fields", Type: "VARCHAR(32768)", NotNull: 1},
			{Name: "display_label", Type: "VARCHAR(1024)", NotNull: 1},
		},
	}
}

func insertArticle(t *testing.T, databasePath, title string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close seeded SQLite database: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, `INSERT INTO "godj_conformance_article" ("title", "published", "summary") VALUES (?, TRUE, NULL)`, title); err != nil {
		t.Fatalf("insert Article sentinel: %v", err)
	}
}

func assertArticleSentinel(t *testing.T, databasePath, title string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close Article sentinel inspection: %v", err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM "godj_conformance_article"`).Scan(&count); err != nil {
		t.Fatalf("count Article sentinels: %v", err)
	}
	want := 0
	if title != "" {
		want = 1
		var stored string
		if err := database.QueryRowContext(ctx, `SELECT "title" FROM "godj_conformance_article"`).Scan(&stored); err != nil {
			t.Fatalf("read Article sentinel: %v", err)
		}
		if stored != title {
			t.Fatalf("Article sentinel = %q, want %q", stored, title)
		}
	}
	if count != want {
		t.Fatalf("Article row count = %d, want %d", count, want)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func runGlobalArticleServerOnce(
	t *testing.T,
	globalBinary, repository, descriptor, expectedAddress string,
	environment []string,
) (int, string) {
	t.Helper()
	stdout := newReadinessOutput()
	stderr := &boundedOutput{maximum: maximumCommandOutput}
	command := exec.Command(globalBinary, "runserver", "--project", descriptor, "--addr", expectedAddress)
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
	finished := false
	var knownGroups []int
	defer func() {
		if !finished {
			_ = interruptAndWait(command, waited, 20*time.Second, knownGroups...)
		}
	}()

	var address string
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case address = <-stdout.ready:
		if address != expectedAddress {
			t.Fatalf("Article readiness address = %q, want %q", address, expectedAddress)
		}
		groups, err := ownedProcessGroups(command.Process.Pid)
		if err != nil || len(groups) < 2 {
			t.Fatalf("capture global/runtime process groups = %v, error=%v", groups, err)
		}
		knownGroups = groups
	case waitErr := <-waited:
		finished = true
		t.Fatalf("global runserver exited before readiness: %v; stdout=%q stderr=%q", waitErr, stdout.String(), stderr.String())
	case <-timer.C:
		cleanup := interruptAndWait(command, waited, 20*time.Second, knownGroups...)
		finished = true
		t.Fatalf("global runserver readiness timed out: %+v", cleanup)
	}

	status, body, requestErr := requestArticlePage(address)
	cleanup := interruptAndWait(command, waited, 20*time.Second, knownGroups...)
	finished = true
	if requestErr != nil {
		t.Fatalf("request Article page: %v; cleanup=%+v", requestErr, cleanup)
	}
	if cleanup.failed() || len(cleanup.ProcessGroups) < 2 {
		t.Fatalf("clean runserver interrupt = %+v", cleanup)
	}
	if stderr.Truncated() || stderr.String() != "" {
		t.Fatalf("runserver stderr = %q truncated=%v", stderr.String(), stderr.Truncated())
	}
	wantReadiness := articleReadinessPrefix + expectedAddress + "\n"
	if stdout.Truncated() || stdout.String() != wantReadiness {
		t.Fatalf("runserver stdout = %q truncated=%v, want %q", stdout.String(), stdout.Truncated(), wantReadiness)
	}
	return status, body
}

func reserveLoopbackAddress(t *testing.T, previous string) string {
	t.Helper()
	candidate := previous
	if candidate == "" {
		candidate = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp4", candidate)
	if err != nil {
		t.Fatalf("reserve loopback address %q: %v", candidate, err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if previous != "" && address != previous {
		t.Fatalf("re-reserved address = %q, want %q", address, previous)
	}
	return address
}

func requestArticlePage(address string) (int, string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := client.Get("http://" + address + articleListPath)
		if err != nil {
			if time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return 0, "", err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		closeErr := response.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return 0, "", err
		}
		return response.StatusCode, string(body), nil
	}
}

type cleanupResult struct {
	WaitError      error
	SignalError    error
	DiscoveryError error
	AbsenceError   error
	Forced         bool
	ProcessGroups  []int
}

func (result cleanupResult) failed() bool {
	return result.WaitError != nil || result.SignalError != nil || result.DiscoveryError != nil || result.AbsenceError != nil || result.Forced
}

func interruptAndWait(command *exec.Cmd, waited <-chan error, timeout time.Duration, knownGroups ...int) cleanupResult {
	result := cleanupResult{}
	groups, discoveryErr := ownedProcessGroups(command.Process.Pid)
	result.ProcessGroups = mergeProcessGroups(knownGroups, groups)
	result.DiscoveryError = discoveryErr
	result.SignalError = signalProcessGroup(command.Process.Pid, syscall.SIGINT)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result.WaitError = <-waited:
	case <-timer.C:
		result.Forced = true
		refreshed, refreshErr := ownedProcessGroups(command.Process.Pid)
		result.ProcessGroups = mergeProcessGroups(result.ProcessGroups, refreshed)
		result.DiscoveryError = errors.Join(result.DiscoveryError, refreshErr)
		result.WaitError = errors.Join(killProcessGroups(result.ProcessGroups, command.Process.Pid), boundedWait(waited, 5*time.Second))
	}
	result.AbsenceError = waitForProcessGroupsAbsent(result.ProcessGroups, 2*time.Second)
	if result.AbsenceError != nil && !result.Forced {
		result.Forced = true
		result.AbsenceError = errors.Join(result.AbsenceError, killProcessGroups(result.ProcessGroups, command.Process.Pid), waitForProcessGroupsAbsent(result.ProcessGroups, 2*time.Second))
	}
	return result
}

func boundedWait(waited <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err
	case <-timer.C:
		return errors.New("process Wait remained blocked after forced cleanup")
	}
}

func ownedProcessGroups(rootPID int) ([]int, error) {
	output, err := exec.Command("ps", "-Ao", "pid=,ppid=,pgid=").Output()
	if err != nil {
		return []int{rootPID}, fmt.Errorf("inspect process tree: %w", err)
	}
	type process struct{ pid, ppid, pgid int }
	var processes []process
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return []int{rootPID}, errors.New("inspect process tree: invalid ps row")
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		pgid, pgidErr := strconv.Atoi(fields[2])
		if errors.Join(pidErr, ppidErr, pgidErr) != nil {
			return []int{rootPID}, errors.New("inspect process tree: invalid identifier")
		}
		processes = append(processes, process{pid: pid, ppid: ppid, pgid: pgid})
	}
	descendants := map[int]struct{}{rootPID: {}}
	for changed := true; changed; {
		changed = false
		for _, candidate := range processes {
			if _, owned := descendants[candidate.ppid]; !owned {
				continue
			}
			if _, exists := descendants[candidate.pid]; exists {
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
			return []int{rootPID}, errors.New("inspect process tree: unsafe process group")
		}
		result = append(result, group)
	}
	sort.Ints(result)
	return result, nil
}

func mergeProcessGroups(left, right []int) []int {
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

func signalProcessGroup(group int, signal syscall.Signal) error {
	if group <= 1 || group == syscall.Getpgrp() {
		return errors.New("refuse to signal unsafe process group")
	}
	err := syscall.Kill(-group, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func killProcessGroups(groups []int, root int) error {
	var result error
	for index := len(groups) - 1; index >= 0; index-- {
		if groups[index] == root {
			continue
		}
		result = errors.Join(result, signalProcessGroup(groups[index], syscall.SIGKILL))
	}
	return errors.Join(result, signalProcessGroup(root, syscall.SIGKILL))
}

func waitForProcessGroupsAbsent(groups []int, timeout time.Duration) error {
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
			return fmt.Errorf("process groups remain: %v", remaining)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type boundedOutput struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (output *boundedOutput) Write(payload []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	remaining := output.maximum - output.buffer.Len()
	if remaining > 0 {
		kept := payload
		if len(kept) > remaining {
			kept = kept[:remaining]
		}
		_, _ = output.buffer.Write(kept)
	}
	if len(payload) > remaining {
		output.truncated = true
	}
	return len(payload), nil
}

func (output *boundedOutput) String() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.String()
}

func (output *boundedOutput) Truncated() bool {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.truncated
}

type readinessOutput struct {
	boundedOutput
	ready   chan string
	once    sync.Once
	scanned int
}

func newReadinessOutput() *readinessOutput {
	return &readinessOutput{
		boundedOutput: boundedOutput{maximum: maximumCommandOutput},
		ready:         make(chan string, 1),
	}
}

func (output *readinessOutput) Write(payload []byte) (int, error) {
	output.mutex.Lock()
	remaining := output.maximum - output.buffer.Len()
	if remaining > 0 {
		kept := payload
		if len(kept) > remaining {
			kept = kept[:remaining]
		}
		_, _ = output.buffer.Write(kept)
	}
	if len(payload) > remaining {
		output.truncated = true
	}
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
	for _, address := range addresses {
		output.once.Do(func() { output.ready <- address })
	}
	return len(payload), nil
}

func repositoryRoot(t *testing.T) string {
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
