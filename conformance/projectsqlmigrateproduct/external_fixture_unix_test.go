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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
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
	repository := sqlProductRepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-sqlmigrate-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external sqlmigrate universe: %v", err)
		}
	})
	sqlProductAssertSeparateRoot(t, repository, universe)

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

	setupEnv := sqlProductSanitizeDatabaseEnvironment(sqlProductRemoveEnvironment(sqlProductEnvironment(os.Environ(), map[string]string{
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
	baseEnv := sqlProductEnvironment(setupEnv, baseOverrides)
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
	project.applicationHash = project.captureApplicationHashes(t)
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
	return sqlProductEnvironment(project.baseEnv, map[string]string{
		sqlProductCatalogEnvironment:        catalog,
		sqlProductRendererEnvironment:       renderer,
		sqlProductInitMarkerEnvironment:     state.initMarker,
		sqlProductRendererMarkerEnvironment: state.rendererMarker,
		sqlProductOpenerMarkerEnvironment:   state.openerMarker,
		sqlProductDatabaseEnvironment:       state.database,
	})
}

func (project *sqlProductProject) postgresEnvironment(state sqlProductState) []string {
	return sqlProductEnvironment(
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
	stdout := &sqlProductBoundedBuffer{maximum: sqlProductMaximumOutput}
	stderr := &sqlProductBoundedBuffer{maximum: sqlProductMaximumOutput}
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
	groups, err := sqlProductOwnedProcessGroups(command.Process.Pid)
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
		killErr := sqlProductKillProcessGroups(groups, command.Process.Pid)
		boundedWaitErr := sqlProductBoundedWaitChannel(waited, 5*time.Second)
		absenceErr := sqlProductWaitProcessGroupsAbsent(groups, 2*time.Second)
		if boundedWaitErr != nil {
			t.Fatalf("interrupted sqlmigrate did not terminate: %v", errors.Join(killErr, boundedWaitErr, absenceErr))
		}
		t.Fatalf("interrupted sqlmigrate exceeded its graceful deadline: %v", errors.Join(killErr, waitErr, absenceErr))
	}
	if err := sqlProductWaitProcessGroupsAbsent(groups, 5*time.Second); err != nil {
		killErr := sqlProductKillProcessGroups(groups, command.Process.Pid)
		cleanupErr := sqlProductWaitProcessGroupsAbsent(groups, 2*time.Second)
		t.Fatalf("interrupted sqlmigrate process groups were not reaped: %v", errors.Join(err, killErr, cleanupErr))
	}
	if stdout.truncated || stderr.truncated {
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
	groups, discoveryErr := sqlProductOwnedProcessGroups(rootPID)
	killErr := sqlProductKillProcessGroups(groups, rootPID)
	waitErr := sqlProductBoundedWaitChannel(waited, 5*time.Second)
	absenceErr := sqlProductWaitProcessGroupsAbsent(groups, 2*time.Second)
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
	project.assertApplicationArtifactsRedacted(t, sensitive...)
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

func (project *sqlProductProject) captureApplicationHashes(t *testing.T) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(project.root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		if !info.Mode().IsRegular() {
			return fmt.Errorf("external application contains non-regular entry %s", path)
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[path] = sha256.Sum256(document)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (project *sqlProductProject) assertApplicationUnchanged(t *testing.T) {
	t.Helper()
	after := project.captureApplicationHashes(t)
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

func (project *sqlProductProject) assertApplicationArtifactsRedacted(t *testing.T, sensitive ...string) {
	t.Helper()
	err := filepath.WalkDir(project.root, func(path string, entry fs.DirEntry, walkErr error) error {
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
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range sensitive {
			if value != "" && bytes.Contains(document, []byte(value)) {
				return errors.New("external application artifact contains a sensitive value")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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
	if result.exitCode != 0 || result.stdout != want || result.stderr != "" {
		t.Fatalf("command success = exit:%d stdout:%q stderr:%q, want 0/%q/empty", result.exitCode, result.stdout, result.stderr, want)
	}
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
	if err := sqlProductWaitProcessGroupsAbsent([]int{pid}, 2*time.Second); err != nil {
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

func sqlProductRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	root, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(source), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func sqlProductAssertSeparateRoot(t *testing.T, repository, external string) {
	t.Helper()
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	resolvedExternal, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(resolvedRepository, resolvedExternal)
	if err != nil {
		t.Fatal(err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		t.Fatalf("external consumer root %q is inside repository %q", resolvedExternal, resolvedRepository)
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

func sqlProductAuditRunnerPipeline(document []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "external-project-runner.go", document, 0)
	if err != nil {
		return err
	}
	if err := sqlProductAuditRunnerImports(file); err != nil {
		return err
	}
	if err := sqlProductAuditRunnerRendererSurface(file); err != nil {
		return err
	}
	mainFunction, err := sqlProductASTFunction(file, "main", "")
	if err != nil {
		return err
	}
	sourcesFunction, err := sqlProductASTFunction(file, "sourcesForCatalog", "")
	if err != nil {
		return err
	}
	rendererFunction, err := sqlProductASTFunction(file, "rendererForMode", "")
	if err != nil {
		return err
	}
	postgresEnvironment, err := sqlProductASTFunction(file, "postgresEnvironmentIsPoisoned", "")
	if err != nil {
		return err
	}
	observedRender, err := sqlProductASTFunction(file, "RenderForwardMigrationSQL", "observedRenderer")
	if err != nil {
		return err
	}
	if err := sqlProductAuditRunnerMain(mainFunction); err != nil {
		return err
	}
	if err := sqlProductAuditRunnerSources(sourcesFunction); err != nil {
		return err
	}
	if err := sqlProductAuditRunnerRenderer(rendererFunction, postgresEnvironment, observedRender); err != nil {
		return err
	}
	stdoutReferences := 0
	var directOutputCalls, hardcodedSQL int
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SelectorExpr:
			if sqlProductASTSelector(value, "os", "Stdout") {
				stdoutReferences++
			}
		case *ast.CallExpr:
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok && sqlProductASTSelectorPackage(selector, "fmt") {
				switch selector.Sel.Name {
				case "Print", "Printf", "Println":
					directOutputCalls++
				}
			}
			if identifier, ok := value.Fun.(*ast.Ident); ok && (identifier.Name == "print" || identifier.Name == "println") {
				directOutputCalls++
			}
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				break
			}
			literal, unquoteErr := strconv.Unquote(value.Value)
			if unquoteErr != nil {
				break
			}
			upper := strings.ToUpper(literal)
			for _, prefix := range []string{"CREATE TABLE", "ALTER TABLE", "DROP TABLE", "SELECT ", "INSERT ", "UPDATE ", "DELETE "} {
				if strings.Contains(upper, prefix) {
					hardcodedSQL++
					break
				}
			}
		}
		return true
	})
	if stdoutReferences != 1 || directOutputCalls != 0 || hardcodedSQL != 0 {
		return fmt.Errorf(
			"external runner output boundary = stdout:%d direct:%d hardcoded_sql:%d",
			stdoutReferences,
			directOutputCalls,
			hardcodedSQL,
		)
	}
	return nil
}

func sqlProductAuditRunnerImports(file *ast.File) error {
	expected := map[string]struct{}{
		"context": {}, "errors": {}, "fmt": {}, "os": {},
		"github.com/progresshans/godj/db/postgres":           {},
		"github.com/progresshans/godj/db/sqlite":             {},
		"github.com/progresshans/godj/migrations":            {},
		"github.com/progresshans/godj/migrations/backend":    {},
		"github.com/progresshans/godj/migrations/definition": {},
		"github.com/progresshans/godj/project":               {},
		"github.com/progresshans/godj/schema/ir":             {},
	}
	for _, specification := range file.Imports {
		if specification.Name != nil {
			return errors.New("external runner import aliases are forbidden")
		}
		path, err := strconv.Unquote(specification.Path.Value)
		if err != nil {
			return err
		}
		if _, ok := expected[path]; !ok {
			return fmt.Errorf("external runner has unexpected import %q", path)
		}
		delete(expected, path)
	}
	if len(expected) != 0 {
		return errors.New("external runner import boundary is incomplete")
	}
	return nil
}

func sqlProductAuditRunnerRendererSurface(file *ast.File) error {
	wantTypes := map[string]int{
		"observedRenderer":         1,
		"failingRenderer":          1,
		"waitCancellationRenderer": 1,
	}
	wantMethods := map[string]int{
		"observedRenderer":         1,
		"failingRenderer":          1,
		"waitCancellationRenderer": 1,
	}
	observedTypes := make(map[string]int, len(wantTypes))
	observedMethods := make(map[string]int, len(wantMethods))
	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.GenDecl:
			if value.Tok != token.TYPE {
				continue
			}
			for _, specification := range value.Specs {
				typeSpecification, ok := specification.(*ast.TypeSpec)
				if !ok {
					return errors.New("external runner type declaration is invalid")
				}
				observedTypes[typeSpecification.Name.Name]++
			}
		case *ast.FuncDecl:
			if value.Name.Name == "RenderForwardMigrationSQL" {
				observedMethods[sqlProductASTReceiver(value)]++
			}
		}
	}
	if len(observedTypes) != len(wantTypes) || len(observedMethods) != len(wantMethods) {
		return errors.New("external runner renderer type or method surface is not exact")
	}
	for name, count := range wantTypes {
		if observedTypes[name] != count || observedMethods[name] != wantMethods[name] {
			return fmt.Errorf(
				"external runner renderer surface %s = types:%d methods:%d, want types:%d methods:%d",
				name,
				observedTypes[name],
				observedMethods[name],
				count,
				wantMethods[name],
			)
		}
	}
	return nil
}

func sqlProductAuditRunnerMain(function *ast.FuncDecl) error {
	var sourceCalls, runCalls []*ast.CallExpr
	var sourceAssignments, runAssignments []*ast.AssignStmt
	sourceWrites := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for _, target := range value.Lhs {
				if sqlProductASTIdentifier(target, "sources") {
					sourceWrites++
				}
			}
			if len(value.Rhs) != 1 {
				break
			}
			call, ok := value.Rhs[0].(*ast.CallExpr)
			if !ok {
				break
			}
			if sqlProductASTIdentifier(call.Fun, "sourcesForCatalog") {
				sourceAssignments = append(sourceAssignments, value)
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && sqlProductASTSelector(selector, "project", "Run") {
				runAssignments = append(runAssignments, value)
			}
		case *ast.CallExpr:
			if sqlProductASTIdentifier(value.Fun, "sourcesForCatalog") {
				sourceCalls = append(sourceCalls, value)
			}
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok && sqlProductASTSelector(selector, "project", "Run") {
				runCalls = append(runCalls, value)
			}
		}
		return true
	})
	if len(sourceCalls) != 1 || len(runCalls) != 1 || len(sourceAssignments) != 1 || len(runAssignments) != 1 || sourceWrites != 1 ||
		sourceCalls[0].Pos() >= runCalls[0].Pos() {
		return errors.New("external runner main does not own one source load before one project.Run call")
	}
	sourceAssignment := sourceAssignments[0]
	if sourceAssignment.Tok != token.DEFINE || len(sourceAssignment.Lhs) != 2 ||
		!sqlProductASTIdentifier(sourceAssignment.Lhs[0], "sources") ||
		!sqlProductASTIdentifier(sourceAssignment.Lhs[1], "err") || sourceAssignment.Rhs[0] != sourceCalls[0] {
		return errors.New("external runner source selection result is not bound to sources and err")
	}
	if len(sourceCalls[0].Args) != 1 || !sqlProductASTGetenv(sourceCalls[0].Args[0], "catalogEnvironment") {
		return errors.New("external runner source selection is not environment-derived")
	}
	runAssignment := runAssignments[0]
	if runAssignment.Tok != token.ASSIGN || len(runAssignment.Lhs) != 1 ||
		!sqlProductASTIdentifier(runAssignment.Lhs[0], "err") || runAssignment.Rhs[0] != runCalls[0] {
		return errors.New("external runner project.Run result is not bound to err")
	}
	run := runCalls[0]
	if len(run.Args) != 5 || !sqlProductASTCall(run.Args[0], "context", "Background", 0) ||
		!sqlProductASTArgsSlice(run.Args[2]) || !sqlProductASTSelectorExpression(run.Args[3], "os", "Stdin") ||
		!sqlProductASTSelectorExpression(run.Args[4], "os", "Stdout") {
		return errors.New("external runner project.Run boundary is not exact")
	}
	config, ok := run.Args[1].(*ast.CompositeLit)
	if !ok || !sqlProductASTSelectorExpression(config.Type, "project", "Config") || len(config.Elts) != 3 {
		return errors.New("external runner project.Config boundary is not exact")
	}
	fields := make(map[string]ast.Expr, len(config.Elts))
	for _, element := range config.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return errors.New("external runner project.Config contains an unkeyed field")
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name == "" {
			return errors.New("external runner project.Config key is invalid")
		}
		fields[key.Name] = pair.Value
	}
	if len(fields) != 3 || !sqlProductASTIdentifier(fields["MigrationDefinitionSources"], "sources") ||
		!sqlProductASTIdentifier(fields["OpenMigrationBackend"], "poisonOpenMigrationBackend") {
		return errors.New("external runner project.Config source/opener bindings are not exact")
	}
	renderer, ok := fields["MigrationSQLRenderer"].(*ast.CallExpr)
	if !ok || len(renderer.Args) != 1 || !sqlProductASTIdentifier(renderer.Fun, "rendererForMode") ||
		!sqlProductASTGetenv(renderer.Args[0], "rendererEnvironment") {
		return errors.New("external runner project.Config renderer binding is not exact")
	}
	return nil
}

func sqlProductAuditRunnerSources(function *ast.FuncDecl) error {
	var fullCatalogCalls, encodeCalls []*ast.CallExpr
	var sourceDocuments []*ast.CompositeLit
	var assignments []*ast.AssignStmt
	var ranges []*ast.RangeStmt
	var returns []*ast.ReturnStmt
	definitionsWrites := 0
	sourcesWrites := 0
	sourceIndexWrites := 0
	documentWrites := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			if sqlProductASTIdentifier(value.Fun, "fullCatalog") && len(value.Args) == 0 {
				fullCatalogCalls = append(fullCatalogCalls, value)
			}
			if selector, ok := value.Fun.(*ast.SelectorExpr); ok && sqlProductASTSelector(selector, "definition", "Encode") {
				encodeCalls = append(encodeCalls, value)
			}
		case *ast.AssignStmt:
			assignments = append(assignments, value)
			for _, target := range value.Lhs {
				switch {
				case sqlProductASTIdentifier(target, "definitions"):
					definitionsWrites++
				case sqlProductASTIdentifier(target, "sources"):
					sourcesWrites++
				case sqlProductASTIdentifier(target, "document"):
					documentWrites++
				default:
					index, ok := target.(*ast.IndexExpr)
					if ok && sqlProductASTIdentifier(index.X, "sources") {
						sourceIndexWrites++
					}
				}
			}
		case *ast.CompositeLit:
			if !sqlProductASTSelectorExpression(value.Type, "definition", "Source") {
				break
			}
			for _, element := range value.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if ok && key.Name == "Document" && sqlProductASTIdentifier(pair.Value, "document") {
					sourceDocuments = append(sourceDocuments, value)
				}
			}
		case *ast.RangeStmt:
			ranges = append(ranges, value)
		case *ast.ReturnStmt:
			returns = append(returns, value)
		}
		return true
	})
	if len(fullCatalogCalls) != 1 || len(encodeCalls) != 1 || len(sourceDocuments) != 1 || len(ranges) != 1 ||
		definitionsWrites != 1 || sourcesWrites != 1 || sourceIndexWrites != 1 || documentWrites != 1 {
		return fmt.Errorf(
			"external runner source encoding boundary = catalog:%d encode:%d documents:%d ranges:%d definitions_writes:%d sources_writes:%d source_index_writes:%d document_writes:%d",
			len(fullCatalogCalls),
			len(encodeCalls),
			len(sourceDocuments),
			len(ranges),
			definitionsWrites,
			sourcesWrites,
			sourceIndexWrites,
			documentWrites,
		)
	}
	if !sqlProductASTAssignmentBindsCall(assignments, fullCatalogCalls[0], token.DEFINE, "definitions") {
		return errors.New("external runner full catalog result is not bound to definitions")
	}
	rangeStatement := ranges[0]
	if rangeStatement.Tok != token.DEFINE || !sqlProductASTIdentifier(rangeStatement.Key, "index") ||
		!sqlProductASTIdentifier(rangeStatement.Value, "migration") ||
		!sqlProductASTIdentifier(rangeStatement.X, "definitions") {
		return errors.New("external runner definition traversal is not bound to index migration and definitions")
	}
	encode := encodeCalls[0]
	if len(encode.Args) != 2 || !sqlProductASTIdentifier(encode.Args[1], "migration") ||
		!sqlProductASTDefinitionProducer(encode.Args[0]) ||
		!sqlProductASTAssignmentBindsCall(assignments, encode, token.DEFINE, "document", "err") {
		return errors.New("external runner definition.Encode result is not bound to document and err")
	}
	if !sqlProductASTSourceAssignment(assignments, sourceDocuments[0]) {
		return errors.New("external runner encoded document is not assigned to sources[index]")
	}
	if !sqlProductASTSourceReturns(returns) {
		return errors.New("external runner source return paths are not coupled to the encoded sources slice")
	}
	return nil
}

func sqlProductAuditRunnerRenderer(rendererFunction, postgresEnvironment, observedRender *ast.FuncDecl) error {
	if err := sqlProductAuditRunnerSupportedRendererBranches(rendererFunction); err != nil {
		return err
	}
	constructors := 0
	configurations := 0
	observedDelegates := 0
	environmentChecks := 0
	ast.Inspect(rendererFunction.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CallExpr:
			if sqlProductASTIdentifier(value.Fun, "postgresEnvironmentIsPoisoned") && len(value.Args) == 0 {
				environmentChecks++
			}
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || !sqlProductASTSelector(selector, "postgres", "NewMigrationSQLRenderer") || len(value.Args) != 1 {
				break
			}
			constructors++
			configuration, ok := value.Args[0].(*ast.CompositeLit)
			if !ok || !sqlProductASTSelectorExpression(configuration.Type, "postgres", "MigrationSQLConfig") {
				break
			}
			for _, element := range configuration.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if ok && key.Name == "Schema" && sqlProductASTGetenv(pair.Value, "postgresSchemaEnvironment") {
					configurations++
				}
			}
		case *ast.CompositeLit:
			if !sqlProductASTIdentifier(value.Type, "observedRenderer") {
				break
			}
			for _, element := range value.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if ok && key.Name == "delegate" && sqlProductASTIdentifier(pair.Value, "delegate") {
					observedDelegates++
				}
			}
		}
		return true
	})
	if err := sqlProductAuditRunnerPostgresEnvironment(postgresEnvironment); err != nil {
		return err
	}
	var delegateCalls []*ast.CallExpr
	var observedReturns []*ast.ReturnStmt
	ast.Inspect(observedRender.Body, func(node ast.Node) bool {
		if result, ok := node.(*ast.ReturnStmt); ok {
			observedReturns = append(observedReturns, result)
		}
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 || !sqlProductASTIdentifier(call.Args[0], "ctx") ||
			!sqlProductASTIdentifier(call.Args[1], "request") {
			return true
		}
		method, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || method.Sel.Name != "RenderForwardMigrationSQL" {
			return true
		}
		delegate, ok := method.X.(*ast.SelectorExpr)
		if ok && sqlProductASTSelector(delegate, "renderer", "delegate") {
			delegateCalls = append(delegateCalls, call)
		}
		return true
	})
	delegateReturnCoupled := false
	if len(observedRender.Body.List) != 2 || len(observedReturns) != 2 || len(delegateCalls) != 1 {
		delegateReturnCoupled = false
	} else if result, ok := observedRender.Body.List[len(observedRender.Body.List)-1].(*ast.ReturnStmt); ok &&
		len(result.Results) == 1 && result.Results[0] == delegateCalls[0] {
		delegateReturnCoupled = true
	}
	if constructors != 1 || configurations != 1 || observedDelegates != 1 || environmentChecks != 1 ||
		len(delegateCalls) != 1 || !delegateReturnCoupled {
		return fmt.Errorf(
			"external runner renderer boundary = constructors:%d configs:%d wrappers:%d environment:%d delegates:%d return_coupled:%t",
			constructors,
			configurations,
			observedDelegates,
			environmentChecks,
			len(delegateCalls),
			delegateReturnCoupled,
		)
	}
	return nil
}

func sqlProductAuditRunnerSupportedRendererBranches(function *ast.FuncDecl) error {
	var modeSwitches []*ast.SwitchStmt
	delegateWrites := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SwitchStmt:
			if sqlProductASTIdentifier(value.Tag, "mode") {
				modeSwitches = append(modeSwitches, value)
			}
		case *ast.AssignStmt:
			for _, target := range value.Lhs {
				if sqlProductASTIdentifier(target, "delegate") {
					delegateWrites++
				}
			}
		}
		return true
	})
	if len(modeSwitches) != 1 || delegateWrites != 1 {
		return fmt.Errorf("external runner renderer mode switches/delegate writes = %d/%d, want 1/1", len(modeSwitches), delegateWrites)
	}
	branches := make(map[string]*ast.CaseClause)
	defaultBranches := 0
	for _, statement := range modeSwitches[0].Body.List {
		clause, ok := statement.(*ast.CaseClause)
		if !ok || len(clause.List) > 1 {
			return errors.New("external runner renderer mode case is invalid")
		}
		if len(clause.List) == 0 {
			defaultBranches++
			continue
		}
		literal, ok := clause.List[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return errors.New("external runner renderer mode case is not a string literal")
		}
		label, err := strconv.Unquote(literal.Value)
		if err != nil || branches[label] != nil {
			return errors.New("external runner renderer mode case is duplicated or invalid")
		}
		branches[label] = clause
	}
	wantLabels := map[string]bool{"sqlite": true, "postgres": true, "fail": true, "nil": true, "wait_cancel": true}
	if defaultBranches != 1 || len(branches) != len(wantLabels) {
		return errors.New("external runner renderer mode set is not exact")
	}
	for label := range branches {
		if !wantLabels[label] {
			return fmt.Errorf("external runner renderer mode %q is unexpected", label)
		}
	}
	sqliteBranch := branches["sqlite"]
	postgresBranch := branches["postgres"]
	if sqliteBranch == nil || postgresBranch == nil || len(sqliteBranch.Body) != 1 || len(postgresBranch.Body) != 4 {
		return errors.New("external runner supported renderer branches are not exact")
	}
	sqliteDelegate, ok := sqlProductASTObservedRendererReturn(sqliteBranch.Body[0])
	if !ok || !sqlProductASTCall(sqliteDelegate, "sqlite", "NewMigrationSQLRenderer", 0) {
		return errors.New("external runner SQLite branch does not return its configured observed renderer")
	}
	poisonCheck, ok := postgresBranch.Body[0].(*ast.IfStmt)
	if !ok {
		return errors.New("external runner PostgreSQL branch does not begin with the poison-environment guard")
	}
	negation, ok := poisonCheck.Cond.(*ast.UnaryExpr)
	if !ok || negation.Op != token.NOT {
		return errors.New("external runner PostgreSQL poison-environment guard is not exact")
	}
	check, ok := negation.X.(*ast.CallExpr)
	if !ok || len(check.Args) != 0 || !sqlProductASTIdentifier(check.Fun, "postgresEnvironmentIsPoisoned") {
		return errors.New("external runner PostgreSQL poison-environment check is not exact")
	}
	delegateAssignment, ok := postgresBranch.Body[1].(*ast.AssignStmt)
	if !ok || delegateAssignment.Tok != token.DEFINE || len(delegateAssignment.Lhs) != 1 ||
		!sqlProductASTIdentifier(delegateAssignment.Lhs[0], "delegate") || len(delegateAssignment.Rhs) != 1 ||
		!sqlProductASTCall(delegateAssignment.Rhs[0], "postgres", "NewMigrationSQLRenderer", 1) {
		return errors.New("external runner PostgreSQL branch does not bind its configured delegate")
	}
	postgresDelegate, ok := sqlProductASTObservedRendererReturn(postgresBranch.Body[3])
	if !ok || !sqlProductASTIdentifier(postgresDelegate, "delegate") {
		return errors.New("external runner PostgreSQL branch does not return its derived observed renderer")
	}
	return nil
}

func sqlProductASTObservedRendererReturn(statement ast.Stmt) (ast.Expr, bool) {
	result, ok := statement.(*ast.ReturnStmt)
	if !ok || len(result.Results) != 1 {
		return nil, false
	}
	wrapper, ok := result.Results[0].(*ast.CompositeLit)
	if !ok || !sqlProductASTIdentifier(wrapper.Type, "observedRenderer") || len(wrapper.Elts) != 1 {
		return nil, false
	}
	pair, ok := wrapper.Elts[0].(*ast.KeyValueExpr)
	if !ok || !sqlProductASTIdentifier(pair.Key, "delegate") {
		return nil, false
	}
	return pair.Value, true
}

func sqlProductAuditRunnerPostgresEnvironment(function *ast.FuncDecl) error {
	want := map[string]int{
		"DATABASE_URL": 1, "GODJ_TEST_POSTGRES_URL": 1, "POSTGRESQL_URL": 1, "POSTGRES_URL": 1,
		"PGHOST": 1, "PGPORT": 1, "PGDATABASE": 1, "PGUSER": 1, "PGPASSWORD": 1, "PGSSLMODE": 1,
		"$secretEnvironment": 1,
	}
	observed := make(map[string]int, len(want))
	invalid := 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if len(call.Args) != 1 || !sqlProductASTSelectorExpression(call.Fun, "os", "Getenv") {
			invalid++
			return true
		}
		switch key := call.Args[0].(type) {
		case *ast.BasicLit:
			value, err := strconv.Unquote(key.Value)
			if err != nil {
				invalid++
			} else {
				observed[value]++
			}
		case *ast.Ident:
			observed["$"+key.Name]++
		default:
			invalid++
		}
		return true
	})
	if invalid != 0 || len(observed) != len(want) {
		return errors.New("external runner PostgreSQL poison environment boundary is invalid")
	}
	for key, count := range want {
		if observed[key] != count {
			return fmt.Errorf("external runner PostgreSQL poison environment key %s count = %d, want %d", key, observed[key], count)
		}
	}
	return nil
}

func sqlProductASTFunction(file *ast.File, name, receiver string) (*ast.FuncDecl, error) {
	var result *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name || sqlProductASTReceiver(function) != receiver {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("external runner function %s is duplicated", name)
		}
		result = function
	}
	if result == nil {
		return nil, fmt.Errorf("external runner function %s is absent", name)
	}
	return result, nil
}

func sqlProductASTReceiver(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return ""
	}
	switch receiver := function.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return receiver.Name
	case *ast.StarExpr:
		if identifier, ok := receiver.X.(*ast.Ident); ok {
			return identifier.Name
		}
	}
	return ""
}

func sqlProductASTIdentifier(expression ast.Expr, name string) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == name
}

func sqlProductASTSelector(expression *ast.SelectorExpr, owner, name string) bool {
	return expression != nil && expression.Sel.Name == name && sqlProductASTIdentifier(expression.X, owner)
}

func sqlProductASTSelectorPackage(expression *ast.SelectorExpr, owner string) bool {
	return expression != nil && sqlProductASTIdentifier(expression.X, owner)
}

func sqlProductASTSelectorExpression(expression ast.Expr, owner, name string) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	return ok && sqlProductASTSelector(selector, owner, name)
}

func sqlProductASTCall(expression ast.Expr, owner, name string, arguments int) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok || len(call.Args) != arguments {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sqlProductASTSelector(selector, owner, name)
}

func sqlProductASTGetenv(expression ast.Expr, key string) bool {
	call, ok := expression.(*ast.CallExpr)
	return ok && len(call.Args) == 1 && sqlProductASTSelectorExpression(call.Fun, "os", "Getenv") &&
		sqlProductASTIdentifier(call.Args[0], key)
}

func sqlProductASTArgsSlice(expression ast.Expr) bool {
	slice, ok := expression.(*ast.SliceExpr)
	if !ok || !sqlProductASTSelectorExpression(slice.X, "os", "Args") || slice.High != nil || slice.Max != nil {
		return false
	}
	low, ok := slice.Low.(*ast.BasicLit)
	return ok && low.Kind == token.INT && low.Value == "1"
}

func sqlProductASTAssignmentBindsCall(
	assignments []*ast.AssignStmt,
	call *ast.CallExpr,
	tokenKind token.Token,
	names ...string,
) bool {
	matches := 0
	for _, assignment := range assignments {
		if assignment.Tok != tokenKind || len(assignment.Lhs) != len(names) ||
			len(assignment.Rhs) != 1 || assignment.Rhs[0] != call {
			continue
		}
		exact := true
		for index, name := range names {
			if !sqlProductASTIdentifier(assignment.Lhs[index], name) {
				exact = false
				break
			}
		}
		if exact {
			matches++
		}
	}
	return matches == 1
}

func sqlProductASTDefinitionProducer(expression ast.Expr) bool {
	producer, ok := expression.(*ast.CompositeLit)
	if !ok || !sqlProductASTSelectorExpression(producer.Type, "definition", "Producer") || len(producer.Elts) != 2 {
		return false
	}
	want := map[string]string{"Name": "sqlmigrate-product", "Version": "1"}
	for _, element := range producer.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			return false
		}
		key, ok := pair.Key.(*ast.Ident)
		literal, literalOK := pair.Value.(*ast.BasicLit)
		if !ok || !literalOK || literal.Kind != token.STRING {
			return false
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil || want[key.Name] != value {
			return false
		}
		delete(want, key.Name)
	}
	return len(want) == 0
}

func sqlProductASTSourceAssignment(assignments []*ast.AssignStmt, source *ast.CompositeLit) bool {
	matches := 0
	for _, assignment := range assignments {
		if assignment.Tok != token.ASSIGN || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 ||
			assignment.Rhs[0] != source {
			continue
		}
		index, ok := assignment.Lhs[0].(*ast.IndexExpr)
		if ok && sqlProductASTIdentifier(index.X, "sources") && sqlProductASTIdentifier(index.Index, "index") {
			matches++
		}
	}
	return matches == 1
}

func sqlProductASTSourceReturns(returns []*ast.ReturnStmt) bool {
	if len(returns) != 4 {
		return false
	}
	directSources := 0
	appendedSources := 0
	encodedErrors := 0
	unknownCatalogErrors := 0
	for _, result := range returns {
		if len(result.Results) != 2 || !sqlProductASTIdentifier(result.Results[1], "nil") {
			if len(result.Results) == 2 && sqlProductASTIdentifier(result.Results[0], "nil") &&
				sqlProductASTIdentifier(result.Results[1], "err") {
				encodedErrors++
				continue
			}
			if len(result.Results) == 2 && sqlProductASTIdentifier(result.Results[0], "nil") &&
				sqlProductASTCall(result.Results[1], "errors", "New", 1) {
				unknownCatalogErrors++
				continue
			}
			return false
		}
		if sqlProductASTIdentifier(result.Results[0], "sources") {
			directSources++
			continue
		}
		appendCall, ok := result.Results[0].(*ast.CallExpr)
		if !ok || !sqlProductASTIdentifier(appendCall.Fun, "append") || len(appendCall.Args) != 2 ||
			!sqlProductASTIdentifier(appendCall.Args[0], "sources") {
			return false
		}
		invalidSource, ok := appendCall.Args[1].(*ast.CompositeLit)
		if !ok || !sqlProductASTSelectorExpression(invalidSource.Type, "definition", "Source") {
			return false
		}
		appendedSources++
	}
	return directSources == 1 && appendedSources == 1 && encodedErrors == 1 && unknownCatalogErrors == 1
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
	stdout := &sqlProductBoundedBuffer{maximum: sqlProductMaximumOutput}
	stderr := &sqlProductBoundedBuffer{maximum: sqlProductMaximumOutput}
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
		groups, discoveryErr := sqlProductOwnedProcessGroups(command.Process.Pid)
		killErr := sqlProductKillProcessGroups(groups, command.Process.Pid)
		waitErr = sqlProductBoundedWait(waited, 5*time.Second)
		absenceErr := sqlProductWaitProcessGroupsAbsent(groups, 2*time.Second)
		return sqlProductResult{}, fmt.Errorf("%s %s timed out: %w", name, strings.Join(arguments, " "), errors.Join(discoveryErr, killErr, waitErr, absenceErr))
	}
	if stdout.truncated || stderr.truncated {
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
	if err := sqlProductWaitProcessGroupsAbsent([]int{command.Process.Pid}, 2*time.Second); err != nil {
		return sqlProductResult{}, fmt.Errorf("wait for external root process group: %w", err)
	}
	return sqlProductResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}, nil
}

func sqlProductBoundedWait(waited <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err
	case <-timer.C:
		return errors.New("process Wait remained blocked after forced cleanup")
	}
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

func sqlProductOwnedProcessGroups(rootPID int) ([]int, error) {
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

func sqlProductKillProcessGroups(groups []int, root int) error {
	var result error
	for index := len(groups) - 1; index >= 0; index-- {
		if groups[index] == root {
			continue
		}
		result = errors.Join(result, sqlProductSignalProcessGroup(groups[index], syscall.SIGKILL))
	}
	return errors.Join(result, sqlProductSignalProcessGroup(root, syscall.SIGKILL))
}

func sqlProductSignalProcessGroup(group int, signal syscall.Signal) error {
	if group <= 1 || group == syscall.Getpgrp() {
		return errors.New("refuse to signal unsafe process group")
	}
	err := syscall.Kill(-group, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func sqlProductWaitProcessGroupsAbsent(groups []int, timeout time.Duration) error {
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

func sqlProductEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
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

func sqlProductSanitizeDatabaseEnvironment(environment []string) []string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || sqlProductIsDatabaseEnvironmentKey(key) {
			continue
		}
		values[key] = value
	}
	return sqlProductEnvironment(nil, values)
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
	return sqlProductEnvironment(nil, values)
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

type sqlProductBoundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *sqlProductBoundedBuffer) Write(document []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	original := len(document)
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if len(document) > remaining {
		buffer.truncated = true
		document = document[:remaining]
	}
	_, _ = buffer.buffer.Write(document)
	return original, nil
}

func (buffer *sqlProductBoundedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
