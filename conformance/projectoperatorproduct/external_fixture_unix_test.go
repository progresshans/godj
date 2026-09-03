//go:build darwin || linux

package projectoperatorproduct_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
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
	operatorCommandTimeout       = 4 * time.Minute
	operatorCleanupTimeout       = 20 * time.Second
	operatorMaximumOutput        = 1 << 20
	operatorMaximumMarkerBytes   = 4 << 10
	operatorMaximumArtifactFiles = 40_000
	operatorMaximumArtifactBytes = 768 << 20
)

type operatorExternalProject struct {
	repository       string
	universe         string
	root             string
	nested           string
	descriptor       string
	globalBinary     string
	scratch          string
	state            string
	baseEnvironment  []string
	sourceSnapshot   map[string][sha256.Size]byte
	artifactBaseline map[string]operatorArtifactVersion
}

type operatorArtifactVersion struct {
	size         int64
	modTimeNanos int64
}

type operatorCommandResult struct {
	PID             int
	ExitCode        int
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
}

func newOperatorExternalProject(t *testing.T) *operatorExternalProject {
	t.Helper()
	repository := operatorRepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-operator-product-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external operator universe: %v", err)
		}
	})
	operatorAssertExternalRoot(t, repository, universe)

	root := filepath.Join(universe, "consumer")
	nested := filepath.Join(root, "nested")
	scratch := filepath.Join(universe, "scratch")
	state := filepath.Join(universe, "state")
	for _, directory := range []string{
		root,
		nested,
		scratch,
		state,
		filepath.Join(universe, "home"),
		filepath.Join(universe, "cache"),
		filepath.Join(root, "application"),
		filepath.Join(root, "cmd", "projectrunner"),
		filepath.Join(root, "cmd", "site"),
		filepath.Join(root, "migrations"),
		filepath.Join(root, "modeldef"),
		filepath.Join(root, "operatorpolicy"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	localReplacements := operatorLocalDependencyReplacements(t, repository)
	operatorWriteFile(t, filepath.Join(root, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-operator-product

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
%s`, filepath.ToSlash(repository), localReplacements)), 0o600)
	operatorWriteFile(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\nrunserver_package = \"./cmd/site\"\n"), 0o600)
	operatorWriteFile(t, filepath.Join(root, "application", "application.go"), []byte(operatorApplicationSource), 0o600)
	operatorWriteFile(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(operatorProjectRunnerSource), 0o600)
	operatorWriteFile(t, filepath.Join(root, "cmd", "site", "main.go"), []byte(operatorSiteSource), 0o600)
	operatorWriteFile(t, filepath.Join(root, "modeldef", "schema.go"), []byte(operatorModelDefinitionSource), 0o600)
	operatorWriteFile(t, filepath.Join(root, "operatorpolicy", "policy.go"), []byte(operatorPolicySource), 0o600)
	migration, err := os.ReadFile(filepath.Join(repository, "examples", "article", "migrations", "0001_initial.godj.json"))
	if err != nil {
		t.Fatal("read Article migration fixture")
	}
	operatorWriteFile(t, filepath.Join(root, "migrations", "0001_initial.godj.json"), migration, 0o600)

	moduleCacheDocument, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal("locate ambient Go module cache")
	}
	moduleCache := strings.TrimSpace(string(moduleCacheDocument))
	if moduleCache == "" || !filepath.IsAbs(moduleCache) {
		t.Fatalf("ambient Go module cache path %q is not absolute", moduleCache)
	}

	setupEnvironment := operatorEnvironment(operatorSanitizeEnvironment(os.Environ()), map[string]string{
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
	})
	operatorRunSetup(t, root, setupEnvironment, "go", "mod", "tidy")

	baseEnvironment := operatorEnvironment(setupEnvironment, map[string]string{
		"GOPROXY": "off",
		"GOSUMDB": "off",
	})
	globalBinary := filepath.Join(universe, "godj")
	operatorRunSetup(t, repository, setupEnvironment, "go", "build", "-buildvcs=false", "-trimpath", "-mod=readonly", "-o", globalBinary, "./cmd/godj")

	generateEnvironment := operatorEnvironment(baseEnvironment, map[string]string{
		operatorSQLiteDatabaseEnvironment: filepath.Join(state, "generate-unused.sqlite3"),
		operatorMarkerEnvironment:         filepath.Join(state, "generate-unused.pid"),
		operatorRunnerMarkerEnvironment:   "",
		operatorHoldEnvironment:           "",
	})
	operatorAssertSecretFreeEnvironment(t, generateEnvironment, nil)
	operatorRunSetup(t, nested, generateEnvironment, globalBinary, "generate", "--project", filepath.Join(root, "godj.toml"))

	operatorAuditExternalSources(t, root)
	project := &operatorExternalProject{
		repository:      repository,
		universe:        universe,
		root:            root,
		nested:          nested,
		descriptor:      filepath.Join(root, "godj.toml"),
		globalBinary:    globalBinary,
		scratch:         scratch,
		state:           state,
		baseEnvironment: baseEnvironment,
	}
	project.sourceSnapshot = project.captureSourceSnapshot(t)
	project.artifactBaseline = project.captureArtifactVersions(t)
	project.assertWorkspaceEmpty(t)
	return project
}

func (project *operatorExternalProject) sqliteEnvironment(t *testing.T, database, marker string) []string {
	t.Helper()
	environment := operatorEnvironment(operatorRemoveEnvironment(
		project.baseEnvironment,
		operatorPostgresURLEnvironment,
		operatorPostgresSchemaEnvironment,
	), map[string]string{
		operatorSQLiteDatabaseEnvironment: database,
		operatorMarkerEnvironment:         marker,
		operatorRunnerMarkerEnvironment:   "1",
		operatorHoldEnvironment:           "",
	})
	operatorAssertSecretFreeEnvironment(t, environment, nil)
	return environment
}

func (project *operatorExternalProject) postgresEnvironment(t *testing.T, databaseURL, schema, marker string) []string {
	t.Helper()
	environment := operatorEnvironment(operatorRemoveEnvironment(
		project.baseEnvironment,
		operatorSQLiteDatabaseEnvironment,
	), map[string]string{
		operatorPostgresURLEnvironment:    databaseURL,
		operatorPostgresSchemaEnvironment: schema,
		operatorMarkerEnvironment:         marker,
		operatorRunnerMarkerEnvironment:   "1",
		operatorHoldEnvironment:           "",
	})
	operatorAssertSecretFreeEnvironment(t, environment, nil)
	return environment
}

func (project *operatorExternalProject) marker(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(project.state, name+".pid")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return path
}

func (project *operatorExternalProject) runMigrate(t *testing.T, environment []string, marker string, sensitive []byte) int {
	t.Helper()
	operatorAssertSecretFreeInvocation(t, project.globalBinary, project.nested, environment, sensitive,
		"migrate", "--project", project.descriptor)
	result := operatorRunCommand(t, project.globalBinary, project.nested, environment,
		"migrate", "--project", project.descriptor)
	operatorAssertCommandSuccess(t, result, sensitive)
	leaf := operatorReadSingleMarkerPID(t, marker)
	if leaf == result.PID || leaf == os.Getpid() {
		t.Fatalf("migrate marker did not identify an external linked leaf: global=%d leaf=%d test=%d", result.PID, leaf, os.Getpid())
	}
	operatorRequireProcessAbsent(t, leaf)
	project.assertWorkspaceEmpty(t)
	project.assertSourceUnchanged(t)
	return leaf
}

func (project *operatorExternalProject) captureSourceSnapshot(t *testing.T) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(project.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("external project source contains a symbolic link")
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(project.root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = sha256.Sum256(contents)
		return nil
	})
	if err != nil {
		t.Fatalf("capture external project source: %v", err)
	}
	return result
}

func (project *operatorExternalProject) assertSourceUnchanged(t *testing.T) {
	t.Helper()
	after := project.captureSourceSnapshot(t)
	if len(after) != len(project.sourceSnapshot) {
		t.Fatalf("external project source file count changed: before=%d after=%d", len(project.sourceSnapshot), len(after))
	}
	for path, before := range project.sourceSnapshot {
		if got, ok := after[path]; !ok || got != before {
			t.Fatalf("external project source changed at %q", path)
		}
	}
}

func (project *operatorExternalProject) captureArtifactVersions(t *testing.T) map[string]operatorArtifactVersion {
	t.Helper()
	result := make(map[string]operatorArtifactVersion)
	err := filepath.WalkDir(project.universe, func(path string, entry fs.DirEntry, walkErr error) error {
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
		if info.Mode().IsRegular() {
			result[path] = operatorArtifactVersion{size: info.Size(), modTimeNanos: info.ModTime().UnixNano()}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("capture external operator artifact baseline: %v", err)
	}
	return result
}

func (project *operatorExternalProject) assertArtifactsExclude(t *testing.T, sensitive ...[]byte) {
	t.Helper()
	files := 0
	var scanned int64
	err := filepath.WalkDir(project.universe, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact %q is a symbolic link", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact %q is not a regular file", path)
		}
		files++
		if files > operatorMaximumArtifactFiles {
			return errors.New("external operator artifact scan exceeded its file limit")
		}
		if baseline, ok := project.artifactBaseline[path]; ok &&
			baseline.size == info.Size() && baseline.modTimeNanos == info.ModTime().UnixNano() {
			return nil
		}
		if info.Size() < 0 || info.Size() > operatorMaximumArtifactBytes-scanned {
			return errors.New("external operator artifact scan exceeded its byte limit")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		contents, readErr := io.ReadAll(io.LimitReader(file, info.Size()+1))
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if int64(len(contents)) != info.Size() {
			return errors.New("external operator artifact changed while scanning")
		}
		scanned += info.Size()
		for _, value := range sensitive {
			if len(value) != 0 && bytes.Contains(contents, value) {
				return fmt.Errorf("artifact %q contains a forbidden raw secret", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan external operator artifacts: %v", err)
	}
}

func (project *operatorExternalProject) assertWorkspaceEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(project.scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		t.Fatalf("external operator scratch retained workspace entries: %v", names)
	}
}

func operatorRunCommand(t *testing.T, binary, directory string, environment []string, arguments ...string) operatorCommandResult {
	t.Helper()
	stdout := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	stderr := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		t.Fatalf("start %s: %v", filepath.Base(binary), err)
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	waitErr, timedOut := operatorBoundedWait(waited, operatorCommandTimeout)
	if timedOut {
		cleanupErr := operatorTerminateProcessTree(command.Process.Pid, waited)
		t.Fatalf("command timed out: cleanup-error=%t", cleanupErr != nil)
	}
	result := operatorCommandResult{
		PID:             command.Process.Pid,
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if waitErr == nil {
		result.ExitCode = 0
	} else {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			t.Fatalf("wait for %s: %v", filepath.Base(binary), waitErr)
		}
		result.ExitCode = exitError.ExitCode()
	}
	operatorRequireProcessAbsent(t, command.Process.Pid)
	return result
}

func operatorRunSetup(t *testing.T, directory string, environment []string, binary string, arguments ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), operatorCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	output := &operatorBoundedOutput{maximum: operatorMaximumOutput}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		t.Fatalf("fixture setup %s failed: %v; output-bytes=%d truncated=%t", filepath.Base(binary), err, len(output.String()), output.Truncated())
	}
	if ctx.Err() != nil || output.Truncated() {
		t.Fatalf("fixture setup %s exceeded a resource bound", filepath.Base(binary))
	}
}

func operatorAssertCommandSuccess(t *testing.T, result operatorCommandResult, sensitive []byte) {
	t.Helper()
	if len(sensitive) != 0 && (strings.Contains(result.Stdout, string(sensitive)) || strings.Contains(result.Stderr, string(sensitive))) {
		t.Fatal("public command output exposed the operator password")
	}
	if result.ExitCode != 0 || result.Stderr != "" || result.StdoutTruncated || result.StderrTruncated || result.Stdout == "" {
		t.Fatalf("public command result = exit:%d stdout-bytes:%d stderr-bytes:%d truncated:%t/%t", result.ExitCode, len(result.Stdout), len(result.Stderr), result.StdoutTruncated, result.StderrTruncated)
	}
}

type operatorBoundedOutput struct {
	mu        sync.Mutex
	maximum   int
	contents  []byte
	truncated bool
}

func (output *operatorBoundedOutput) Write(document []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	remaining := output.maximum - len(output.contents)
	if remaining > 0 {
		retain := len(document)
		if retain > remaining {
			retain = remaining
		}
		output.contents = append(output.contents, document[:retain]...)
	}
	if len(document) > remaining {
		output.truncated = true
	}
	return len(document), nil
}

func (output *operatorBoundedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return string(append([]byte(nil), output.contents...))
}

func (output *operatorBoundedOutput) Bytes() []byte {
	output.mu.Lock()
	defer output.mu.Unlock()
	return append([]byte(nil), output.contents...)
}

func (output *operatorBoundedOutput) Truncated() bool {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.truncated
}

func operatorBoundedWait(waited <-chan error, timeout time.Duration) (error, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err, false
	case <-timer.C:
		return errors.New("process timed out"), true
	}
}

func operatorTerminateProcessTree(rootPID int, waited <-chan error) error {
	groups, discoveryErr := operatorOwnedProcessGroups(rootPID)
	for _, group := range groups {
		_ = syscall.Kill(-group, syscall.SIGINT)
	}
	if _, timedOut := operatorBoundedWait(waited, 5*time.Second); !timedOut {
		return discoveryErr
	}
	for _, group := range groups {
		_ = syscall.Kill(-group, syscall.SIGKILL)
	}
	waitErr, _ := operatorBoundedWait(waited, 5*time.Second)
	return errors.Join(discoveryErr, waitErr)
}

func operatorOwnedProcessGroups(rootPID int) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", "-Ao", "pid=,ppid=,pgid=")
	document, err := command.Output()
	if err != nil {
		return []int{rootPID}, err
	}
	type process struct{ pid, parent, group int }
	processes := make([]process, 0, 128)
	for _, line := range strings.Split(string(document), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		group, groupErr := strconv.Atoi(fields[2])
		if pidErr == nil && parentErr == nil && groupErr == nil {
			processes = append(processes, process{pid: pid, parent: parent, group: group})
		}
	}
	owned := map[int]struct{}{rootPID: {}}
	changed := true
	for changed {
		changed = false
		for _, process := range processes {
			if _, parentOwned := owned[process.parent]; !parentOwned {
				continue
			}
			if _, exists := owned[process.pid]; !exists {
				owned[process.pid] = struct{}{}
				changed = true
			}
		}
	}
	groups := map[int]struct{}{rootPID: {}}
	for _, process := range processes {
		if _, exists := owned[process.pid]; exists && process.group > 0 {
			groups[process.group] = struct{}{}
		}
	}
	result := make([]int, 0, len(groups))
	for group := range groups {
		result = append(result, group)
	}
	sort.Ints(result)
	return result, nil
}

func operatorRequireProcessAbsent(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d remains after bounded cleanup", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func operatorReadSingleMarkerPID(t *testing.T, path string) int {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read external operator process marker: %v", err)
	}
	if len(document) == 0 || len(document) > operatorMaximumMarkerBytes {
		t.Fatalf("external operator process marker bytes = %d", len(document))
	}
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("external operator process marker line count = %d, want 1", len(lines))
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil || pid <= 1 {
		t.Fatalf("external operator process marker is not a PID")
	}
	return pid
}

func operatorAssertSecretFreeInvocation(t *testing.T, binary, directory string, environment []string, sensitive []byte, arguments ...string) {
	t.Helper()
	operatorAssertSecretFreeEnvironment(t, environment, sensitive)
	joined := strings.Join(append([]string{binary, directory}, arguments...), "\x00")
	if len(sensitive) != 0 && strings.Contains(joined, string(sensitive)) {
		t.Fatal("operator password entered argv or cwd")
	}
	if strings.Contains(directory, operatorRetiredUsernameEnvironment) || strings.Contains(directory, operatorRetiredPasswordEnvironment) {
		t.Fatal("operator invocation cwd contains a retired credential key")
	}
}

func operatorAssertSecretFreeEnvironment(t *testing.T, environment []string, sensitive []byte) {
	t.Helper()
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			t.Fatal("operator environment contains a malformed entry")
		}
		if key == operatorRetiredUsernameEnvironment || key == operatorRetiredPasswordEnvironment {
			t.Fatalf("operator environment retained retired credential key %s", key)
		}
		if len(sensitive) != 0 && strings.Contains(value, string(sensitive)) {
			t.Fatalf("operator password entered environment key %s", key)
		}
	}
}

func operatorEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
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

func operatorRemoveEnvironment(environment []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, remove := blocked[key]; !remove {
			result = append(result, entry)
		}
	}
	return result
}

func operatorSanitizeEnvironment(environment []string) []string {
	return operatorRemoveEnvironment(
		environment,
		operatorSQLiteDatabaseEnvironment,
		operatorPostgresURLEnvironment,
		operatorPostgresSchemaEnvironment,
		operatorMarkerEnvironment,
		operatorRunnerMarkerEnvironment,
		operatorHoldEnvironment,
		operatorResponseModeEnvironment,
		operatorRetiredUsernameEnvironment,
		operatorRetiredPasswordEnvironment,
		"GODJ_REQUIRE_POSTGRES",
		"GODJ_TEST_POSTGRES_URL",
		"GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE",
		operatorPostgresAttestationCaptureEnvironment,
	)
}

func TestOperatorSanitizeEnvironmentDropsHostOnlyControls(t *testing.T) {
	input := []string{
		"KEEP=bounded",
		operatorPostgresTestURLEnvironment + "=postgresql://host-only",
		operatorPostgresRequiredEnvironment + "=1",
		"GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE=/host-only/system-state-capture.json",
		operatorPostgresAttestationCaptureEnvironment + "=/host-only/capture.json",
		operatorResponseModeEnvironment + "=abort",
		operatorRetiredUsernameEnvironment + "=retired",
		operatorRetiredPasswordEnvironment + "=retired",
	}
	got := operatorSanitizeEnvironment(input)
	if len(got) != 1 || got[0] != "KEEP=bounded" {
		t.Fatalf("sanitized external project environment = %#v, want only KEEP", got)
	}
}

func operatorAuditExternalSources(t *testing.T, root string) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		files++
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse external Go source %q: %w", path, err)
		}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(value, "github.com/progresshans/godj/") && strings.Contains(value, "/internal/") {
				return fmt.Errorf("external Go source imports non-public GoDj package %q", value)
			}
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range []string{operatorRetiredUsernameEnvironment, operatorRetiredPasswordEnvironment} {
			if bytes.Contains(contents, []byte(forbidden)) {
				return fmt.Errorf("external Go source contains retired credential key %q", forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("audit external operator project source: %v", err)
	}
	if files < 8 {
		t.Fatalf("external operator project source audit covered %d files, want generated and handwritten packages", files)
	}
}

func operatorLocalDependencyReplacements(t *testing.T, repository string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{if not .Main}}{{.Path}}|{{.Dir}}{{end}}", "all")
	command.Dir = repository
	document, err := command.Output()
	if err != nil || ctx.Err() != nil {
		t.Fatal("resolve local external-project dependency replacements")
	}
	lines := make([]string, 0, 32)
	for _, line := range strings.Split(string(document), "\n") {
		module, directory, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || module == "" || directory == "" || module == "github.com/progresshans/godj" {
			continue
		}
		if !filepath.IsAbs(directory) {
			t.Fatalf("module replacement directory for %s is not absolute", module)
		}
		if info, err := os.Stat(filepath.Join(directory, "go.mod")); err != nil || !info.Mode().IsRegular() {
			continue
		}
		lines = append(lines, "replace "+module+" => "+filepath.ToSlash(directory))
	}
	if len(lines) < 10 {
		t.Fatalf("resolved only %d local dependency replacements", len(lines))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

func operatorRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve operator product test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func operatorAssertExternalRoot(t *testing.T, repository, external string) {
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
		t.Fatal("operator product fixture is not repository-external")
	}
}

func operatorWriteFile(t *testing.T, path string, document []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, document, mode); err != nil {
		t.Fatal(err)
	}
}
