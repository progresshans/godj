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
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
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
		values := environmentMap(projectMigratePostgresEnvironment(t, databaseURL, schema, workspaceBase))
		values[fullConcurrencyBarrierEnv] = barrierDirectory
		environment := sortedEnvironment(values)
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
						SourceCount:         expected.Command.SourceCount,
						DefinitionCount:     expected.Command.DefinitionCount,
						DefinitionSetDigest: expected.Command.DefinitionSetDigest,
					},
				}
				if privateResponse != want {
					t.Fatal("PostgreSQL winner private response did not match the exact successful migration result")
				}
				continue
			}
			want := migrateprotocol.Response{Failure: migrateprotocol.Failure{
				Category: migrateprotocol.CategoryTransaction,
				Code:     "history_revision_contended",
			}}
			if privateResponse != want {
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

		reconciliationValues := environmentMap(environment)
		delete(reconciliationValues, fullConcurrencyBarrierEnv)
		reconciliationEnvironment := sortedEnvironment(reconciliationValues)
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
	values := environmentMap(offlineEnvironment(os.Environ()))
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
	command.Env = sortedEnvironment(values)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build global godj for PostgreSQL product: failed with %d output bytes", len(output))
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
		t.Fatalf("connect PostgreSQL schema owner: %v", projectMigratePostgresSafeError(err))
	}
	defer func() {
		if err := admin.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL schema owner: %v", projectMigratePostgresSafeError(err))
		}
	}()
	var before int
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM "pg_catalog"."pg_namespace" WHERE "nspname" = $1`, schema).Scan(&before); err != nil {
		t.Fatalf("inspect PostgreSQL schema uniqueness: %v", projectMigratePostgresSafeError(err))
	}
	if before != 0 {
		t.Fatal("generated PostgreSQL product schema was not unique")
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatalf("create isolated PostgreSQL product schema: %v", projectMigratePostgresSafeError(err))
	}
	var after int
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM "pg_catalog"."pg_namespace" WHERE "nspname" = $1`, schema).Scan(&after); err != nil {
		t.Fatalf("verify isolated PostgreSQL product schema: %v", projectMigratePostgresSafeError(err))
	}
	if after != 1 {
		t.Fatal("isolated PostgreSQL product schema was not created exactly once")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.Connect(cleanupCtx, databaseURL)
		if err != nil {
			t.Errorf("connect PostgreSQL schema cleanup: %v", projectMigratePostgresSafeError(err))
			return
		}
		if _, err := cleanup.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop isolated PostgreSQL product schema: %v", projectMigratePostgresSafeError(err))
		}
		if err := cleanup.Close(cleanupCtx); err != nil {
			t.Errorf("close PostgreSQL schema cleanup: %v", projectMigratePostgresSafeError(err))
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
	values := environmentMap(articleEnvironmentWithoutDatabase(t, workspaceBase))
	delete(values, projectMigratePostgresTestURLEnv)
	delete(values, projectMigratePostgresRequiredEnv)
	values[articlePostgresURLEnv] = databaseURL
	values[articlePostgresSchemaEnv] = schema
	return sortedEnvironment(values)
}

func projectMigratePostgresAssertEnvironment(t *testing.T, environment []string, databaseURL, schema string) {
	t.Helper()
	values := environmentMap(environment)
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

type projectMigratePostgresColumn struct {
	Table            string
	Ordinal          int
	Name             string
	Type             string
	NotNull          bool
	Identity         string
	Generated        string
	HasDefault       bool
	DefaultCollation bool
	Primary          bool
}

type projectMigratePostgresConstraint struct {
	Table            string
	Name             string
	Kind             string
	Deferrable       bool
	Deferred         bool
	Validated        bool
	Key              string
	IndexName        string
	InternalTriggers int
}

type projectMigratePostgresIndex struct {
	Table          string
	Name           string
	Primary        bool
	Unique         bool
	Valid          bool
	Ready          bool
	Live           bool
	KeyCount       int
	AttributeCount int
	Keys           string
	Method         string
	HasPredicate   bool
	HasExpressions bool
	Exclusion      bool
}

type projectMigratePostgresOtherRelation struct {
	Name string
	Kind string
}

type projectMigratePostgresRevision struct {
	FormatVersion      int
	Epoch              [16]byte
	Revision           int64
	HistoryFingerprint [32]byte
	AdditionalRows     int
}

type projectMigratePostgresSequence struct {
	Name        string
	Kind        string
	Persistence string
	DataType    string
	Start       int64
	Increment   int64
	Minimum     int64
	Maximum     int64
	Cache       int64
	Cycle       bool
	OwnerTable  string
	OwnerColumn string
	Dependency  string
	Last        int64
	Called      bool
}

type projectMigratePostgresArticle struct {
	ID        int64
	Title     string
	Published bool
}

type projectMigratePostgresSnapshot struct {
	Tables         []string
	OtherRelations []projectMigratePostgresOtherRelation
	Columns        []projectMigratePostgresColumn
	Constraints    []projectMigratePostgresConstraint
	Indexes        []projectMigratePostgresIndex
	Triggers       int
	Policies       int
	Rules          int
	History        []historyRow
	Revision       projectMigratePostgresRevision
	Sequences      []projectMigratePostgresSequence
	Counts         map[string]int64
	Articles       []projectMigratePostgresArticle
}

func projectMigratePostgresInspect(t *testing.T, databaseURL, schema string) projectMigratePostgresSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL product inspection: %v", projectMigratePostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL product inspection: %v", projectMigratePostgresSafeError(err))
		}
	}()

	snapshot := projectMigratePostgresSnapshot{Counts: make(map[string]int64)}
	rows, err := connection.Query(ctx, `
		SELECT "c"."relname"
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1 AND "c"."relkind" = 'r'
		ORDER BY "c"."relname"`, schema)
	if err != nil {
		t.Fatalf("query PostgreSQL product tables: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL product table: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Tables = append(snapshot.Tables, table)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL product table query: %v", projectMigratePostgresSafeError(err))
	}
	rows, err = connection.Query(ctx, `
		SELECT "c"."relname", "c"."relkind"::text
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		WHERE "n"."nspname" = $1 AND "c"."relkind" NOT IN ('r', 'i', 'S')
		ORDER BY "c"."relkind", "c"."relname"`, schema)
	if err != nil {
		t.Fatalf("query PostgreSQL unexpected product relations: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var relation projectMigratePostgresOtherRelation
		if err := rows.Scan(&relation.Name, &relation.Kind); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL unexpected product relation: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.OtherRelations = append(snapshot.OtherRelations, relation)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL unexpected product relation query: %v", projectMigratePostgresSafeError(err))
	}

	rows, err = connection.Query(ctx, `
		SELECT "c"."relname", "a"."attnum", "a"."attname",
		       "pg_catalog"."format_type"("a"."atttypid", "a"."atttypmod"),
		       "a"."attnotnull", "a"."attidentity"::text, "a"."attgenerated"::text,
		       ("d"."oid" IS NOT NULL),
		       COALESCE("a"."attcollation" = "type"."typcollation", "a"."attcollation" = 0),
		       EXISTS (
		         SELECT 1 FROM "pg_catalog"."pg_index" AS "i"
		         WHERE "i"."indrelid" = "c"."oid" AND "i"."indisprimary"
		           AND "a"."attnum" = ANY("i"."indkey")
		       )
		FROM "pg_catalog"."pg_class" AS "c"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		JOIN "pg_catalog"."pg_attribute" AS "a" ON "a"."attrelid" = "c"."oid"
		JOIN "pg_catalog"."pg_type" AS "type" ON "type"."oid" = "a"."atttypid"
		LEFT JOIN "pg_catalog"."pg_attrdef" AS "d"
		  ON "d"."adrelid" = "a"."attrelid" AND "d"."adnum" = "a"."attnum"
		WHERE "n"."nspname" = $1 AND "c"."relkind" = 'r'
		  AND "a"."attnum" > 0 AND NOT "a"."attisdropped"
		ORDER BY "c"."relname", "a"."attnum"`, schema)
	if err != nil {
		t.Fatalf("query PostgreSQL product columns: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var column projectMigratePostgresColumn
		if err := rows.Scan(
			&column.Table,
			&column.Ordinal,
			&column.Name,
			&column.Type,
			&column.NotNull,
			&column.Identity,
			&column.Generated,
			&column.HasDefault,
			&column.DefaultCollation,
			&column.Primary,
		); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL product column: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Columns = append(snapshot.Columns, column)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL product column query: %v", projectMigratePostgresSafeError(err))
	}

	rows, err = connection.Query(ctx, `
		SELECT "table"."relname", "constraint"."conname", "constraint"."contype"::text,
		       "constraint"."condeferrable", "constraint"."condeferred", "constraint"."convalidated",
		       COALESCE("pg_catalog"."array_to_string"("constraint"."conkey", ','), ''),
		       COALESCE("constraint_index"."relname", ''),
		       (SELECT COUNT(*) FROM "pg_catalog"."pg_trigger" AS "trigger"
		        WHERE "trigger"."tgconstraint" = "constraint"."oid" AND "trigger"."tgisinternal")
		FROM "pg_catalog"."pg_constraint" AS "constraint"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "constraint"."conrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		LEFT JOIN "pg_catalog"."pg_class" AS "constraint_index"
		  ON "constraint_index"."oid" = "constraint"."conindid"
		WHERE "n"."nspname" = $1
		ORDER BY "table"."relname", "constraint"."conname"`, schema)
	if err != nil {
		t.Fatalf("query PostgreSQL product constraints: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var constraint projectMigratePostgresConstraint
		if err := rows.Scan(
			&constraint.Table,
			&constraint.Name,
			&constraint.Kind,
			&constraint.Deferrable,
			&constraint.Deferred,
			&constraint.Validated,
			&constraint.Key,
			&constraint.IndexName,
			&constraint.InternalTriggers,
		); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL product constraint: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Constraints = append(snapshot.Constraints, constraint)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL product constraint query: %v", projectMigratePostgresSafeError(err))
	}

	rows, err = connection.Query(ctx, `
		SELECT "table"."relname", "index_class"."relname",
		       "index"."indisprimary", "index"."indisunique", "index"."indisvalid",
		       "index"."indisready", "index"."indislive",
		       "index"."indnkeyatts"::integer, "index"."indnatts"::integer,
		       COALESCE((
		         SELECT "pg_catalog"."string_agg"("attribute"."attname", ',' ORDER BY "key"."ordinality")
		         FROM "pg_catalog"."unnest"("index"."indkey"::smallint[]) WITH ORDINALITY
		           AS "key"("attribute_number", "ordinality")
		         JOIN "pg_catalog"."pg_attribute" AS "attribute"
		           ON "attribute"."attrelid" = "index"."indrelid"
		          AND "attribute"."attnum" = "key"."attribute_number"
		         WHERE "key"."ordinality" <= "index"."indnkeyatts"
		       ), ''),
		       "access_method"."amname",
		       ("index"."indpred" IS NOT NULL), ("index"."indexprs" IS NOT NULL),
		       "index"."indisexclusion"
		FROM "pg_catalog"."pg_index" AS "index"
		JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "index"."indrelid"
		JOIN "pg_catalog"."pg_class" AS "index_class" ON "index_class"."oid" = "index"."indexrelid"
		JOIN "pg_catalog"."pg_am" AS "access_method" ON "access_method"."oid" = "index_class"."relam"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		WHERE "n"."nspname" = $1
		ORDER BY "table"."relname", "index_class"."relname"`, schema)
	if err != nil {
		t.Fatalf("query PostgreSQL product indexes: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var index projectMigratePostgresIndex
		if err := rows.Scan(
			&index.Table,
			&index.Name,
			&index.Primary,
			&index.Unique,
			&index.Valid,
			&index.Ready,
			&index.Live,
			&index.KeyCount,
			&index.AttributeCount,
			&index.Keys,
			&index.Method,
			&index.HasPredicate,
			&index.HasExpressions,
			&index.Exclusion,
		); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL product index: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Indexes = append(snapshot.Indexes, index)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL product index query: %v", projectMigratePostgresSafeError(err))
	}
	if err := connection.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*)
		   FROM "pg_catalog"."pg_trigger" AS "trigger"
		   JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "trigger"."tgrelid"
		   JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		   WHERE "n"."nspname" = $1),
		  (SELECT COUNT(*)
		   FROM "pg_catalog"."pg_policy" AS "policy"
		   JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "policy"."polrelid"
		   JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		   WHERE "n"."nspname" = $1),
		  (SELECT COUNT(*)
		   FROM "pg_catalog"."pg_rewrite" AS "rule"
		   JOIN "pg_catalog"."pg_class" AS "table" ON "table"."oid" = "rule"."ev_class"
		   JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "table"."relnamespace"
		   WHERE "n"."nspname" = $1)`, schema).Scan(&snapshot.Triggers, &snapshot.Policies, &snapshot.Rules); err != nil {
		t.Fatalf("count PostgreSQL product trigger, policy, and rule artifacts: %v", projectMigratePostgresSafeError(err))
	}

	quotedSchema := pgx.Identifier{schema}.Sanitize()
	for _, table := range snapshot.Tables {
		quotedTable := pgx.Identifier{schema, table}.Sanitize()
		var count int64
		if err := connection.QueryRow(ctx, "SELECT COUNT(*) FROM "+quotedTable).Scan(&count); err != nil {
			t.Fatalf("count PostgreSQL product table rows: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Counts[table] = count
	}

	rows, err = connection.Query(ctx, `SELECT "app", "name" FROM `+quotedSchema+`."godj_migrations" ORDER BY "app", "name"`)
	if err != nil {
		t.Fatalf("query PostgreSQL migration history: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var row historyRow
		if err := rows.Scan(&row.App, &row.Name); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL migration history: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.History = append(snapshot.History, row)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL migration history query: %v", projectMigratePostgresSafeError(err))
	}
	var epoch []byte
	var fingerprint []byte
	if err := connection.QueryRow(ctx, `SELECT "format_version", "epoch", "revision", "history_fingerprint" FROM `+quotedSchema+`."godj_migration_revision" WHERE "singleton" = 1`).Scan(
		&snapshot.Revision.FormatVersion,
		&epoch,
		&snapshot.Revision.Revision,
		&fingerprint,
	); err != nil {
		t.Fatalf("query PostgreSQL migration revision: %v", projectMigratePostgresSafeError(err))
	}
	if len(epoch) != len(snapshot.Revision.Epoch) || len(fingerprint) != len(snapshot.Revision.HistoryFingerprint) {
		t.Fatal("PostgreSQL migration revision contained an invalid bounded digest or epoch")
	}
	copy(snapshot.Revision.Epoch[:], epoch)
	copy(snapshot.Revision.HistoryFingerprint[:], fingerprint)
	if err := connection.QueryRow(ctx, `SELECT COUNT(*) - 1 FROM `+quotedSchema+`."godj_migration_revision"`).Scan(&snapshot.Revision.AdditionalRows); err != nil {
		t.Fatalf("count PostgreSQL migration revision rows: %v", projectMigratePostgresSafeError(err))
	}

	rows, err = connection.Query(ctx, `
		SELECT "c"."relname", "c"."relkind"::text, "c"."relpersistence"::text,
		       "pg_catalog"."format_type"("s"."seqtypid", NULL),
		       "s"."seqstart", "s"."seqincrement", "s"."seqmin",
		       "s"."seqmax", "s"."seqcache", "s"."seqcycle",
		       COALESCE("owner_table"."relname", ''), COALESCE("owner_column"."attname", ''),
		       COALESCE("dependency"."deptype"::text, '')
		FROM "pg_catalog"."pg_sequence" AS "s"
		JOIN "pg_catalog"."pg_class" AS "c" ON "c"."oid" = "s"."seqrelid"
		JOIN "pg_catalog"."pg_namespace" AS "n" ON "n"."oid" = "c"."relnamespace"
		LEFT JOIN "pg_catalog"."pg_depend" AS "dependency"
		  ON "dependency"."classid" = 'pg_catalog.pg_class'::regclass
		 AND "dependency"."objid" = "c"."oid"
		 AND "dependency"."refclassid" = 'pg_catalog.pg_class'::regclass
		 AND "dependency"."deptype" IN ('a', 'i')
		LEFT JOIN "pg_catalog"."pg_class" AS "owner_table"
		  ON "owner_table"."oid" = "dependency"."refobjid"
		LEFT JOIN "pg_catalog"."pg_attribute" AS "owner_column"
		  ON "owner_column"."attrelid" = "dependency"."refobjid"
		 AND "owner_column"."attnum" = "dependency"."refobjsubid"
		WHERE "n"."nspname" = $1
		ORDER BY "c"."relname"`, schema)
	if err != nil {
		t.Fatalf("query PostgreSQL product sequences: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var sequence projectMigratePostgresSequence
		if err := rows.Scan(
			&sequence.Name,
			&sequence.Kind,
			&sequence.Persistence,
			&sequence.DataType,
			&sequence.Start,
			&sequence.Increment,
			&sequence.Minimum,
			&sequence.Maximum,
			&sequence.Cache,
			&sequence.Cycle,
			&sequence.OwnerTable,
			&sequence.OwnerColumn,
			&sequence.Dependency,
		); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL product sequence: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Sequences = append(snapshot.Sequences, sequence)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL product sequence query: %v", projectMigratePostgresSafeError(err))
	}
	for index := range snapshot.Sequences {
		sequenceName := pgx.Identifier{schema, snapshot.Sequences[index].Name}.Sanitize()
		if err := connection.QueryRow(ctx, "SELECT last_value, is_called FROM "+sequenceName).Scan(
			&snapshot.Sequences[index].Last,
			&snapshot.Sequences[index].Called,
		); err != nil {
			t.Fatalf("inspect PostgreSQL product sequence state: %v", projectMigratePostgresSafeError(err))
		}
	}

	rows, err = connection.Query(ctx, `SELECT "id", "title", "published" FROM `+quotedSchema+`."godj_conformance_article" ORDER BY "id"`)
	if err != nil {
		t.Fatalf("query PostgreSQL Article rows: %v", projectMigratePostgresSafeError(err))
	}
	for rows.Next() {
		var article projectMigratePostgresArticle
		if err := rows.Scan(&article.ID, &article.Title, &article.Published); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL Article row: %v", projectMigratePostgresSafeError(err))
		}
		snapshot.Articles = append(snapshot.Articles, article)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL Article row query: %v", projectMigratePostgresSafeError(err))
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
		"godj_migration_revision",
		"godj_migrations",
		"godj_system_audit",
		"godj_system_credential",
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
	if snapshot.Triggers != 0 || snapshot.Policies != 0 || snapshot.Rules != 0 {
		t.Fatalf(
			"PostgreSQL latest schema trigger/policy/rule counts = %d/%d/%d, want 0/0/0",
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
		"godj_conformance_article": int64(len(articles)),
		"godj_migration_revision":  1,
		"godj_migrations":          int64(len(expected.History)),
		"godj_system_audit":        0,
		"godj_system_credential":   0,
		"godj_system_session":      0,
	}
	if !reflect.DeepEqual(snapshot.Counts, wantCounts) {
		t.Fatal("PostgreSQL latest row cardinalities did not match the exact clean product state")
	}
	if !reflect.DeepEqual(snapshot.Articles, articles) {
		t.Fatal("PostgreSQL Article rows did not match the exact durable fixture")
	}
	if len(snapshot.Sequences) != 4 {
		t.Fatalf("PostgreSQL identity sequence count = %d, want 4", len(snapshot.Sequences))
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

func projectMigratePostgresExpectedConstraints() []projectMigratePostgresConstraint {
	primary := func(table, name, key string) projectMigratePostgresConstraint {
		return projectMigratePostgresConstraint{
			Table:     table,
			Name:      name,
			Kind:      "p",
			Validated: true,
			Key:       key,
			IndexName: name,
		}
	}
	return []projectMigratePostgresConstraint{
		primary("godj_conformance_article", "godj_pk_7f21c7e928b78be2fc532391565427000c1cf0627bdaf24a", "1"),
		primary("godj_migration_revision", "godj_migration_revision_pkey", "1"),
		primary("godj_migrations", "godj_migrations_pkey", "1,2"),
		primary("godj_system_audit", "godj_pk_d62f2a2b334710f31d7485fc8902dea702de6ecb9dad9f02", "1"),
		primary("godj_system_credential", "godj_pk_7aad345323cce9490877362ad8c707fbd02a643680270e40", "1"),
		primary("godj_system_session", "godj_pk_83d7214ff051fc5f24c0f9870b69166345f11de9f42188dc", "1"),
	}
}

func projectMigratePostgresExpectedIndexes() []projectMigratePostgresIndex {
	primary := func(table, name, keys string, count int) projectMigratePostgresIndex {
		return projectMigratePostgresIndex{
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
	return []projectMigratePostgresIndex{
		primary("godj_conformance_article", "godj_pk_7f21c7e928b78be2fc532391565427000c1cf0627bdaf24a", "id", 1),
		primary("godj_migration_revision", "godj_migration_revision_pkey", "singleton", 1),
		primary("godj_migrations", "godj_migrations_pkey", "app,name", 2),
		primary("godj_system_audit", "godj_pk_d62f2a2b334710f31d7485fc8902dea702de6ecb9dad9f02", "id", 1),
		primary("godj_system_credential", "godj_pk_7aad345323cce9490877362ad8c707fbd02a643680270e40", "id", 1),
		primary("godj_system_session", "godj_pk_83d7214ff051fc5f24c0f9870b69166345f11de9f42188dc", "id", 1),
	}
}

func projectMigratePostgresExpectedColumns() []projectMigratePostgresColumn {
	column := func(table string, ordinal int, name, fieldType string, notNull bool, identity string, primary bool) projectMigratePostgresColumn {
		return projectMigratePostgresColumn{
			Table:            table,
			Ordinal:          ordinal,
			Name:             name,
			Type:             fieldType,
			NotNull:          notNull,
			Identity:         identity,
			DefaultCollation: true,
			Primary:          primary,
		}
	}
	return []projectMigratePostgresColumn{
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
}

func projectMigratePostgresInsertArticle(t *testing.T, databaseURL, schema, title string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect PostgreSQL Article seed: %v", projectMigratePostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL Article seed: %v", projectMigratePostgresSafeError(err))
		}
	}()
	var id int64
	statement := `INSERT INTO ` + pgx.Identifier{schema, "godj_conformance_article"}.Sanitize() + ` ("title", "published", "summary") VALUES ($1, TRUE, NULL) RETURNING "id"`
	if err := connection.QueryRow(ctx, statement, title).Scan(&id); err != nil {
		t.Fatalf("insert PostgreSQL Article sentinel: %v", projectMigratePostgresSafeError(err))
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
	stdout := newReadinessOutput()
	stderr := &boundedOutput{maximum: maximumCommandOutput}
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
	case address = <-stdout.ready:
		projectMigratePostgresAssertVisibleSecretFree(t, stdout.String(), stderr.String(), sensitive)
		if address != expectedAddress {
			t.Fatal("PostgreSQL Article readiness address did not match the reserved loopback address")
		}
		groups, err := ownedProcessGroups(command.Process.Pid)
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
		t.Fatalf("connect PostgreSQL artifact inspection: %v", projectMigratePostgresSafeError(err))
	}
	defer func() {
		if err := connection.Close(ctx); err != nil {
			t.Errorf("close PostgreSQL artifact inspection: %v", projectMigratePostgresSafeError(err))
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
		t.Fatalf("enumerate PostgreSQL text artifacts: %v", projectMigratePostgresSafeError(err))
	}
	type field struct{ table, column string }
	var fields []field
	for rows.Next() {
		var value field
		if err := rows.Scan(&value.table, &value.column); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL text artifact field: %v", projectMigratePostgresSafeError(err))
		}
		fields = append(fields, value)
	}
	if err := projectMigratePostgresCloseRows(rows); err != nil {
		t.Fatalf("finish PostgreSQL text artifact enumeration: %v", projectMigratePostgresSafeError(err))
	}
	for _, field := range fields {
		statement := "SELECT COALESCE(" + pgx.Identifier{field.column}.Sanitize() + ", '') FROM " + pgx.Identifier{schema, field.table}.Sanitize()
		values, err := connection.Query(ctx, statement)
		if err != nil {
			t.Fatalf("query PostgreSQL text artifact: %v", projectMigratePostgresSafeError(err))
		}
		for values.Next() {
			var value string
			if err := values.Scan(&value); err != nil {
				values.Close()
				t.Fatalf("scan PostgreSQL text artifact: %v", projectMigratePostgresSafeError(err))
			}
			for _, secret := range sensitive {
				if secret != "" && strings.Contains(value, secret) {
					values.Close()
					t.Fatal("PostgreSQL durable row artifact exposed a database credential")
				}
			}
		}
		if err := projectMigratePostgresCloseRows(values); err != nil {
			t.Fatalf("finish PostgreSQL text artifact query: %v", projectMigratePostgresSafeError(err))
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

func projectMigratePostgresSafeError(err error) error {
	if err == nil {
		return nil
	}
	var structured interface{ SQLState() string }
	if errors.As(err, &structured) {
		return fmt.Errorf("PostgreSQL SQLSTATE %s", structured.SQLState())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("PostgreSQL operation timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("PostgreSQL operation was canceled")
	}
	return errors.New("PostgreSQL operation failed")
}
