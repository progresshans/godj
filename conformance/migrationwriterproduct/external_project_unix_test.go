//go:build darwin || linux

package migrationwriterproduct_test

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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	externalDatabaseEnvironment = "GODJ_EXTERNAL_SQLITE_DATABASE"
	externalBackendMarker       = "GODJ_EXTERNAL_BACKEND_MARKER"
	externalSecretEnvironment   = "GODJ_EXTERNAL_SECRET_CANARY"
	externalCommandTimeout      = 3 * time.Minute
	externalMaximumOutput       = 64 << 10
)

var externalAllowedGoDjImports = map[string]struct{}{
	"github.com/progresshans/godj/codegen":   {},
	"github.com/progresshans/godj/db/sqlite": {},
	"github.com/progresshans/godj/project":   {},
	"github.com/progresshans/godj/query":     {},
	"github.com/progresshans/godj/schema/ir": {},
}

type externalCommandResult struct {
	exitCode int
	stdout   string
	stderr   string
}

type externalMakemigrationsResult struct {
	Status         string `json:"status"`
	CandidateCount int    `json:"candidate_count"`
	Candidates     []struct {
		App      string `json:"app"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		SourceID string `json:"source_id"`
		SHA256   string `json:"sha256"`
	} `json:"candidates"`
}

type externalMigrateResult struct {
	SourceCount         int    `json:"source_count"`
	DefinitionCount     int    `json:"definition_count"`
	DefinitionSetDigest string `json:"definition_set_digest"`
}

type externalRestartResult struct {
	PID     int      `json:"pid"`
	History []string `json:"history"`
	Title   string   `json:"title"`
	Author  string   `json:"author"`
}

func TestMigrationWriterExternalProjectSQLitePublicSurface(t *testing.T) {
	repository := externalRepositoryRoot(t)
	universe, err := os.MkdirTemp("", "godj-migrationwriter-external-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(universe); err != nil {
			t.Errorf("remove external test universe: %v", err)
		}
	})
	externalAssertSeparateRoot(t, repository, universe)

	projectRoot := filepath.Join(universe, "consumer")
	for _, directory := range []string{
		projectRoot,
		filepath.Join(projectRoot, "cmd", "projectrunner"),
		filepath.Join(projectRoot, "migrations"),
		filepath.Join(projectRoot, "nested"),
		filepath.Join(universe, "home"),
		filepath.Join(universe, "scratch"),
		filepath.Join(universe, "cache"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	externalWriteFile(t, filepath.Join(projectRoot, "go.mod"), []byte(fmt.Sprintf(`module example.com/godj-migrationwriter-external

go 1.26.0

require github.com/progresshans/godj v0.0.0

replace github.com/progresshans/godj => %s
`, filepath.ToSlash(repository))), 0o600)
	externalWriteFile(t, filepath.Join(projectRoot, "godj.toml"), []byte("format_version = 1\n[project]\npackage = \"./cmd/projectrunner\"\n"), 0o600)
	externalWriteFile(t, filepath.Join(projectRoot, "cmd", "projectrunner", "main.go"), []byte(externalProjectRunnerSource), 0o600)
	externalAuditApplicationSources(t, repository, projectRoot)
	moduleCacheCommand := exec.Command("go", "env", "GOMODCACHE")
	moduleCacheDocument, err := moduleCacheCommand.Output()
	if err != nil {
		t.Fatalf("locate ambient read-only module cache: %v", err)
	}
	moduleCache := strings.TrimSpace(string(moduleCacheDocument))
	if info, err := os.Stat(moduleCache); err != nil || !info.IsDir() {
		t.Fatalf("ambient module cache %q is unavailable: %v", moduleCache, err)
	}

	baseEnvironment := externalEnvironment(os.Environ(), map[string]string{
		"HOME":                    filepath.Join(universe, "home"),
		"XDG_CONFIG_HOME":         filepath.Join(universe, "home"),
		"XDG_CACHE_HOME":          filepath.Join(universe, "cache"),
		"TMPDIR":                  filepath.Join(universe, "scratch"),
		"GOCACHE":                 filepath.Join(universe, "cache", "go-build"),
		"GOMODCACHE":              moduleCache,
		"GOWORK":                  "off",
		"GOTOOLCHAIN":             "local",
		"GOPROXY":                 "off",
		"GOSUMDB":                 "off",
		"GOFLAGS":                 "",
		externalSecretEnvironment: "external-secret-canary-6e9b2ac73d18",
	})
	externalRunSuccess(t, projectRoot, baseEnvironment, "go", "mod", "tidy")

	globalBinary := filepath.Join(universe, "godj")
	externalRunSuccess(t, repository, baseEnvironment, "go", "build", "-buildvcs=false", "-trimpath", "-mod=readonly", "-o", globalBinary, "./cmd/godj")
	runnerBinary := filepath.Join(universe, "projectrunner")
	externalRunSuccess(t, projectRoot, baseEnvironment, "go", "build", "-buildvcs=false", "-trimpath", "-mod=readonly", "-o", runnerBinary, "./cmd/projectrunner")

	databasePath := filepath.Join(universe, "sqlite-secret-path-93b4f7.sqlite3")
	backendMarker := filepath.Join(universe, "backend-opened.log")
	descriptor := filepath.Join(projectRoot, "godj.toml")
	commandEnvironment := externalEnvironment(baseEnvironment, map[string]string{
		externalDatabaseEnvironment: databasePath,
		externalBackendMarker:       backendMarker,
	})
	sensitive := []string{
		databasePath,
		filepath.ToSlash(databasePath),
		"sqlite-secret-path-93b4f7",
		"external-secret-canary-6e9b2ac73d18",
	}

	firstWriter := externalRun(t, filepath.Join(projectRoot, "nested"), commandEnvironment, globalBinary, "makemigrations", "--project", descriptor)
	externalAssertSuccessAndRedacted(t, firstWriter, sensitive...)
	firstWriterResult := externalDecodeOne[externalMakemigrationsResult](t, firstWriter.stdout)
	if firstWriterResult.Status != "generated" || firstWriterResult.CandidateCount != 2 || len(firstWriterResult.Candidates) != 2 {
		t.Fatalf("first external makemigrations result = %+v", firstWriterResult)
	}
	if _, err := os.Lstat(backendMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external makemigrations opened the project database backend: %v", err)
	}
	if _, err := os.Lstat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external makemigrations created the project database: %v", err)
	}

	wantCandidates := []struct {
		app  string
		path string
	}{
		{app: "authors", path: "migrations/authors_0001_initial.godj.json"},
		{app: "blog", path: "migrations/blog_0001_initial.godj.json"},
	}
	published := make(map[string][sha256.Size]byte, len(wantCandidates))
	for index, want := range wantCandidates {
		candidate := firstWriterResult.Candidates[index]
		if candidate.App != want.app || candidate.Name != "0001_initial" || candidate.Path != want.path || candidate.SourceID != want.path {
			t.Fatalf("external candidate[%d] = %+v", index, candidate)
		}
		path := filepath.Join(projectRoot, filepath.FromSlash(candidate.Path))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("external candidate %s mode = %v, want regular 0600", candidate.Path, info.Mode())
		}
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(document)
		if candidate.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("external candidate %s digest = %q, want %x", candidate.Path, candidate.SHA256, digest)
		}
		published[candidate.Path] = digest
	}

	repeatWriter := externalRun(t, filepath.Join(projectRoot, "nested"), commandEnvironment, globalBinary, "makemigrations", "--project", descriptor)
	externalAssertSuccessAndRedacted(t, repeatWriter, sensitive...)
	repeatWriterResult := externalDecodeOne[externalMakemigrationsResult](t, repeatWriter.stdout)
	if repeatWriterResult.Status != "clean" || repeatWriterResult.CandidateCount != 0 || len(repeatWriterResult.Candidates) != 0 {
		t.Fatalf("repeat external makemigrations result = %+v", repeatWriterResult)
	}
	if _, err := os.Lstat(backendMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repeat external makemigrations opened the project database backend: %v", err)
	}
	if _, err := os.Lstat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repeat external makemigrations created the project database: %v", err)
	}
	for relative, before := range published {
		document, err := os.ReadFile(filepath.Join(projectRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if after := sha256.Sum256(document); after != before {
			t.Fatalf("repeat external makemigrations changed %s bytes", relative)
		}
	}

	firstMigrate := externalRun(t, filepath.Join(projectRoot, "nested"), commandEnvironment, globalBinary, "migrate", "--project", descriptor)
	externalAssertSuccessAndRedacted(t, firstMigrate, sensitive...)
	firstMigrateResult := externalDecodeOne[externalMigrateResult](t, firstMigrate.stdout)
	if firstMigrateResult.SourceCount != 2 || firstMigrateResult.DefinitionCount != 2 ||
		len(firstMigrateResult.DefinitionSetDigest) != len("sha256:")+64 || !strings.HasPrefix(firstMigrateResult.DefinitionSetDigest, "sha256:") {
		t.Fatalf("first external migrate result = %+v", firstMigrateResult)
	}

	seed := externalRun(t, projectRoot, commandEnvironment, runnerBinary, "seed")
	externalAssertSuccessAndRedacted(t, seed, sensitive...)
	seedPID := externalDecodePID(t, seed.stdout, "seeded")
	databaseBeforeNoop := externalDigestFile(t, databasePath)

	secondMigrate := externalRun(t, filepath.Join(projectRoot, "nested"), commandEnvironment, globalBinary, "migrate", "--project", descriptor)
	externalAssertSuccessAndRedacted(t, secondMigrate, sensitive...)
	secondMigrateResult := externalDecodeOne[externalMigrateResult](t, secondMigrate.stdout)
	if secondMigrateResult != firstMigrateResult {
		t.Fatalf("second external migrate result = %+v, want %+v", secondMigrateResult, firstMigrateResult)
	}
	if databaseAfterNoop := externalDigestFile(t, databasePath); databaseAfterNoop != databaseBeforeNoop {
		t.Fatalf("external no-op migrate changed SQLite bytes: before=%x after=%x", databaseBeforeNoop, databaseAfterNoop)
	}

	restart := externalRun(t, projectRoot, commandEnvironment, runnerBinary, "verify")
	externalAssertSuccessAndRedacted(t, restart, sensitive...)
	restartResult := externalDecodeOne[externalRestartResult](t, restart.stdout)
	if restartResult.PID <= 0 || restartResult.PID == seedPID ||
		strings.Join(restartResult.History, ",") != "authors/0001_initial,blog/0001_initial" ||
		restartResult.Title != "external restart survives" || restartResult.Author != "External Author" {
		t.Fatalf("fresh external restart result = %+v, seed pid %d", restartResult, seedPID)
	}

	marker, err := os.ReadFile(backendMarker)
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != "opened\nopened\n" {
		t.Fatalf("external backend marker = %q, want two migrate opens", marker)
	}
	externalAuditApplicationSources(t, repository, projectRoot)
	externalAssertArtifactsRedacted(t, projectRoot, sensitive...)
	entries, err := os.ReadDir(filepath.Join(universe, "scratch"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("external product left private workspace artifacts: %v", externalEntryNames(entries))
	}
}

func externalRepositoryRoot(t *testing.T) string {
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

func externalAssertSeparateRoot(t *testing.T, repository, external string) {
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

func externalAuditApplicationSources(t *testing.T, repository, root string) {
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
			importPath, err := strconvUnquote(specification.Path.Value)
			if err != nil {
				return fmt.Errorf("decode import in %s: %w", path, err)
			}
			if !strings.HasPrefix(importPath, "github.com/progresshans/godj") {
				continue
			}
			if strings.Contains(importPath, "/internal/") || strings.Contains(importPath, "/conformance/") || strings.Contains(importPath, "/examples/") {
				return fmt.Errorf("application source %s imports forbidden GoDj package %s", path, importPath)
			}
			if _, ok := externalAllowedGoDjImports[importPath]; !ok {
				return fmt.Errorf("application source %s imports non-allowlisted GoDj package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func externalWriteFile(t *testing.T, path string, document []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, document, mode); err != nil {
		t.Fatal(err)
	}
}

func externalRunSuccess(t *testing.T, directory string, environment []string, name string, arguments ...string) {
	t.Helper()
	result := externalRun(t, directory, environment, name, arguments...)
	if result.exitCode != 0 {
		t.Fatalf("%s %s failed: exit=%d stdout=%q stderr=%q", name, strings.Join(arguments, " "), result.exitCode, result.stdout, result.stderr)
	}
}

func externalRun(t *testing.T, directory string, environment []string, name string, arguments ...string) externalCommandResult {
	t.Helper()
	stdout := &externalBoundedBuffer{maximum: externalMaximumOutput}
	stderr := &externalBoundedBuffer{maximum: externalMaximumOutput}
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
	timer := time.NewTimer(externalCommandTimeout)
	defer timer.Stop()
	var err error
	select {
	case err = <-waited:
	case <-timer.C:
		killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		select {
		case err = <-waited:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s %s timed out and process group did not exit; kill error: %v", name, strings.Join(arguments, " "), killErr)
		}
		externalWaitProcessGroupAbsent(t, command.Process.Pid)
		t.Fatalf("%s %s timed out; kill error: %v; wait error: %v", name, strings.Join(arguments, " "), killErr, err)
	}
	externalWaitProcessGroupAbsent(t, command.Process.Pid)
	if stdout.truncated || stderr.truncated {
		t.Fatalf("%s %s exceeded output limit", name, strings.Join(arguments, " "))
	}
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run %s %s: %v", name, strings.Join(arguments, " "), err)
		}
		exitCode = exitError.ExitCode()
	}
	return externalCommandResult{exitCode: exitCode, stdout: stdout.String(), stderr: stderr.String()}
}

func externalWaitProcessGroupAbsent(t *testing.T, processGroup int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-processGroup, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("inspect external process group %d: %v", processGroup, err)
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-processGroup, syscall.SIGKILL)
			t.Fatalf("external process group %d survived command completion", processGroup)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func externalAssertSuccessAndRedacted(t *testing.T, result externalCommandResult, sensitive ...string) {
	t.Helper()
	for _, value := range sensitive {
		if value != "" && (strings.Contains(result.stdout, value) || strings.Contains(result.stderr, value)) {
			t.Fatal("external command exposed a sensitive value")
		}
	}
	if result.exitCode != 0 || result.stderr != "" {
		t.Fatalf(
			"external command failed: exit=%d stdout_bytes=%d stderr_bytes=%d",
			result.exitCode,
			len(result.stdout),
			len(result.stderr),
		)
	}
}

func externalDecodeOne[T any](t *testing.T, document string) T {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.DisallowUnknownFields()
	var result T
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode external output %q: %v", document, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("external output %q has trailing value: %v", document, err)
	}
	return result
}

func externalDecodePID(t *testing.T, document, status string) int {
	t.Helper()
	var result struct {
		Status string `json:"status"`
		PID    int    `json:"pid"`
	}
	result = externalDecodeOne[struct {
		Status string `json:"status"`
		PID    int    `json:"pid"`
	}](t, document)
	if result.Status != status || result.PID <= 0 {
		t.Fatalf("external helper output = %+v, want status %q and positive pid", result, status)
	}
	return result.PID
}

func externalDigestFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(document)
}

func externalAssertArtifactsRedacted(t *testing.T, root string, sensitive ...string) {
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

func externalEnvironment(base []string, overrides map[string]string) []string {
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

func externalEntryNames(entries []os.DirEntry) []string {
	result := make([]string, len(entries))
	for index := range entries {
		result[index] = entries[index].Name()
	}
	sort.Strings(result)
	return result
}

type externalBoundedBuffer struct {
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *externalBoundedBuffer) Write(document []byte) (int, error) {
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

func (buffer *externalBoundedBuffer) String() string {
	return buffer.buffer.String()
}

func strconvUnquote(value string) (string, error) {
	var decoded string
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return "", err
	}
	return decoded, nil
}

const externalProjectRunnerSource = `package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

const databaseEnvironment = "GODJ_EXTERNAL_SQLITE_DATABASE"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "seed" {
		seed()
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "verify" {
		verify()
		return
	}
	config := project.Config{
		MigrationDefinitionRoots: []string{"migrations"},
		LoadProjectSpec: loadProjectSpec,
		OpenMigrationBackend: func(ctx context.Context) (project.MigrationBackend, error) {
			if marker := os.Getenv("GODJ_EXTERNAL_BACKEND_MARKER"); marker != "" {
				file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					return nil, err
				}
				if _, err := file.WriteString("opened\n"); err != nil {
					_ = file.Close()
					return nil, err
				}
				if err := file.Close(); err != nil {
					return nil, err
				}
			}
			return sqlite.Open(ctx, os.Getenv(databaseEnvironment))
		},
	}
	if err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "project runner failed")
		os.Exit(1)
	}
}

func loadProjectSpec(context.Context) (codegen.ProjectSpec, error) {
	authors := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel: "authors",
		Models: []ir.Model{{Name: "author", GoName: "Author", Fields: []ir.Field{
			{Name: "name", GoName: "Name", Kind: ir.FieldChar, MaxLength: 100},
		}}},
	}
	blog := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel: "blog",
		Models: []ir.Model{{Name: "blog_post", GoName: "BlogPost", Fields: []ir.Field{
			{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
			{Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{
				Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
				Cardinality: ir.RelationManyToOne,
				Reverse: ir.ReverseRelation{Name: "blog_posts"},
				OnDelete: ir.DeleteProtect,
			}},
		}}},
	}
	return codegen.ProjectSpec{
		Project: codegen.PackageSpec{PackageName: "project", ImportPath: "example.com/godj-migrationwriter-external/generated/project", Directory: "generated/project"},
		Apps: []codegen.AppSpec{
			{Alias: "blog", Package: codegen.PackageSpec{PackageName: "blog", ImportPath: "example.com/godj-migrationwriter-external/generated/blog", Directory: "generated/blog"}, Schema: blog},
			{Alias: "authors", Package: codegen.PackageSpec{PackageName: "authors", ImportPath: "example.com/godj-migrationwriter-external/generated/authors", Directory: "generated/authors"}, Schema: authors},
		},
	}, nil
}

func seed() {
	ctx := context.Background()
	backend, err := sqlite.Open(ctx, os.Getenv(databaseEnvironment))
	if err != nil {
		fatal()
	}
	if _, err := backend.ExecContext(ctx, "INSERT INTO authors_author (name) VALUES (?)", "External Author"); err != nil {
		_ = backend.Close()
		fatal()
	}
	if _, err := backend.ExecContext(ctx, "INSERT INTO blog_blog_post (title, author_id) VALUES (?, ?)", "external restart survives", int64(1)); err != nil {
		_ = backend.Close()
		fatal()
	}
	if err := backend.Close(); err != nil {
		fatal()
	}
	writeJSON(struct {
		Status string ` + "`json:\"status\"`" + `
		PID int ` + "`json:\"pid\"`" + `
	}{Status: "seeded", PID: os.Getpid()})
}

func verify() {
	ctx := context.Background()
	backend, err := sqlite.Open(ctx, os.Getenv(databaseEnvironment))
	if err != nil {
		fatal()
	}
	defer func() {
		if err := backend.Close(); err != nil {
			fatal()
		}
	}()
	history, err := backend.ReadAppliedMigrations(ctx)
	if err != nil {
		fatal()
	}
	historyKeys := make([]string, len(history))
	for index := range history {
		historyKeys[index] = history[index].App + "/" + history[index].Name
	}
	sort.Strings(historyKeys)
	rows, err := backend.Query(ctx, query.NewPlan("blog_blog_post", []query.FieldRef{
		query.NewFieldRef("title", "title", query.FieldString, false),
		query.NewFieldRef("author", "author_id", query.FieldInteger, false),
	}))
	if err != nil || !rows.Next() {
		if rows != nil {
			_ = rows.Close()
		}
		fatal()
	}
	var title string
	var authorID int64
	if err := rows.Scan(&title, &authorID); err != nil || authorID != 1 || rows.Next() || rows.Err() != nil {
		_ = rows.Close()
		fatal()
	}
	if err := rows.Close(); err != nil {
		fatal()
	}
	authorRows, err := backend.Query(ctx, query.NewPlan("authors_author", []query.FieldRef{
		query.NewFieldRef("name", "name", query.FieldString, false),
	}))
	if err != nil || !authorRows.Next() {
		if authorRows != nil {
			_ = authorRows.Close()
		}
		fatal()
	}
	var author string
	if err := authorRows.Scan(&author); err != nil || authorRows.Next() || authorRows.Err() != nil {
		_ = authorRows.Close()
		fatal()
	}
	if err := authorRows.Close(); err != nil {
		fatal()
	}
	writeJSON(struct {
		PID int ` + "`json:\"pid\"`" + `
		History []string ` + "`json:\"history\"`" + `
		Title string ` + "`json:\"title\"`" + `
		Author string ` + "`json:\"author\"`" + `
	}{PID: os.Getpid(), History: historyKeys, Title: title, Author: author})
}

func writeJSON(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fatal()
	}
}

func fatal() {
	_, _ = fmt.Fprintln(os.Stderr, "external project helper failed")
	os.Exit(1)
}
`
