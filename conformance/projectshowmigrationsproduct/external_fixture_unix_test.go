//go:build darwin || linux

package projectshowmigrationsproduct_test

import (
	"errors"
	"fmt"
	"io/fs"
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
	externalStatusDatabaseEnvironment       = "GODJ_SHOWMIGRATIONS_SQLITE_DATABASE"
	externalStatusBackendEnvironment        = "GODJ_SHOWMIGRATIONS_BACKEND"
	externalStatusPostgresURLEnvironment    = "GODJ_SHOWMIGRATIONS_POSTGRES_URL"
	externalStatusPostgresSchemaEnvironment = "GODJ_SHOWMIGRATIONS_POSTGRES_SCHEMA"
	externalStatusMarkerEnvironment         = "GODJ_SHOWMIGRATIONS_BACKEND_MARKER"
	externalStatusCatalogEnvironment        = "GODJ_SHOWMIGRATIONS_CATALOG"
	externalStatusSecretEnvironment         = "GODJ_SHOWMIGRATIONS_SECRET_CANARY"
	externalStatusCommandTimeout            = 4 * time.Minute
	externalStatusMaximumOutput             = 64 << 10
)

var externalStatusAllowedImports = map[string]struct{}{
	"github.com/progresshans/godj/db/postgres":           {},
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

type externalStatusMarker struct {
	event string
	pid   int
}

func newExternalStatusProject(t *testing.T) *externalStatusProject {
	t.Helper()
	repository := testfixture.RepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-showmigrations-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external showmigrations universe: %v", err)
		}
	})
	testfixture.AssertSeparateRoot(t, repository, universe)

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
	testfixture.AuditApplicationSources(t, repository, root, externalStatusAllowedImports)

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

	setupEnv := testfixture.Environment(os.Environ(), map[string]string{
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
	setupEnv = gobuild.Environment(setupEnv, os.Environ(), universe)
	externalStatusRunSuccess(t, root, setupEnv, "go", "mod", "tidy")

	secret := "showmigrations-secret-canary-2f11630d7a"
	baseEnv := testfixture.Environment(setupEnv, map[string]string{
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
	return testfixture.Environment(project.baseEnv, map[string]string{
		externalStatusBackendEnvironment:  "sqlite",
		externalStatusDatabaseEnvironment: database,
		externalStatusMarkerEnvironment:   marker,
		externalStatusCatalogEnvironment:  catalog,
	})
}

func (project *externalStatusProject) postgresEnvironment(t *testing.T, databaseURL, schema, marker, catalog string) []string {
	t.Helper()
	base := externalStatusRemoveEnvironment(
		project.baseEnv,
		externalStatusPostgresTestURLEnvironment,
		externalStatusPostgresRequiredEnvironment,
		externalStatusDatabaseEnvironment,
	)
	environment := testfixture.Environment(base, map[string]string{
		externalStatusBackendEnvironment:        "postgres",
		externalStatusPostgresURLEnvironment:    databaseURL,
		externalStatusPostgresSchemaEnvironment: schema,
		externalStatusMarkerEnvironment:         marker,
		externalStatusCatalogEnvironment:        catalog,
	})
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if _, exists := values[externalStatusPostgresTestURLEnvironment]; exists {
		t.Fatal("PostgreSQL project environment retained test-only database URL")
	}
	if _, exists := values[externalStatusPostgresRequiredEnvironment]; exists {
		t.Fatal("PostgreSQL project environment retained test-only required sentinel")
	}
	if _, exists := values[externalStatusDatabaseEnvironment]; exists {
		t.Fatal("PostgreSQL project environment retained SQLite database configuration")
	}
	if values[externalStatusBackendEnvironment] != "postgres" ||
		values[externalStatusPostgresURLEnvironment] != databaseURL ||
		values[externalStatusPostgresSchemaEnvironment] != schema {
		t.Fatal("PostgreSQL project environment did not retain exact project-owned database configuration")
	}
	return environment
}

func (project *externalStatusProject) run(t *testing.T, environment []string, arguments ...string) testprocess.CommandResult {
	t.Helper()
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: externalStatusCommandTimeout, Output: externalStatusMaximumOutput}, project.nested, environment, project.globalBinary, arguments...)
	project.assertWorkspaceEmpty(t)
	return result
}

func (project *externalStatusProject) runShow(t *testing.T, environment []string) testprocess.CommandResult {
	t.Helper()
	return project.run(t, environment, "showmigrations", "--project", project.descriptor)
}

func (project *externalStatusProject) runMigrate(t *testing.T, environment []string) testprocess.CommandResult {
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

func externalStatusWriteFile(t *testing.T, path string, document []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, document, mode); err != nil {
		t.Fatal(err)
	}
}

func externalStatusRunSuccess(t *testing.T, directory string, environment []string, name string, arguments ...string) {
	t.Helper()
	result := testprocess.Run(t, testprocess.CommandLimits{Timeout: externalStatusCommandTimeout, Output: externalStatusMaximumOutput}, directory, environment, name, arguments...)
	if result.ExitCode != 0 {
		t.Fatalf("%s %s failed: exit=%d stdout=%q stderr=%q", name, strings.Join(arguments, " "), result.ExitCode, result.Stdout, result.Stderr)
	}
}

func externalStatusAssertSuccess(t *testing.T, result testprocess.CommandResult, want string, sensitive ...string) {
	t.Helper()
	externalStatusAssertRedacted(t, result, sensitive...)
	if result.ExitCode != 0 || result.Stdout != want || result.Stderr != "" {
		t.Fatalf("showmigrations success = exit:%d stdout:%q stderr:%q, want 0/%q/empty", result.ExitCode, result.Stdout, result.Stderr, want)
	}
}

func externalStatusAssertFailure(t *testing.T, result testprocess.CommandResult, exit int, stderr string, sensitive ...string) {
	t.Helper()
	externalStatusAssertRedacted(t, result, sensitive...)
	if result.ExitCode != exit || result.Stdout != "" || result.Stderr != stderr {
		t.Fatalf("showmigrations failure = exit:%d stdout:%q stderr:%q, want %d/empty/%q", result.ExitCode, result.Stdout, result.Stderr, exit, stderr)
	}
}

func externalStatusAssertRedacted(t *testing.T, result testprocess.CommandResult, sensitive ...string) {
	t.Helper()
	combined := result.Stdout + result.Stderr
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
	if err := testprocess.WaitAbsent([]int{pid}, 2*time.Second); err != nil {
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

func externalStatusRemoveEnvironment(base []string, keys ...string) []string {
	removed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		removed[key] = struct{}{}
	}
	result := make([]string, 0, len(base))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if _, excluded := removed[key]; ok && excluded {
			continue
		}
		result = append(result, entry)
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

const externalStatusProjectRunnerSource = `package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/schema/ir"
)

const (
	databaseEnvironment = "GODJ_SHOWMIGRATIONS_SQLITE_DATABASE"
	backendEnvironment = "GODJ_SHOWMIGRATIONS_BACKEND"
	postgresURLEnvironment = "GODJ_SHOWMIGRATIONS_POSTGRES_URL"
	postgresSchemaEnvironment = "GODJ_SHOWMIGRATIONS_POSTGRES_SCHEMA"
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
	var opened project.MigrationBackend
	var err error
	switch os.Getenv(backendEnvironment) {
	case "sqlite":
		opened, err = sqlite.Open(ctx, os.Getenv(databaseEnvironment))
	case "postgres":
		opened, err = postgres.Open(ctx, postgres.Config{
			URL: os.Getenv(postgresURLEnvironment),
			Schema: os.Getenv(postgresSchemaEnvironment),
		})
	default:
		return nil, errors.New("unsupported external backend")
	}
	if err != nil {
		return nil, err
	}
	if err := appendMarker("backend_acquired"); err != nil {
		return nil, errors.Join(err, opened.Close())
	}
	return &observedBackend{delegate: opened}, nil
}

type observedBackend struct {
	delegate project.MigrationBackend
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
