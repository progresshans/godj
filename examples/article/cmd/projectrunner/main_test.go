//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/examples/article/internal/operatorconfig"
	"github.com/progresshans/godj/internal/projectcheck/createsuperuserprotocol"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
	"github.com/progresshans/godj/internal/projectcheck/sqlmigrateprotocol"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	godjproject "github.com/progresshans/godj/project"
	"github.com/progresshans/godj/systemstate"
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

func TestArticleProjectRunnerExplicitlyProvisionsAndOpensWithOneSharedPolicy(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	const (
		username = "project-runner-admin"
		password = "project-runner-provision-secret"
	)
	databasePath := filepath.Join(t.TempDir(), "article-operator.sqlite3")
	config := articleProjectConfig(environmentLookup(map[string]string{
		databaseconfig.SQLiteDatabaseEnv: databasePath,
	}))
	if config.OpenMigrationBackend == nil || config.OpenSystemStateBackend == nil ||
		config.SystemOperatorPolicy.Principal.ID() != operatorconfig.PrincipalID {
		t.Fatalf("Article project config does not expose independent migration/system-state ownership: %+v", config)
	}

	var migrateOutput bytes.Buffer
	if err := godjproject.Run(
		context.Background(),
		config,
		[]string{migrateprotocol.PrivateArgument},
		bytes.NewReader(migrateprotocol.RequestDocument()),
		&migrateOutput,
	); err != nil {
		t.Fatalf("project migrate: %v", err)
	}
	if response, failure, failed := migrateprotocol.ParseResponse(migrateOutput.Bytes(), true); failed ||
		failure != (migrateprotocol.Failure{}) || !response.OK {
		t.Fatalf("project migrate response = %+v, %+v, %t", response, failure, failed)
	}

	request, err := createsuperuserprotocol.EncodeRequest(createsuperuserprotocol.Request{
		Username: []byte(username),
		Password: []byte(password),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(request)
	var provisionOutput bytes.Buffer
	if err := godjproject.Run(
		context.Background(),
		config,
		[]string{createsuperuserprotocol.PrivateArgument},
		bytes.NewReader(request),
		&provisionOutput,
	); err != nil {
		t.Fatalf("project createsuperuser: %v", err)
	}
	response, failure, failed := createsuperuserprotocol.ParseResponse(provisionOutput.Bytes(), true)
	if failed || failure != (createsuperuserprotocol.Failure{}) || !response.OK || !response.Created {
		t.Fatalf("project createsuperuser response = %+v, %+v, %t", response, failure, failed)
	}
	if strings.Contains(provisionOutput.String(), password) {
		t.Fatalf("private response exposed raw password: %q", provisionOutput.String())
	}

	backend, err := config.OpenSystemStateBackend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close reopened system-state backend: %v", err)
		}
	}()
	runtimeConfig, err := operatorconfig.RuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := systemstate.OpenExisting(context.Background(), backend, runtimeConfig)
	if err != nil {
		t.Fatalf("OpenExisting(): %v", err)
	}
	principal, err := runtime.Authenticator().Authenticate(context.Background(), username, password)
	if err != nil || principal.ID() != operatorconfig.PrincipalID {
		t.Fatalf("raw-password-free reopened authentication = principal %q error %v", principal.ID(), err)
	}
}

func TestArticleProjectRunnerMainPreservesKnownCreatedExitOnBrokenPrivateStdout(t *testing.T) {
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(projectRoot)

	const (
		username = "project-runner-broken-output-admin"
		password = "project-runner-broken-output-secret"
	)
	databasePath := filepath.Join(t.TempDir(), "article-runner-broken-output.sqlite3")
	config := articleProjectConfig(environmentLookup(map[string]string{
		databaseconfig.SQLiteDatabaseEnv: databasePath,
	}))
	var migrateOutput bytes.Buffer
	if err := godjproject.Run(
		context.Background(),
		config,
		[]string{migrateprotocol.PrivateArgument},
		bytes.NewReader(migrateprotocol.RequestDocument()),
		&migrateOutput,
	); err != nil {
		t.Fatalf("project migrate: %v", err)
	}
	if response, failure, failed := migrateprotocol.ParseResponse(migrateOutput.Bytes(), true); failed ||
		failure != (migrateprotocol.Failure{}) || !response.OK {
		t.Fatalf("project migrate response = %+v, %+v, %t", response, failure, failed)
	}

	binary := filepath.Join(t.TempDir(), "article-project-runner")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "./cmd/projectrunner")
	build.Dir = projectRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build canonical Article project runner: %v; output bytes=%d", err, len(output))
	}
	request, err := createsuperuserprotocol.EncodeRequest(createsuperuserprotocol.Request{
		Username: []byte(username),
		Password: []byte(password),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(request)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		_ = writer.Close()
		t.Fatal(err)
	}
	command := exec.Command(binary, createsuperuserprotocol.PrivateArgument)
	command.Dir = projectRoot
	command.Env = articleProjectRunnerSQLiteEnvironment(databasePath)
	command.Stdin = bytes.NewReader(request)
	command.Stdout = writer
	var stderr bytes.Buffer
	command.Stderr = &stderr
	runErr := command.Run()
	_ = writer.Close()
	var exitError *exec.ExitError
	if !errors.As(runErr, &exitError) || exitError.ExitCode() != createsuperuserprotocol.KnownCreatedResponseFailureExitCode ||
		exitError.ProcessState == nil || !exitError.ProcessState.Exited() || stderr.Len() != 0 {
		t.Fatalf("canonical Article broken-output exit = %v stderr-bytes=%d", runErr, stderr.Len())
	}

	backend, err := config.OpenSystemStateBackend(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close reconciled Article system-state backend: %v", err)
		}
	}()
	runtimeConfig, err := operatorconfig.RuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := systemstate.OpenExisting(context.Background(), backend, runtimeConfig)
	if err != nil {
		t.Fatalf("OpenExisting after private stdout loss: %v", err)
	}
	principal, err := runtime.Authenticator().Authenticate(context.Background(), username, password)
	if err != nil || principal.ID() != operatorconfig.PrincipalID {
		t.Fatalf("broken-output reconciliation principal=%q error=%v", principal.ID(), err)
	}
}

func articleProjectRunnerSQLiteEnvironment(databasePath string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok && (key == databaseconfig.SQLiteDatabaseEnv || key == databaseconfig.PostgresURLEnv || key == databaseconfig.PostgresSchemaEnv) {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, databaseconfig.SQLiteDatabaseEnv+"="+databasePath)
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
