//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db/postgres"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
)

const (
	actualE2EPostgresTestURLEnvironment  = "GODJ_TEST_POSTGRES_URL"
	actualE2EPostgresRequiredEnvironment = "GODJ_REQUIRE_POSTGRES"
	actualE2EPostgresURLEnvironment      = "GODJ_E2E_POSTGRES_URL"
	actualE2EPostgresSchemaEnvironment   = "GODJ_E2E_POSTGRES_SCHEMA"
	actualE2EPostgresRestartEnvironment  = "GODJ_E2E_POSTGRES_RESTART_HELPER"
)

func TestActualGodjMakemigrationsPostgresGeneratedMigrateNoopRestart(t *testing.T) {
	databaseURL := actualE2EPostgresTestURL(t)
	schema := actualE2EPostgresCreateSchema(t, databaseURL)
	sensitive := actualE2EPostgresSensitiveValues(t, databaseURL, schema)

	fixture := newProcessFixture(t)
	prepareActualMakemigrationsFixture(t, fixture)
	fixture.writeMain(t, actualE2EProjectMainWithBackendProbe(t))
	backendProbe := filepath.Join(fixture.universe, "postgres-makemigrations-backend-opened")
	makemigrationsEnvironment := map[string]string{
		"GODJ_E2E_USE_MIGRATIONS":          "1",
		actualE2EBackendOpenMarker:         backendProbe,
		actualE2EPostgresURLEnvironment:    databaseURL,
		actualE2EPostgresSchemaEnvironment: schema,
		"GOPROXY":                          "off",
	}

	first := actualE2EPostgresRun(t, fixture, fixture.nested, makemigrationsEnvironment, "makemigrations")
	actualE2EPostgresAssertVisibleSecretFree(t, first.stdout, first.stderr, sensitive)
	if first.exit != 0 || first.stderr != "" {
		t.Fatalf("first PostgreSQL makemigrations = exit:%d stdout-bytes:%d stderr-bytes:%d", first.exit, len(first.stdout), len(first.stderr))
	}
	firstOutput := decodeActualMakemigrationsOutput(t, first.stdout)
	if firstOutput.Status != "generated" || firstOutput.CandidateCount != 2 || len(firstOutput.Candidates) != 2 {
		t.Fatalf("first PostgreSQL makemigrations output = %+v", firstOutput)
	}
	assertActualE2EPathMissing(t, backendProbe)
	actualE2EPostgresAssertSchemaEmpty(t, databaseURL, schema)

	publishedSources := make([]definition.Source, 0, len(firstOutput.Candidates))
	publishedBytes := make(map[string][]byte, len(firstOutput.Candidates))
	publishedInfo := make(map[string]os.FileInfo, len(firstOutput.Candidates))
	for index, wantApp := range []string{"authors", "blog"} {
		candidate := firstOutput.Candidates[index]
		wantRelative := "migrations/" + wantApp + "_0001_initial.godj.json"
		if candidate.App != wantApp || candidate.Name != "0001_initial" || candidate.Path != wantRelative ||
			candidate.SourceID != wantRelative || len(candidate.SHA256) != 64 {
			t.Fatalf("PostgreSQL makemigrations candidate[%d] = %+v", index, candidate)
		}
		path := filepath.Join(fixture.project, filepath.FromSlash(candidate.Path))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal("inspect PostgreSQL generated candidate")
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("PostgreSQL generated candidate %s mode = %v, want regular 0600", candidate.Path, info.Mode())
		}
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("read PostgreSQL generated candidate")
		}
		actualE2EPostgresAssertDocumentSecretFree(t, document, sensitive)
		if actualE2ESHA256(document) != candidate.SHA256 {
			t.Fatalf("PostgreSQL generated candidate %s checksum did not match output", candidate.Path)
		}
		publishedBytes[candidate.Path] = document
		publishedInfo[candidate.Path] = info
		publishedSources = append(publishedSources, definition.Source{SourceID: candidate.SourceID, Document: document})
	}
	loaded, _, err := definition.Load(publishedSources...)
	if err != nil {
		t.Fatalf("strict-load PostgreSQL generated definitions: %v", err)
	}
	definitions := loaded.Definitions()
	if len(definitions) != 2 || definitions[0].App != "authors" || definitions[0].Name != "0001_initial" ||
		definitions[1].App != "blog" || definitions[1].Name != "0001_initial" ||
		len(definitions[0].Dependencies) != 0 || len(definitions[1].Dependencies) != 1 ||
		definitions[1].Dependencies[0].App != "authors" || definitions[1].Dependencies[0].Name != "0001_initial" {
		t.Fatalf("PostgreSQL generated definitions = %+v", definitions)
	}

	repeat := actualE2EPostgresRun(t, fixture, fixture.nested, makemigrationsEnvironment, "makemigrations")
	actualE2EPostgresAssertVisibleSecretFree(t, repeat.stdout, repeat.stderr, sensitive)
	if repeat.exit != 0 || repeat.stderr != "" {
		t.Fatalf("repeat PostgreSQL makemigrations = exit:%d stdout-bytes:%d stderr-bytes:%d", repeat.exit, len(repeat.stdout), len(repeat.stderr))
	}
	repeatOutput := decodeActualMakemigrationsOutput(t, repeat.stdout)
	if repeatOutput.Status != "clean" || repeatOutput.CandidateCount != 0 || len(repeatOutput.Candidates) != 0 {
		t.Fatalf("repeat PostgreSQL makemigrations output = %+v", repeatOutput)
	}
	assertActualE2EPathMissing(t, backendProbe)
	actualE2EPostgresAssertSchemaEmpty(t, databaseURL, schema)
	for candidatePath, before := range publishedBytes {
		path := filepath.Join(fixture.project, filepath.FromSlash(candidatePath))
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("repeat PostgreSQL makemigrations changed %s", candidatePath)
		}
		afterInfo, err := os.Lstat(path)
		if err != nil || !os.SameFile(publishedInfo[candidatePath], afterInfo) {
			t.Fatalf("repeat PostgreSQL makemigrations replaced %s", candidatePath)
		}
	}

	fixture.writeMain(t, actualE2EProjectMainWithPostgresBackend(t))
	tidyActualE2EProject(t, fixture)
	migrateMarker := filepath.Join(fixture.universe, "postgres-migrate-backend-opened")
	migrateEnvironment := map[string]string{
		"GODJ_E2E_USE_MIGRATIONS":          "1",
		actualE2EBackendOpenMarker:         migrateMarker,
		actualE2EPostgresURLEnvironment:    databaseURL,
		actualE2EPostgresSchemaEnvironment: schema,
		"GOPROXY":                          "off",
	}

	firstMigrate := actualE2EPostgresRun(t, fixture, fixture.nested, migrateEnvironment, "migrate")
	actualE2EPostgresAssertVisibleSecretFree(t, firstMigrate.stdout, firstMigrate.stderr, sensitive)
	if firstMigrate.exit != 0 || firstMigrate.stderr != "" {
		t.Fatalf("first generated PostgreSQL migrate = exit:%d stdout-bytes:%d stderr-bytes:%d", firstMigrate.exit, len(firstMigrate.stdout), len(firstMigrate.stderr))
	}
	firstMigrateOutput := decodeActualMigrateOutput(t, firstMigrate.stdout)
	if firstMigrateOutput.SourceCount != 2 || firstMigrateOutput.DefinitionCount != 2 ||
		!strings.HasPrefix(firstMigrateOutput.DefinitionSetDigest, "sha256:") ||
		len(firstMigrateOutput.DefinitionSetDigest) != len("sha256:")+64 {
		t.Fatalf("first generated PostgreSQL migrate output = %+v", firstMigrateOutput)
	}
	wantHistory := []migrationbackend.AppliedMigration{
		{App: "authors", Name: "0001_initial"},
		{App: "blog", Name: "0001_initial"},
	}
	if history := actualE2EPostgresAppliedMigrations(t, databaseURL, schema); !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("generated PostgreSQL migration history = %+v, want %+v", history, wantHistory)
	}
	actualE2EPostgresInsertCrossAppRows(t, databaseURL, schema)
	revisionBeforeNoop := actualE2EPostgresRevisionState(t, databaseURL, schema)
	schemaBeforeNoop := actualE2EPostgresSchemaFingerprint(t, databaseURL, schema)

	secondMigrate := actualE2EPostgresRun(t, fixture, fixture.nested, migrateEnvironment, "migrate")
	actualE2EPostgresAssertVisibleSecretFree(t, secondMigrate.stdout, secondMigrate.stderr, sensitive)
	if secondMigrate.exit != 0 || secondMigrate.stderr != "" {
		t.Fatalf("second generated PostgreSQL migrate = exit:%d stdout-bytes:%d stderr-bytes:%d", secondMigrate.exit, len(secondMigrate.stdout), len(secondMigrate.stderr))
	}
	secondMigrateOutput := decodeActualMigrateOutput(t, secondMigrate.stdout)
	if !reflect.DeepEqual(secondMigrateOutput, firstMigrateOutput) {
		t.Fatalf("second generated PostgreSQL migrate output = %+v, want %+v", secondMigrateOutput, firstMigrateOutput)
	}
	if history := actualE2EPostgresAppliedMigrations(t, databaseURL, schema); !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("second generated PostgreSQL history = %+v, want unchanged %+v", history, wantHistory)
	}
	if revisionAfterNoop := actualE2EPostgresRevisionState(t, databaseURL, schema); revisionAfterNoop != revisionBeforeNoop {
		t.Fatal("second generated PostgreSQL migrate changed the revision fence")
	}
	if schemaAfterNoop := actualE2EPostgresSchemaFingerprint(t, databaseURL, schema); schemaAfterNoop != schemaBeforeNoop {
		t.Fatal("second generated PostgreSQL migrate changed the managed schema")
	}
	actualE2EPostgresAssertCrossAppRow(t, databaseURL, schema)
	markerDocument, err := os.ReadFile(migrateMarker)
	if err != nil || string(markerDocument) != "opened\nopened\n" {
		t.Fatalf("generated PostgreSQL migrate backend marker = %q", markerDocument)
	}

	actualE2EPostgresRunRestartHelper(t, fixture, databaseURL, schema, sensitive)
	actualE2EPostgresAssertStoredTextSecretFree(t, databaseURL, schema, sensitive)
	actualE2EPostgresAssertArtifactTreeSecretFree(t, fixture.universe, sensitive)
}

func TestActualGodjMakemigrationsPostgresRestartHelper(t *testing.T) {
	if os.Getenv(actualE2EPostgresRestartEnvironment) != "1" {
		t.Skip("fresh-process PostgreSQL restart helper")
	}
	databaseURL := strings.TrimSpace(os.Getenv(actualE2EPostgresURLEnvironment))
	schema := strings.TrimSpace(os.Getenv(actualE2EPostgresSchemaEnvironment))
	if databaseURL == "" || !actualE2EPostgresValidSchema(schema) {
		t.Fatal("invalid fresh-process PostgreSQL restart environment")
	}
	wantHistory := []migrationbackend.AppliedMigration{
		{App: "authors", Name: "0001_initial"},
		{App: "blog", Name: "0001_initial"},
	}
	if history := actualE2EPostgresAppliedMigrations(t, databaseURL, schema); !reflect.DeepEqual(history, wantHistory) {
		t.Fatalf("fresh-process PostgreSQL history = %+v, want %+v", history, wantHistory)
	}
	actualE2EPostgresAssertCrossAppRow(t, databaseURL, schema)
}

func TestActualGodjMakemigrationsPostgresProjectMainParses(t *testing.T) {
	source := actualE2EProjectMainWithPostgresBackend(t)
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", source, parser.AllErrors); err != nil {
		t.Fatalf("parse generated PostgreSQL project main: %v", err)
	}
}

func actualE2EPostgresRun(
	t *testing.T,
	fixture processFixture,
	cwd string,
	extra map[string]string,
	args ...string,
) commandResult {
	t.Helper()
	command := exec.Command(fixture.godj, args...)
	command.Dir = cwd
	command.Env = fixture.environment(extra)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal("start generated PostgreSQL product command")
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	var runErr error
	select {
	case runErr = <-waited:
	case <-timer.C:
		killErr := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		select {
		case runErr = <-waited:
		case <-time.After(5 * time.Second):
			t.Fatalf("generated PostgreSQL product command timed out and did not exit; kill error: %v", killErr)
		}
		actualE2EPostgresWaitProcessGroupAbsent(t, command.Process.Pid)
		t.Fatalf("generated PostgreSQL product command timed out; kill error: %v; wait error: %v", killErr, runErr)
	}
	actualE2EPostgresWaitProcessGroupAbsent(t, command.Process.Pid)
	exit := 0
	if runErr != nil {
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) {
			t.Fatal("run generated PostgreSQL product command")
		}
		exit = exitError.ExitCode()
	}
	return commandResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func actualE2EPostgresWaitProcessGroupAbsent(t *testing.T, processGroup int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-processGroup, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatal("inspect generated PostgreSQL product process group")
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-processGroup, syscall.SIGKILL)
			t.Fatal("generated PostgreSQL product process group survived command completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func actualE2EPostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(actualE2EPostgresTestURLEnvironment))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(actualE2EPostgresRequiredEnvironment) == "1" {
		t.Fatalf("%s=1 requires %s", actualE2EPostgresRequiredEnvironment, actualE2EPostgresTestURLEnvironment)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; generated PostgreSQL makemigrations E2E was not run")
	return ""
}

func actualE2EPostgresSensitiveValues(t *testing.T, databaseURL, schema string) []string {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse generated PostgreSQL product URL")
	}
	values := []string{databaseURL, schema}
	if config.Password != "" {
		values = append(values, config.Password)
	}
	return values
}

func actualE2EPostgresAssertSchemaEmpty(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect generated PostgreSQL DB-zero inspection")
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close generated PostgreSQL DB-zero inspection")
		}
	}()
	var relations int
	if err := connection.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1`, schema).Scan(&relations); err != nil {
		t.Fatal("inspect generated PostgreSQL DB-zero schema")
	}
	if relations != 0 {
		t.Fatalf("makemigrations mutated PostgreSQL schema: relations=%d", relations)
	}
}

func actualE2EPostgresCreateSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	schema := fmt.Sprintf("godj_mm_%d_%d", os.Getpid(), time.Now().UnixNano())
	if !actualE2EPostgresValidSchema(schema) {
		t.Fatal("generated PostgreSQL makemigrations schema is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect generated PostgreSQL schema owner")
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := connection.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = connection.Close(ctx)
		t.Fatal("create generated PostgreSQL isolated schema")
	}
	if err := connection.Close(ctx); err != nil {
		t.Fatal("close generated PostgreSQL schema owner")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect generated PostgreSQL schema cleanup")
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop generated PostgreSQL isolated schema")
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close generated PostgreSQL schema cleanup")
		}
	})
	return schema
}

func actualE2EPostgresValidSchema(schema string) bool {
	if !strings.HasPrefix(schema, "godj_mm_") || len(schema) > 63 {
		return false
	}
	for _, character := range []byte(strings.TrimPrefix(schema, "godj_mm_")) {
		if (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return len(strings.TrimPrefix(schema, "godj_mm_")) != 0
}

func actualE2EProjectMainWithPostgresBackend(t *testing.T) string {
	t.Helper()
	source := replaceActualE2ESourceOnce(t, e2eProjectMain,
		`"github.com/progresshans/godj/codegen"`,
		`"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/db/postgres"`)
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
		return postgres.Open(ctx, postgres.Config{
			URL: os.Getenv("GODJ_E2E_POSTGRES_URL"),
			Schema: os.Getenv("GODJ_E2E_POSTGRES_SCHEMA"),
		})
	}
	if err := project.Run(context.Background(), config, os.Args[1:], os.Stdin, os.Stdout); err != nil {`)
}

func actualE2EPostgresAppliedMigrations(t *testing.T, databaseURL, schema string) []migrationbackend.AppliedMigration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatal("open generated PostgreSQL history backend")
	}
	history, readErr := backend.ReadAppliedMigrations(ctx)
	closeErr := backend.Close()
	if readErr != nil {
		t.Fatal("read generated PostgreSQL migration history")
	}
	if closeErr != nil {
		t.Fatal("close generated PostgreSQL history backend")
	}
	return history
}

func actualE2EPostgresRevisionState(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect generated PostgreSQL revision inspection")
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close generated PostgreSQL revision inspection")
		}
	}()
	var singleton int16
	var formatVersion int32
	var epoch, fingerprint string
	var revision int64
	table := pgx.Identifier{schema, "godj_migration_revision"}.Sanitize()
	if err := connection.QueryRow(ctx, `SELECT "singleton", "format_version", encode("epoch", 'hex'), "revision", encode("history_fingerprint", 'hex') FROM `+table).Scan(
		&singleton,
		&formatVersion,
		&epoch,
		&revision,
		&fingerprint,
	); err != nil {
		t.Fatal("read generated PostgreSQL revision fence")
	}
	return fmt.Sprintf("%d/%d/%s/%d/%s", singleton, formatVersion, epoch, revision, fingerprint)
}

func actualE2EPostgresSchemaFingerprint(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect generated PostgreSQL schema fingerprint")
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close generated PostgreSQL schema fingerprint")
		}
	}()
	rows, err := connection.Query(ctx, `
		SELECT "kind", "relation", "position", "name", "definition", "flag"
		FROM (
			SELECT 'column'::text AS "kind", "c"."relname" AS "relation",
			       "a"."attnum"::text AS "position", "a"."attname" AS "name",
			       pg_catalog.format_type("a"."atttypid", "a"."atttypmod") AS "definition",
			       "a"."attnotnull"::text AS "flag"
			FROM "pg_catalog"."pg_class" AS "c"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid"
			WHERE "n"."nspname" = $1 AND "c"."relkind" IN ('r', 'p')
			  AND "a"."attnum" > 0 AND NOT "a"."attisdropped"
			UNION ALL
			SELECT 'constraint'::text, "c"."relname", "constraint"."conname",
			       "constraint"."contype"::text,
			       pg_catalog.pg_get_constraintdef("constraint"."oid", false),
			       "constraint"."convalidated"::text
			FROM "pg_catalog"."pg_constraint" AS "constraint"
			JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "constraint"."conrelid"
			JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
			WHERE "n"."nspname" = $1
		) AS "fingerprint"
		ORDER BY "kind", "relation", "position", "name", "definition", "flag"`, schema)
	if err != nil {
		t.Fatal("query generated PostgreSQL schema fingerprint")
	}
	digest := sha256.New()
	for rows.Next() {
		values := make([]string, 6)
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5]); err != nil {
			rows.Close()
			t.Fatal("scan generated PostgreSQL schema fingerprint")
		}
		for _, value := range values {
			_, _ = fmt.Fprintf(digest, "%d:%s", len(value), value)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal("iterate generated PostgreSQL schema fingerprint")
	}
	rows.Close()
	return hex.EncodeToString(digest.Sum(nil))
}

func actualE2EPostgresInsertCrossAppRows(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect generated PostgreSQL row seed")
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close generated PostgreSQL row seed")
		}
	}()
	authorTable := pgx.Identifier{schema, "authors_author"}.Sanitize()
	postTable := pgx.Identifier{schema, "blog_blog_post"}.Sanitize()
	var authorID int64
	if err := connection.QueryRow(ctx, `INSERT INTO `+authorTable+` ("name") VALUES ($1) RETURNING "id"`, "Ada").Scan(&authorID); err != nil || authorID <= 0 {
		t.Fatal("insert generated PostgreSQL Author")
	}
	var postID int64
	if err := connection.QueryRow(ctx, `INSERT INTO `+postTable+` ("title", "author_id") VALUES ($1, $2) RETURNING "id"`, "Published migration survives PostgreSQL restart", authorID).Scan(&postID); err != nil || postID <= 0 {
		t.Fatal("insert generated PostgreSQL BlogPost")
	}
}

func actualE2EPostgresAssertCrossAppRow(t *testing.T, databaseURL, schema string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatal("open generated PostgreSQL restart backend")
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close generated PostgreSQL restart backend")
		}
	}()
	rows, err := backend.Query(ctx, query.NewPlan(
		"blog_blog_post",
		[]query.FieldRef{
			query.NewFieldRef("id", "id", query.FieldInteger, false),
			query.NewFieldRef("title", "title", query.FieldString, false),
			query.NewFieldRef("author", "author_id", query.FieldInteger, false),
		},
	))
	if err != nil {
		t.Fatal("query generated PostgreSQL restart row")
	}
	if !rows.Next() {
		closeErr := rows.Close()
		t.Fatalf("generated PostgreSQL restart query has no row: iteration=%v close=%v", rows.Err(), closeErr)
	}
	var id, authorID int64
	var title string
	if err := rows.Scan(&id, &title, &authorID); err != nil {
		_ = rows.Close()
		t.Fatal("scan generated PostgreSQL restart row")
	}
	if id <= 0 || authorID <= 0 || title != "Published migration survives PostgreSQL restart" {
		_ = rows.Close()
		t.Fatalf("generated PostgreSQL restart row = (%d, %q, %d)", id, title, authorID)
	}
	if rows.Next() {
		_ = rows.Close()
		t.Fatal("generated PostgreSQL restart query returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal("iterate generated PostgreSQL restart row")
	}
	if err := rows.Close(); err != nil {
		t.Fatal("close generated PostgreSQL restart rows")
	}
}

func actualE2EPostgresRunRestartHelper(
	t *testing.T,
	fixture processFixture,
	databaseURL, schema string,
	sensitive []string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualGodjMakemigrationsPostgresRestartHelper$", "-test.count=1", "-test.v")
	command.Dir = fixture.project
	command.Env = fixture.environment(map[string]string{
		actualE2EPostgresRestartEnvironment: "1",
		actualE2EPostgresURLEnvironment:     databaseURL,
		actualE2EPostgresSchemaEnvironment:  schema,
	})
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	actualE2EPostgresAssertVisibleSecretFree(t, stdout.String(), stderr.String(), sensitive)
	if err != nil || ctx.Err() != nil {
		t.Fatalf("fresh-process PostgreSQL restart helper failed: stdout-bytes:%d stderr-bytes:%d", stdout.Len(), stderr.Len())
	}
}

func actualE2EPostgresAssertVisibleSecretFree(t *testing.T, stdout, stderr string, sensitive []string) {
	t.Helper()
	for _, secret := range sensitive {
		if secret != "" && (strings.Contains(stdout, secret) || strings.Contains(stderr, secret)) {
			t.Fatal("generated PostgreSQL command output exposed a sensitive value")
		}
	}
}

func actualE2EPostgresAssertDocumentSecretFree(t *testing.T, document []byte, sensitive []string) {
	t.Helper()
	for _, secret := range sensitive {
		if secret != "" && bytes.Contains(document, []byte(secret)) {
			t.Fatal("generated PostgreSQL definition exposed a sensitive value")
		}
	}
}

func actualE2EPostgresAssertArtifactTreeSecretFree(t *testing.T, root string, sensitive []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errors.New("inspect generated PostgreSQL artifact")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		document, err := os.ReadFile(path)
		if err != nil {
			return errors.New("read generated PostgreSQL artifact")
		}
		for _, secret := range sensitive {
			if secret != "" && bytes.Contains(document, []byte(secret)) {
				return errors.New("generated PostgreSQL artifact exposed a sensitive value")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func actualE2EPostgresAssertStoredTextSecretFree(t *testing.T, databaseURL, schema string, sensitive []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect generated PostgreSQL stored-secret inspection")
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close generated PostgreSQL stored-secret inspection")
		}
	}()
	rows, err := connection.Query(ctx, `
		SELECT "c"."relname", "a"."attname"
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid"
		JOIN "pg_catalog"."pg_type" AS "type" ON "type"."oid" = "a"."atttypid"
		WHERE "n"."nspname" = $1 AND "c"."relkind" = 'r'
		  AND "a"."attnum" > 0 AND NOT "a"."attisdropped"
		  AND "type"."typname" IN ('varchar', 'text')
		ORDER BY "c"."relname", "a"."attnum"`, schema)
	if err != nil {
		t.Fatal("enumerate generated PostgreSQL text fields")
	}
	type field struct{ table, column string }
	fields := make([]field, 0)
	for rows.Next() {
		var value field
		if err := rows.Scan(&value.table, &value.column); err != nil {
			rows.Close()
			t.Fatal("scan generated PostgreSQL text field")
		}
		fields = append(fields, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal("iterate generated PostgreSQL text fields")
	}
	rows.Close()
	for _, field := range fields {
		statement := "SELECT COALESCE(" + pgx.Identifier{field.column}.Sanitize() + ", '') FROM " + pgx.Identifier{schema, field.table}.Sanitize()
		values, err := connection.Query(ctx, statement)
		if err != nil {
			t.Fatal("query generated PostgreSQL text artifact")
		}
		for values.Next() {
			var value string
			if err := values.Scan(&value); err != nil {
				values.Close()
				t.Fatal("scan generated PostgreSQL text artifact")
			}
			for _, secret := range sensitive {
				if secret != "" && strings.Contains(value, secret) {
					values.Close()
					t.Fatal("generated PostgreSQL durable row exposed a sensitive value")
				}
			}
		}
		if err := values.Err(); err != nil {
			values.Close()
			t.Fatal("iterate generated PostgreSQL text artifact")
		}
		values.Close()
	}
}

func actualE2ESHA256(document []byte) string {
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:])
}
