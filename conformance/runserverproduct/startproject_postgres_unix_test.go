//go:build darwin || linux

package runserverproduct_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/conformance/internal/dbstate"
	"github.com/progresshans/godj/conformance/internal/testfixture"
	"github.com/progresshans/godj/conformance/internal/testprocess"
	"github.com/progresshans/godj/internal/testenv"
)

func TestGlobalStartProjectPostgresResourceChecksAndAuthenticatedRestart(t *testing.T) {
	databaseURL := runserverPostgresTestURL(t)
	repository := runserverRepositoryRoot(t)
	schema := createRunserverPostgresSchema(t, databaseURL)
	applicationName := fmt.Sprintf("godj_starter_check_%d_%d", os.Getpid(), time.Now().UnixNano())
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("PostgreSQL fixture URL is invalid")
	}
	parameters := parsed.Query()
	parameters.Set("application_name", applicationName)
	parsed.RawQuery = parameters.Encode()
	selectedURL := parsed.String()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	admin, err := pgx.Connect(ctx, databaseURL)
	cancel()
	if err != nil {
		t.Fatal("open PostgreSQL observation connection", testfixture.PostgresSafeError(err))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := admin.Close(ctx); err != nil {
			t.Error("close PostgreSQL observation connection", testfixture.PostgresSafeError(err))
		}
	})
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspaces")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	environment := runserverEnvironment(t, "unused-sqlite", workspace)
	environment = testenv.Without(environment, articleSQLiteDatabaseEnv, runserverPostgresTestURLEnv, runserverPostgresRequiredEnv, "GODJ_SQLITE_DATABASE", "GODJ_POSTGRES_URL", "GODJ_POSTGRES_SCHEMA")
	environment = testenv.With(environment, map[string]string{"GODJ_POSTGRES_URL": selectedURL, "GODJ_POSTGRES_SCHEMA": schema})
	global := buildGlobalGodj(t, repository)
	project := filepath.Join(root, "site")
	run := func(directory string, env []string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := runProjectCLI(t, directory, env, global, args...)
		assertRunserverWorkspaceEmpty(t, workspace)
		assertRunserverPostgresOutputSecretFree(t, selectedURL, result.Stdout, result.Stderr)
		assertStartedPostgresConnectionsClosed(t, admin, applicationName)
		return result
	}
	mustRun := func(directory string, args ...string) testprocess.CommandResult {
		t.Helper()
		result := run(directory, environment, args...)
		if result.ExitCode != 0 || result.Stderr != "" {
			t.Fatal("PostgreSQL starter command failed", args[0], result.ExitCode, result.Stderr)
		}
		return result
	}
	initial := captureStartedPostgresState(t, admin, schema)
	if len(initial.Catalog.Tables) != 0 {
		t.Fatal("new schema was not empty")
	}
	mustRun(root, "startproject", "example.com/started-site", "site", "--framework-source", repository)
	for _, args := range [][]string{{"check"}, {"check", "--list"}, {"check", "--resource", "database"}} {
		mustRun(project, args...)
	}
	missing := "missing_" + schema
	assertMissing := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		var exists bool
		if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)", missing).Scan(&exists); err != nil || exists {
			t.Fatal("check created an absent PostgreSQL schema", testfixture.PostgresSafeError(err))
		}
	}
	assertMissing()
	failed := run(project, testenv.With(environment, map[string]string{"GODJ_POSTGRES_SCHEMA": missing}), "check", "--resource", "database")
	if failed.ExitCode != 1 || failed.Stdout != "" || failed.Stderr != "command_error/resource_check_failed/database/database\n" {
		t.Fatal("missing PostgreSQL schema was not diagnosed", failed.ExitCode, failed.Stderr)
	}
	assertMissing()
	failed = run(project, testenv.Without(environment, "GODJ_POSTGRES_SCHEMA"), "check", "--resource", "database")
	if failed.ExitCode != 1 || failed.Stdout != "" || failed.Stderr != "command_error/check_failed/GODJ_POSTGRES_SCHEMA\n" {
		t.Fatal("missing PostgreSQL setting was not diagnosed before I/O", failed.ExitCode, failed.Stderr)
	}
	if !reflect.DeepEqual(initial, captureStartedPostgresState(t, admin, schema)) {
		t.Fatal("scaffold/check created PostgreSQL tables or changed data")
	}
	for _, args := range [][]string{{"generate", "--check"}, {"migrations", "check"}, {"migrate"}} {
		mustRun(project, args...)
	}
	if result := mustRun(project, "command", "count-groups"); result.Stdout != "0\n" {
		t.Fatal("new PostgreSQL project already has groups")
	}
	const username, password = "starter-postgres-admin", "Starter-Postgres-734!private"
	descriptor := filepath.Join(project, "godj.toml")
	provisionRunserverOperator(t, global, project, descriptor, environment, username, password)
	assertRunserverWorkspaceEmpty(t, workspace)
	assertStartedPostgresConnectionsClosed(t, admin, applicationName)
	for attempt := range 2 {
		address := reserveRunserverLoopbackAddress(t, "")
		sensitive := []string{password, selectedURL}
		if parsed.User != nil {
			if password, ok := parsed.User.Password(); ok && password != "" {
				sensitive = append(sensitive, password)
			}
		}
		runGlobalProjectServer(t, global, project, descriptor, address, environment, "site listening on http://", &sensitive, func(address string) error {
			return exerciseStartedIdentity(address, username, password, attempt == 0, &sensitive)
		})
		assertRunserverWorkspaceEmpty(t, workspace)
		assertStartedPostgresConnectionsClosed(t, admin, applicationName)
	}
	if result := mustRun(project, "command", "count-groups"); result.Stdout != "1\n" {
		t.Fatal("PostgreSQL restart lost authenticated storage")
	}
	stored := captureStartedPostgresState(t, admin, schema)
	before := snapshotRunserverProjectTree(t, project)
	if result := mustRun(project, "check", "--resource", "database"); result.Stdout != "" {
		t.Fatal("database check published unexpected output")
	}
	if !reflect.DeepEqual(stored, captureStartedPostgresState(t, admin, schema)) || !reflect.DeepEqual(before, snapshotRunserverProjectTree(t, project)) {
		t.Fatal("database check changed PostgreSQL catalog, rows, sequence state or project source")
	}
	if _, err := os.Lstat(filepath.Join(project, "site.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("PostgreSQL project silently created the SQLite fallback", err)
	}
	t.Log("PostgreSQL starter: pure and selected checks, absent schema preservation, explicit migration/operator, authenticated storage/restart, closed physical sessions")
}

func assertStartedPostgresConnectionsClosed(t *testing.T, admin *pgx.Conn, application string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE application_name=$1", application).Scan(&count); err != nil {
			t.Fatal("inspect PostgreSQL check lifetime", testfixture.PostgresSafeError(err))
		}
		if count == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("completed project operation retained PostgreSQL connections")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type startedPostgresState struct {
	Catalog dbstate.PostgresCatalog
	Rows    map[string][32]byte
}

func captureStartedPostgresState(t *testing.T, admin *pgx.Conn, schema string) startedPostgresState {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	catalog, err := dbstate.CapturePostgresCatalog(ctx, admin, schema)
	if err != nil {
		t.Fatal("inspect PostgreSQL catalog", testfixture.PostgresSafeError(err))
	}
	result := startedPostgresState{Catalog: catalog, Rows: make(map[string][32]byte)}
	for _, table := range catalog.Tables {
		rows, err := admin.Query(ctx, "SELECT row_to_json(observed)::text FROM "+pgx.Identifier{schema, table}.Sanitize()+" AS observed ORDER BY row_to_json(observed)::text")
		if err != nil {
			t.Fatal("inspect PostgreSQL fixture rows", testfixture.PostgresSafeError(err))
		}
		digest := sha256.New()
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				t.Fatal("read PostgreSQL fixture row", testfixture.PostgresSafeError(err))
			}
			_, _ = digest.Write([]byte(value))
			_, _ = digest.Write([]byte{'\n'})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal("complete PostgreSQL fixture rows", testfixture.PostgresSafeError(err))
		}
		var checksum [32]byte
		copy(checksum[:], digest.Sum(nil))
		result.Rows[table] = checksum
	}
	return result
}
