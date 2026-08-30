package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
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
		size:   8010,
		sha256: "7074d37ffc5889d86374a14c528a6eeca0007c9a7789b1fc7ffbacbb2a776703",
	},
	migrationSQLRenderingBaselineArtifact: {
		size:   1727,
		sha256: "217e906548e57dab1020d6fcefcfb02700e6184001bc6aed204c557236f30144",
	},
	migrationSQLRenderingOracleArtifact: {
		size:   46941,
		sha256: "fa015cb0414709d0fc66d20d34776821fc2612ddac7702f8854141deb89abc99",
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

func TestMigrationSQLRenderingPublishedArtifactsAreLockedAndReferenceOnly(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	for name, want := range migrationSQLRenderingArtifactLocks {
		contents := mustReadMigrationSQLRenderingFile(t, filepath.Join(root, filepath.FromSlash(name)))
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
			contract.Status != ContractOracleLocked ||
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
				changed = mutateMigrationSQLRenderingValue(observation.Result)
			case CompareDBState:
				changed = mutateMigrationSQLRenderingValue(observation.DBState)
			case CompareMetrics:
				changed = mutateMigrationSQLRenderingValue(observation.Metrics)
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
	decision := string(mustReadMigrationSQLRenderingFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_sql_rendering_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "sqlite3", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(decision, forbidden) {
			t.Fatalf("migration-sql-rendering decision source crosses forbidden boundary %q", forbidden)
		}
	}

	djangoSource := string(mustReadMigrationSQLRenderingFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_sql_rendering_scenarios.py")))
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
		contents := string(mustReadMigrationSQLRenderingFile(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-sql-rendering source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
}

func TestMigrationSQLRenderingReferenceOnlyWiringIsExact(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	runner := string(mustReadMigrationSQLRenderingFile(t, filepath.Join(root, "conformance", "runners", "django", "runner.py")))
	for fragment, want := range map[string]int{
		"SCENARIOS as MIGRATION_SQL_RENDERING_DECISION_SCENARIOS": 1,
		"SCENARIOS as MIGRATION_SQL_RENDERING_DJANGO_SCENARIOS":   1,
		"MIGRATION_SQL_RENDERING_SCENARIOS = {":                   1,
		"    MIGRATION_SQL_RENDERING_SCENARIOS,":                  1,
		"DEFAULT_MIGRATION_SQL_RENDERING_MANIFEST = (":            1,
		"DEFAULT_MIGRATION_SQL_RENDERING_ORACLE = (":              1,
		"DEFAULT_MIGRATION_SQL_RENDERING_MANIFEST.resolve(): (":   1,
		"        DEFAULT_MIGRATION_SQL_RENDERING_ORACLE":          1,
	} {
		if got := strings.Count(runner, fragment); got != want {
			t.Fatalf("Django runner fragment %q count = %d, want %d", fragment, got, want)
		}
	}

	makeText := string(mustReadMigrationSQLRenderingFile(t, filepath.Join(root, "Makefile")))
	for variable, value := range map[string]string{
		"MIGRATION_SQL_RENDERING_MANIFEST":        migrationSQLRenderingManifestArtifact,
		"MIGRATION_SQL_RENDERING_ORACLE":          migrationSQLRenderingOracleArtifact,
		"MIGRATION_SQL_RENDERING_NOT_IMPLEMENTED": migrationSQLRenderingBaselineArtifact,
	} {
		definition := variable + " := " + value
		if got := strings.Count(makeText, definition); got != 1 {
			t.Fatalf("Makefile definition %q count = %d, want 1", definition, got)
		}
	}
	conformanceStart := strings.Index(makeText, "conformance-check:\n")
	productStart := strings.Index(makeText, "godj-conformance:\n")
	oracleCheckStart := strings.Index(makeText, "oracle-check:\n")
	oracleRegenerateStart := strings.Index(makeText, "oracle-regenerate:\n")
	ciStart := strings.Index(makeText, "\nci:")
	if conformanceStart < 0 || productStart <= conformanceStart || oracleCheckStart <= productStart || oracleRegenerateStart <= oracleCheckStart || ciStart <= oracleRegenerateStart {
		t.Fatal("cannot isolate Makefile conformance targets")
	}
	referenceTarget := makeText[conformanceStart:productStart]
	productTarget := makeText[productStart:oracleCheckStart]
	oracleCheckTarget := makeText[oracleCheckStart:oracleRegenerateStart]
	oracleRegenerateTarget := makeText[oracleRegenerateStart:ciStart]
	if got := strings.Count(referenceTarget, "$(MIGRATION_SQL_RENDERING_MANIFEST)"); got != 2 {
		t.Fatalf("reference migration-sql-rendering manifest count = %d, want oracle and NI", got)
	}
	if got := strings.Count(referenceTarget, "$(MIGRATION_SQL_RENDERING_ORACLE)"); got != 1 {
		t.Fatalf("reference migration-sql-rendering oracle count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "$(MIGRATION_SQL_RENDERING_NOT_IMPLEMENTED)"); got != 1 {
		t.Fatalf("reference migration-sql-rendering NI count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "go run ./conformance/cmd/contractcheck"); got != 54 {
		t.Fatalf("reference contractcheck count = %d, want 54", got)
	}
	for variable, want := range map[string]int{
		"$(MIGRATION_SQL_RENDERING_MANIFEST)":        0,
		"$(MIGRATION_SQL_RENDERING_ORACLE)":          0,
		"$(MIGRATION_SQL_RENDERING_NOT_IMPLEMENTED)": 0,
	} {
		if got := strings.Count(productTarget, variable); got != want {
			t.Fatalf("product migration-sql-rendering variable %s count = %d, want %d", variable, got, want)
		}
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 25 {
		t.Fatalf("product adapter count = %d, want 25 with migration-sql-rendering reference-only", got)
	}
	for name, target := range map[string]string{"oracle-check": oracleCheckTarget, "oracle-regenerate": oracleRegenerateTarget} {
		if got := strings.Count(target, "$(MIGRATION_SQL_RENDERING_MANIFEST)"); got != 1 {
			t.Fatalf("%s migration-sql-rendering manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "python -m conformance.runners.django"); got != 27 {
			t.Fatalf("%s reference runner count = %d, want 27", name, got)
		}
	}

	workflow := string(mustReadMigrationSQLRenderingFile(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	if got := strings.Count(workflow, migrationSQLRenderingBaselineArtifact); got != 2 {
		t.Fatalf("workflow migration-sql-rendering NI lock count = %d, want 2", got)
	}
	if strings.Contains(makeText, "MIGRATION_SQL_RENDERING_DEVIATION_EXPECTED") {
		t.Fatal("Phase A unexpectedly wires a migration-sql-rendering deviation fixture")
	}
	if _, err := os.Stat(filepath.Join(root, "conformance", "projectsqlmigrateproduct")); !os.IsNotExist(err) {
		t.Fatalf("Phase A product adapter exists or cannot be classified: %v", err)
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
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, filepath.FromSlash(migrationSQLRenderingManifestArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, filepath.FromSlash(migrationSQLRenderingOracleArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, filepath.FromSlash(migrationSQLRenderingBaselineArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func mustReadMigrationSQLRenderingFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func mutateMigrationSQLRenderingValue(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
	case ValueBool:
		*value.Bool = !*value.Bool
		return true
	case ValueInt:
		if *value.Text == "0" {
			*value.Text = "1"
		} else {
			*value.Text = "0"
		}
		return true
	case ValueString, ValueDecimal, ValueDatetime, ValueUUID, ValueBytes:
		*value.Text += "_changed"
		return true
	case ValuePK:
		return mutateMigrationSQLRenderingValue(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateMigrationSQLRenderingValue(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateMigrationSQLRenderingValue(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
