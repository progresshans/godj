//go:build darwin || linux

package projectsqlmigrateproduct_test

import (
	"bytes"
	"crypto/sha256"
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
	sqlProductCommandTimeout = 4 * time.Minute
	sqlProductMaximumOutput  = 1 << 20
)

var sqlProductAllowedGoDjImports = map[string]struct{}{
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

	setupEnv := sqlProductRemoveEnvironment(sqlProductEnvironment(os.Environ(), map[string]string{
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
	)
	// Dependency resolution is fixture setup. Every product invocation after
	// this point executes with network access disabled.
	sqlProductRunSuccess(t, root, setupEnv, "go", "mod", "tidy")

	secret := "sqlmigrate-secret-canary-81ae0d75"
	baseEnv := sqlProductEnvironment(setupEnv, map[string]string{
		"GOPROXY":                     "off",
		"GOSUMDB":                     "off",
		sqlProductSecretEnvironment:   secret,
		sqlProductCatalogEnvironment:  sqlProductCatalogFull,
		sqlProductRendererEnvironment: sqlProductRendererSQLite,
	})
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

func (project *sqlProductProject) assertCommandBoundary(t *testing.T, state sqlProductState, result sqlProductResult) {
	t.Helper()
	sensitive := project.sensitive(state)
	sqlProductAssertRedacted(t, result, sensitive...)
	project.assertPoisonAbsent(t, state)
	project.assertWorkspaceEmpty(t)
	project.assertApplicationUnchanged(t)
	project.assertApplicationArtifactsRedacted(t, sensitive...)
	sqlProductAssertStateArtifactsRedacted(t, state.directory, sensitive...)
}

func (project *sqlProductProject) sensitive(state sqlProductState) []string {
	return []string{
		project.secret,
		sqlProductPartialCanary,
		state.database,
		filepath.ToSlash(state.database),
		filepath.Base(state.database),
		state.openerMarker,
		filepath.ToSlash(state.openerMarker),
		filepath.Base(state.openerMarker),
	}
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
			if _, ok := sqlProductAllowedGoDjImports[importPath]; !ok {
				return fmt.Errorf("application source %s imports non-allowlisted GoDj package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
