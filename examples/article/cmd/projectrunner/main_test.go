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
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/settings/database"
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
			response.Result.Execute.SourceCount != 11 || response.Result.Execute.DefinitionCount != 11 {
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
	const wantPlanResponse = `{"protocol_version":3,"status":"ok","result":{"mode":"plan","plan":[]}}`
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
	want := []migrationbackend.AppliedMigration{
		{App: "godj_account_audit", Name: "0001_initial"},
		{App: "godj_account_domain", Name: "0001_initial"},
		{App: "godj_account_domain", Name: "0002_auto_fe206c392ab4", HistoryDigest: "sha256:20709649efac31941b23ac45cb246933ef40b9150c92f7233bcefcb56b49cdec"},
		{App: "godj_conformance", Name: "0001_initial"},
		{App: "godj_conformance", Name: "0002_alter_article_summary"},
		{App: "godj_conformance", Name: "0003_article_slug"},
		{App: "godj_identity", Name: "0001_initial"},
		{App: "godj_identity", Name: "0002_permission_revision", HistoryDigest: "sha256:9a64814a89c5562a96ea9f07bfeacb61a92e7d8056d5438a41f6239702edb630"},
		{App: "godj_identity", Name: "0003_alter_user_email"},
		{App: "godj_identity", Name: "0004_auto_1da4dbd173ec"},
		{App: "godj_sessions", Name: "0001_initial"},
	}
	if !reflect.DeepEqual(historyAfterPlan, want) {
		t.Fatalf("migration installation differs: %+v", historyAfterPlan)
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

func TestArticleProjectRunnerExecutesRegisteredHistoricalData(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.Mkdir("migrations", 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "article.sqlite3")
	config := articleProjectConfig(environmentLookup(map[string]string{databaseconfig.SQLiteDatabaseEnv: path}))
	key := migrations.DataKey{App: "issues", Name: "freeze", Version: 1}
	op := migrations.RunData{Key: key, Forward: migrations.DataCall, Reverse: migrations.DataIrreversible}
	document, err := definition.Encode(definition.Producer{Name: "issues", Version: "1"}, migrations.Migration{App: "issues", Name: "0001_freeze", Operations: []migrations.Operation{op}})
	if err != nil {
		t.Fatal(err)
	}
	config.MigrationDefinitionSources = []definition.Source{{SourceID: "embedded-issue-history", Document: document}}
	calls := 0
	registry, err := migrations.NewDataRegistry(migrations.DataRegistration{Key: key, Forward: func(ctx context.Context, models migrations.HistoricalModels) error {
		if _, err := models.Model(ctx, "issues", "ticket"); err == nil {
			return errors.New("unscoped model became available")
		}
		calls++
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	config.DataMigrations = registry
	call := func(mode migrateprotocol.Mode, target migrateprotocol.Target, wantOK bool) migrateprotocol.Response {
		t.Helper()
		request, err := migrateprotocol.EncodeRequest(migrateprotocol.Request{Mode: mode, Target: target})
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err = godjproject.Run(t.Context(), config, []string{migrateprotocol.PrivateArgument}, bytes.NewReader(request), &output)
		if err != nil {
			t.Fatal("project runner transport", err, output.String())
		}
		response, failure, failed := migrateprotocol.ParseResponse(output.Bytes(), true)
		if failed || response.OK != wantOK {
			t.Fatal("unexpected outcome", failure, output.String())
		}
		return response
	}
	latest := migrateprotocol.Target{Kind: migrateprotocol.TargetLatest}
	zero := migrateprotocol.Target{Kind: migrateprotocol.TargetZero, App: "issues"}
	if plan := call(migrateprotocol.ModePlan, latest, true).Result.Plan; len(plan) != 1 || plan[0].Irreversible || calls != 0 {
		t.Fatal("forward plan", plan, calls)
	}
	call(migrateprotocol.ModeExecute, latest, true)
	call(migrateprotocol.ModeExecute, latest, true)
	records := readArticleMigrationHistory(t, path)
	if calls != 1 || len(records) != 1 || records[0].HistoryDigest == "" {
		t.Fatal("registration or receipt", calls, records)
	}
	wantPlan := []migrateprotocol.PlanRow{{App: "issues", Name: "0001_freeze", Direction: migrateprotocol.DirectionBackward, Irreversible: true, IrreversibleOperation: 0}}
	if plan := call(migrateprotocol.ModePlan, zero, true).Result.Plan; !reflect.DeepEqual(plan, wantPlan) {
		t.Fatal("reverse plan", plan)
	}
	if failure := call(migrateprotocol.ModeExecute, zero, false).Failure; failure.Code != string(migrations.CodeIrreversibleData) {
		t.Fatal("reverse error", failure)
	}
	config.DataMigrations = migrations.DataRegistry{}
	if failure := call(migrateprotocol.ModePlan, latest, false).Failure; failure.Code != string(migrations.CodeMissingDataCallback) {
		t.Fatal("missing registration", failure)
	}
	if calls != 1 || !reflect.DeepEqual(records, readArticleMigrationHistory(t, path)) {
		t.Fatal("plan or rejected command changed durable history")
	}
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

func environmentLookup(values map[string]string) database.LookupEnvFunc {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
