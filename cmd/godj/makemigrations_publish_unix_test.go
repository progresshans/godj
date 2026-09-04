//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

const (
	actualE2ESQLiteDatabaseEnvironment = "GODJ_E2E_SQLITE_DATABASE"
	actualE2EBackendOpenMarker         = "GODJ_E2E_DB_OPEN_MARKER"
	actualE2ERestartHelper             = "GODJ_E2E_SQLITE_RESTART_HELPER"
)

type actualMakemigrationsOutput struct {
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

type actualMigrateOutput struct {
	SourceCount         int    `json:"source_count"`
	DefinitionCount     int    `json:"definition_count"`
	DefinitionSetDigest string `json:"definition_set_digest"`
}

func TestActualGodjMakemigrationsNormalPublishesCrossAppAndMigrateRestartsNoop(t *testing.T) {
	fixture := newProcessFixture(t)
	prepareActualMakemigrationsFixture(t, fixture)
	fixture.writeMain(t, actualE2EProjectMainWithBackendProbe(t))

	backendProbe := filepath.Join(fixture.universe, "makemigrations-backend-opened")
	makemigrationsEnvironment := map[string]string{
		"GODJ_E2E_USE_MIGRATIONS":  "1",
		actualE2EBackendOpenMarker: backendProbe,
		"GOPROXY":                  "off",
	}
	first := fixture.run(t, fixture.nested, makemigrationsEnvironment, "makemigrations")
	if first.exit != 0 || first.stderr != "" {
		t.Fatalf("first makemigrations = %+v", first)
	}
	firstOutput := decodeActualMakemigrationsOutput(t, first.stdout)
	if firstOutput.Status != "generated" || firstOutput.CandidateCount != 2 || len(firstOutput.Candidates) != 2 {
		t.Fatalf("first makemigrations output = %+v", firstOutput)
	}
	assertActualE2EPathMissing(t, backendProbe)

	publishedDocuments := make([]definition.Source, 0, len(firstOutput.Candidates))
	publishedBytes := make(map[string][]byte, len(firstOutput.Candidates))
	publishedInfo := make(map[string]os.FileInfo, len(firstOutput.Candidates))
	for index, wantApp := range []string{"authors", "blog"} {
		candidate := firstOutput.Candidates[index]
		wantRelative := "migrations/" + wantApp + "_0001_initial.godj.json"
		if candidate.App != wantApp || candidate.Name != "0001_initial" ||
			candidate.Path != wantRelative || candidate.SourceID != wantRelative || len(candidate.SHA256) != 64 {
			t.Fatalf("first candidate[%d] = %+v", index, candidate)
		}
		path := filepath.Join(fixture.project, filepath.FromSlash(candidate.Path))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("published candidate %s mode = %v, want regular 0600", candidate.Path, info.Mode())
		}
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(document)
		if candidate.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("published candidate %s digest = %q", candidate.Path, candidate.SHA256)
		}
		publishedBytes[candidate.Path] = document
		publishedInfo[candidate.Path] = info
		publishedDocuments = append(publishedDocuments, definition.Source{
			SourceID: candidate.SourceID,
			Document: document,
		})
	}
	loaded, _, err := definition.Load(publishedDocuments...)
	if err != nil {
		t.Fatalf("load published cross-app definitions: %v", err)
	}
	definitions := loaded.Definitions()
	if len(definitions) != 2 || definitions[0].App != "authors" || definitions[0].Name != "0001_initial" ||
		definitions[1].App != "blog" || definitions[1].Name != "0001_initial" ||
		len(definitions[0].Dependencies) != 0 || len(definitions[1].Dependencies) != 1 ||
		definitions[1].Dependencies[0].App != "authors" || definitions[1].Dependencies[0].Name != "0001_initial" {
		t.Fatalf("published definitions = %+v", definitions)
	}

	repeat := fixture.run(t, fixture.nested, makemigrationsEnvironment, "makemigrations")
	if repeat.exit != 0 || repeat.stderr != "" {
		t.Fatalf("repeat makemigrations = %+v", repeat)
	}
	repeatOutput := decodeActualMakemigrationsOutput(t, repeat.stdout)
	if repeatOutput.Status != "clean" || repeatOutput.CandidateCount != 0 || len(repeatOutput.Candidates) != 0 {
		t.Fatalf("repeat makemigrations output = %+v", repeatOutput)
	}
	assertActualE2EPathMissing(t, backendProbe)
	for candidatePath, before := range publishedBytes {
		path := filepath.Join(fixture.project, filepath.FromSlash(candidatePath))
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatalf("repeat makemigrations changed bytes for %s", candidatePath)
		}
		afterInfo, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(publishedInfo[candidatePath], afterInfo) {
			t.Fatalf("repeat makemigrations replaced inode for %s", candidatePath)
		}
	}

	fixture.writeMain(t, actualE2EProjectMainWithSQLiteBackend(t))
	tidyActualE2EProject(t, fixture)
	databasePath := filepath.Join(fixture.universe, "makemigrations.sqlite3")
	migrateMarker := filepath.Join(fixture.universe, "migrate-backend-opened")
	migrateEnvironment := map[string]string{
		"GODJ_E2E_USE_MIGRATIONS":          "1",
		actualE2ESQLiteDatabaseEnvironment: databasePath,
		actualE2EBackendOpenMarker:         migrateMarker,
		"GOPROXY":                          "off",
	}
	firstMigrate := fixture.run(t, fixture.nested, migrateEnvironment, "migrate")
	if firstMigrate.exit != 0 || firstMigrate.stderr != "" {
		t.Fatalf("first migrate = %+v", firstMigrate)
	}
	firstMigrateOutput := decodeActualMigrateOutput(t, firstMigrate.stdout)
	if firstMigrateOutput.SourceCount != 2 || firstMigrateOutput.DefinitionCount != 2 ||
		len(firstMigrateOutput.DefinitionSetDigest) != len("sha256:")+64 ||
		!strings.HasPrefix(firstMigrateOutput.DefinitionSetDigest, "sha256:") {
		t.Fatalf("first migrate output = %+v", firstMigrateOutput)
	}
	wantHistory := []migrationbackend.AppliedMigration{
		{App: "authors", Name: "0001_initial"},
		{App: "blog", Name: "0001_initial"},
	}
	if history := actualE2EAppliedMigrations(t, databasePath); !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("first migrate history = %+v, want %+v", history, wantHistory)
	}
	insertActualE2ECrossAppRows(t, databasePath)

	secondMigrate := fixture.run(t, fixture.nested, migrateEnvironment, "migrate")
	if secondMigrate.exit != 0 || secondMigrate.stderr != "" {
		t.Fatalf("second migrate = %+v", secondMigrate)
	}
	secondMigrateOutput := decodeActualMigrateOutput(t, secondMigrate.stdout)
	if !reflect.DeepEqual(secondMigrateOutput, firstMigrateOutput) {
		t.Fatalf("second migrate output = %+v, want %+v", secondMigrateOutput, firstMigrateOutput)
	}
	if history := actualE2EAppliedMigrations(t, databasePath); !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("second migrate history = %+v, want unchanged %+v", history, wantHistory)
	}
	markerDocument, err := os.ReadFile(migrateMarker)
	if err != nil {
		t.Fatal(err)
	}
	if string(markerDocument) != "opened\nopened\n" {
		t.Fatalf("migrate backend opener marker = %q, want two opens", markerDocument)
	}
	runActualE2ESQLiteRestartHelper(t, fixture, databasePath)
}

func TestActualGodjMakemigrationsSQLiteRestartHelper(t *testing.T) {
	if os.Getenv(actualE2ERestartHelper) != "1" {
		t.Skip("fresh-process SQLite restart helper")
	}
	databasePath := os.Getenv(actualE2ESQLiteDatabaseEnvironment)
	if databasePath == "" {
		t.Fatal("missing SQLite database path")
	}
	ctx := context.Background()
	backend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close fresh-process backend: %v", err)
		}
	}()
	wantHistory := []migrationbackend.AppliedMigration{
		{App: "authors", Name: "0001_initial"},
		{App: "blog", Name: "0001_initial"},
	}
	history, err := backend.ReadAppliedMigrations(ctx)
	if err != nil || !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("fresh-process migration history = (%+v, %v), want %+v", history, err, wantHistory)
	}
	rows, err := backend.Query(ctx, query.NewPlan(
		"blog_blog_post",
		[]query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.NewFieldRef("title", "title", query.FieldString, false),
			query.NewFieldRef("author", "author_id", query.FieldInteger, false),
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		closeErr := rows.Close()
		t.Fatalf("fresh-process query has no row: iteration=%v close=%v", rows.Err(), closeErr)
	}
	var id, authorID int64
	var title string
	if err := rows.Scan(&id, &title, &authorID); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if id != 1 || title != "Published migration survives restart" || authorID != 1 {
		_ = rows.Close()
		t.Fatalf("fresh-process row = (%d, %q, %d)", id, title, authorID)
	}
	if rows.Next() {
		_ = rows.Close()
		t.Fatal("fresh-process query returned more than one row")
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func prepareActualMakemigrationsFixture(t *testing.T, fixture processFixture) {
	t.Helper()
	modulePath := filepath.Join(fixture.project, "go.mod")
	moduleDocument, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	moduleLines := strings.Split(string(moduleDocument), "\n")
	filtered := moduleLines[:0]
	for _, line := range moduleLines {
		if !strings.HasPrefix(line, "replace golang.org/x/sys => ") {
			filtered = append(filtered, line)
		}
	}
	if err := os.WriteFile(modulePath, []byte(strings.Join(filtered, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.project, "go.sum"), []byte(
		"golang.org/x/sys v0.47.0 h1:o7XGOvZQCADBQQ4Y7VNq2dRWQR7JmOUW8Kxx4ZsNgWs=\n"+
			"golang.org/x/sys v0.47.0/go.mod h1:4GL1E5IUh+htKOUEOaiffhrAeqysfVGipDYzABqnCmw=\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(fixture.project, "migrations"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func decodeActualMakemigrationsOutput(t *testing.T, document string) actualMakemigrationsOutput {
	t.Helper()
	var output actualMakemigrationsOutput
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("decode makemigrations output %q: %v", document, err)
	}
	if decoder.More() {
		t.Fatalf("extra makemigrations output after %q", document)
	}
	return output
}

func decodeActualMigrateOutput(t *testing.T, document string) actualMigrateOutput {
	t.Helper()
	var output actualMigrateOutput
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		t.Fatalf("decode migrate output %q: %v", document, err)
	}
	if decoder.More() {
		t.Fatalf("extra migrate output after %q", document)
	}
	return output
}

func assertActualE2EPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path %s exists or cannot be inspected: %v", path, err)
	}
}

func actualE2EProjectMainWithBackendProbe(t *testing.T) string {
	t.Helper()
	return replaceActualE2ESourceOnce(t, e2eProjectMain,
		"\tif err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {",
		`	if marker := os.Getenv("GODJ_E2E_DB_OPEN_MARKER"); marker != "" {
		config.OpenMigrationBackend = func(context.Context) (project.MigrationBackend, error) {
			if err := os.WriteFile(marker, []byte("opened\n"), 0o600); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("makemigrations opened its migration backend")
		}
	}
	if err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {`)
}

func actualE2EProjectMainWithSQLiteBackend(t *testing.T) string {
	t.Helper()
	source := replaceActualE2ESourceOnce(t, e2eProjectMain,
		`	"github.com/progresshans/godj/codegen"`,
		`	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/db/sqlite"`)
	return replaceActualE2ESourceOnce(t, source,
		"\tif err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {",
		`	config.OpenMigrationBackend = func(ctx context.Context) (project.MigrationBackend, error) {
		if marker := os.Getenv("GODJ_E2E_DB_OPEN_MARKER"); marker != "" {
			file, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
		return sqlite.Open(ctx, os.Getenv("GODJ_E2E_SQLITE_DATABASE"))
	}
	if err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {`)
}

func replaceActualE2ESourceOnce(t *testing.T, source, old, replacement string) string {
	t.Helper()
	if count := strings.Count(source, old); count != 1 {
		t.Fatalf("e2e source anchor %q occurs %d times", old, count)
	}
	return strings.Replace(source, old, replacement, 1)
}

func tidyActualE2EProject(t *testing.T, fixture processFixture) {
	t.Helper()
	command := exec.Command("go", "mod", "tidy")
	command.Dir = fixture.project
	// Dependency resolution prepares the fixture; it is not a GoDj product
	// command. The actual makemigrations and migrate invocations below still
	// force GOPROXY=off. Keeping setup on the ambient configured proxy avoids
	// making a cold runner's incidental module-cache contents part of the
	// product contract.
	command.Env = fixture.environment(nil)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("tidy SQLite e2e project: %v\n%s", err, output)
	}
}

func actualE2EAppliedMigrations(t *testing.T, databasePath string) []migrationbackend.AppliedMigration {
	t.Helper()
	backend, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	history, readErr := backend.ReadAppliedMigrations(context.Background())
	closeErr := backend.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return history
}

func insertActualE2ECrossAppRows(t *testing.T, databasePath string) {
	t.Helper()
	ctx := context.Background()
	backend, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "authors_author" ("name") VALUES (?)`, "Ada"); err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	if _, err := backend.ExecContext(ctx, `INSERT INTO "blog_blog_post" ("title", "author_id") VALUES (?, ?)`, "Published migration survives restart", 1); err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
}

func runActualE2ESQLiteRestartHelper(t *testing.T, fixture processFixture, databasePath string) {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestActualGodjMakemigrationsSQLiteRestartHelper$", "-test.v")
	command.Dir = fixture.project
	command.Env = fixture.environment(map[string]string{
		actualE2ERestartHelper:             "1",
		actualE2ESQLiteDatabaseEnvironment: databasePath,
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fresh-process SQLite restart helper: %v\n%s", err, output)
	}
}
