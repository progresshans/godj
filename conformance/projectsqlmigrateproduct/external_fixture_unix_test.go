//go:build darwin || linux

package projectsqlmigrateproduct_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testfixture"
	"github.com/progresshans/godj/conformance/internal/testprocess"
)

const (
	sqlProductCommandTimeout = 4 * time.Minute
	sqlProductMaximumOutput  = 1 << 20

	sqlProductHostedDatabaseCanary = "hosted-live-database-environment-must-not-survive"
	sqlProductPoisonDatabase       = "sqlmigrate_db_path_canary_529d"
	sqlProductPoisonUser           = "sqlmigrate_user"
	sqlProductPoisonBarrier        = "godj-sqlmigrate-poison-listener-barrier-v1"
)

var sqlProductDatabaseURLKeys = map[string]struct{}{
	"DATABASE_URL":           {},
	"GODJ_TEST_POSTGRES_URL": {},
	"POSTGRESQL_URL":         {},
	"POSTGRES_URL":           {},
}

var sqlProductAllowedGoDjImports = map[string]struct{}{
	"github.com/progresshans/godj/db/postgres":           {},
	"github.com/progresshans/godj/db/sqlite":             {},
	"github.com/progresshans/godj/migrations":            {},
	"github.com/progresshans/godj/migrations/backend":    {},
	"github.com/progresshans/godj/migrations/definition": {},
	"github.com/progresshans/godj/project":               {},
	"github.com/progresshans/godj/schema/ir":             {},
}

type sqlProductProject struct {
	repository       string
	universe         string
	root             string
	nested           string
	unselected       string
	descriptor       string
	poisonDescriptor string
	globalBinary     string
	scratch          string
	baseEnv          []string
	secret           string
	postgresURL      string
	postgresPoison   *sqlProductPostgresPoison
	applicationHash  map[string][sha256.Size]byte
}

type sqlProductState struct {
	directory      string
	database       string
	initMarker     string
	rendererMarker string
	openerMarker   string
}

type sqlProductResult struct {
	exitCode int
	stdout   string
	stderr   string
}

// sqlProductPostgresPoison is a live loopback endpoint. Any inherited or
// framework-created PostgreSQL connection reaches this listener, is counted,
// and is closed before a PostgreSQL handshake can succeed. checkpoint uses an
// identified control connection so every earlier queued attempt is observed
// before the counter is read.
type sqlProductPostgresPoison struct {
	listener net.Listener
	host     string
	port     string
	url      string
	secret   string

	attempts atomic.Int64
	barrier  chan struct{}
	done     chan error
}

func newSQLProductPostgresPoison(t *testing.T, secret string) *sqlProductPostgresPoison {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("start PostgreSQL poison listener: %v", err)
	}
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil || host != "127.0.0.1" || port == "" {
		_ = listener.Close()
		t.Fatalf("resolve PostgreSQL poison listener address: %v", err)
	}
	poison := &sqlProductPostgresPoison{
		listener: listener,
		host:     host,
		port:     port,
		secret:   secret,
		barrier:  make(chan struct{}, 1),
		done:     make(chan error, 1),
	}
	poison.url = "postgres://" + sqlProductPoisonUser + ":" + secret + "@" +
		net.JoinHostPort(host, port) + "/" + sqlProductPoisonDatabase + "?sslmode=disable"
	go poison.serve()
	t.Cleanup(func() {
		closeErr := poison.listener.Close()
		serveErr := <-poison.done
		if err := errors.Join(closeErr, serveErr); err != nil {
			t.Errorf("close PostgreSQL poison listener: %v", err)
		}
	})
	return poison
}

func (poison *sqlProductPostgresPoison) serve() {
	for {
		connection, err := poison.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				poison.done <- nil
			} else {
				poison.done <- err
			}
			return
		}
		_ = connection.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
		document := make([]byte, len(sqlProductPoisonBarrier))
		_, readErr := io.ReadFull(connection, document)
		_ = connection.Close()
		if readErr == nil && string(document) == sqlProductPoisonBarrier {
			select {
			case poison.barrier <- struct{}{}:
			default:
			}
			continue
		}
		poison.attempts.Add(1)
	}
}

func (poison *sqlProductPostgresPoison) environment() map[string]string {
	return map[string]string{
		"DATABASE_URL":                   poison.url,
		"GODJ_TEST_POSTGRES_URL":         poison.url,
		"POSTGRESQL_URL":                 poison.url,
		"POSTGRES_URL":                   poison.url,
		"PGCONNECT_TIMEOUT":              "1",
		"PGDATABASE":                     sqlProductPoisonDatabase,
		"PGHOST":                         poison.host,
		"PGPASSWORD":                     poison.secret,
		"PGPORT":                         poison.port,
		"PGSSLMODE":                      "disable",
		"PGUSER":                         sqlProductPoisonUser,
		sqlProductPostgresURLEnvironment: poison.url,
	}
}

func (poison *sqlProductPostgresPoison) checkpoint() (int64, error) {
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort(poison.host, poison.port), 2*time.Second)
	if err != nil {
		return 0, fmt.Errorf("dial PostgreSQL poison listener barrier: %w", err)
	}
	_, writeErr := io.WriteString(connection, sqlProductPoisonBarrier)
	closeErr := connection.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return 0, fmt.Errorf("write PostgreSQL poison listener barrier: %w", err)
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-poison.barrier:
		return poison.attempts.Load(), nil
	case <-timer.C:
		return 0, errors.New("PostgreSQL poison listener barrier timed out")
	}
}

func (poison *sqlProductPostgresPoison) verifyAttemptObservation() error {
	// Exercise the same listener used by the product commands, then reset it
	// before any child receives the poison environment.
	connection, err := net.DialTimeout("tcp4", net.JoinHostPort(poison.host, poison.port), 2*time.Second)
	if err != nil {
		return fmt.Errorf("dial PostgreSQL poison listener observation probe: %w", err)
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		_ = connection.Close()
		return fmt.Errorf("set PostgreSQL poison listener observation deadline: %w", err)
	}
	if _, err := connection.Write([]byte{0}); err != nil {
		_ = connection.Close()
		return fmt.Errorf("write PostgreSQL poison listener observation probe: %w", err)
	}
	tcpConnection, ok := connection.(*net.TCPConn)
	if !ok {
		_ = connection.Close()
		return errors.New("PostgreSQL poison listener observation connection is not TCP")
	}
	if err := tcpConnection.CloseWrite(); err != nil {
		_ = connection.Close()
		return fmt.Errorf("close PostgreSQL poison listener observation write side: %w", err)
	}
	var response [1]byte
	_, readErr := connection.Read(response[:])
	closeErr := connection.Close()
	if !errors.Is(readErr, io.EOF) || closeErr != nil {
		return fmt.Errorf("PostgreSQL poison listener did not reject observation probe: %w", errors.Join(readErr, closeErr))
	}
	attempts, err := poison.checkpoint()
	if err != nil {
		return err
	}
	if attempts != 1 {
		return fmt.Errorf("PostgreSQL poison listener observation count = %d, want 1", attempts)
	}
	if reset := poison.attempts.Swap(0); reset != attempts {
		return fmt.Errorf("reset PostgreSQL poison listener observation count = %d, want %d", reset, attempts)
	}
	resetAttempts, err := poison.checkpoint()
	if err != nil {
		return err
	}
	if resetAttempts != 0 {
		return fmt.Errorf("PostgreSQL poison listener reset observation count = %d, want 0", resetAttempts)
	}
	return nil
}

func newSQLProductProject(t *testing.T) *sqlProductProject {
	t.Helper()
	repository := testfixture.RepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-sqlmigrate-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external sqlmigrate universe: %v", err)
		}
	})
	testfixture.AssertSeparateRoot(t, repository, universe)

	root := filepath.Join(universe, "consumer")
	nested := filepath.Join(root, "nested")
	unselected := filepath.Join(universe, "unselected")
	scratch := filepath.Join(universe, "scratch")
	for _, directory := range []string{
		root,
		filepath.Join(root, "cmd", "projectrunner"),
		nested,
		unselected,
		filepath.Join(universe, "home"),
		filepath.Join(universe, "cache"),
		filepath.Join(universe, "state"),
		scratch,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	sqlProductWriteFile(t, filepath.Join(root, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-sqlmigrate-external

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(repository))), 0o600)
	sqlProductWriteFile(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"), 0o600)
	poisonDescriptor := filepath.Join(root, "godj-poison-build.toml")
	sqlProductWriteFile(t, poisonDescriptor, []byte("format_version = 1\n[project]\npackage = \"./cmd/build-must-not-run\"\n"), 0o600)
	sqlProductWriteFile(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(sqlProductRunnerSource), 0o600)
	sqlProductAuditApplicationSources(t, repository, root)

	moduleCacheDocument, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("locate ambient module cache: %v", err)
	}
	moduleCache := strings.TrimSpace(string(moduleCacheDocument))
	if moduleCache == "" || !filepath.IsAbs(moduleCache) {
		t.Fatalf("ambient module cache path %q is not absolute", moduleCache)
	}
	if info, statErr := os.Stat(moduleCache); statErr == nil {
		if !info.IsDir() {
			t.Fatalf("ambient module cache %q is not a directory", moduleCache)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("inspect ambient module cache %q: %v", moduleCache, statErr)
	}

	setupEnv := sqlProductSanitizeDatabaseEnvironment(sqlProductRemoveEnvironment(testfixture.Environment(os.Environ(), map[string]string{
		"HOME":            filepath.Join(universe, "home"),
		"XDG_CONFIG_HOME": filepath.Join(universe, "home"),
		"XDG_CACHE_HOME":  filepath.Join(universe, "cache"),
		"TMPDIR":          scratch,
		"GOCACHE":         filepath.Join(universe, "cache", "go-build"),
		"GOMODCACHE":      moduleCache,
		"GOWORK":          "off",
		"GOTOOLCHAIN":     "local",
		"GOENV":           "off",
		"GOFLAGS":         "",
		"GOCACHEPROG":     "",
	}),
		sqlProductCatalogEnvironment,
		sqlProductRendererEnvironment,
		sqlProductInitMarkerEnvironment,
		sqlProductRendererMarkerEnvironment,
		sqlProductOpenerMarkerEnvironment,
		sqlProductDatabaseEnvironment,
		sqlProductSecretEnvironment,
		sqlProductPostgresSchemaEnvironment,
		sqlProductPostgresPoisonEnvironment,
		sqlProductPostgresURLEnvironment,
	))
	// Dependency resolution is fixture setup. Every product invocation after
	// this point disables dependency-network resolution and retains only the
	// test-owned loopback PostgreSQL poison endpoint.
	sqlProductRunSuccess(t, root, setupEnv, "go", "mod", "tidy")

	secret := "sqlmigrate-secret-canary-81ae0d75"
	postgresPoison := newSQLProductPostgresPoison(t, secret)
	if err := postgresPoison.verifyAttemptObservation(); err != nil {
		t.Fatalf("verify PostgreSQL poison listener: %v", err)
	}
	postgresURL := postgresPoison.url
	baseOverrides := postgresPoison.environment()
	baseOverrides["GOPROXY"] = "off"
	baseOverrides["GOSUMDB"] = "off"
	baseOverrides[sqlProductSecretEnvironment] = secret
	baseOverrides[sqlProductCatalogEnvironment] = sqlProductCatalogFull
	baseOverrides[sqlProductRendererEnvironment] = sqlProductRendererSQLite
	baseOverrides[sqlProductPostgresURLEnvironment] = postgresURL
	baseEnv := testfixture.Environment(setupEnv, baseOverrides)
	globalBinary := filepath.Join(universe, "godj")
	sqlProductRunSuccess(t, repository, baseEnv, "go", "build", "-buildvcs=false", "-trimpath", "-mod=readonly", "-o", globalBinary, "./cmd/godj")

	project := &sqlProductProject{
		repository:       repository,
		universe:         universe,
		root:             root,
		nested:           nested,
		unselected:       unselected,
		descriptor:       filepath.Join(root, "godj.toml"),
		poisonDescriptor: poisonDescriptor,
		globalBinary:     globalBinary,
		scratch:          scratch,
		baseEnv:          baseEnv,
		secret:           secret,
		postgresURL:      postgresURL,
		postgresPoison:   postgresPoison,
	}
	project.applicationHash = testfixture.ApplicationHashes(t, project.root)
	project.assertWorkspaceEmpty(t)
	return project
}

func (project *sqlProductProject) state(t *testing.T, name string) sqlProductState {
	t.Helper()
	directory := filepath.Join(project.universe, "state", name)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return sqlProductState{
		directory:      directory,
		database:       filepath.Join(directory, "sqlite-secret-path-74c3.sqlite3"),
		initMarker:     filepath.Join(directory, "init-events.log"),
		rendererMarker: filepath.Join(directory, "renderer-events.log"),
		openerMarker:   filepath.Join(directory, "opener-events.log"),
	}
}

func (project *sqlProductProject) environment(state sqlProductState, catalog, renderer string) []string {
	return testfixture.Environment(project.baseEnv, map[string]string{
		sqlProductCatalogEnvironment:        catalog,
		sqlProductRendererEnvironment:       renderer,
		sqlProductInitMarkerEnvironment:     state.initMarker,
		sqlProductRendererMarkerEnvironment: state.rendererMarker,
		sqlProductOpenerMarkerEnvironment:   state.openerMarker,
		sqlProductDatabaseEnvironment:       state.database,
	})
}

func (project *sqlProductProject) postgresEnvironment(state sqlProductState) []string {
	return testfixture.Environment(
		project.environment(state, sqlProductCatalogFull, sqlProductRendererPostgres),
		map[string]string{
			sqlProductPostgresSchemaEnvironment: sqlProductPostgresSchema,
			sqlProductPostgresPoisonEnvironment: sqlProductPostgresPoisonSchema,
		},
	)
}

func (project *sqlProductProject) run(
	t *testing.T,
	state sqlProductState,
	environment []string,
	arguments ...string,
) sqlProductResult {
	t.Helper()
	return project.runAt(t, state, project.nested, environment, arguments...)
}

func (project *sqlProductProject) runAt(
	t *testing.T,
	state sqlProductState,
	directory string,
	environment []string,
	arguments ...string,
) sqlProductResult {
	t.Helper()
	result, err := sqlProductRun(directory, environment, project.globalBinary, arguments...)
	if err != nil {
		t.Fatalf("run godj %s: %v", strings.Join(arguments, " "), err)
	}
	project.assertCommandBoundary(t, state, result)
	return result
}

func (project *sqlProductProject) runExplicit(
	t *testing.T,
	state sqlProductState,
	environment []string,
	app,
	name string,
) sqlProductResult {
	t.Helper()
	return project.runAt(t, state, project.unselected, environment, "sqlmigrate", app, name, "--project", project.descriptor)
}

func (project *sqlProductProject) runInterrupted(
	t *testing.T,
	state sqlProductState,
	environment []string,
	app,
	name string,
) sqlProductResult {
	t.Helper()
	stdout := testprocess.NewBuffer(sqlProductMaximumOutput)
	stderr := testprocess.NewBuffer(sqlProductMaximumOutput)
	command := exec.Command(
		project.globalBinary,
		"sqlmigrate", app, name, "--project", project.descriptor,
	)
	command.Dir = project.unselected
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start interrupted sqlmigrate: %v", err)
	}

	var waitErr error
	waited := make(chan struct{})
	go func() {
		waitErr = command.Wait()
		close(waited)
	}()

	runnerPID, err := sqlProductWaitMarkerEvent(
		state.rendererMarker,
		"render_wait",
		waited,
		sqlProductCommandTimeout,
	)
	if err != nil {
		project.abortInterruptedCommand(t, command.Process.Pid, waited)
		t.Fatalf("wait for built runner cancellation point: %v", err)
	}
	groups, err := testprocess.OwnedGroups(command.Process.Pid)
	if err != nil {
		project.abortInterruptedCommand(t, command.Process.Pid, waited)
		t.Fatalf("capture interrupted process groups: %v", err)
	}
	runnerGroup, err := syscall.Getpgid(runnerPID)
	if err != nil || runnerGroup != runnerPID || !sqlProductContainsInt(groups, runnerGroup) {
		project.abortInterruptedCommand(t, command.Process.Pid, waited)
		t.Fatalf("built runner process group = pid:%d pgid:%d groups:%v error:%v", runnerPID, runnerGroup, groups, err)
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		project.abortInterruptedCommand(t, command.Process.Pid, waited)
		t.Fatalf("signal global sqlmigrate process: %v", err)
	}
	timer := time.NewTimer(30 * time.Second)
	select {
	case <-waited:
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	case <-timer.C:
		killErr := testprocess.KillGroups(groups, command.Process.Pid)
		boundedWaitErr := sqlProductBoundedWaitChannel(waited, 5*time.Second)
		absenceErr := testprocess.WaitAbsent(groups, 2*time.Second)
		if boundedWaitErr != nil {
			t.Fatalf("interrupted sqlmigrate did not terminate: %v", errors.Join(killErr, boundedWaitErr, absenceErr))
		}
		t.Fatalf("interrupted sqlmigrate exceeded its graceful deadline: %v", errors.Join(killErr, waitErr, absenceErr))
	}
	if err := testprocess.WaitAbsent(groups, 5*time.Second); err != nil {
		killErr := testprocess.KillGroups(groups, command.Process.Pid)
		cleanupErr := testprocess.WaitAbsent(groups, 2*time.Second)
		t.Fatalf("interrupted sqlmigrate process groups were not reaped: %v", errors.Join(err, killErr, cleanupErr))
	}
	if stdout.Truncated() || stderr.Truncated() {
		t.Fatal("interrupted sqlmigrate exceeded output limit")
	}
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatalf("wait for interrupted sqlmigrate: %v", waitErr)
		}
		exitCode = exitError.ExitCode()
	}
	result := sqlProductResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}
	project.assertCommandBoundary(t, state, result)
	return result
}

func (project *sqlProductProject) abortInterruptedCommand(t *testing.T, rootPID int, waited <-chan struct{}) {
	t.Helper()
	groups, discoveryErr := testprocess.OwnedGroups(rootPID)
	killErr := testprocess.KillGroups(groups, rootPID)
	waitErr := sqlProductBoundedWaitChannel(waited, 5*time.Second)
	absenceErr := testprocess.WaitAbsent(groups, 2*time.Second)
	if err := errors.Join(discoveryErr, killErr, waitErr, absenceErr); err != nil {
		t.Errorf("cleanup failed interrupted sqlmigrate: %v", err)
	}
}

func (project *sqlProductProject) assertCommandBoundary(t *testing.T, state sqlProductState, result sqlProductResult) {
	t.Helper()
	sensitive := project.sensitive(state)
	sqlProductAssertRedacted(t, result, sensitive...)
	project.assertNoPostgresConnectionAttempts(t)
	project.assertPoisonAbsent(t, state)
	project.assertWorkspaceEmpty(t)
	project.assertApplicationUnchanged(t)
	testfixture.AssertArtifactsRedacted(t, project.root, sensitive...)
	sqlProductAssertStateArtifactsRedacted(t, state.directory, sensitive...)
}

func (project *sqlProductProject) sensitive(state sqlProductState) []string {
	return []string{
		project.secret,
		project.postgresURL,
		sqlProductHostedDatabaseCanary,
		"sqlmigrate-db-path-canary-529d",
		sqlProductPostgresPoisonSchema,
		sqlProductPartialCanary,
		state.database,
		filepath.ToSlash(state.database),
		filepath.Base(state.database),
		state.openerMarker,
		filepath.ToSlash(state.openerMarker),
		filepath.Base(state.openerMarker),
	}
}

func (project *sqlProductProject) assertNoPostgresConnectionAttempts(t *testing.T) int64 {
	t.Helper()
	if project.postgresPoison == nil {
		t.Fatal("PostgreSQL poison listener is absent")
	}
	attempts, err := project.postgresPoison.checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("database-free sqlmigrate made %d PostgreSQL connection attempts, want 0", attempts)
	}
	return attempts
}

func (project *sqlProductProject) assertPoisonAbsent(t *testing.T, state sqlProductState) {
	t.Helper()
	for _, path := range []string{
		state.openerMarker,
		state.database,
		state.database + "-journal",
		state.database + "-wal",
		state.database + "-shm",
	} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("database-free sqlmigrate created poison artifact %q: %v", filepath.Base(path), err)
		}
	}
}

func (project *sqlProductProject) assertApplicationUnchanged(t *testing.T) {
	t.Helper()
	after := testfixture.ApplicationHashes(t, project.root)
	if len(after) != len(project.applicationHash) {
		t.Fatalf("sqlmigrate changed external application file roster: before=%d after=%d", len(project.applicationHash), len(after))
	}
	for path, before := range project.applicationHash {
		current, exists := after[path]
		if !exists || current != before {
			t.Fatalf("sqlmigrate changed external application file %s: exists=%t before=%x after=%x", filepath.Base(path), exists, before, current)
		}
	}
}

func (project *sqlProductProject) assertWorkspaceEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(project.scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("sqlmigrate left private workspace residue: %v", sqlProductEntryNames(entries))
	}
}

func sqlProductAssertStateArtifactsRedacted(t *testing.T, root string, sensitive ...string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return errors.New("external SQL migration state artifact is not bounded and regular")
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range sensitive {
			if value != "" && bytes.Contains(document, []byte(value)) {
				return errors.New("external SQL migration state artifact contains a sensitive value")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func sqlProductAssertSuccess(t *testing.T, result sqlProductResult, want string, sensitive ...string) {
	t.Helper()
	sqlProductAssertRedacted(t, result, sensitive...)
	if !result.matchesSuccess(want) {
		t.Fatalf("command success = exit:%d stdout:%q stderr:%q, want 0/%q/empty", result.exitCode, result.stdout, result.stderr, want)
	}
}

func (result sqlProductResult) matchesSuccess(want string) bool {
	return result.exitCode == 0 && result.stdout == want && result.stderr == ""
}

func sqlProductAssertFailure(t *testing.T, result sqlProductResult, exit int, stderr string, sensitive ...string) {
	t.Helper()
	sqlProductAssertRedacted(t, result, sensitive...)
	if result.exitCode != exit || result.stdout != "" || result.stderr != stderr {
		t.Fatalf("command failure = exit:%d stdout:%q stderr:%q, want %d/empty/%q", result.exitCode, result.stdout, result.stderr, exit, stderr)
	}
}

func sqlProductAssertRedacted(t *testing.T, result sqlProductResult, sensitive ...string) {
	t.Helper()
	combined := result.stdout + result.stderr
	for _, value := range sensitive {
		if value != "" && strings.Contains(combined, value) {
			t.Fatal("sqlmigrate command output exposed a sensitive value")
		}
	}
}

func sqlProductAssertMarker(t *testing.T, path, want string) int {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("marker %s has %d lines, want 1", filepath.Base(path), len(lines))
	}
	pidText, event, ok := strings.Cut(lines[0], "\t")
	pid, parseErr := strconv.Atoi(pidText)
	if !ok || parseErr != nil || pid <= 1 || event != want {
		t.Fatalf("marker %s = %q, want one %q event", filepath.Base(path), lines[0], want)
	}
	if err := testprocess.WaitAbsent([]int{pid}, 2*time.Second); err != nil {
		t.Fatalf("project runner process group was not reaped: %v", err)
	}
	return pid
}

func sqlProductWaitMarkerEvent(path, want string, done <-chan struct{}, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for {
		document, err := os.ReadFile(path)
		if err == nil {
			for _, line := range strings.Split(strings.TrimSuffix(string(document), "\n"), "\n") {
				pidText, event, ok := strings.Cut(line, "\t")
				pid, parseErr := strconv.Atoi(pidText)
				if ok && parseErr == nil && pid > 1 && event == want {
					return pid, nil
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		select {
		case <-done:
			return 0, errors.New("global sqlmigrate exited before the runner marker")
		default:
		}
		if time.Now().After(deadline) {
			return 0, errors.New("timed out waiting for the runner marker")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sqlProductAssertMarkerAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker %s unexpectedly exists: %v", filepath.Base(path), err)
	}
}

func sqlProductAuditApplicationSources(t *testing.T, repository, root string) {
	t.Helper()
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
	runnerAudits := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(document, []byte(resolvedRepository)) || bytes.Contains(document, []byte(filepath.ToSlash(resolvedRepository))) {
			return fmt.Errorf("application source %s contains repository absolute path", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relative) == "cmd/projectrunner/main.go" {
			if err := sqlProductAuditRunnerPipeline(document); err != nil {
				return fmt.Errorf("audit external project runner pipeline: %w", err)
			}
			runnerAudits++
		}
		parsed, err := parser.ParseFile(fileSet, path, document, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, specification := range parsed.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", path, err)
			}
			if !strings.HasPrefix(importPath, "github.com/progresshans/godj") {
				continue
			}
			if strings.Contains(importPath, "/internal/") || strings.Contains(importPath, "/conformance/") || strings.Contains(importPath, "/examples/") {
				return fmt.Errorf("application source %s imports forbidden GoDj package %s", path, importPath)
			}
			if _, ok := sqlProductAllowedGoDjImports[importPath]; !ok {
				return fmt.Errorf("application source %s imports non-allowlisted GoDj package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if runnerAudits != 1 {
		t.Fatalf("external application runner pipeline audits = %d, want 1", runnerAudits)
	}
}

// This audit owns source and I/O boundaries only. Actual source selection,
// encoding, renderer delegation and result publication are exercised by the
// built-runner counterexamples; private names and statement shapes are free.
func sqlProductAuditRunnerPipeline(document []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "external-project-runner.go", document, 0)
	if err != nil {
		return err
	}
	imports := make(map[string]string)
	for _, specification := range file.Imports {
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			return err
		}
		if _, ok := sqlProductAllowedGoDjImports[path]; !ok &&
			path != "context" && path != "errors" && path != "fmt" && path != "os" {
			return fmt.Errorf("external runner has unexpected import %q", path)
		}
		name := filepath.Base(path)
		if specification.Name != nil {
			name = specification.Name.Name
		}
		if name == "." || name == "_" || imports[name] != "" {
			return errors.New("external runner import has no distinct package binding")
		}
		imports[name] = path
	}
	binding := func(expression ast.Expr) string {
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok {
			return ""
		}
		owner, ok := selector.X.(*ast.Ident)
		if !ok || owner.Obj != nil || imports[owner.Name] == "" {
			return ""
		}
		return imports[owner.Name] + "." + selector.Sel.Name
	}
	required := map[string]bool{
		"github.com/progresshans/godj/project.Run":                         false,
		"github.com/progresshans/godj/migrations/definition.Encode":        false,
		"github.com/progresshans/godj/db/sqlite.NewMigrationSQLRenderer":   false,
		"github.com/progresshans/godj/db/postgres.NewMigrationSQLRenderer": false,
	}
	streams := make(map[ast.Expr]bool)
	writeOpens := make(map[ast.Expr]bool)
	var boundaryErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if boundaryErr != nil {
			return false
		}
		switch value := node.(type) {
		case *ast.CallExpr:
			call := binding(value.Fun)
			if _, ok := required[call]; ok {
				required[call] = true
			}
			if call == "github.com/progresshans/godj/project.Run" && len(value.Args) == 5 {
				streams[value.Args[3]] = binding(value.Args[3]) == "os.Stdin"
				streams[value.Args[4]] = binding(value.Args[4]) == "os.Stdout"
			}
			if call == "os.OpenFile" {
				// Marker descriptors are write-only; they cannot read oracle files.
				writable := false
				var writeFlags func(ast.Expr) bool
				writeFlags = func(expression ast.Expr) bool {
					if binary, ok := expression.(*ast.BinaryExpr); ok {
						return binary.Op == token.OR && writeFlags(binary.X) && writeFlags(binary.Y)
					}
					switch binding(expression) {
					case "os.O_WRONLY":
						writable = true
						return true
					case "os.O_CREATE", "os.O_APPEND":
						return true
					}
					return false
				}
				if len(value.Args) != 3 || !writeFlags(value.Args[1]) || !writable {
					boundaryErr = errors.New("external runner file descriptor is not write-only")
				}
				writeOpens[value.Fun] = true
			}
			if identifier, ok := value.Fun.(*ast.Ident); ok && (identifier.Name == "print" || identifier.Name == "println") {
				boundaryErr = errors.New("external runner writes directly to process output")
			}
		case *ast.SelectorExpr:
			symbol := binding(value)
			switch {
			case symbol == "os.Stdin" || symbol == "os.Stdout":
				if !streams[value] {
					boundaryErr = errors.New("external runner stream bypasses project.Run")
				}
			case symbol == "os.OpenFile":
				if !writeOpens[value] {
					boundaryErr = errors.New("external runner file opener bypasses flag validation")
				}
			case strings.HasPrefix(symbol, "os."):
				switch symbol {
				case "os.Args", "os.Stderr", "os.Getenv", "os.Setenv", "os.Getpid", "os.Exit",
					"os.WriteFile", "os.O_CREATE", "os.O_APPEND", "os.O_WRONLY":
				default:
					boundaryErr = fmt.Errorf("external runner uses forbidden I/O symbol %s", symbol)
				}
			case strings.HasPrefix(symbol, "fmt."):
				switch symbol {
				case "fmt.Errorf", "fmt.Sprintf", "fmt.Fprintf", "fmt.Fprintln":
				default:
					boundaryErr = fmt.Errorf("external runner uses direct or input formatting %s", symbol)
				}
			case strings.HasPrefix(symbol, "github.com/progresshans/godj/db/"):
				if _, ok := required[symbol]; !ok && symbol != "github.com/progresshans/godj/db/postgres.MigrationSQLConfig" {
					boundaryErr = fmt.Errorf("external runner uses database-bearing symbol %s", symbol)
				}
			}
		case *ast.BasicLit:
			if value.Kind == token.STRING {
				literal, _ := strconv.Unquote(value.Value)
				for _, prefix := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "SELECT ", "INSERT ", "UPDATE ", "DELETE "} {
					if strings.Contains(strings.ToUpper(literal), prefix) {
						boundaryErr = errors.New("external runner embeds SQL output")
					}
				}
			}
		}
		return true
	})
	if boundaryErr != nil {
		return boundaryErr
	}
	for call, seen := range required {
		if !seen {
			return fmt.Errorf("external runner omits public boundary %s", call)
		}
	}
	return nil
}

func sqlProductWriteFile(t *testing.T, path string, document []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, document, mode); err != nil {
		t.Fatal(err)
	}
}

func sqlProductRunSuccess(t *testing.T, directory string, environment []string, name string, arguments ...string) {
	t.Helper()
	result, err := sqlProductRun(directory, environment, name, arguments...)
	if err != nil {
		t.Fatalf("%s %s failed: %v", name, strings.Join(arguments, " "), err)
	}
	if result.exitCode != 0 {
		t.Fatalf("%s %s failed: exit=%d stdout=%q stderr=%q", name, strings.Join(arguments, " "), result.exitCode, result.stdout, result.stderr)
	}
}

func sqlProductRun(directory string, environment []string, name string, arguments ...string) (sqlProductResult, error) {
	stdout := testprocess.NewBuffer(sqlProductMaximumOutput)
	stderr := testprocess.NewBuffer(sqlProductMaximumOutput)
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return sqlProductResult{}, fmt.Errorf("start %s %s: %w", name, strings.Join(arguments, " "), err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(sqlProductCommandTimeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-timer.C:
		groups, discoveryErr := testprocess.OwnedGroups(command.Process.Pid)
		killErr := testprocess.KillGroups(groups, command.Process.Pid)
		waitErr = testprocess.Wait(waited, 5*time.Second)
		absenceErr := testprocess.WaitAbsent(groups, 2*time.Second)
		return sqlProductResult{}, fmt.Errorf("%s %s timed out: %w", name, strings.Join(arguments, " "), errors.Join(discoveryErr, killErr, waitErr, absenceErr))
	}
	if stdout.Truncated() || stderr.Truncated() {
		return sqlProductResult{}, fmt.Errorf("%s %s exceeded output limit", name, strings.Join(arguments, " "))
	}
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			return sqlProductResult{}, fmt.Errorf("wait for %s %s: %w", name, strings.Join(arguments, " "), waitErr)
		}
		exitCode = exitError.ExitCode()
	}
	if err := testprocess.WaitAbsent([]int{command.Process.Pid}, 2*time.Second); err != nil {
		return sqlProductResult{}, fmt.Errorf("wait for external root process group: %w", err)
	}
	return sqlProductResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}, nil
}

func sqlProductBoundedWaitChannel(waited <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waited:
		return nil
	case <-timer.C:
		return errors.New("process Wait remained blocked after forced cleanup")
	}
}

func sqlProductSanitizeDatabaseEnvironment(environment []string) []string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || sqlProductIsDatabaseEnvironmentKey(key) {
			continue
		}
		values[key] = value
	}
	return testfixture.Environment(nil, values)
}

func sqlProductIsDatabaseEnvironmentKey(key string) bool {
	if _, ok := sqlProductDatabaseURLKeys[key]; ok {
		return true
	}
	return strings.HasPrefix(key, "PG") ||
		strings.HasPrefix(key, "POSTGRES_") ||
		strings.HasPrefix(key, "POSTGRESQL_") ||
		strings.HasSuffix(key, "_POSTGRES_URL") ||
		strings.HasSuffix(key, "_DATABASE_URL")
}

func sqlProductRemoveEnvironment(environment []string, keys ...string) []string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for _, key := range keys {
		delete(values, key)
	}
	return testfixture.Environment(nil, values)
}

func sqlProductEntryNames(entries []os.DirEntry) []string {
	result := make([]string, len(entries))
	for index := range entries {
		result[index] = entries[index].Name()
	}
	sort.Strings(result)
	return result
}

func sqlProductContainsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
