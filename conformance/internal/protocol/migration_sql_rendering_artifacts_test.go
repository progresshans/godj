package protocol

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	migrationSQLRenderingManifestArtifact = "conformance/contracts/migration-sql-rendering-manifest.json"
	migrationSQLRenderingBaselineArtifact = "conformance/fixtures/godj-migration-sql-rendering-not-implemented.json"
	migrationSQLRenderingOracleArtifact   = "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-sql-rendering-oracle.json"
)

type migrationSQLRenderingArtifactLock struct {
	size   int
	sha256 string
}

var migrationSQLRenderingArtifactLocks = map[string]migrationSQLRenderingArtifactLock{
	migrationSQLRenderingManifestArtifact: {
		size:   7950,
		sha256: "fb737465cabf955fced0e04f52d5d2a89b6c00a2646b3a4e339eae37d6f084b9",
	},
	migrationSQLRenderingBaselineArtifact: {
		size:   1727,
		sha256: "217e906548e57dab1020d6fcefcfb02700e6184001bc6aed204c557236f30144",
	},
	migrationSQLRenderingOracleArtifact: {
		size:   47337,
		sha256: "0d51318daf8c26aa58d8f10b49234f032fcc90c147743a41ca6e0d053c2921df",
	},
}

var migrationSQLRenderingScenarios = []string{
	"godj.migration.sql_rendering.argv_and_pre_io_rejection",
	"godj.migration.sql_rendering.complete_load_exact_lookup_and_request",
	"django.migration.sql_rendering.forward_before_state_order",
	"django.migration.sql_rendering.sqlite_create_add_semantics",
	"godj.migration.sql_rendering.postgres_current_projection",
	"godj.migration.sql_rendering.canonical_deterministic_output",
	"godj.migration.sql_rendering.database_and_history_zero_calls",
	"godj.migration.sql_rendering.renderer_and_operation_fail_closed",
	"godj.migration.sql_rendering.resource_cleanup_redaction_and_write",
	"godj.migration.sql_rendering.external_project_configuration",
}

var migrationSQLRenderingPhases = []Phase{
	PhaseEnvironment,
	PhaseConstruction,
	PhaseConstruction,
	PhaseConstruction,
	PhaseConstruction,
	PhaseEvaluation,
	PhaseEnvironment,
	PhaseEvaluation,
	PhaseEnvironment,
	PhaseEnvironment,
}

var migrationSQLRenderingComparisons = [][]ComparisonDimension{
	{CompareResult, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult},
	{CompareResult},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
}

func TestMigrationSQLRenderingPublishedArtifactsAreLockedAndProductEligible(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	for name, want := range migrationSQLRenderingArtifactLocks {
		contents := readArtifact(t, filepath.Join(root, filepath.FromSlash(name)))
		if len(contents) != want.size {
			t.Fatalf("migration-sql-rendering artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
			t.Fatalf("migration-sql-rendering artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
	if _, locked := migrationSQLRenderingArtifactLocks[migrationSQLRenderingOracleArtifact]; !locked {
		t.Fatal("migration-sql-rendering oracle has no exact byte lock")
	}

	profile, manifest, oracle, baseline := loadMigrationSQLRenderingArtifacts(t)
	if profile.ID != "django-6.1-sqlite-darwin-arm64" ||
		profile.Fingerprint.DjangoVersion != "6.1" ||
		profile.Fingerprint.DjangoCommit != "fe0a859f537d4238cf49fca39073513206f83122" ||
		profile.Fingerprint.PythonVersion != "3.14.3" ||
		profile.Fingerprint.SQLiteVersion != "3.50.4" ||
		profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected migration-sql-rendering profile: %#v", profile)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("migration-sql-rendering suites do not preserve the exact profile snapshot")
	}
	if len(manifest.Contracts) != 10 || len(oracle.Contracts) != 10 || len(baseline.Contracts) != 10 {
		t.Fatalf("migration-sql-rendering lengths = %d/%d/%d, want 10/10/10", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+129)
		if contract.ID != wantID ||
			contract.Scenario != migrationSQLRenderingScenarios[index] ||
			contract.Phase != migrationSQLRenderingPhases[index] ||
			contract.Status != ContractPassing ||
			!reflect.DeepEqual(contract.Comparison, migrationSQLRenderingComparisons[index]) {
			t.Fatalf("migration-sql-rendering contract %d = %#v", index, contract)
		}
		assertMigrationSQLRenderingProvenance(t, contract)

		observed := oracle.Contracts[index]
		if observed.ID != wantID || observed.Phase != contract.Phase || observed.Status != StatusObserved {
			t.Fatalf("migration-sql-rendering oracle contract %d = %#v", index, observed)
		}
		assertMigrationSQLRenderingDeclaredPayloads(t, contract, observed)

		locked := baseline.Contracts[index]
		if locked.ID != wantID ||
			locked.Phase != contract.Phase ||
			locked.Status != StatusNotImplemented ||
			locked.Result != nil ||
			locked.Error != nil ||
			locked.DBState != nil ||
			locked.Metrics != nil {
			t.Fatalf("migration-sql-rendering baseline contract %d is not payload-free: %#v", index, locked)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-sql-rendering oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-sql-rendering baseline does not validate: %v", err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 10 {
		t.Fatalf("migration-sql-rendering oracle/baseline differences = %d, want 10: %#v", len(differences), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != fmt.Sprintf("MIG-%03d", index+129) ||
			difference.Path != "status" ||
			difference.Expected != string(StatusObserved) ||
			difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("migration-sql-rendering difference %d = %#v", index, difference)
		}
	}
}

func TestMigrationSQLRenderingDeclaredDimensionsAndBindingsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationSQLRenderingArtifacts(t)
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateFirstValue(observation.Result)
			case CompareDBState:
				changed = mutateFirstValue(observation.DBState)
			case CompareMetrics:
				changed = mutateFirstValue(observation.Metrics)
			default:
				t.Fatalf("contract %s unexpected comparison dimension %q", contract.ID, dimension)
			}
			if !changed {
				t.Fatalf("contract %s declared %s without mutable payload", contract.ID, dimension)
			}
			differences, err := Compare(profile, manifest, oracle, actual)
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) == 0 {
				t.Fatalf("contract %s %s mutation produced a false green", contract.ID, dimension)
			}
			for _, difference := range differences {
				if difference.ContractID != contract.ID {
					t.Fatalf("contract %s %s mutation reported against %s", contract.ID, dimension, difference.ContractID)
				}
			}
		}
	}

	reordered := cloneSuite(t, oracle)
	reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
	if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
		t.Fatal("migration-sql-rendering oracle reorder produced a false green")
	}
}

func TestMigrationSQLRenderingAuthoritySourcesAreIndependentAndArtifactBlind(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	decision := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_sql_rendering_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "sqlite3", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(decision, forbidden) {
			t.Fatalf("migration-sql-rendering decision source crosses forbidden boundary %q", forbidden)
		}
	}

	djangoSource := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_sql_rendering_scenarios.py")))
	for _, forbidden := range []string{
		"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(djangoSource, forbidden) {
			t.Fatalf("migration-sql-rendering Django source crosses forbidden boundary %q", forbidden)
		}
	}
	for _, required := range []string{
		"output = command.handle(",
		"statements = original_collect_sql(materialized_plan)",
		"operation.database_forwards = observed_database_forwards",
		"CreateModel(",
		"AddField(",
	} {
		if !strings.Contains(djangoSource, required) {
			t.Fatalf("migration-sql-rendering Django source does not execute authority boundary %q", required)
		}
	}
	for _, name := range []string{
		migrationSQLRenderingOracleArtifact,
		"conformance/runners/django/migration_sql_rendering_decisions.py",
		"conformance/runners/django/migration_sql_rendering_scenarios.py",
	} {
		contents := string(readArtifact(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-sql-rendering source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
}

func assertMigrationSQLRenderingDeclaredPayloads(t *testing.T, contract Contract, observation Observation) {
	t.Helper()
	want := map[ComparisonDimension]bool{}
	for _, dimension := range contract.Comparison {
		want[dimension] = true
	}
	got := map[ComparisonDimension]bool{
		CompareResult:  observation.Result != nil,
		CompareError:   observation.Error != nil,
		CompareDBState: observation.DBState != nil,
		CompareMetrics: observation.Metrics != nil,
	}
	for _, dimension := range []ComparisonDimension{CompareResult, CompareError, CompareDBState, CompareMetrics} {
		if got[dimension] != want[dimension] {
			t.Fatalf("contract %s payload dimension %s = %v, want %v", contract.ID, dimension, got[dimension], want[dimension])
		}
	}
}

func assertMigrationSQLRenderingProvenance(t *testing.T, contract Contract) {
	t.Helper()
	want := []migrationSQLRenderingProvenance{
		{kind: "proposal", reference: "GDJ-0054"},
		{kind: "documentation", reference: "ADR-0055"},
	}
	switch contract.ID {
	case "MIG-131":
		want = append(want,
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/core/management/commands/sqlmigrate.py::Command.handle", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/loader.py::MigrationLoader.project_state", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/loader.py::MigrationLoader.collect_sql", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/migration.py::Migration.apply", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/migrations/test_commands.py::MigrateTests.test_sqlmigrate_forwards", license: "BSD-3-Clause"},
		)
	case "MIG-132":
		want = append(want,
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/loader.py::MigrationLoader.collect_sql", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/migration.py::Migration.apply", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/operations/models.py::CreateModel.database_forwards", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/migrations/operations/fields.py::AddField.database_forwards", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/backends/base/schema.py::BaseDatabaseSchemaEditor.create_model", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/backends/base/schema.py::BaseDatabaseSchemaEditor.add_field", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "source", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:django/db/backends/sqlite3/schema.py::DatabaseSchemaEditor.add_field", license: "BSD-3-Clause"},
			migrationSQLRenderingProvenance{kind: "test", reference: "django@fe0a859f537d4238cf49fca39073513206f83122:tests/migrations/test_commands.py::MigrateTests.test_sqlmigrate_forwards", license: "BSD-3-Clause"},
		)
	}
	if len(contract.Provenance) != len(want) {
		t.Fatalf("contract %s provenance count = %d, want %d: %#v", contract.ID, len(contract.Provenance), len(want), contract.Provenance)
	}
	for index, expected := range want {
		actual := contract.Provenance[index]
		if actual.Kind != expected.kind ||
			actual.Reference != expected.reference ||
			actual.License != expected.license ||
			actual.Derived == nil || *actual.Derived {
			t.Fatalf("contract %s provenance %d = %#v, want %#v", contract.ID, index, actual, expected)
		}
	}
}

type migrationSQLRenderingProvenance struct {
	kind      string
	reference string
	license   string
}

func loadMigrationSQLRenderingArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile := requireArtifact(t, filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"), LoadProfile)
	manifest := requireArtifact(t, filepath.Join(root, filepath.FromSlash(migrationSQLRenderingManifestArtifact)), LoadManifest)
	oracle := requireArtifact(t, filepath.Join(root, filepath.FromSlash(migrationSQLRenderingOracleArtifact)), LoadObservationSuite)
	baseline := requireArtifact(t, filepath.Join(root, filepath.FromSlash(migrationSQLRenderingBaselineArtifact)), LoadObservationSuite)
	return profile, manifest, oracle, baseline
}
