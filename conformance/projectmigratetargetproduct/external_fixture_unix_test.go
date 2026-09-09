//go:build darwin || linux

package projectmigratetargetproduct_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/internal/testfixture"
	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/gobuild"
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
	repository := testfixture.RepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-target-migrate-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external target migration universe: %v", err)
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

	targetWriteFile(t, filepath.Join(root, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-target-migrate-external

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(repository))), 0o600)
	targetWriteFile(t, filepath.Join(root, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"), 0o600)
	targetWriteFile(t, filepath.Join(root, "cmd", "projectrunner", "main.go"), []byte(targetProjectRunnerSource), 0o600)
	testfixture.AuditApplicationSources(t, repository, root, targetAllowedGoDjImports)

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

	setupEnv := targetRemoveEnvironment(testfixture.Environment(os.Environ(), map[string]string{
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
	setupEnv = gobuild.Environment(setupEnv, os.Environ(), universe)
	targetRunSuccess(t, root, setupEnv, "go", "mod", "tidy")

	secret := "target-migrate-secret-canary-5d7e248ca1"
	baseEnv := testfixture.Environment(setupEnv, map[string]string{
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
	project.applicationHash = testfixture.ApplicationHashes(t, project.root)
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
	return testfixture.Environment(project.baseEnv, values)
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
	environment := testfixture.Environment(base, values)
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

func (project *targetExternalProject) run(t *testing.T, environment []string, arguments ...string) testprocess.CommandResult {
	t.Helper()
	return project.runAt(t, project.nested, environment, arguments...)
}

func (project *targetExternalProject) runAt(t *testing.T, directory string, environment []string, arguments ...string) testprocess.CommandResult {
	t.Helper()
	project.recordPublicFamily(arguments)
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: targetCommandTimeout, Output: targetMaximumOutput}, directory, environment, project.globalBinary, arguments...)
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
	testfixture.AssertArtifactsRedacted(t, project.root, sensitive...)
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

func (project *targetExternalProject) assertApplicationUnchanged(t *testing.T) {
	t.Helper()
	after := testfixture.ApplicationHashes(t, project.root)
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

func targetDecodeExecuteResult(t *testing.T, result testprocess.CommandResult) targetExecuteResult {
	t.Helper()
	if result.ExitCode != 0 || result.Stderr != "" || result.Stdout == "" {
		t.Fatalf("cannot decode unsuccessful migrate result: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
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

func targetAssertSuccess(t *testing.T, result testprocess.CommandResult, want string, sensitive ...string) {
	t.Helper()
	targetAssertRedacted(t, result, sensitive...)
	if result.ExitCode != 0 || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("command success = exit:%d stdout:%q stderr:%q, want 0/%q/empty", result.ExitCode, result.Stdout, result.Stderr, want)
	}
}

func targetAssertFailure(t *testing.T, result testprocess.CommandResult, exit int, stderr string, sensitive ...string) {
	t.Helper()
	targetAssertRedacted(t, result, sensitive...)
	if result.ExitCode != exit || result.Stdout != "" || result.Stderr != stderr {
		t.Fatalf("command failure = exit:%d stdout:%q stderr:%q, want %d/empty/%q", result.ExitCode, result.Stdout, result.Stderr, exit, stderr)
	}
}

func targetAssertRedacted(t *testing.T, result testprocess.CommandResult, sensitive ...string) {
	t.Helper()
	combined := result.Stdout + result.Stderr
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
	if err := testprocess.WaitAbsent(groups, 2*time.Second); err != nil {
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
	if err := testprocess.WaitAbsent(groups, 2*time.Second); err != nil {
		t.Fatal("linked project runner process group was not reaped")
	}
}

func targetResetMarker(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
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
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: targetCommandTimeout, Output: targetMaximumOutput}, directory, environment, name, arguments...)
	if result.ExitCode != 0 {
		t.Fatalf("%s %s failed: exit=%d stdout=%q stderr=%q", name, strings.Join(arguments, " "), result.ExitCode, result.Stdout, result.Stderr)
	}
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
	return testfixture.Environment(nil, values)
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
