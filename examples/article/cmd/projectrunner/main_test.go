//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	godjproject "github.com/progresshans/godj/project"
)

func TestArticleProjectRunnerMigratesFreshSQLiteAndSecondRunIsNoop(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	databasePath := filepath.Join(t.TempDir(), "article.sqlite3")
	config := articleProjectConfig(environmentLookup(map[string]string{
		databaseconfig.SQLiteDatabaseEnv: databasePath,
	}))

	var firstDigest string
	for invocation := 0; invocation < 2; invocation++ {
		var output bytes.Buffer
		err := godjproject.Run(
			context.Background(),
			config,
			[]string{migrateprotocol.PrivateArgument},
			bytes.NewReader(migrateprotocol.RequestDocument()),
			&output,
		)
		if err != nil {
			t.Fatalf("project migrate invocation %d: %v", invocation+1, err)
		}
		response, failure, failed := migrateprotocol.ParseResponse(output.Bytes(), true)
		if failed || !response.OK {
			t.Fatalf("project migrate invocation %d response=%+v failure=%+v", invocation+1, response, failure)
		}
		if response.Result.Mode != migrateprotocol.ModeExecute || response.Result.Plan != nil ||
			response.Result.Execute.SourceCount != 2 || response.Result.Execute.DefinitionCount != 2 {
			t.Fatalf("project migrate invocation %d result=%+v", invocation+1, response.Result)
		}
		if invocation == 0 {
			firstDigest = response.Result.Execute.DefinitionSetDigest
		} else if response.Result.Execute.DefinitionSetDigest != firstDigest {
			t.Fatalf("second invocation digest=%q, want %q", response.Result.Execute.DefinitionSetDigest, firstDigest)
		}
	}

	historyBeforePlan := readArticleMigrationHistory(t, databasePath)
	requestDocument, err := migrateprotocol.EncodeRequest(migrateprotocol.Request{
		Mode:   migrateprotocol.ModePlan,
		Target: migrateprotocol.Target{Kind: migrateprotocol.TargetLatest},
	})
	if err != nil {
		t.Fatal(err)
	}
	var planOutput bytes.Buffer
	err = godjproject.Run(
		context.Background(),
		config,
		[]string{migrateprotocol.PrivateArgument},
		bytes.NewReader(requestDocument),
		&planOutput,
	)
	if err != nil {
		t.Fatalf("project migration plan: %v", err)
	}
	const wantPlanResponse = `{"protocol_version":2,"status":"ok","result":{"mode":"plan","plan":[]}}`
	if got := planOutput.String(); got != wantPlanResponse {
		t.Fatalf("project migration plan response=%q, want canonical %q", got, wantPlanResponse)
	}
	planResponse, failure, failed := migrateprotocol.ParseResponse(planOutput.Bytes(), true)
	if failed || !planResponse.OK || planResponse.Result.Mode != migrateprotocol.ModePlan ||
		planResponse.Result.Plan == nil || len(planResponse.Result.Plan) != 0 ||
		planResponse.Result.Execute != (migrateprotocol.ExecuteResult{}) {
		t.Fatalf("project migration plan response=%+v failure=%+v failed=%t", planResponse, failure, failed)
	}
	historyAfterPlan := readArticleMigrationHistory(t, databasePath)
	if !reflect.DeepEqual(historyAfterPlan, historyBeforePlan) {
		t.Fatalf("project migration plan changed history: before=%+v after=%+v", historyBeforePlan, historyAfterPlan)
	}
	if len(historyAfterPlan) != 2 ||
		historyAfterPlan[0].App != "godj_conformance" || historyAfterPlan[0].Name != "0001_initial" ||
		historyAfterPlan[1].App != "godj_system" || historyAfterPlan[1].Name != "0001_initial" {
		t.Fatalf("second invocation history=%+v", historyAfterPlan)
	}
}

func readArticleMigrationHistory(t *testing.T, databasePath string) []migrationbackend.AppliedMigration {
	t.Helper()
	backend, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := backend.OpenRevisionFencedSession(context.Background())
	if err != nil {
		_ = backend.Close()
		t.Fatal(err)
	}
	records, readErr := session.ReadAppliedMigrations(context.Background())
	sessionCloseErr := session.Close(context.Background())
	backendCloseErr := backend.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if sessionCloseErr != nil {
		t.Fatal(sessionCloseErr)
	}
	if backendCloseErr != nil {
		t.Fatal(backendCloseErr)
	}
	return records
}

func TestArticleProjectRunnerClosesConfigurationFailureWithoutSecret(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	const secret = "article-project-runner-secret"
	config := articleProjectConfig(environmentLookup(map[string]string{
		databaseconfig.PostgresURLEnv: "postgres://article:" + secret + "@localhost/article",
	}))
	var output bytes.Buffer
	err = godjproject.Run(
		context.Background(),
		config,
		[]string{migrateprotocol.PrivateArgument},
		bytes.NewReader(migrateprotocol.RequestDocument()),
		&output,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, failure, failed := migrateprotocol.ParseResponse(output.Bytes(), true)
	want := migrateprotocol.Failure{
		Category: migrateprotocol.CategoryBackend,
		Code:     migrateprotocol.CodeBackendOpenFailed,
	}
	if failed || response.OK || response.Failure != want {
		t.Fatalf("configuration failure response=%+v parse_failure=%+v failed=%t", response, failure, failed)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("private response exposed database secret: %q", output.String())
	}
}

func TestArticleProjectConfigFreezesDatabaseSelectionOnce(t *testing.T) {
	values := map[string]string{
		databaseconfig.SQLiteDatabaseEnv: filepath.Join(t.TempDir(), "frozen.sqlite3"),
	}
	lookups := make(map[string]int)
	config := articleProjectConfig(func(name string) (string, bool) {
		lookups[name]++
		value, ok := values[name]
		return value, ok
	})
	wantDatabase := values[databaseconfig.SQLiteDatabaseEnv]
	changedDatabase := filepath.Join(t.TempDir(), "changed.sqlite3")
	values[databaseconfig.SQLiteDatabaseEnv] = changedDatabase

	backend, err := config.OpenMigrationBackend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wantDatabase); err != nil {
		t.Fatalf("frozen SQLite database was not opened: %v", err)
	}
	if _, err := os.Stat(changedDatabase); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("mutated SQLite selection was observed: %v", err)
	}
	if config.MigrationSQLRenderer == nil {
		t.Fatal("frozen SQLite renderer is nil")
	}
	for _, name := range []string{
		databaseconfig.SQLiteDatabaseEnv,
		databaseconfig.PostgresURLEnv,
		databaseconfig.PostgresSchemaEnv,
	} {
		if lookups[name] != 1 {
			t.Fatalf("environment lookup %s = %d, want 1", name, lookups[name])
		}
	}
}

func TestArticleProjectRunnerSQLMigrateSQLiteNeverOpensDatabase(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	databasePath := filepath.Join(t.TempDir(), "must-not-exist.sqlite3")
	lookups := 0
	config := articleProjectConfig(func(name string) (string, bool) {
		lookups++
		if name == databaseconfig.SQLiteDatabaseEnv {
			return databasePath, true
		}
		return "", false
	})
	openerCalls := 0
	config.OpenMigrationBackend = func(context.Context) (godjproject.MigrationBackend, error) {
		openerCalls++
		return nil, errors.New("must not open")
	}
	request, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{
		App: "godj_conformance", Name: "0001_initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	var first []byte
	for iteration := 0; iteration < 2; iteration++ {
		var output bytes.Buffer
		if err := godjproject.Run(
			context.Background(), config,
			[]string{sqlmigrateprotocol.PrivateArgument},
			bytes.NewReader(request), &output,
		); err != nil {
			t.Fatalf("sqlmigrate invocation %d: %v", iteration+1, err)
		}
		response, failure, failed := sqlmigrateprotocol.ParseResponse(output.Bytes(), true)
		if failed || failure != (sqlmigrateprotocol.Failure{}) || !response.OK || len(response.Result.Statements) == 0 {
			t.Fatalf("sqlmigrate invocation %d = %+v, %+v, %v", iteration+1, response, failure, failed)
		}
		if iteration == 0 {
			first = append([]byte(nil), output.Bytes()...)
		} else if !bytes.Equal(output.Bytes(), first) {
			t.Fatalf("repeat private SQL drifted:\nfirst %q\nsecond %q", first, output.Bytes())
		}
	}
	if openerCalls != 0 || lookups != 3 {
		t.Fatalf("SQL projection ownership = opener %d lookup %d, want 0/3", openerCalls, lookups)
	}
	if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SQL projection created SQLite database: %v", err)
	}
}

func TestArticleProjectRunnerSQLMigratePostgresUsesFrozenSchemaWithoutURL(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	const urlSecret = "article-sqlmigrate-url-secret"
	values := map[string]string{
		databaseconfig.PostgresURLEnv:    "postgres://article:" + urlSecret + "@localhost/article",
		databaseconfig.PostgresSchemaEnv: "frozen_schema",
	}
	config := articleProjectConfig(environmentLookup(values))
	values[databaseconfig.PostgresURLEnv] = "postgres://mutated.invalid/other"
	values[databaseconfig.PostgresSchemaEnv] = "mutated_schema"
	openerCalls := 0
	config.OpenMigrationBackend = func(context.Context) (godjproject.MigrationBackend, error) {
		openerCalls++
		return nil, errors.New("must not open")
	}

	document, err := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{
		App: "godj_conformance", Name: "0001_initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := godjproject.Run(
		context.Background(), config,
		[]string{sqlmigrateprotocol.PrivateArgument},
		bytes.NewReader(document), &output,
	); err != nil {
		t.Fatal(err)
	}
	response, failure, failed := sqlmigrateprotocol.ParseResponse(output.Bytes(), true)
	if failed || failure != (sqlmigrateprotocol.Failure{}) || !response.OK || len(response.Result.Statements) == 0 {
		t.Fatalf("PostgreSQL SQL response = %+v, %+v, %v", response, failure, failed)
	}
	if openerCalls != 0 || !bytes.Contains(output.Bytes(), []byte(`\"frozen_schema\".`)) ||
		bytes.Contains(output.Bytes(), []byte("mutated_schema")) || bytes.Contains(output.Bytes(), []byte(urlSecret)) {
		t.Fatalf("PostgreSQL frozen projection = opener %d wire %q", openerCalls, output.Bytes())
	}
}

func TestArticleProjectRunnerSQLMigrateSelectionFailurePrecedenceAndRedaction(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	const secret = "selection-error-url-secret"
	config := articleProjectConfig(environmentLookup(map[string]string{
		databaseconfig.PostgresURLEnv: "postgres://article:" + secret + "@localhost/article",
	}))
	openerCalls := 0
	originalOpener := config.OpenMigrationBackend
	config.OpenMigrationBackend = func(ctx context.Context) (godjproject.MigrationBackend, error) {
		openerCalls++
		return originalOpener(ctx)
	}

	for _, test := range []struct {
		name string
		want sqlmigrateprotocol.Failure
	}{
		{
			name: "0000_missing",
			want: sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategoryPlan, Code: "target_not_found"},
		},
		{
			name: "0001_initial",
			want: sqlmigrateprotocol.Failure{Category: sqlmigrateprotocol.CategorySQLRender, Code: sqlmigrateprotocol.CodeRendererUnavailable},
		},
	} {
		document, encodeErr := sqlmigrateprotocol.EncodeRequest(sqlmigrateprotocol.Request{
			App: "godj_conformance", Name: test.name,
		})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		var output bytes.Buffer
		if runErr := godjproject.Run(
			context.Background(), config,
			[]string{sqlmigrateprotocol.PrivateArgument},
			bytes.NewReader(document), &output,
		); runErr != nil {
			t.Fatal(runErr)
		}
		response, failure, failed := sqlmigrateprotocol.ParseResponse(output.Bytes(), true)
		if failed || failure != (sqlmigrateprotocol.Failure{}) || response.OK || response.Failure != test.want {
			t.Fatalf("selection precedence %s = %+v, %+v, %v, want %+v", test.name, response, failure, failed, test.want)
		}
		if strings.Contains(output.String(), secret) {
			t.Fatalf("selection failure exposed secret: %q", output.String())
		}
	}
	if openerCalls != 0 {
		t.Fatalf("selection failure SQL path opened backend %d times", openerCalls)
	}
}

func environmentLookup(values map[string]string) databaseconfig.LookupEnvFunc {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
