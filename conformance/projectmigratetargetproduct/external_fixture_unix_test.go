//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/url"
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
	targetCommandTimeout = 4 * time.Minute
	targetMaximumOutput  = 64 << 10
)

var targetAllowedGoDjImports = map[string]struct{}{
	"github.com/progresshans/godj/db/postgres":           {},
	"github.com/progresshans/godj/db/sqlite":             {},
	"github.com/progresshans/godj/migrations":            {},
	"github.com/progresshans/godj/migrations/backend":    {},
	"github.com/progresshans/godj/migrations/definition": {},
	"github.com/progresshans/godj/project":               {},
	"github.com/progresshans/godj/schema/ir":             {},
}

type targetExternalProject struct {
	repository      string
	universe        string
	root            string
	nested          string
	unselected      string
	descriptor      string
	globalBinary    string
	scratch         string
	baseEnv         []string
	secret          string
	applicationHash map[string][sha256.Size]byte
	families        map[string]int
}

type targetCommandResult struct {
	exitCode int
	stdout   string
	stderr   string
}

type targetExecuteResult struct {
	SourceCount         int    `json:"source_count"`
	DefinitionCount     int    `json:"definition_count"`
	DefinitionSetDigest string `json:"definition_set_digest"`
}

type targetPlanRow struct {
	App       string `json:"app"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

type targetMarker struct {
	pid   int
	event string
}

func newTargetExternalProject(t *testing.T) *targetExternalProject {
	t.Helper()
	repository := targetRepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-target-migrate-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external target migration universe: %v", err)
		}
	})
	targetAssertSeparateRoot(t, repository, universe)

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

	targetWriteFile(t, filepath.Join(root, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-target-migrate-external

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(repository))), 0o600)
	targetWriteFile(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"), 0o600)
	targetWriteFile(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(targetProjectRunnerSource), 0o600)
	targetAuditApplicationSources(t, repository, root)

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

	setupEnv := targetRemoveEnvironment(targetEnvironment(os.Environ(), map[string]string{
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
		targetPostgresTestURLEnvironment,
		targetPostgresRequiredEnvironment,
		targetBackendEnvironment,
		targetDatabaseEnvironment,
		targetPostgresURLEnvironment,
		targetPostgresSchemaEnvironment,
		targetCatalogEnvironment,
		targetMarkerEnvironment,
	)
	// Dependency resolution is fixture setup. Every product invocation after
	// this point runs with both the proxy and checksum database disabled.
	targetRunSuccess(t, root, setupEnv, "go", "mod", "tidy")

	secret := "target-migrate-secret-canary-5d7e248ca1"
	baseEnv := targetEnvironment(setupEnv, map[string]string{
		"GOPROXY":                         "off",
		"GOSUMDB":                         "off",
		targetSecretEnvironment:           secret,
		targetFailDeleteTableEnvironment:  "",
		targetFailBackendOpenEnvironment:  "",
		targetFailBackendCloseEnvironment: "",
	})
	globalBinary := filepath.Join(universe, "godj")
	targetRunSuccess(t, repository, baseEnv, "go", "build", "-buildvcs=false", "-trimpath", "-mod=readonly", "-o", globalBinary, "./cmd/godj")

	project := &targetExternalProject{
		repository:   repository,
		universe:     universe,
		root:         root,
		nested:       nested,
		unselected:   unselected,
		descriptor:   filepath.Join(root, "godj.toml"),
		globalBinary: globalBinary,
		scratch:      scratch,
		baseEnv:      baseEnv,
		secret:       secret,
		families:     make(map[string]int),
	}
	project.applicationHash = project.captureApplicationHashes(t)
	project.assertWorkspaceEmpty(t)
	return project
}

func (project *targetExternalProject) paths(t *testing.T, name string) (string, string) {
	t.Helper()
	base := filepath.Join(project.universe, "state", name)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "sqlite-secret-path-cc83.sqlite3"), filepath.Join(base, "runner-events.log")
}

func (project *targetExternalProject) environment(database, marker, catalog string) []string {
	return project.environmentWith(database, marker, catalog, nil)
}

func (project *targetExternalProject) environmentWith(database, marker, catalog string, overrides map[string]string) []string {
	values := map[string]string{
		targetBackendEnvironment:          targetBackendSQLite,
		targetDatabaseEnvironment:         database,
		targetPostgresURLEnvironment:      "",
		targetPostgresSchemaEnvironment:   "",
		targetMarkerEnvironment:           marker,
		targetCatalogEnvironment:          catalog,
		targetFailDeleteTableEnvironment:  "",
		targetFailBackendOpenEnvironment:  "",
		targetFailBackendCloseEnvironment: "",
	}
	for key, value := range overrides {
		values[key] = value
	}
	return targetEnvironment(project.baseEnv, values)
}

func (project *targetExternalProject) postgresEnvironment(
	t *testing.T,
	databaseURL,
	schema,
	marker,
	catalog string,
) []string {
	return project.postgresEnvironmentWith(t, databaseURL, schema, marker, catalog, nil)
}

func (project *targetExternalProject) postgresEnvironmentWith(
	t *testing.T,
	databaseURL,
	schema,
	marker,
	catalog string,
	overrides map[string]string,
) []string {
	t.Helper()
	base := targetRemoveEnvironment(
		project.baseEnv,
		targetPostgresTestURLEnvironment,
		targetPostgresRequiredEnvironment,
		targetDatabaseEnvironment,
	)
	values := map[string]string{
		targetBackendEnvironment:          targetBackendPostgres,
		targetPostgresURLEnvironment:      databaseURL,
		targetPostgresSchemaEnvironment:   schema,
		targetMarkerEnvironment:           marker,
		targetCatalogEnvironment:          catalog,
		targetFailDeleteTableEnvironment:  "",
		targetFailBackendOpenEnvironment:  "",
		targetFailBackendCloseEnvironment: "",
	}
	for key, value := range overrides {
		values[key] = value
	}
	environment := targetEnvironment(base, values)
	actual := targetEnvironmentMap(environment)
	if _, exists := actual[targetPostgresTestURLEnvironment]; exists {
		t.Fatal("PostgreSQL target project environment retained test-only database URL")
	}
	if _, exists := actual[targetPostgresRequiredEnvironment]; exists {
		t.Fatal("PostgreSQL target project environment retained test-only required sentinel")
	}
	if _, exists := actual[targetDatabaseEnvironment]; exists {
		t.Fatal("PostgreSQL target project environment retained SQLite database configuration")
	}
	if actual[targetBackendEnvironment] != targetBackendPostgres ||
		actual[targetPostgresURLEnvironment] != databaseURL ||
		actual[targetPostgresSchemaEnvironment] != schema {
		t.Fatal("PostgreSQL target project environment did not retain exact project-owned database configuration")
	}
	return environment
}

func (project *targetExternalProject) run(t *testing.T, environment []string, arguments ...string) targetCommandResult {
	t.Helper()
	return project.runAt(t, project.nested, environment, arguments...)
}

func (project *targetExternalProject) runAt(t *testing.T, directory string, environment []string, arguments ...string) targetCommandResult {
	t.Helper()
	project.recordPublicFamily(arguments)
	result := targetRun(t, directory, environment, project.globalBinary, arguments...)
	database := targetEnvironmentValue(environment, targetDatabaseEnvironment)
	databaseURL := targetEnvironmentValue(environment, targetPostgresURLEnvironment)
	schema := targetEnvironmentValue(environment, targetPostgresSchemaEnvironment)
	marker := targetEnvironmentValue(environment, targetMarkerEnvironment)
	sensitive := project.sensitive(database, databaseURL, schema, marker, filepath.ToSlash(marker), filepath.Base(marker))
	if password := targetURLPassword(databaseURL); len(password) >= 4 {
		sensitive = append(sensitive, password)
	}
	targetAssertRedacted(t, result, sensitive...)
	targetAssertMarkerProcessesReaped(t, marker)
	project.assertWorkspaceEmpty(t)
	project.assertApplicationUnchanged(t)
	project.assertArtifactsRedacted(t, sensitive...)
	targetAssertStateArtifactsRedacted(t, []string{database, marker}, sensitive...)
	return result
}

func (project *targetExternalProject) sensitive(database string, extras ...string) []string {
	values := []string{project.secret}
	if database != "" {
		values = append(values, database, filepath.ToSlash(database), filepath.Base(database))
	}
	return append(values, extras...)
}

func (project *targetExternalProject) assertAllPublicFamilies(t *testing.T) {
	t.Helper()
	want := []string{
		"execute_latest_implicit", "execute_latest_explicit", "plan_latest_implicit", "plan_latest_explicit",
		"execute_target_implicit", "execute_target_explicit", "plan_target_implicit", "plan_target_explicit",
	}
	for _, family := range want {
		if project.families[family] == 0 {
			t.Errorf("public migrate argv family %q was not exercised", family)
		}
	}
}

func (project *targetExternalProject) recordPublicFamily(arguments []string) {
	family := ""
	switch {
	case len(arguments) == 1 && arguments[0] == "migrate":
		family = "execute_latest_implicit"
	case len(arguments) == 3 && arguments[0] == "migrate" && arguments[1] == "--project" && arguments[2] == project.descriptor:
		family = "execute_latest_explicit"
	case len(arguments) == 2 && arguments[0] == "migrate" && arguments[1] == "--plan":
		family = "plan_latest_implicit"
	case len(arguments) == 4 && arguments[0] == "migrate" && arguments[1] == "--plan" && arguments[2] == "--project" && arguments[3] == project.descriptor:
		family = "plan_latest_explicit"
	case len(arguments) == 3 && arguments[0] == "migrate" && targetPublicToken(arguments[1]) && targetPublicToken(arguments[2]):
		family = "execute_target_implicit"
	case len(arguments) == 5 && arguments[0] == "migrate" && targetPublicToken(arguments[1]) && targetPublicToken(arguments[2]) && arguments[3] == "--project" && arguments[4] == project.descriptor:
		family = "execute_target_explicit"
	case len(arguments) == 4 && arguments[0] == "migrate" && targetPublicToken(arguments[1]) && targetPublicToken(arguments[2]) && arguments[3] == "--plan":
		family = "plan_target_implicit"
	case len(arguments) == 6 && arguments[0] == "migrate" && targetPublicToken(arguments[1]) && targetPublicToken(arguments[2]) && arguments[3] == "--plan" && arguments[4] == "--project" && arguments[5] == project.descriptor:
		family = "plan_target_explicit"
	}
	if family != "" {
		project.families[family]++
	}
}

func targetPublicToken(value string) bool {
	return value != "" && !strings.HasPrefix(value, "-")
}

func (project *targetExternalProject) captureApplicationHashes(t *testing.T) map[string][sha256.Size]byte {
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

func (project *targetExternalProject) assertApplicationUnchanged(t *testing.T) {
	t.Helper()
	after := project.captureApplicationHashes(t)
	if len(after) != len(project.applicationHash) {
		t.Fatalf("product command changed external application file roster: before=%d after=%d", len(project.applicationHash), len(after))
	}
	for path, before := range project.applicationHash {
		if current, exists := after[path]; !exists || current != before {
			t.Fatalf("product command changed external application file %s: exists=%t before=%x after=%x", filepath.Base(path), exists, before, current)
		}
	}
}

func (project *targetExternalProject) assertWorkspaceEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(project.scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("global command left private workspace residue: %v", targetEntryNames(entries))
	}
}

func (project *targetExternalProject) assertArtifactsRedacted(t *testing.T, sensitive ...string) {
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

func targetAssertStateArtifactsRedacted(t *testing.T, paths []string, sensitive ...string) {
	t.Helper()
	for _, path := range paths {
		if path == "" {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal("inspect external state artifact")
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			t.Fatal("external state artifact is not a bounded regular file")
		}
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("read external state artifact")
		}
		for _, value := range sensitive {
			if value != "" && bytes.Contains(document, []byte(value)) {
				t.Fatal("external state artifact contains a sensitive value")
			}
		}
	}
}

func targetPlanOutput(t *testing.T, rows ...targetPlanRow) string {
	t.Helper()
	plan := append([]targetPlanRow(nil), rows...)
	if len(plan) == 0 {
		plan = make([]targetPlanRow, 0)
	}
	document, err := json.Marshal(struct {
		Plan []targetPlanRow `json:"plan"`
	}{Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	return string(append(document, '\n'))
}

func targetDecodeExecuteResult(t *testing.T, result targetCommandResult) targetExecuteResult {
	t.Helper()
	if result.exitCode != 0 || result.stderr != "" || result.stdout == "" {
		t.Fatalf("cannot decode unsuccessful migrate result: exit=%d stdout=%q stderr=%q", result.exitCode, result.stdout, result.stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(result.stdout))
	decoder.DisallowUnknownFields()
	var decoded targetExecuteResult
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode migrate result: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("migrate result has trailing JSON: %v", err)
	}
	if decoded.SourceCount < 0 || decoded.DefinitionCount < 0 || !strings.HasPrefix(decoded.DefinitionSetDigest, "sha256:") {
		t.Fatalf("migrate result is not bounded/current: %+v", decoded)
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(decoded.DefinitionSetDigest, "sha256:"))
	if err != nil || len(digest) != sha256.Size {
		t.Fatalf("migrate result digest = %q: bytes=%d error=%v", decoded.DefinitionSetDigest, len(digest), err)
	}
	return decoded
}

func targetAssertSuccess(t *testing.T, result targetCommandResult, want string, sensitive ...string) {
	t.Helper()
	targetAssertRedacted(t, result, sensitive...)
	if result.exitCode != 0 || result.stdout != want || result.stderr != "" {
		t.Fatalf("command success = exit:%d stdout:%q stderr:%q, want 0/%q/empty", result.exitCode, result.stdout, result.stderr, want)
	}
}

func targetAssertFailure(t *testing.T, result targetCommandResult, exit int, stderr string, sensitive ...string) {
	t.Helper()
	targetAssertRedacted(t, result, sensitive...)
	if result.exitCode != exit || result.stdout != "" || result.stderr != stderr {
		t.Fatalf("command failure = exit:%d stdout:%q stderr:%q, want %d/empty/%q", result.exitCode, result.stdout, result.stderr, exit, stderr)
	}
}

func targetAssertRedacted(t *testing.T, result targetCommandResult, sensitive ...string) {
	t.Helper()
	combined := result.stdout + result.stderr
	for _, value := range sensitive {
		if value != "" && strings.Contains(combined, value) {
			t.Fatal("target migration command output exposed a sensitive value")
		}
	}
}

func targetReadMarkers(t *testing.T, path string) []targetMarker {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(document), "\n"), "\n")
	markers := make([]targetMarker, len(lines))
	for index, line := range lines {
		pidText, event, ok := strings.Cut(line, "\t")
		pid, parseErr := strconv.Atoi(pidText)
		if !ok || parseErr != nil || pid <= 0 || event == "" {
			t.Fatalf("invalid target migration marker line %q", line)
		}
		markers[index] = targetMarker{pid: pid, event: event}
	}
	return markers
}

func targetMarkerEventNames(markers []targetMarker) []string {
	result := make([]string, len(markers))
	for index := range markers {
		result[index] = markers[index].event
	}
	return result
}

func targetAssertMarkerEvents(t *testing.T, path string, want ...string) []targetMarker {
	t.Helper()
	markers := targetReadMarkers(t, path)
	if got := targetMarkerEventNames(markers); !equalTargetStrings(got, want) {
		t.Fatalf("target migration marker events = %q, want %q", got, want)
	}
	pids := make(map[int]struct{})
	for _, marker := range markers {
		pids[marker.pid] = struct{}{}
	}
	if len(pids) != 1 {
		t.Fatalf("one global migrate command used %d linked runner PIDs: %+v", len(pids), markers)
	}
	groups := make([]int, 0, len(pids))
	for pid := range pids {
		groups = append(groups, pid)
	}
	sort.Ints(groups)
	if err := targetWaitProcessGroupsAbsent(groups, 2*time.Second); err != nil {
		t.Fatalf("linked project runner process group was not reaped: %v", err)
	}
	return markers
}

func targetAssertMarkerProcessesReaped(t *testing.T, path string) {
	t.Helper()
	if path == "" {
		return
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatal("inspect target migration marker")
	}
	markers := targetReadMarkers(t, path)
	pids := make(map[int]struct{})
	for _, marker := range markers {
		pids[marker.pid] = struct{}{}
	}
	groups := make([]int, 0, len(pids))
	for pid := range pids {
		groups = append(groups, pid)
	}
	sort.Ints(groups)
	if err := targetWaitProcessGroupsAbsent(groups, 2*time.Second); err != nil {
		t.Fatal("linked project runner process group was not reaped")
	}
}

func targetResetMarker(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func targetAssertMarkerAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target migration marker unexpectedly exists: %v", err)
	}
}

func targetRepositoryRoot(t *testing.T) string {
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

func targetAssertSeparateRoot(t *testing.T, repository, external string) {
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

func targetAuditApplicationSources(t *testing.T, repository, root string) {
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
			if _, ok := targetAllowedGoDjImports[importPath]; !ok {
				return fmt.Errorf("application source %s imports non-allowlisted GoDj package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func targetWriteFile(t *testing.T, path string, document []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, document, mode); err != nil {
		t.Fatal(err)
	}
}

func targetRunSuccess(t *testing.T, directory string, environment []string, name string, arguments ...string) {
	t.Helper()
	result := targetRun(t, directory, environment, name, arguments...)
	if result.exitCode != 0 {
		t.Fatalf("%s %s failed: exit=%d stdout=%q stderr=%q", name, strings.Join(arguments, " "), result.exitCode, result.stdout, result.stderr)
	}
}

func targetRun(t *testing.T, directory string, environment []string, name string, arguments ...string) targetCommandResult {
	t.Helper()
	stdout := &targetBoundedBuffer{maximum: targetMaximumOutput}
	stderr := &targetBoundedBuffer{maximum: targetMaximumOutput}
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
	timer := time.NewTimer(targetCommandTimeout)
	defer timer.Stop()
	var waitErr error
	select {
	case waitErr = <-waited:
	case <-timer.C:
		groups, discoveryErr := targetOwnedProcessGroups(command.Process.Pid)
		killErr := targetKillProcessGroups(groups, command.Process.Pid)
		waitErr = targetBoundedWait(waited, 5*time.Second)
		absenceErr := targetWaitProcessGroupsAbsent(groups, 2*time.Second)
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
	if err := targetWaitProcessGroupsAbsent([]int{command.Process.Pid}, 2*time.Second); err != nil {
		t.Fatalf("wait for external root process group: %v", err)
	}
	return targetCommandResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}
}

func targetBoundedWait(waited <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-waited:
		return err
	case <-timer.C:
		return errors.New("process Wait remained blocked after forced cleanup")
	}
}

func targetOwnedProcessGroups(rootPID int) ([]int, error) {
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

func targetKillProcessGroups(groups []int, root int) error {
	var result error
	for index := len(groups) - 1; index >= 0; index-- {
		if groups[index] == root {
			continue
		}
		result = errors.Join(result, targetSignalProcessGroup(groups[index], syscall.SIGKILL))
	}
	return errors.Join(result, targetSignalProcessGroup(root, syscall.SIGKILL))
}

func targetSignalProcessGroup(group int, signal syscall.Signal) error {
	if group <= 1 || group == syscall.Getpgrp() {
		return errors.New("refuse to signal unsafe process group")
	}
	err := syscall.Kill(-group, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func targetWaitProcessGroupsAbsent(groups []int, timeout time.Duration) error {
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

func targetEnvironment(base []string, overrides map[string]string) []string {
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

func targetEnvironmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func targetRemoveEnvironment(environment []string, keys ...string) []string {
	values := targetEnvironmentMap(environment)
	for _, key := range keys {
		delete(values, key)
	}
	return targetEnvironment(nil, values)
}

func targetURLPassword(databaseURL string) string {
	if databaseURL == "" {
		return ""
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.User == nil {
		return ""
	}
	password, _ := parsed.User.Password()
	return password
}

func targetEnvironmentValue(environment []string, wanted string) string {
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == wanted {
			return value
		}
	}
	return ""
}

func targetDigestFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(document)
}

func targetEntryNames(entries []os.DirEntry) []string {
	result := make([]string, len(entries))
	for index := range entries {
		result[index] = entries[index].Name()
	}
	sort.Strings(result)
	return result
}

func equalTargetStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type targetBoundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *targetBoundedBuffer) Write(document []byte) (int, error) {
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

func (buffer *targetBoundedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
