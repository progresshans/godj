//go:build darwin || linux

package projectmigrateproduct_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/conformance/internal/dbstate"
	"github.com/progresshans/godj/conformance/internal/testfixture"
	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/gobuild"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/testenv"
)

const (
	projectMigratePostgresTestURLEnv  = "GODJ_TEST_POSTGRES_URL"
	projectMigratePostgresRequiredEnv = "GODJ_REQUIRE_POSTGRES"
)

var projectMigratePostgresSchemaSequence atomic.Uint64

func TestGlobalMigrateArticlePostgresProduct(t *testing.T) {
	databaseURL := projectMigratePostgresTestURL(t)
	sensitive := projectMigratePostgresSensitiveValues(t, databaseURL)
	repository := repositoryRoot(t)
	descriptor := filepath.Join(repository, "examples", "article", "godj.toml")
	globalBinary := projectMigratePostgresBuildGlobalGodj(t, repository)
	expected := expectedArticleCatalog(t, repository)

	t.Run("clean_latest_and_fresh_process_semantic_noop", func(t *testing.T) {
		schema := projectMigratePostgresCreateSchema(t, databaseURL)
		secrets := append(append([]string(nil), sensitive...), schema)
		workspaceBase := newWorkspaceBase(t)
		environment := projectMigratePostgresEnvironment(t, databaseURL, schema, workspaceBase)
		projectMigratePostgresAssertEnvironment(t, environment, databaseURL, schema)

		first := runMigrate(t, globalBinary, repository, descriptor, environment)
		projectMigratePostgresAssertVisibleSecretFree(t, first.Stdout, first.Stderr, secrets)
		assertMigrateSuccess(t, first, expected, secrets...)
		assertWorkspaceEmpty(t, workspaceBase)
		before := projectMigratePostgresInspect(t, databaseURL, schema)
		projectMigratePostgresAssertLatest(t, before, expected, nil)

		second := runMigrate(t, globalBinary, repository, descriptor, environment)
		projectMigratePostgresAssertVisibleSecretFree(t, second.Stdout, second.Stderr, secrets)
		assertMigrateSuccess(t, second, expected, secrets...)
		assertWorkspaceEmpty(t, workspaceBase)
		after := projectMigratePostgresInspect(t, databaseURL, schema)
		projectMigratePostgresAssertLatest(t, after, expected, nil)
		if !reflect.DeepEqual(after, before) {
			t.Fatal("second fresh-process PostgreSQL migrate changed the exact semantic database snapshot")
		}
		projectMigratePostgresAssertStoredValuesSecretFree(t, databaseURL, schema, secrets)
		projectMigratePostgresAssertArtifactsSecretFree(
			t,
			[]string{filepath.Dir(globalBinary), workspaceBase},
			secrets,
		)
	})

	t.Run("two_actual_global_children_exact_winner_and_closed_contention", func(t *testing.T) {
		schema := projectMigratePostgresCreateSchema(t, databaseURL)
		secrets := append(append([]string(nil), sensitive...), schema)
		contentionDescriptor := fullConcurrencyProject(t, repository)
		barrierDirectory := filepath.Join(t.TempDir(), "postgres-snapshot-barrier")
		if err := os.Mkdir(barrierDirectory, 0o700); err != nil {
			t.Fatal("create PostgreSQL migration barrier directory")
		}
		workspaceBase := newWorkspaceBase(t)
		values := testenv.Map(projectMigratePostgresEnvironment(t, databaseURL, schema, workspaceBase))
		values[fullConcurrencyBarrierEnv] = barrierDirectory
		environment := testenv.Sorted(values)
		projectMigratePostgresAssertEnvironment(t, environment, databaseURL, schema)

		start := make(chan struct{})
		completed := make(chan fullConcurrencyExecution, 2)
		for child := 0; child < 2; child++ {
			go func() {
				<-start
				result, err := executeBounded(globalBinary, repository, environment, "migrate", "--project", contentionDescriptor)
				completed <- fullConcurrencyExecution{result: result, err: err}
			}()
		}
		close(start)
		executions := []fullConcurrencyExecution{<-completed, <-completed}
		observed := make([]commandResult, len(executions))
		for index, execution := range executions {
			if execution.err != nil {
				t.Fatalf("concurrent PostgreSQL global migrate %d did not complete within the bounded process contract", index+1)
			}
			observed[index] = execution.result
			projectMigratePostgresAssertVisibleSecretFree(t, execution.result.Stdout, execution.result.Stderr, secrets)
		}
		assertWorkspaceEmpty(t, workspaceBase)

		markers := fullConcurrencyMarkers(t, barrierDirectory)
		if len(markers) != 2 {
			t.Fatalf("PostgreSQL snapshot barrier participant count = %d, want two", len(markers))
		}
		if markers[0].PID == markers[1].PID || markers[0].ParentPID == markers[1].ParentPID {
			t.Fatal("PostgreSQL snapshot barrier did not observe two distinct project children owned by two global commands")
		}
		for index, marker := range markers {
			if marker.PID <= 0 || marker.ParentPID <= 0 || marker.PID == marker.ParentPID || marker.Records != 0 {
				t.Fatal("PostgreSQL snapshot barrier did not bind a distinct child to the clean history snapshot")
			}
			beginCount := 1
			if index == 0 {
				beginCount = len(expected.History)
			}
			fullConcurrencyAssertSingleAttempt(t, barrierDirectory, marker.PID, beginCount)
			privateResponse := fullConcurrencyAssertPrivateWire(
				t,
				barrierDirectory,
				marker.PID,
				append(
					append([]string(nil), secrets...),
					barrierDirectory,
					contentionDescriptor,
					filepath.Dir(contentionDescriptor),
					workspaceBase,
				)...,
			)
			if index == 0 {
				want := migrateprotocol.Response{
					OK: true,
					Result: migrateprotocol.Result{
						Mode: migrateprotocol.ModeExecute,
						Execute: migrateprotocol.ExecuteResult{
							SourceCount:         expected.Command.SourceCount,
							DefinitionCount:     expected.Command.DefinitionCount,
							DefinitionSetDigest: expected.Command.DefinitionSetDigest,
						},
					},
				}
				if !reflect.DeepEqual(privateResponse, want) {
					t.Fatal("PostgreSQL winner private response did not match the exact successful migration result")
				}
				continue
			}
			want := migrateprotocol.Response{Failure: migrateprotocol.Failure{
				Category: migrateprotocol.CategoryTransaction,
				Code:     "history_revision_contended",
			}}
			if !reflect.DeepEqual(privateResponse, want) {
				t.Fatal("PostgreSQL contender private response did not match the exact closed revision contention")
			}
		}
		winnerDocument := fullConcurrencyCoordinationMarker(t, barrierDirectory, "winner-lock")
		contenderDocument := fullConcurrencyCoordinationMarker(t, barrierDirectory, "contender-observed")
		if winnerDocument != fmt.Sprintf("pid=%d\n", markers[0].PID) ||
			contenderDocument != fmt.Sprintf("pid=%d\nstatus=contended\n", markers[1].PID) {
			t.Fatal("PostgreSQL transaction barrier did not bind the lower-PID winner to an observed revision contention")
		}

		winners := 0
		fenced := 0
		for _, result := range observed {
			switch {
			case result.ExitCode == 0:
				assertMigrateSuccess(t, result, expected, secrets...)
				winners++
			case result.ExitCode == 3 && result.Stdout == "" &&
				result.Stderr == "migration_transaction_error/history_revision_contended\n" &&
				!result.StdoutTruncated && !result.StderrTruncated:
				fenced++
			default:
				t.Fatal("concurrent PostgreSQL migrate returned an outcome outside success or the closed revision-contention taxonomy")
			}
		}
		if winners != 1 || fenced != 1 {
			t.Fatalf("concurrent PostgreSQL migrate outcomes = winners:%d fenced:%d, want exactly one of each", winners, fenced)
		}
		converged := projectMigratePostgresInspect(t, databaseURL, schema)
		projectMigratePostgresAssertLatest(t, converged, expected, nil)

		reconciliationValues := testenv.Map(environment)
		delete(reconciliationValues, fullConcurrencyBarrierEnv)
		reconciliationEnvironment := testenv.Sorted(reconciliationValues)
		reconciled := runMigrate(t, globalBinary, repository, contentionDescriptor, reconciliationEnvironment)
		projectMigratePostgresAssertVisibleSecretFree(t, reconciled.Stdout, reconciled.Stderr, secrets)
		assertMigrateSuccess(t, reconciled, expected, secrets...)
		assertWorkspaceEmpty(t, workspaceBase)
		afterReconciliation := projectMigratePostgresInspect(t, databaseURL, schema)
		projectMigratePostgresAssertLatest(t, afterReconciliation, expected, nil)
		if !reflect.DeepEqual(afterReconciliation, converged) {
			t.Fatal("fresh PostgreSQL reconciliation changed the converged semantic database snapshot")
		}
		projectMigratePostgresAssertStoredValuesSecretFree(t, databaseURL, schema, secrets)
		projectMigratePostgresAssertArtifactsSecretFree(
			t,
			[]string{filepath.Dir(globalBinary), filepath.Dir(contentionDescriptor), barrierDirectory, workspaceBase},
			secrets,
		)
	})

	t.Run("migrate_then_distinct_global_runserver_restart_preserves_article", func(t *testing.T) {
		schema := projectMigratePostgresCreateSchema(t, databaseURL)
		secrets := append(append([]string(nil), sensitive...), schema)
		workspaceBase := newWorkspaceBase(t)
		environment := projectMigratePostgresEnvironment(t, databaseURL, schema, workspaceBase)
		projectMigratePostgresAssertEnvironment(t, environment, databaseURL, schema)

		migration := runMigrate(t, globalBinary, repository, descriptor, environment)
		projectMigratePostgresAssertVisibleSecretFree(t, migration.Stdout, migration.Stderr, secrets)
		assertMigrateSuccess(t, migration, expected, secrets...)
		assertWorkspaceEmpty(t, workspaceBase)
		const sentinel = "PostgreSQL explicit migrate restart sentinel"
		projectMigratePostgresInsertArticle(t, databaseURL, schema, sentinel)
		projectMigratePostgresAssertLatest(
			t,
			projectMigratePostgresInspect(t, databaseURL, schema),
			expected,
			[]projectMigratePostgresArticle{{ID: 1, Title: sentinel, Published: true}},
		)

		address := reserveLoopbackAddress(t, "")
		first := projectMigratePostgresRunServerOnce(t, globalBinary, repository, descriptor, address, environment, secrets)
		if first.Status != 200 || !strings.Contains(first.Body, sentinel) {
			t.Fatal("first global PostgreSQL runserver process did not read the durable Article row")
		}
		assertWorkspaceEmpty(t, workspaceBase)
		address = reserveLoopbackAddress(t, address)
		second := projectMigratePostgresRunServerOnce(t, globalBinary, repository, descriptor, address, environment, secrets)
		if second.Status != 200 || !strings.Contains(second.Body, sentinel) {
			t.Fatal("second global PostgreSQL runserver process did not read the durable Article row")
		}
		if first.PID <= 0 || second.PID <= 0 || first.PID == second.PID {
			t.Fatal("PostgreSQL restart proof did not use two distinct global runserver processes")
		}
		assertWorkspaceEmpty(t, workspaceBase)
		projectMigratePostgresAssertLatest(
			t,
			projectMigratePostgresInspect(t, databaseURL, schema),
			expected,
			[]projectMigratePostgresArticle{{ID: 1, Title: sentinel, Published: true}},
		)
		projectMigratePostgresAssertStoredValuesSecretFree(t, databaseURL, schema, secrets)
		projectMigratePostgresAssertArtifactsSecretFree(
			t,
			[]string{filepath.Dir(globalBinary), workspaceBase},
			secrets,
		)
	})
}

func projectMigratePostgresTestURL(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(projectMigratePostgresTestURLEnv))
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv(projectMigratePostgresRequiredEnv) == "1" {
		t.Fatalf("%s=1 requires %s", projectMigratePostgresRequiredEnv, projectMigratePostgresTestURLEnv)
	}
	t.Skip("GODJ_TEST_POSTGRES_URL is not configured; project migrate PostgreSQL product E2E was not run")
	return ""
}

func projectMigratePostgresSensitiveValues(t *testing.T, databaseURL string) []string {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse project migrate PostgreSQL URL: database URL is invalid")
	}
	values := []string{databaseURL}
	if len(config.Password) >= 4 {
		values = append(values, config.Password)
	}
	return values
}

func projectMigratePostgresBuildGlobalGodj(t *testing.T, repository string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "godj")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-mod=readonly", "-o", binary, "./cmd/godj")
	command.Dir = repository
	values := testenv.Map(testenv.OfflineGo(os.Environ(), ""))
	for _, key := range []string{
		projectMigratePostgresTestURLEnv,
		projectMigratePostgresRequiredEnv,
		articleSQLiteDatabaseEnv,
		articlePostgresURLEnv,
		articlePostgresSchemaEnv,
		articleAdminUsernameEnv,
		articleAdminPasswordEnv,
	} {
		delete(values, key)
	}
	command.Env = testenv.Sorted(values)
	var stdout, stderr gobuild.Capture
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("build global godj for PostgreSQL product: %v\n%s", err, gobuild.Summary(stdout.Bytes(), stderr.Bytes(), command.Env))
	}
	return binary
}

func projectMigratePostgresCreateSchema(t *testing.T, databaseURL string) string {
	t.Helper()
	sequence := projectMigratePostgresSchemaSequence.Add(1)
	schema := fmt.Sprintf("godj_pm_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), sequence)
	if !projectMigratePostgresValidSchema(schema) {
		t.Fatal("generated PostgreSQL product schema is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL schema owner: %v", testfixture.PostgresSafeError(err))
	}
	defer func() {
		if err := admin.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL schema owner: %v", testfixture.PostgresSafeError(err))
		}
	}()
	var before int
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM "pg_catalog"."pg_namespace" WHERE "nspname" = $1`, schema).Scan(&before); err != nil {
		t.Fatalf("inspect PostgreSQL schema uniqueness: %v", testfixture.PostgresSafeError(err))
	}
	if before != 0 {
		t.Fatal("generated PostgreSQL product schema was not unique")
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatalf("create isolated PostgreSQL product schema: %v", testfixture.PostgresSafeError(err))
	}
	var after int
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM "pg_catalog"."pg_namespace" WHERE "nspname" = $1`, schema).Scan(&after); err != nil {
		t.Fatalf("verify isolated PostgreSQL product schema: %v", testfixture.PostgresSafeError(err))
	}
	if after != 1 {
		t.Fatal("isolated PostgreSQL product schema was not created exactly once")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect PostgreSQL schema cleanup: %v", testfixture.PostgresSafeError(err))
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop isolated PostgreSQL product schema: %v", testfixture.PostgresSafeError(err))
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL schema cleanup: %v", testfixture.PostgresSafeError(err))
		}
	})
	return schema
}

func projectMigratePostgresValidSchema(schema string) bool {
	if len(schema) == 0 || len(schema) > 63 || schema[0] < 'a' || schema[0] > 'z' {
		return false
	}
	for _, character := range schema[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func projectMigratePostgresEnvironment(t *testing.T, databaseURL, schema, workspaceBase string) []string {
	t.Helper()
	values := testenv.Map(articleEnvironmentWithoutDatabase(t, workspaceBase))
	delete(values, projectMigratePostgresTestURLEnv)
	delete(values, projectMigratePostgresRequiredEnv)
	values[articlePostgresURLEnv] = databaseURL
	values[articlePostgresSchemaEnv] = schema
	return testenv.Sorted(values)
}

func projectMigratePostgresAssertEnvironment(t *testing.T, environment []string, databaseURL, schema string) {
	t.Helper()
	values := testenv.Map(environment)
	if _, exists := values[articleSQLiteDatabaseEnv]; exists {
		t.Fatal("PostgreSQL product environment retained SQLite configuration")
	}
	if _, exists := values[projectMigratePostgresTestURLEnv]; exists {
		t.Fatal("PostgreSQL product environment retained the test-only database URL")
	}
	if _, exists := values[projectMigratePostgresRequiredEnv]; exists {
		t.Fatal("PostgreSQL product environment retained the test-only required sentinel")
	}
	if values[articlePostgresURLEnv] != databaseURL || values[articlePostgresSchemaEnv] != schema {
		t.Fatal("PostgreSQL product environment did not retain exact project-owned database configuration")
	}
}

type projectMigratePostgresRevision struct {
	FormatVersion      int
	Epoch              [16]byte
	Revision           int64
	HistoryFingerprint [32]byte
	AdditionalRows     int
}

type projectMigratePostgresArticle struct {
	ID        int64
	Title     string
	Published bool
}

type projectMigratePostgresSnapshot struct {
	dbstate.PostgresCatalog
	History  []dbstate.HistoryRow
	Revision projectMigratePostgresRevision
	Counts   map[string]int64
	Articles []projectMigratePostgresArticle
}

func projectMigratePostgresInspect(t *testing.T, databaseURL, schema string) projectMigratePostgresSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL product inspection: %v", testfixture.PostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL product inspection: %v", testfixture.PostgresSafeError(err))
		}
	}()

	catalog, err := dbstate.CapturePostgresCatalog(ctx, connection, schema)
	if err != nil {
		t.Fatalf("inspect PostgreSQL product catalog: %v", testfixture.PostgresSafeError(err))
	}
	snapshot := projectMigratePostgresSnapshot{PostgresCatalog: catalog, Counts: make(map[string]int64)}
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	for _, table := range snapshot.Tables {
		quotedTable := pgx.Identifier{schema, table}.Sanitize()
		var count int64
		if err := connection.QueryRow(ctx, "SELECT COUNT(*) FROM "+quotedTable).Scan(&count); err != nil {
			t.Fatalf("count PostgreSQL product table rows: %v", testfixture.PostgresSafeError(err))
		}
		snapshot.Counts[table] = count
	}

	rows, err := connection.Query(ctx, `SELECT "app", "name" FROM `+quotedSchema+`."godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		t.Fatalf("query PostgreSQL migration history: %v", testfixture.PostgresSafeError(err))
	}
	for rows.Next() {
		var row dbstate.HistoryRow
		if err := rows.Scan(&row.App, &row.Name); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL migration history: %v", testfixture.PostgresSafeError(err))
		}
		snapshot.History = append(snapshot.History, row)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL migration history query: %v", testfixture.PostgresSafeError(err))
	}
	var epoch []byte
	var fingerprint []byte
	if err := connection.QueryRow(ctx, `SELECT "format_version", "epoch", "revision", "history_fingerprint" FROM `+quotedSchema+`."godj_migration_revision" WHERE "singleton" = 1`).Scan(
		&snapshot.Revision.FormatVersion,
		&epoch,
		&snapshot.Revision.Revision,
		&fingerprint,
	); err != nil {
		t.Fatalf("query PostgreSQL migration revision: %v", testfixture.PostgresSafeError(err))
	}
	if len(epoch) != len(snapshot.Revision.Epoch) || len(fingerprint) != len(snapshot.Revision.HistoryFingerprint) {
		t.Fatal("PostgreSQL migration revision contained an invalid bounded digest or epoch")
	}
	copy(snapshot.Revision.Epoch[:], epoch)
	copy(snapshot.Revision.HistoryFingerprint[:], fingerprint)
	if err := connection.QueryRow(ctx, `SELECT COUNT(*) - 1 FROM `+quotedSchema+`."godj_migration_revision"`).Scan(&snapshot.Revision.AdditionalRows); err != nil {
		t.Fatalf("count PostgreSQL migration revision rows: %v", testfixture.PostgresSafeError(err))
	}

	rows, err = connection.Query(ctx, `SELECT "id", "title", "published" FROM `+quotedSchema+`."godj_conformance_article" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("query PostgreSQL Article rows: %v", testfixture.PostgresSafeError(err))
	}
	for rows.Next() {
		var article projectMigratePostgresArticle
		if err := rows.Scan(&article.ID, &article.Title, &article.Published); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL Article row: %v", testfixture.PostgresSafeError(err))
		}
		snapshot.Articles = append(snapshot.Articles, article)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL Article row query: %v", testfixture.PostgresSafeError(err))
	}
	return snapshot
}

func projectMigratePostgresAssertLatest(
	t *testing.T,
	snapshot projectMigratePostgresSnapshot,
	expected articleCatalogExpectation,
	articles []projectMigratePostgresArticle,
) {
	t.Helper()
	wantTables := []string{
		"godj_conformance_article",
		"godj_identity_group",
		"godj_identity_group_permissions",
		"godj_identity_permission",
		"godj_identity_user",
		"godj_identity_user_groups",
		"godj_identity_user_permissions",
		"godj_migration_revision",
		"godj_migrations",
		"godj_system_audit",
		"godj_system_credential",
		"godj_system_identity_transition",
		"godj_system_session",
	}
	if !reflect.DeepEqual(snapshot.Tables, wantTables) {
		t.Fatalf("PostgreSQL latest table names = %v, want exact current tables", snapshot.Tables)
	}
	if len(snapshot.OtherRelations) != 0 {
		t.Fatalf("PostgreSQL latest schema contained %d unexpected relation objects", len(snapshot.OtherRelations))
	}
	if !reflect.DeepEqual(snapshot.Columns, projectMigratePostgresExpectedColumns()) {
		t.Fatal("PostgreSQL latest column shape did not match the exact current Article/system/control schema")
	}
	if !reflect.DeepEqual(snapshot.Constraints, projectMigratePostgresExpectedConstraints()) {
		t.Fatal("PostgreSQL latest constraints did not match the exact current primary-key profile")
	}
	if !reflect.DeepEqual(snapshot.Indexes, projectMigratePostgresExpectedIndexes()) {
		t.Fatal("PostgreSQL latest indexes did not match the exact current primary-key profile")
	}
	if snapshot.Triggers != 24 || snapshot.Policies != 0 || snapshot.Rules != 0 {
		t.Fatalf(
			"PostgreSQL latest schema trigger/policy/rule counts = %d/%d/%d, want 24/0/0",
			snapshot.Triggers,
			snapshot.Policies,
			snapshot.Rules,
		)
	}
	if !reflect.DeepEqual(snapshot.History, expected.History) {
		t.Fatalf("PostgreSQL latest history = %+v, want %+v", snapshot.History, expected.History)
	}
	if snapshot.Revision.FormatVersion != 1 || snapshot.Revision.Epoch == ([16]byte{}) ||
		snapshot.Revision.Revision != int64(len(expected.History)) ||
		snapshot.Revision.HistoryFingerprint != expected.HistoryFingerprint ||
		snapshot.Revision.AdditionalRows != 0 {
		t.Fatal("PostgreSQL latest revision did not match exact version/epoch/revision/fingerprint/cardinality")
	}
	wantCounts := map[string]int64{
		"godj_conformance_article":        int64(len(articles)),
		"godj_migration_revision":         1,
		"godj_migrations":                 int64(len(expected.History)),
		"godj_system_audit":               0,
		"godj_system_credential":          0,
		"godj_system_session":             0,
		"godj_identity_group":             0,
		"godj_identity_group_permissions": 0,
		"godj_identity_permission":        0,
		"godj_identity_user":              0,
		"godj_identity_user_groups":       0,
		"godj_identity_user_permissions":  0,
		"godj_system_identity_transition": 0,
	}
	if !reflect.DeepEqual(snapshot.Counts, wantCounts) {
		t.Fatal("PostgreSQL latest row cardinalities did not match the exact clean product state")
	}
	if !reflect.DeepEqual(snapshot.Articles, articles) {
		t.Fatal("PostgreSQL Article rows did not match the exact durable fixture")
	}
	if len(snapshot.Sequences) != 11 {
		t.Fatalf("PostgreSQL identity sequence count = %d, want 11", len(snapshot.Sequences))
	}
	wantSequences := map[string]struct {
		table  string
		column string
	}{
		"godj_seq_252f7fa19100868ad219e09d43f1ec7976b9da7ea41187b1": {table: "godj_system_credential", column: "id"},
		"godj_seq_40de8cd32f6d0448e55ebd389d6d16ec2199fe8a37f5e361": {table: "godj_conformance_article", column: "id"},
		"godj_seq_a20c4fe52a0de9485bb4f12211be3f9484ead09e11cb9cab": {table: "godj_system_audit", column: "id"},
		"godj_seq_bfd1f1eb7fae8e25fff75cf1565851fcccf210b6a24fdb85": {table: "godj_system_session", column: "id"},
	}
	wantSequences["godj_seq_98b4860bbba06f67d7b73e596a56cd6488d499703124f33d"] = struct{ table, column string }{"godj_identity_group", "id"}
	wantSequences["godj_seq_2804c4a5b9bf99a71b7cbc8bf03876215a9a64588b3e9230"] = struct{ table, column string }{"godj_identity_group_permissions", "id"}
	wantSequences["godj_seq_723e2845dfaac5384e66530fa865a09c98e4e444a37d3dbf"] = struct{ table, column string }{"godj_identity_permission", "id"}
	wantSequences["godj_seq_154922bf1e45ea63050edbdb153bf88c18b985b4958a4eab"] = struct{ table, column string }{"godj_identity_user", "id"}
	wantSequences["godj_seq_ae2450e02bb215e140cf9e7a87dd57e1001ae602e88ed739"] = struct{ table, column string }{"godj_identity_user_groups", "id"}
	wantSequences["godj_seq_91de1d24fc1db736bd9e42f4478547ae544d60ee06eb479e"] = struct{ table, column string }{"godj_identity_user_permissions", "id"}
	wantSequences["godj_seq_eb0478a786733dafcd0605d53354cb607524c30ed06affdb"] = struct{ table, column string }{"godj_system_identity_transition", "id"}
	seen := make(map[string]struct{}, len(snapshot.Sequences))
	for _, sequence := range snapshot.Sequences {
		owner, exists := wantSequences[sequence.Name]
		if !exists || sequence.Kind != "S" || sequence.Persistence != "p" || sequence.DataType != "bigint" ||
			sequence.Start != 1 || sequence.Increment != 1 || sequence.Minimum != 1 ||
			sequence.Maximum != int64(^uint64(0)>>1) || sequence.Cache != 1 || sequence.Cycle ||
			sequence.OwnerTable != owner.table || sequence.OwnerColumn != owner.column || sequence.Dependency != "i" {
			t.Fatal("PostgreSQL identity sequence profile did not match the current AutoField contract")
		}
		if _, exists := seen[sequence.Name]; exists {
			t.Fatal("PostgreSQL identity sequence names were not unique")
		}
		seen[sequence.Name] = struct{}{}
		wantLast := int64(1)
		wantCalled := false
		if owner.table == "godj_conformance_article" && len(articles) > 0 {
			wantLast = articles[len(articles)-1].ID
			wantCalled = true
		}
		if sequence.Last != wantLast || sequence.Called != wantCalled {
			t.Fatal("PostgreSQL identity sequence state did not match the exact durable row profile")
		}
	}
}

func projectMigratePostgresExpectedConstraints() []dbstate.PostgresConstraint {
	primary := func(table, name, key string) dbstate.PostgresConstraint {
		return dbstate.PostgresConstraint{
			Table:     table,
			Name:      name,
			Kind:      "p",
			Validated: true,
			Key:       key,
			IndexName: name,
		}
	}
	out := []dbstate.PostgresConstraint{
		{Table: "godj_identity_group", Name: "godj_pk_3e82b0051a7b46d4c048cc3b39646a339afb6cad0eb810fc", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_3e82b0051a7b46d4c048cc3b39646a339afb6cad0eb810fc"},
		{Table: "godj_identity_group", Name: "godj_uq_eee573325ab096e9d2cc0c7cc7f403194f15652afe0589e4", Kind: "u", Validated: true, Key: "2", IndexName: "godj_uq_eee573325ab096e9d2cc0c7cc7f403194f15652afe0589e4"},
		{Table: "godj_identity_group_permissions", Name: "godj_fk_4d941cdcb1482febe5876a931ec55f074ae5f49d21fb135b", Kind: "f", Deferrable: true, Deferred: true, Validated: true, Key: "3", IndexName: "godj_pk_81b5075da6e298b86fd86bf47e14d3a90d26545657d27593", InternalTriggers: 4},
		{Table: "godj_identity_group_permissions", Name: "godj_fk_d70af36ba7dae00ee9eb3abf2b7ec08941de2ab65077fdda", Kind: "f", Deferrable: true, Deferred: true, Validated: true, Key: "2", IndexName: "godj_pk_3e82b0051a7b46d4c048cc3b39646a339afb6cad0eb810fc", InternalTriggers: 4},
		{Table: "godj_identity_group_permissions", Name: "godj_pk_bdf464a175800635e3e03d60480045e3cf1eedd17d325e37", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_bdf464a175800635e3e03d60480045e3cf1eedd17d325e37"},
		{Table: "godj_identity_group_permissions", Name: "godj_uq_1a45e74f191e6b959df415a2ab0a2ba9cf6e959f39e51d02", Kind: "u", Validated: true, Key: "2,3", IndexName: "godj_uq_1a45e74f191e6b959df415a2ab0a2ba9cf6e959f39e51d02"},
		{Table: "godj_identity_permission", Name: "godj_pk_81b5075da6e298b86fd86bf47e14d3a90d26545657d27593", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_81b5075da6e298b86fd86bf47e14d3a90d26545657d27593"},
		{Table: "godj_identity_permission", Name: "godj_uq_c9f7b6a61f55b30c597c01d676b9b257a58b68431acf86d2", Kind: "u", Validated: true, Key: "2", IndexName: "godj_uq_c9f7b6a61f55b30c597c01d676b9b257a58b68431acf86d2"},
		{Table: "godj_identity_user", Name: "godj_pk_aff9f46bc5dbf69b334f798b63c3477fd0439da48c684991", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_aff9f46bc5dbf69b334f798b63c3477fd0439da48c684991"},
		{Table: "godj_identity_user", Name: "godj_uq_bb907ef5853d711e0ee9fc7cdceab0de36414bef36e9c35f", Kind: "u", Validated: true, Key: "3", IndexName: "godj_uq_bb907ef5853d711e0ee9fc7cdceab0de36414bef36e9c35f"},
		{Table: "godj_identity_user", Name: "godj_uq_f75cc270adc6cafbfbe847705d487fbb5e8e605a6b66e768", Kind: "u", Validated: true, Key: "2", IndexName: "godj_uq_f75cc270adc6cafbfbe847705d487fbb5e8e605a6b66e768"},
		{Table: "godj_identity_user_groups", Name: "godj_fk_67dc0ce8c89494041045de2b45d972ddf4e0a3678a6bb8ff", Kind: "f", Deferrable: true, Deferred: true, Validated: true, Key: "2", IndexName: "godj_pk_aff9f46bc5dbf69b334f798b63c3477fd0439da48c684991", InternalTriggers: 4},
		{Table: "godj_identity_user_groups", Name: "godj_fk_6d100aa09f460b843e916592587b18c260df4938e55a5de7", Kind: "f", Deferrable: true, Deferred: true, Validated: true, Key: "3", IndexName: "godj_pk_3e82b0051a7b46d4c048cc3b39646a339afb6cad0eb810fc", InternalTriggers: 4},
		{Table: "godj_identity_user_groups", Name: "godj_pk_58d91427221c3c506841414072a9d8456b588ee4d9b6e8c7", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_58d91427221c3c506841414072a9d8456b588ee4d9b6e8c7"},
		{Table: "godj_identity_user_groups", Name: "godj_uq_062b9496e0b725625b100ab5312415e3e90e00429b5a7c02", Kind: "u", Validated: true, Key: "2,3", IndexName: "godj_uq_062b9496e0b725625b100ab5312415e3e90e00429b5a7c02"},
		{Table: "godj_identity_user_permissions", Name: "godj_fk_0743d6e96a08ff03562534df244049db084457146f054cfa", Kind: "f", Deferrable: true, Deferred: true, Validated: true, Key: "3", IndexName: "godj_pk_81b5075da6e298b86fd86bf47e14d3a90d26545657d27593", InternalTriggers: 4},
		{Table: "godj_identity_user_permissions", Name: "godj_fk_cd6198f362d871e2df8665240045fb305835145a8c69891c", Kind: "f", Deferrable: true, Deferred: true, Validated: true, Key: "2", IndexName: "godj_pk_aff9f46bc5dbf69b334f798b63c3477fd0439da48c684991", InternalTriggers: 4},
		{Table: "godj_identity_user_permissions", Name: "godj_pk_55af4ef59ff2228405b16be81eede352b7ca2555d4196a67", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_55af4ef59ff2228405b16be81eede352b7ca2555d4196a67"},
		{Table: "godj_identity_user_permissions", Name: "godj_uq_10a96914af7dd470ff35b3113f8853ffb7e6c3fb4791753c", Kind: "u", Validated: true, Key: "2,3", IndexName: "godj_uq_10a96914af7dd470ff35b3113f8853ffb7e6c3fb4791753c"},
		{Table: "godj_system_identity_transition", Name: "godj_pk_471ed8b53f7cae308deb114e52995e450a66abfff8a60fd4", Kind: "p", Validated: true, Key: "1", IndexName: "godj_pk_471ed8b53f7cae308deb114e52995e450a66abfff8a60fd4"},
		{Table: "godj_system_identity_transition", Name: "godj_uq_17ffc31535e69584638fa8420dbb87407688914afe7f715a", Kind: "u", Validated: true, Key: "5", IndexName: "godj_uq_17ffc31535e69584638fa8420dbb87407688914afe7f715a"},
		{Table: "godj_system_identity_transition", Name: "godj_uq_b35f9446067a60fae29e388346e3b726790741a177b4abf7", Kind: "u", Validated: true, Key: "4", IndexName: "godj_uq_b35f9446067a60fae29e388346e3b726790741a177b4abf7"},
		primary("godj_conformance_article", "godj_pk_7f21c7e928b78be2fc532391565427000c1cf0627bdaf24a", "1"),
		primary("godj_migration_revision", "godj_migration_revision_pkey", "1"),
		primary("godj_migrations", "godj_migrations_pkey", "1,2"),
		primary("godj_system_audit", "godj_pk_d62f2a2b334710f31d7485fc8902dea702de6ecb9dad9f02", "1"),
		primary("godj_system_credential", "godj_pk_7aad345323cce9490877362ad8c707fbd02a643680270e40", "1"),
		primary("godj_system_session", "godj_pk_83d7214ff051fc5f24c0f9870b69166345f11de9f42188dc", "1"),
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func projectMigratePostgresExpectedIndexes() []dbstate.PostgresIndex {
	primary := func(table, name, keys string, count int) dbstate.PostgresIndex {
		return dbstate.PostgresIndex{
			Table:          table,
			Name:           name,
			Primary:        true,
			Unique:         true,
			Valid:          true,
			Ready:          true,
			Live:           true,
			KeyCount:       count,
			AttributeCount: count,
			Keys:           keys,
			Method:         "btree",
		}
	}
	out := []dbstate.PostgresIndex{
		{Table: "godj_identity_group", Name: "godj_pk_3e82b0051a7b46d4c048cc3b39646a339afb6cad0eb810fc", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_identity_group", Name: "godj_uq_eee573325ab096e9d2cc0c7cc7f403194f15652afe0589e4", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "name", Method: "btree"},
		{Table: "godj_identity_group_permissions", Name: "godj_pk_bdf464a175800635e3e03d60480045e3cf1eedd17d325e37", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_identity_group_permissions", Name: "godj_uq_1a45e74f191e6b959df415a2ab0a2ba9cf6e959f39e51d02", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 2, AttributeCount: 2, Keys: "source_id,target_id", Method: "btree"},
		{Table: "godj_identity_permission", Name: "godj_pk_81b5075da6e298b86fd86bf47e14d3a90d26545657d27593", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_identity_permission", Name: "godj_uq_c9f7b6a61f55b30c597c01d676b9b257a58b68431acf86d2", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "code", Method: "btree"},
		{Table: "godj_identity_user", Name: "godj_pk_aff9f46bc5dbf69b334f798b63c3477fd0439da48c684991", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_identity_user", Name: "godj_uq_bb907ef5853d711e0ee9fc7cdceab0de36414bef36e9c35f", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "username", Method: "btree"},
		{Table: "godj_identity_user", Name: "godj_uq_f75cc270adc6cafbfbe847705d487fbb5e8e605a6b66e768", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "principal_id", Method: "btree"},
		{Table: "godj_identity_user_groups", Name: "godj_pk_58d91427221c3c506841414072a9d8456b588ee4d9b6e8c7", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_identity_user_groups", Name: "godj_uq_062b9496e0b725625b100ab5312415e3e90e00429b5a7c02", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 2, AttributeCount: 2, Keys: "source_id,target_id", Method: "btree"},
		{Table: "godj_identity_user_permissions", Name: "godj_pk_55af4ef59ff2228405b16be81eede352b7ca2555d4196a67", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_identity_user_permissions", Name: "godj_uq_10a96914af7dd470ff35b3113f8853ffb7e6c3fb4791753c", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 2, AttributeCount: 2, Keys: "source_id,target_id", Method: "btree"},
		{Table: "godj_system_identity_transition", Name: "godj_pk_471ed8b53f7cae308deb114e52995e450a66abfff8a60fd4", Primary: true, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "id", Method: "btree"},
		{Table: "godj_system_identity_transition", Name: "godj_uq_17ffc31535e69584638fa8420dbb87407688914afe7f715a", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "user_id", Method: "btree"},
		{Table: "godj_system_identity_transition", Name: "godj_uq_b35f9446067a60fae29e388346e3b726790741a177b4abf7", Primary: false, Unique: true, Valid: true, Ready: true, Live: true, KeyCount: 1, AttributeCount: 1, Keys: "principal_id", Method: "btree"},
		primary("godj_conformance_article", "godj_pk_7f21c7e928b78be2fc532391565427000c1cf0627bdaf24a", "id", 1),
		primary("godj_migration_revision", "godj_migration_revision_pkey", "singleton", 1),
		primary("godj_migrations", "godj_migrations_pkey", "app,name", 2),
		primary("godj_system_audit", "godj_pk_d62f2a2b334710f31d7485fc8902dea702de6ecb9dad9f02", "id", 1),
		primary("godj_system_credential", "godj_pk_7aad345323cce9490877362ad8c707fbd02a643680270e40", "id", 1),
		primary("godj_system_session", "godj_pk_83d7214ff051fc5f24c0f9870b69166345f11de9f42188dc", "id", 1),
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func projectMigratePostgresExpectedColumns() []dbstate.PostgresColumn {
	column := func(table string, ordinal int, name, fieldType string, notNull bool, identity string, primary bool) dbstate.PostgresColumn {
		return dbstate.PostgresColumn{
			Table:            table,
			Ordinal:          ordinal,
			Name:             name,
			DataType:         fieldType,
			NotNull:          notNull,
			Identity:         identity,
			DefaultCollation: true,
			Primary:          primary,
		}
	}
	out := []dbstate.PostgresColumn{
		column("godj_identity_group", 1, "id", "bigint", true, "d", true),
		column("godj_identity_group", 2, "name", "character varying(150)", true, "", false),
		column("godj_identity_group", 3, "revision", "bigint", true, "", false),
		column("godj_identity_group_permissions", 1, "id", "bigint", true, "d", true),
		column("godj_identity_group_permissions", 2, "source_id", "bigint", true, "", false),
		column("godj_identity_group_permissions", 3, "target_id", "bigint", true, "", false),
		column("godj_identity_permission", 1, "id", "bigint", true, "d", true),
		column("godj_identity_permission", 2, "code", "character varying(128)", true, "", false),
		column("godj_identity_permission", 3, "name", "character varying(255)", true, "", false),
		column("godj_identity_permission", 4, "revision", "bigint", true, "", false),
		column("godj_identity_user", 1, "id", "bigint", true, "d", true),
		column("godj_identity_user", 2, "principal_id", "character varying(128)", true, "", false),
		column("godj_identity_user", 3, "username", "character varying(256)", true, "", false),
		column("godj_identity_user", 4, "encoded_password", "character varying(2048)", true, "", false),
		column("godj_identity_user", 5, "first_name", "character varying(150)", true, "", false),
		column("godj_identity_user", 6, "last_name", "character varying(150)", true, "", false),
		column("godj_identity_user", 7, "email", "character varying(254)", true, "", false),
		column("godj_identity_user", 8, "active", "boolean", true, "", false),
		column("godj_identity_user", 9, "staff", "boolean", true, "", false),
		column("godj_identity_user", 10, "superuser", "boolean", true, "", false),
		column("godj_identity_user", 11, "date_joined", "timestamp with time zone", true, "", false),
		column("godj_identity_user", 12, "last_login", "timestamp with time zone", false, "", false),
		column("godj_identity_user", 13, "revision", "bigint", true, "", false),
		column("godj_identity_user_groups", 1, "id", "bigint", true, "d", true),
		column("godj_identity_user_groups", 2, "source_id", "bigint", true, "", false),
		column("godj_identity_user_groups", 3, "target_id", "bigint", true, "", false),
		column("godj_identity_user_permissions", 1, "id", "bigint", true, "d", true),
		column("godj_identity_user_permissions", 2, "source_id", "bigint", true, "", false),
		column("godj_identity_user_permissions", 3, "target_id", "bigint", true, "", false),
		column("godj_system_identity_transition", 1, "id", "bigint", true, "d", true),
		column("godj_system_identity_transition", 2, "source_kind", "character varying(16)", true, "", false),
		column("godj_system_identity_transition", 3, "source_id", "bigint", true, "", false),
		column("godj_system_identity_transition", 4, "principal_id", "character varying(128)", true, "", false),
		column("godj_system_identity_transition", 5, "user_id", "bigint", true, "", false),
		column("godj_system_identity_transition", 6, "staff", "boolean", true, "", false),
		column("godj_system_identity_transition", 7, "superuser", "boolean", true, "", false),
		column("godj_system_identity_transition", 8, "transitioned_at", "timestamp with time zone", true, "", false),
		column("godj_system_identity_transition", 9, "source_fingerprint", "character varying(64)", true, "", false),
		column("godj_conformance_article", 1, "id", "bigint", true, "d", true),
		column("godj_conformance_article", 2, "title", "character varying(200)", true, "", false),
		column("godj_conformance_article", 3, "published", "boolean", true, "", false),
		column("godj_conformance_article", 4, "summary", "character varying(200)", false, "", false),
		column("godj_migration_revision", 1, "singleton", "smallint", true, "", true),
		column("godj_migration_revision", 2, "format_version", "integer", true, "", false),
		column("godj_migration_revision", 3, "epoch", "bytea", true, "", false),
		column("godj_migration_revision", 4, "revision", "bigint", true, "", false),
		column("godj_migration_revision", 5, "history_fingerprint", "bytea", true, "", false),
		column("godj_migrations", 1, "app", "character varying(255)", true, "", true),
		column("godj_migrations", 2, "name", "character varying(255)", true, "", true),
		column("godj_system_audit", 1, "id", "bigint", true, "d", true),
		column("godj_system_audit", 2, "actor_id", "character varying(128)", true, "", false),
		column("godj_system_audit", 3, "model", "character varying(128)", true, "", false),
		column("godj_system_audit", 4, "object_id", "character varying(64)", true, "", false),
		column("godj_system_audit", 5, "action", "character varying(16)", true, "", false),
		column("godj_system_audit", 6, "changed_fields", "character varying(32768)", true, "", false),
		column("godj_system_audit", 7, "display_label", "character varying(1024)", true, "", false),
		column("godj_system_credential", 1, "id", "bigint", true, "d", true),
		column("godj_system_credential", 2, "principal_id", "character varying(128)", true, "", false),
		column("godj_system_credential", 3, "username", "character varying(256)", true, "", false),
		column("godj_system_credential", 4, "encoded_password", "character varying(2048)", true, "", false),
		column("godj_system_credential", 5, "active", "boolean", true, "", false),
		column("godj_system_credential", 6, "permissions", "character varying(65536)", true, "", false),
		column("godj_system_credential", 7, "definition_digest", "character varying(71)", true, "", false),
		column("godj_system_session", 1, "id", "bigint", true, "d", true),
		column("godj_system_session", 2, "digest", "character varying(64)", true, "", false),
		column("godj_system_session", 3, "payload", "character varying(32768)", true, "", false),
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table != out[j].Table {
			return out[i].Table < out[j].Table
		}
		return out[i].Ordinal < out[j].Ordinal
	})
	return out
}

func projectMigratePostgresInsertArticle(t *testing.T, databaseURL, schema, title string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL Article seed: %v", testfixture.PostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL Article seed: %v", testfixture.PostgresSafeError(err))
		}
	}()
	var id int64
	statement := `INSERT INTO ` + pgx.Identifier{schema, "godj_conformance_article"}.Sanitize() + ` ("title", "published", "summary") VALUES ($1, TRUE, NULL) RETURNING "id"`
	if err := connection.QueryRow(ctx, statement, title).Scan(&id); err != nil {
		t.Fatalf("insert PostgreSQL Article sentinel: %v", testfixture.PostgresSafeError(err))
	}
	if id != 1 {
		t.Fatalf("inserted PostgreSQL Article ID = %d, want 1", id)
	}
}

type projectMigratePostgresServerObservation struct {
	PID    int
	Status int
	Body   string
}

func projectMigratePostgresRunServerOnce(
	t *testing.T,
	globalBinary, repository, descriptor, expectedAddress string,
	environment []string,
	sensitive []string,
) projectMigratePostgresServerObservation {
	t.Helper()
	stdout := testprocess.NewReadinessBuffer(maximumCommandOutput, articleReadinessPrefix)
	stderr := testprocess.NewBuffer(maximumCommandOutput)
	command := exec.Command(globalBinary, "runserver", "--project", descriptor, "--addr", expectedAddress)
	command.Dir = repository
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	for _, argument := range command.Args {
		for _, secret := range sensitive {
			if secret != "" && strings.Contains(argument, secret) {
				t.Fatal("global PostgreSQL runserver placed a secret in process arguments")
			}
		}
	}
	if err := command.Start(); err != nil {
		t.Fatal("start global PostgreSQL runserver failed")
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	finished := false
	var knownGroups []int
	defer func() {
		if !finished {
			_ = interruptAndWait(command, waited, 20*time.Second, knownGroups...)
		}
	}()

	var address string
	timer := time.NewTimer(commandTimeout)
	defer timer.Stop()
	select {
	case address = <-stdout.Ready():
		projectMigratePostgresAssertVisibleSecretFree(t, stdout.String(), stderr.String(), sensitive)
		if address != expectedAddress {
			t.Fatal("PostgreSQL Article readiness address did not match the reserved loopback address")
		}
		groups, err := testprocess.OwnedGroups(command.Process.Pid)
		if err != nil || len(groups) < 2 {
			t.Fatal("capture global/PostgreSQL runtime process ownership failed")
		}
		knownGroups = groups
	case <-waited:
		finished = true
		projectMigratePostgresAssertVisibleSecretFree(t, stdout.String(), stderr.String(), sensitive)
		t.Fatalf("global PostgreSQL runserver exited before readiness; stdout_bytes=%d stderr_bytes=%d", len(stdout.String()), len(stderr.String()))
	case <-timer.C:
		cleanup := interruptAndWait(command, waited, 20*time.Second, knownGroups...)
		finished = true
		projectMigratePostgresAssertVisibleSecretFree(t, stdout.String(), stderr.String(), sensitive)
		t.Fatalf("global PostgreSQL runserver readiness timed out; forced=%t process_groups=%d stdout_bytes=%d stderr_bytes=%d", cleanup.Forced, len(cleanup.ProcessGroups), len(stdout.String()), len(stderr.String()))
	}

	status, body, requestErr := requestArticlePage(address)
	projectMigratePostgresAssertVisibleSecretFree(t, stdout.String()+body, stderr.String(), sensitive)
	cleanup := interruptAndWait(command, waited, 20*time.Second, knownGroups...)
	finished = true
	if requestErr != nil {
		t.Fatalf("request PostgreSQL Article page failed; forced=%t process_groups=%d", cleanup.Forced, len(cleanup.ProcessGroups))
	}
	if cleanup.failed() || len(cleanup.ProcessGroups) < 2 {
		t.Fatalf("clean global PostgreSQL runserver interrupt failed; forced=%t process_groups=%d", cleanup.Forced, len(cleanup.ProcessGroups))
	}
	projectMigratePostgresAssertVisibleSecretFree(t, stdout.String()+body, stderr.String(), sensitive)
	if stderr.Truncated() || stderr.String() != "" {
		t.Fatalf("global PostgreSQL runserver stderr bytes = %d, want 0", len(stderr.String()))
	}
	wantReadiness := articleReadinessPrefix + expectedAddress + "\n"
	if stdout.Truncated() || stdout.String() != wantReadiness {
		t.Fatalf("global PostgreSQL runserver stdout bytes = %d, want exact readiness bytes = %d", len(stdout.String()), len(wantReadiness))
	}
	return projectMigratePostgresServerObservation{PID: command.Process.Pid, Status: status, Body: body}
}

func projectMigratePostgresAssertVisibleSecretFree(t *testing.T, stdout, stderr string, sensitive []string) {
	t.Helper()
	for _, value := range sensitive {
		if value == "" {
			continue
		}
		if strings.Contains(stdout, value) || strings.Contains(stderr, value) {
			t.Fatal("PostgreSQL product stdout, stderr, or response wire exposed a sensitive value")
		}
	}
}

func projectMigratePostgresAssertStoredValuesSecretFree(t *testing.T, databaseURL, schema string, sensitive []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL artifact inspection: %v", testfixture.PostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL artifact inspection: %v", testfixture.PostgresSafeError(err))
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
		t.Fatalf("enumerate PostgreSQL text artifacts: %v", testfixture.PostgresSafeError(err))
	}
	type field struct{ table, column string }
	var fields []field
	for rows.Next() {
		var value field
		if err := rows.Scan(&value.table, &value.column); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL text artifact field: %v", testfixture.PostgresSafeError(err))
		}
		fields = append(fields, value)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL text artifact enumeration: %v", testfixture.PostgresSafeError(err))
	}
	for _, field := range fields {
		statement := "SELECT COALESCE(" + pgx.Identifier{field.column}.Sanitize() + ", '') FROM " + pgx.Identifier{schema, field.table}.Sanitize()
		values, err := connection.Query(ctx, statement)
		if err != nil {
			t.Fatalf("query PostgreSQL text artifact: %v", testfixture.PostgresSafeError(err))
		}
		for values.Next() {
			var value string
			if err := values.Scan(&value); err != nil {
				values.Close()
				t.Fatalf("scan PostgreSQL text artifact: %v", testfixture.PostgresSafeError(err))
			}
			for _, secret := range sensitive {
				if secret != "" && strings.Contains(value, secret) {
					values.Close()
					t.Fatal("PostgreSQL durable row artifact exposed a database credential")
				}
			}
		}
		if err := projectMigratePostgresCloseRows(values); err != nil {
			t.Fatalf("finish PostgreSQL text artifact query: %v", testfixture.PostgresSafeError(err))
		}
	}
}

func projectMigratePostgresAssertArtifactsSecretFree(t *testing.T, roots, sensitive []string) {
	t.Helper()
	unique := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if _, exists := unique[root]; exists {
			continue
		}
		unique[root] = struct{}{}
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return errors.New("inspect PostgreSQL product artifact")
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			document, err := os.ReadFile(path)
			if err != nil {
				return errors.New("read PostgreSQL product artifact")
			}
			for _, secret := range sensitive {
				if secret != "" && bytes.Contains(document, []byte(secret)) {
					return errors.New("PostgreSQL product temp artifact exposed a sensitive value")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func projectMigratePostgresCloseRows(rows pgx.Rows) error {
	if rows == nil {
		return errors.New("PostgreSQL query returned nil rows")
	}
	rows.Close()
	return rows.Err()
}
