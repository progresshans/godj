//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/databaseconfig"
	"github.com/progresshans/godj/internal/projectcheck/migrateprotocol"
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
		if response.Result.SourceCount != 2 || response.Result.DefinitionCount != 2 {
			t.Fatalf("project migrate invocation %d result=%+v", invocation+1, response.Result)
		}
		if invocation == 0 {
			firstDigest = response.Result.DefinitionSetDigest
		} else if response.Result.DefinitionSetDigest != firstDigest {
			t.Fatalf("second invocation digest=%q, want %q", response.Result.DefinitionSetDigest, firstDigest)
		}
	}

	backend, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close migrated Article backend: %v", err)
		}
	}()
	session, err := backend.OpenRevisionFencedSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	records, readErr := session.ReadAppliedMigrations(context.Background())
	closeErr := session.Close(context.Background())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if len(records) != 2 || records[0].App != "godj_conformance" || records[0].Name != "0001_initial" ||
		records[1].App != "godj_system" || records[1].Name != "0001_initial" {
		t.Fatalf("second invocation history=%+v", records)
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

func environmentLookup(values map[string]string) databaseconfig.LookupEnvFunc {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
