//go:build darwin || linux

package projectshowmigrationsproduct_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	externalStatusDatabaseEnvironment = "GODJ_SHOWMIGRATIONS_SQLITE_DATABASE"
	externalStatusMarkerEnvironment   = "GODJ_SHOWMIGRATIONS_BACKEND_MARKER"
	externalStatusCatalogEnvironment  = "GODJ_SHOWMIGRATIONS_CATALOG"
	externalStatusSecretEnvironment   = "GODJ_SHOWMIGRATIONS_SECRET_CANARY"
	externalStatusCommandTimeout      = 4 * time.Minute
	externalStatusMaximumOutput       = 64 << 10
)

var externalStatusAllowedImports = map[string]struct{}{
	"github.com/progresshans/godj/db/sqlite":             {},
	"github.com/progresshans/godj/migrations":            {},
	"github.com/progresshans/godj/migrations/backend":    {},
	"github.com/progresshans/godj/migrations/definition": {},
	"github.com/progresshans/godj/project":               {},
	"github.com/progresshans/godj/schema/ir":             {},
}

type externalStatusProject struct {
	repository   string
	universe     string
	root         string
	nested       string
	descriptor   string
	globalBinary string
	scratch      string
	baseEnv      []string
	secret       string
}

type externalStatusResult struct {
	exitCode int
	stdout   string
	stderr   string
}

type externalStatusMarker struct {
	event string
	pid   int
}

func newExternalStatusProject(t *testing.T) *externalStatusProject {
	t.Helper()
	repository := externalStatusRepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-showmigrations-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external showmigrations universe: %v", err)
		}
	})
	externalStatusAssertSeparateRoot(t, repository, universe)

	root := filepath.Join(universe, "consumer")
	nested := filepath.Join(root, "nested")
	scratch := filepath.Join(universe, "scratch")
	for _, directory := range []string{
		root,
		filepath.Join(root, "cmd", "projectrunner"),
		nested,
		filepath.Join(universe, "home"),
		scratch,
		filepath.Join(universe, "cache"),
		filepath.Join(universe, "state"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	externalStatusWriteFile(t, filepath.Join(root, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-showmigrations-external

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(repository))), 0o600)
	externalStatusWriteFile(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"), 0o600)
	externalStatusWriteFile(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(externalStatusProjectRunnerSource), 0o600)
	externalStatusAuditApplicationSources(t, repository, root)

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

	setupEnv := externalStatusEnvironment(os.Environ(), map[string]string{
		"HOME":            filepath.Join(universe, "home"),
		"XDG_CONFIG_HOME": filepath.Join(universe, "home"),
		"XDG_CACHE_HOME":  filepath.Join(universe, "cache"),
		"TMPDIR":          scratch,
		"GOCACHE":         filepath.Join(universe, "cache", "go-build"),
		"GOMODCACHE":      moduleCache,
		"GOWORK":          "off",
		"GOTOOLCHAIN":     "local",
		"GOFLAGS":         "",
	})
	// Module resolution is fixture setup. Every product command after this
	// point executes with network access disabled.
	externalStatusRunSuccess(t, root, setupEnv, "go", "mod", "tidy")

	secret := "showmigrations-secret-canary-2f11630d7a"
	baseEnv := externalStatusEnvironment(setupEnv, map[string]string{
		"GOPROXY":                       "off",
		"GOSUMDB":                       "off",
		externalStatusSecretEnvironment: secret,
	})
	globalBinary := filepath.Join(universe, "godj")
	externalStatusRunSuccess(t, repository, baseEnv, "go", "build", "-buildvcs=false", "-trimpath", "-mod=readonly", "-o", globalBinary, "./cmd/godj")

	return &externalStatusProject{
		repository:   repository,
		universe:     universe,
		root:         root,
		nested:       nested,
		descriptor:   filepath.Join(root, "godj.toml"),
		globalBinary: globalBinary,
		scratch:      scratch,
		baseEnv:      baseEnv,
		secret:       secret,
	}
}

func (project *externalStatusProject) paths(t *testing.T, name string) (string, string) {
	t.Helper()
	base := filepath.Join(project.universe, "state", name)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "sqlite-secret-path-8c813d.sqlite3"), filepath.Join(base, "backend-events.log")
}

func (project *externalStatusProject) environment(database, marker, catalog string) []string {
	return externalStatusEnvironment(project.baseEnv, map[string]string{
		externalStatusDatabaseEnvironment: database,
		externalStatusMarkerEnvironment:   marker,
		externalStatusCatalogEnvironment:  catalog,
	})
}

func (project *externalStatusProject) run(t *testing.T, environment []string, arguments ...string) externalStatusResult {
	t.Helper()
	result := externalStatusRun(t, project.nested, environment, project.globalBinary, arguments...)
	project.assertWorkspaceEmpty(t)
	return result
}

func (project *externalStatusProject) runShow(t *testing.T, environment []string) externalStatusResult {
	t.Helper()
	return project.run(t, environment, "showmigrations", "--project", project.descriptor)
}

func (project *externalStatusProject) runMigrate(t *testing.T, environment []string) externalStatusResult {
	t.Helper()
	return project.run(t, environment, "migrate", "--project", project.descriptor)
}

func (project *externalStatusProject) assertWorkspaceEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(project.scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("external showmigrations left private workspace artifacts: %v", externalStatusEntryNames(entries))
	}
}

func (project *externalStatusProject) sensitive(database string) []string {
	return []string{
		database,
		filepath.ToSlash(database),
		"sqlite-secret-path-8c813d",
		project.secret,
	}
}

func externalStatusRepositoryRoot(t *testing.T) string {
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

func externalStatusAssertSeparateRoot(t *testing.T, repository, external string) {
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

func externalStatusAuditApplicationSources(t *testing.T, repository, root string) {
	t.Helper()
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	fileSet := token.NewFileSet()
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
			if _, ok := externalStatusAllowedImports[importPath]; !ok {
				return fmt.Errorf("application source %s imports non-allowlisted GoDj package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func externalStatusWriteFile(t *testing.T, path string, document []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, document, mode); err != nil {
		t.Fatal(err)
	}
}

func externalStatusRunSuccess(t *testing.T, directory string, environment []string, name string, arguments ...string) {
	t.Helper()
	result := externalStatusRun(t, directory, environment, name, arguments...)
	if result.exitCode != 0 {
		t.Fatalf("%s %s failed: exit=%d stdout=%q stderr=%q", name, strings.Join(arguments, " "), result.exitCode, result.stdout, result.stderr)
	}
}

func externalStatusRun(t *testing.T, directory string, environment []string, name string, arguments ...string) externalStatusResult {
	t.Helper()
	stdout := &externalStatusBoundedBuffer{maximum: externalStatusMaximumOutput}
	stderr := &externalStatusBoundedBuffer{maximum: externalStatusMaximumOutput}
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start %s %s: %v", name, strings.Join(arguments, " "), err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(externalStatusCommandTimeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-timer.C:
		groups, discoveryErr := externalStatusOwnedProcessGroups(command.Process.Pid)
		killErr := externalStatusKillProcessGroups(groups, command.Process.Pid)
		waitErr = externalStatusBoundedWait(waited, 5*time.Second)
		absenceErr := externalStatusWaitProcessGroupsAbsent(groups, 2*time.Second)
		t.Fatalf("%s %s timed out: %v", name, strings.Join(arguments, " "), errors.Join(discoveryErr, killErr, waitErr, absenceErr))
	}
	if stdout.truncated || stderr.truncated {
		t.Fatalf("%s %s exceeded output limit", name, strings.Join(arguments, " "))
	}
	exitCode := 0
	if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatalf("run %s %s: %v", name, strings.Join(arguments, " "), waitErr)
		}
		exitCode = exitError.ExitCode()
	}
	if err := externalStatusWaitProcessGroupsAbsent([]int{command.Process.Pid}, 2*time.Second); err != nil {
		t.Fatalf("wait for external root process group: %v", err)
	}
	return externalStatusResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}
}

func externalStatusBoundedWait(waited <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err
	case <-timer.C:
		return errors.New("process Wait remained blocked after forced cleanup")
	}
}

func externalStatusOwnedProcessGroups(rootPID int) ([]int, error) {
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

func externalStatusKillProcessGroups(groups []int, root int) error {
	var result error
	for index := len(groups) - 1; index >= 0; index-- {
		if groups[index] == root {
			continue
		}
		result = errors.Join(result, externalStatusSignalProcessGroup(groups[index], syscall.SIGKILL))
	}
	return errors.Join(result, externalStatusSignalProcessGroup(root, syscall.SIGKILL))
}

func externalStatusSignalProcessGroup(group int, signal syscall.Signal) error {
	if group <= 1 || group == syscall.Getpgrp() {
		return errors.New("refuse to signal unsafe process group")
	}
	err := syscall.Kill(-group, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func externalStatusWaitProcessGroupsAbsent(groups []int, timeout time.Duration) error {
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

func externalStatusAssertSuccess(t *testing.T, result externalStatusResult, want string, sensitive ...string) {
	t.Helper()
	externalStatusAssertRedacted(t, result, sensitive...)
	if result.exitCode != 0 || result.stdout != want || result.stderr != "" {
		t.Fatalf("showmigrations success = exit:%d stdout:%q stderr:%q, want 0/%q/empty", result.exitCode, result.stdout, result.stderr, want)
	}
}

func externalStatusAssertFailure(t *testing.T, result externalStatusResult, exit int, stderr string, sensitive ...string) {
	t.Helper()
	externalStatusAssertRedacted(t, result, sensitive...)
	if result.exitCode != exit || result.stdout != "" || result.stderr != stderr {
		t.Fatalf("showmigrations failure = exit:%d stdout:%q stderr:%q, want %d/empty/%q", result.exitCode, result.stdout, result.stderr, exit, stderr)
	}
}

func externalStatusAssertRedacted(t *testing.T, result externalStatusResult, sensitive ...string) {
	t.Helper()
	combined := result.stdout + result.stderr
	for _, value := range sensitive {
		if value != "" && strings.Contains(combined, value) {
			t.Fatal("external showmigrations output exposed a sensitive value")
		}
	}
}

func externalStatusReadMarkers(t *testing.T, path string) []externalStatusMarker {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	markers := make([]externalStatusMarker, len(lines))
	for index, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[0], "event=") || !strings.HasPrefix(fields[1], "pid=") {
			t.Fatalf("invalid backend marker line %q", line)
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(fields[1], "pid="))
		if err != nil || pid <= 0 {
			t.Fatalf("invalid backend marker pid in %q", line)
		}
		markers[index] = externalStatusMarker{event: strings.TrimPrefix(fields[0], "event="), pid: pid}
	}
	return markers
}

func externalStatusAssertReadLifecycle(t *testing.T, path string) int {
	t.Helper()
	markers := externalStatusReadMarkers(t, path)
	want := []string{
		"backend_open_call",
		"backend_acquired",
		"session_open_call",
		"session_acquired",
		"history_read",
		"session_close",
		"backend_close",
	}
	if len(markers) != len(want) {
		t.Fatalf("backend marker count = %d, want %d: %+v", len(markers), len(want), markers)
	}
	pid := markers[0].pid
	for index := range want {
		if markers[index].event != want[index] || markers[index].pid != pid {
			t.Fatalf("backend marker[%d] = %+v, want event=%q pid=%d", index, markers[index], want[index], pid)
		}
	}
	if err := externalStatusWaitProcessGroupsAbsent([]int{pid}, 2*time.Second); err != nil {
		t.Fatalf("linked project-runner process group was not reaped: %v", err)
	}
	return pid
}

func externalStatusResetMarker(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func externalStatusAssertMarkerAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backend marker unexpectedly exists: %v", err)
	}
}

func externalStatusAssertArtifactsRedacted(t *testing.T, root string, sensitive ...string) {
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
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range sensitive {
			if value != "" && bytes.Contains(document, []byte(value)) {
				return errors.New("external artifact contains a sensitive value")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func externalStatusEnvironment(base []string, overrides map[string]string) []string {
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
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func externalStatusEntryNames(entries []os.DirEntry) []string {
	result := make([]string, len(entries))
	for index := range entries {
		result[index] = entries[index].Name()
	}
	sort.Strings(result)
	return result
}

type externalStatusBoundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *externalStatusBoundedBuffer) Write(document []byte) (int, error) {
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

func (buffer *externalStatusBoundedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

const externalStatusProjectRunnerSource = `package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
)

const (
	databaseEnvironment = "GODJ_SHOWMIGRATIONS_SQLITE_DATABASE"
	markerEnvironment = "GODJ_SHOWMIGRATIONS_BACKEND_MARKER"
	catalogEnvironment = "GODJ_SHOWMIGRATIONS_CATALOG"
)

func main() {
	sources, err := sourcesForCatalog(os.Getenv(catalogEnvironment))
	if err != nil {
		fatal()
	}
	err = project.Run(context.Background(), project.Config{
		MigrationDefinitionSources: sources,
		OpenMigrationBackend: openObservedBackend,
	}, os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
		fatal()
	}
}

func openObservedBackend(ctx context.Context) (project.MigrationBackend, error) {
	if err := appendMarker("backend_open_call"); err != nil {
		return nil, err
	}
	opened, err := sqlite.Open(ctx, os.Getenv(databaseEnvironment))
	if err != nil {
		return nil, err
	}
	if err := appendMarker("backend_acquired"); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return &observedBackend{delegate: opened}, nil
}

type observedBackend struct {
	delegate *sqlite.Backend
}

func (observed *observedBackend) MigrationCapabilities() backend.MigrationCapabilities {
	return observed.delegate.MigrationCapabilities()
}

func (observed *observedBackend) OpenRevisionFencedSession(ctx context.Context) (backend.RevisionFencedSession, error) {
	if err := appendMarker("session_open_call"); err != nil {
		return nil, err
	}
	session, err := observed.delegate.OpenRevisionFencedSession(ctx)
	if err != nil {
		return nil, err
	}
	if err := appendMarker("session_acquired"); err != nil {
		return nil, errors.Join(err, session.Close(ctx))
	}
	return &observedSession{delegate: session}, nil
}

func (observed *observedBackend) Close() error {
	return errors.Join(observed.delegate.Close(), appendMarker("backend_close"))
}

type observedSession struct {
	delegate backend.RevisionFencedSession
}

func (observed *observedSession) ReadAppliedMigrations(ctx context.Context) ([]backend.AppliedMigration, error) {
	if err := appendMarker("history_read"); err != nil {
		return nil, err
	}
	return observed.delegate.ReadAppliedMigrations(ctx)
}

func (observed *observedSession) BeginMigration(ctx context.Context, transition backend.HistoryTransition, intent backend.MigrationIntent) (backend.RevisionFencedTransaction, error) {
	if err := appendMarker("migration_begin"); err != nil {
		return nil, err
	}
	return observed.delegate.BeginMigration(ctx, transition, intent)
}

func (observed *observedSession) Close(ctx context.Context) error {
	return errors.Join(observed.delegate.Close(ctx), appendMarker("session_close"))
}

func appendMarker(event string) error {
	marker := os.Getenv(markerEnvironment)
	if marker == "" {
		return errors.New("backend marker path is empty")
	}
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "event=%s pid=%d\n", event, os.Getpid())
	return errors.Join(writeErr, file.Close())
}

func sourcesForCatalog(catalog string) ([]definition.Source, error) {
	if catalog == "invalid" {
		return []definition.Source{{SourceID: "invalid.godj.json", Document: []byte("{")}}, nil
	}
	var definitions []migrations.Migration
	switch catalog {
	case "empty":
		return nil, nil
	case "prefix":
		definitions = fullCatalog()[:2]
	case "full":
		definitions = fullCatalog()
	case "branch":
		definitions = branchCatalog()
	case "unknown_seed":
		definitions = append(fullCatalog()[:2], unknownCatalog()...)
	default:
		return nil, errors.New("unknown external catalog")
	}
	sources := make([]definition.Source, len(definitions))
	for index, migration := range definitions {
		document, err := definition.Encode(definition.Producer{Name: "showmigrations-product", Version: "1"}, migration)
		if err != nil {
			return nil, err
		}
		sources[index] = definition.Source{
			SourceID: fmt.Sprintf("generated/%02d_%s_%s.godj.json", index, migration.App, migration.Name),
			Document: document,
		}
	}
	return sources, nil
}

func fullCatalog() []migrations.Migration {
	author := normalizedModel("authors", ir.Model{
		Name: "author", GoName: "Author", DBTable: "authors_author",
		Fields: []ir.Field{{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100}},
	})
	article := normalizedModel("blog", ir.Model{
		Name: "article", GoName: "Article", DBTable: "blog_article",
		Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
	})
	return []migrations.Migration{
		{
			App: "authors", Name: "0001_author",
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "authors", Model: author}},
		},
		{
			App: "blog", Name: "0001_article",
			Dependencies: []migrations.MigrationKey{{App: "authors", Name: "0001_author"}},
			Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "blog", Model: article}},
		},
		{
			App: "blog", Name: "0002_publish",
			Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_article"}},
			Operations: []migrations.Operation{migrations.AddField{
				AppLabel: "blog", ModelName: "article",
				Field: ir.Field{
					Name: "published", GoName: "Published", Column: "published", Kind: ir.FieldBoolean,
					Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean},
				},
			}},
		},
	}
}

func branchCatalog() []migrations.Migration {
	return []migrations.Migration{
		{App: "zeta", Name: "0001_root"},
		{App: "alpha", Name: "0099_parent", Dependencies: []migrations.MigrationKey{{App: "zeta", Name: "0001_root"}}},
		{App: "alpha", Name: "0001_child", Dependencies: []migrations.MigrationKey{{App: "alpha", Name: "0099_parent"}}},
	}
}

func unknownCatalog() []migrations.Migration {
	return []migrations.Migration{
		{App: "blog", Name: "0000_removed", Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_article"}}},
		{App: "blog", Name: "9999_removed", Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0000_removed"}}},
		{App: "legacy", Name: "0001_gone", Dependencies: []migrations.MigrationKey{{App: "blog", Name: "9999_removed"}}},
	}
}

func normalizedModel(app string, model ir.Model) ir.Model {
	schema, err := ir.Normalize(ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel: app,
		Models: []ir.Model{model},
	})
	if err != nil {
		panic("invalid static external model")
	}
	return schema.Models[0]
}

func fatal() {
	_, _ = fmt.Fprintln(os.Stderr, "external project runner failed")
	os.Exit(1)
}
`
