//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
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

func environmentLookup(values map[string]string) databaseconfig.LookupEnvFunc {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
