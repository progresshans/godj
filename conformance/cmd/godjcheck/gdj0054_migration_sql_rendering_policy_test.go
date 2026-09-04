package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	godjrunner "github.com/progresshans/godj/conformance/runners/godj"
)

var gdj0054MigrationSQLRenderingContractIDs = []string{
	"MIG-129", "MIG-130", "MIG-131", "MIG-132", "MIG-133",
	"MIG-134", "MIG-135", "MIG-136", "MIG-137", "MIG-138",
}

func TestRunGDJ0054StrictProductExpectationWritesTenActuals(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	manifestPath := filepath.Join(root, "conformance", "contracts", "migration-sql-rendering-manifest.json")
	oraclePath := filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-sql-rendering-oracle.json")
	actualPath := filepath.Join(t.TempDir(), "migration-sql-rendering-actual.json")

	manifest, err := protocol.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Contracts) != len(gdj0054MigrationSQLRenderingContractIDs) {
		t.Fatalf("migration-sql-rendering manifest count = %d, want %d", len(manifest.Contracts), len(gdj0054MigrationSQLRenderingContractIDs))
	}
	for index, wantID := range gdj0054MigrationSQLRenderingContractIDs {
		contract := manifest.Contracts[index]
		if contract.ID != wantID || contract.Status != protocol.ContractPassing {
			t.Fatalf("migration-sql-rendering manifest contract %d = %s/%s, want %s/passing", index, contract.ID, contract.Status, wantID)
		}
	}
	required, err := godjrunner.RequiredObservedContractIDs(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !gdj0054ExactContractIDs(required) {
		t.Fatalf("migration-sql-rendering required product contracts = %v, want exact %v", required, gdj0054MigrationSQLRenderingContractIDs)
	}

	arguments := []string{
		"-profile", filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"),
		"-manifest", manifestPath,
		"-expected", oraclePath,
		"-actual-output", actualPath,
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "GoDj observations match the locked Django oracle for 10 contracts\n" {
		t.Fatalf("stdout = %q, want exact success publication", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	document, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"migration-sql-rendering-manifest.json",
		"migration-sql-rendering-oracle.json",
		"godj-migration-sql-rendering-not-implemented.json",
		"migration_sql_rendering_decisions.py",
		"migration_sql_rendering_scenarios.py",
		"django/core/management/commands/sqlmigrate.py",
		"django/db/migrations/loader.py",
		"django/db/migrations/migration.py",
		"django/db/migrations/operations/fields.py",
		"django/db/migrations/operations/models.py",
		"django/db/backends/base/schema.py",
		"django/db/backends/sqlite3/schema.py",
		"tests/migrations/test_commands.py",
		"sqlmigrate-secret-canary-81ae0d75",
		"sqlmigrate-db-path-canary-529d",
		"sqlite-secret-path-74c3.sqlite3",
		"PARTIAL_SQL_MUST_NOT_BE_PUBLISHED_1f9da742",
		"postgres://sqlmigrate_user:",
		"injected renderer failure",
	} {
		if bytes.Contains(document, []byte(forbidden)) {
			t.Fatalf("GDJ-0054 actual artifact contains forbidden source, artifact, or secret boundary %q", forbidden)
		}
	}

	actual, err := protocol.LoadObservationSuite(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(actual.Contracts) != len(gdj0054MigrationSQLRenderingContractIDs) {
		t.Fatalf("migration-sql-rendering actual count = %d, want %d", len(actual.Contracts), len(gdj0054MigrationSQLRenderingContractIDs))
	}
	for index, wantID := range gdj0054MigrationSQLRenderingContractIDs {
		observation := actual.Contracts[index]
		if observation.ID != wantID || observation.Status != protocol.StatusObserved || observation.Phase != manifest.Contracts[index].Phase {
			t.Fatalf("migration-sql-rendering actual contract %d = %s/%s/%s, want %s/observed/%s", index, observation.ID, observation.Status, observation.Phase, wantID, manifest.Contracts[index].Phase)
		}
	}
}

func TestGDJ0054HistoricalNotImplementedArtifactRemainsPayloadFreeReference(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	baseline, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-sql-rendering-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Contracts) != len(gdj0054MigrationSQLRenderingContractIDs) {
		t.Fatalf("historical migration-sql-rendering baseline count = %d, want %d", len(baseline.Contracts), len(gdj0054MigrationSQLRenderingContractIDs))
	}
	for index, wantID := range gdj0054MigrationSQLRenderingContractIDs {
		observation := baseline.Contracts[index]
		if observation.ID != wantID || observation.Status != protocol.StatusNotImplemented || observation.Result != nil || observation.Error != nil || observation.DBState != nil || observation.Metrics != nil {
			t.Fatalf("historical migration-sql-rendering baseline contract %d = %#v, want %s payload-free not_implemented", index, observation, wantID)
		}
	}
}

func gdj0054ExactContractIDs(got []string) bool {
	if len(got) != len(gdj0054MigrationSQLRenderingContractIDs) {
		return false
	}
	for index, want := range gdj0054MigrationSQLRenderingContractIDs {
		if got[index] != want {
			return false
		}
	}
	return true
}
