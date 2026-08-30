package protocol

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	migrationTargetPlanManifestArtifact = "conformance/contracts/migration-target-plan-manifest.json"
	migrationTargetPlanBaselineArtifact = "conformance/fixtures/godj-migration-target-plan-not-implemented.json"
	migrationTargetPlanOracleArtifact   = "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-target-plan-oracle.json"
)

type migrationTargetPlanArtifactLock struct {
	size   int
	sha256 string
}

type migrationTargetPlanProvenanceLock struct {
	kind      string
	reference string
	license   string
}

var migrationTargetPlanPhaseAArtifactLocks = map[string]migrationTargetPlanArtifactLock{
	migrationTargetPlanManifestArtifact: {
		size:   6781,
		sha256: "d76a42f2a0fb4daa190d03f18d18707192c8b42881b94a1462b701a9d481947b",
	},
	migrationTargetPlanBaselineArtifact: {
		size:   1707,
		sha256: "dfefb6fd6ca27e5e70dffea002fd07d801792ba7c6a83142dab18b969617bd44",
	},
	migrationTargetPlanOracleArtifact: {
		size:   43516,
		sha256: "dc688e27a727270594b32291e8cff83e1bd929af0a0fcd6fcf9b1f706dba9a7f",
	},
}

var migrationTargetPlanScenarios = []string{
	"godj.migration.target_plan.target_argv_and_pre_io_rejection",
	"django.migration.target_plan.named_forward_closure",
	"django.migration.target_plan.named_reverse_descendants",
	"django.migration.target_plan.app_zero_cross_app_dependents",
	"godj.migration.target_plan.target_noop_and_legacy_zero",
	"godj.migration.target_plan.plan_exact_and_no_mutation",
	"godj.migration.target_plan.preview_drift_fresh_execute",
	"godj.migration.target_plan.reverse_middle_failure_resume",
	"godj.migration.target_plan.reverse_commit_outcomes",
	"godj.migration.target_plan.project_protocol_and_ownership",
}

var migrationTargetPlanPhases = []Phase{
	PhaseEnvironment,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseCommit,
	PhaseRollback,
	PhaseCommit,
	PhaseEnvironment,
}

var migrationTargetPlanComparisons = [][]ComparisonDimension{
	{CompareResult, CompareMetrics},
	{CompareResult},
	{CompareResult},
	{CompareResult},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
}

func TestMigrationTargetPlanPhaseAStaticArtifactsAreLockedAndPayloadFree(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	for name, want := range migrationTargetPlanPhaseAArtifactLocks {
		assertMigrationTargetPlanArtifactLock(t, root, name, want)
	}

	profile, manifest, baseline := loadMigrationTargetPlanStaticArtifacts(t)
	if profile.ID != "django-6.1-sqlite-darwin-arm64" ||
		profile.Fingerprint.DjangoVersion != "6.1" ||
		profile.Fingerprint.DjangoCommit != "fe0a859f537d4238cf49fca39073513206f83122" ||
		profile.Fingerprint.PythonVersion != "3.14.3" ||
		profile.Fingerprint.SQLiteVersion != "3.50.4" ||
		profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected migration-target-plan profile: %#v", profile)
	}
	if !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("migration-target-plan baseline does not preserve the exact profile snapshot")
	}
	if len(manifest.Contracts) != 10 || len(baseline.Contracts) != 10 {
		t.Fatalf("migration-target-plan static lengths = %d/%d, want 10/10", len(manifest.Contracts), len(baseline.Contracts))
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+119)
		if contract.ID != wantID ||
			contract.Scenario != migrationTargetPlanScenarios[index] ||
			contract.Phase != migrationTargetPlanPhases[index] ||
			contract.Status != ContractOracleLocked ||
			!reflect.DeepEqual(contract.Comparison, migrationTargetPlanComparisons[index]) {
			t.Fatalf("migration-target-plan contract %d = %#v", index, contract)
		}
		assertMigrationTargetPlanProvenance(t, contract)

		locked := baseline.Contracts[index]
		if locked.ID != wantID ||
			locked.Phase != contract.Phase ||
			locked.Status != StatusNotImplemented ||
			locked.Result != nil ||
			locked.Error != nil ||
			locked.DBState != nil ||
			locked.Metrics != nil {
			t.Fatalf("migration-target-plan baseline contract %d is not payload-free: %#v", index, locked)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-target-plan baseline does not validate: %v", err)
	}
}

func TestMigrationTargetPlanOracleIsLockedValidatedAndCannotFalseGreen(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	oracleContents := mustReadMigrationTargetPlanFile(t, filepath.Join(root, filepath.FromSlash(migrationTargetPlanOracleArtifact)))
	wantLock, locked := migrationTargetPlanPhaseAArtifactLocks[migrationTargetPlanOracleArtifact]
	if !locked {
		t.Fatalf("generated migration-target-plan oracle has no exact size/SHA-256 lock in migrationTargetPlanPhaseAArtifactLocks")
	}
	assertMigrationTargetPlanArtifactContents(t, migrationTargetPlanOracleArtifact, oracleContents, wantLock)

	profile, manifest, baseline := loadMigrationTargetPlanStaticArtifacts(t)
	oracle, err := LoadObservationSuite(filepath.Join(root, filepath.FromSlash(migrationTargetPlanOracleArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) {
		t.Fatal("migration-target-plan oracle does not preserve the exact profile snapshot")
	}
	if len(oracle.Contracts) != 10 {
		t.Fatalf("migration-target-plan oracle contracts = %d, want 10", len(oracle.Contracts))
	}

	for index, contract := range manifest.Contracts {
		observation := oracle.Contracts[index]
		if observation.ID != contract.ID || observation.Phase != contract.Phase || observation.Status != StatusObserved || observation.Error != nil {
			t.Fatalf("migration-target-plan oracle contract %d = %#v", index, observation)
		}
		assertMigrationTargetPlanDeclaredPayloads(t, contract, observation)
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-target-plan oracle does not validate: %v", err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 10 {
		t.Fatalf("migration-target-plan oracle/baseline differences = %d, want 10: %#v", len(differences), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != fmt.Sprintf("MIG-%03d", index+119) ||
			difference.Path != "status" ||
			difference.Expected != string(StatusObserved) ||
			difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("migration-target-plan difference %d = %#v", index, difference)
		}
	}

	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneMigrationTargetPlanSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateMigrationTargetPlanValue(observation.Result)
			case CompareDBState:
				changed = mutateMigrationTargetPlanValue(observation.DBState)
			case CompareMetrics:
				changed = mutateMigrationTargetPlanValue(observation.Metrics)
			default:
				t.Fatalf("contract %s has unexpected comparison dimension %q", contract.ID, dimension)
			}
			if !changed {
				t.Fatalf("contract %s declares %s without a mutable oracle payload", contract.ID, dimension)
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

	reordered := cloneMigrationTargetPlanSuite(t, oracle)
	reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
	if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
		t.Fatal("migration-target-plan oracle contract reorder produced a false green")
	}
}

func TestMigrationTargetPlanAuthoritySourcesAreIndependentAndArtifactBlind(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	decisionPath := filepath.Join(root, "conformance", "runners", "django", "migration_target_plan_decisions.py")
	decision := string(mustReadMigrationTargetPlanFile(t, decisionPath))
	for _, forbidden := range []string{
		"from django", "import django", "sqlite3", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(decision, forbidden) {
			t.Fatalf("migration-target-plan decision source crosses forbidden boundary %q", forbidden)
		}
	}

	djangoPath := filepath.Join(root, "conformance", "runners", "django", "migration_target_plan_scenarios.py")
	djangoSource := string(mustReadMigrationTargetPlanFile(t, djangoPath))
	for _, forbidden := range []string{
		"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(djangoSource, forbidden) {
			t.Fatalf("migration-target-plan Django source crosses forbidden boundary %q", forbidden)
		}
	}
	for _, required := range []string{
		"planning._plan_case(",
		"django.migration.target_plan.named_forward_closure",
		"django.migration.target_plan.named_reverse_descendants",
		"django.migration.target_plan.app_zero_cross_app_dependents",
		"B1, A3, A2, A1",
		"DEV-0002",
	} {
		if !strings.Contains(djangoSource, required) {
			t.Fatalf("migration-target-plan Django authority does not preserve required fragment %q", required)
		}
	}
	for _, name := range []string{decisionPath, djangoPath} {
		contents := string(mustReadMigrationTargetPlanFile(t, name))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-target-plan source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
}

func TestMigrationTargetPlanDjangoOrderHazardMustRemainExplicit(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	for _, name := range []string{
		"work/0052-project-linked-targeted-migrate-plan-and-bounded-reverse.md",
		"docs/adr/0054-project-linked-targeted-migration-plan-and-reverse-safety.md",
	} {
		contents := string(mustReadMigrationTargetPlanFile(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, required := range []string{"DEV-0002", "B1, A3, A2, A1"} {
			if !strings.Contains(contents, required) {
				t.Fatalf("%s does not preserve the Django order hazard fragment %q", name, required)
			}
		}
		if !strings.Contains(contents, "incomparable") && !strings.Contains(contents, "비교 불가능") {
			t.Fatalf("%s does not identify the Django order as an incomparable-sibling hazard", name)
		}
	}
}

func TestMigrationTargetPlanPublishedReferenceWiringIsExact(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	runner := string(mustReadMigrationTargetPlanFile(t, filepath.Join(root, "conformance", "runners", "django", "runner.py")))
	for fragment, want := range map[string]int{
		"SCENARIOS as MIGRATION_TARGET_PLAN_DECISION_SCENARIOS": 1,
		"SCENARIOS as MIGRATION_TARGET_PLAN_DJANGO_SCENARIOS":   1,
		"MIGRATION_TARGET_PLAN_SCENARIOS = {":                   1,
		"    MIGRATION_TARGET_PLAN_SCENARIOS,":                  1,
		"DEFAULT_MIGRATION_TARGET_PLAN_MANIFEST = (":            1,
		"DEFAULT_MIGRATION_TARGET_PLAN_ORACLE = (":              1,
		"DEFAULT_MIGRATION_TARGET_PLAN_MANIFEST.resolve(): (":   1,
		"        DEFAULT_MIGRATION_TARGET_PLAN_ORACLE":          1,
	} {
		if got := strings.Count(runner, fragment); got != want {
			t.Fatalf("Django runner fragment %q count = %d, want %d", fragment, got, want)
		}
	}

	makeText := string(mustReadMigrationTargetPlanFile(t, filepath.Join(root, "Makefile")))
	for variable, value := range map[string]string{
		"MIGRATION_TARGET_PLAN_MANIFEST":        migrationTargetPlanManifestArtifact,
		"MIGRATION_TARGET_PLAN_ORACLE":          migrationTargetPlanOracleArtifact,
		"MIGRATION_TARGET_PLAN_NOT_IMPLEMENTED": migrationTargetPlanBaselineArtifact,
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
	for variable, want := range map[string]int{
		"$(MIGRATION_TARGET_PLAN_MANIFEST)":        2,
		"$(MIGRATION_TARGET_PLAN_ORACLE)":          1,
		"$(MIGRATION_TARGET_PLAN_NOT_IMPLEMENTED)": 1,
	} {
		if got := strings.Count(referenceTarget, variable); got != want {
			t.Fatalf("reference target variable %s count = %d, want %d", variable, got, want)
		}
	}
	for variable, want := range map[string]int{
		"$(MIGRATION_TARGET_PLAN_MANIFEST)":        0,
		"$(MIGRATION_TARGET_PLAN_ORACLE)":          0,
		"$(MIGRATION_TARGET_PLAN_NOT_IMPLEMENTED)": 0,
	} {
		if got := strings.Count(productTarget, variable); got != want {
			t.Fatalf("product target variable %s count = %d, want %d", variable, got, want)
		}
	}
	for name, target := range map[string]string{"oracle-check": oracleCheckTarget, "oracle-regenerate": oracleRegenerateTarget} {
		if got := strings.Count(target, "$(MIGRATION_TARGET_PLAN_MANIFEST)"); got != 1 {
			t.Fatalf("%s migration-target-plan manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "$(MIGRATION_TARGET_PLAN_ORACLE)"); got != 1 {
			t.Fatalf("%s migration-target-plan oracle count = %d, want 1", name, got)
		}
	}

	workflow := string(mustReadMigrationTargetPlanFile(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	if got := strings.Count(workflow, migrationTargetPlanBaselineArtifact); got != 2 {
		t.Fatalf("workflow migration-target-plan NI lock count = %d, want 2", got)
	}
}

func assertMigrationTargetPlanProvenance(t *testing.T, contract Contract) {
	t.Helper()

	want := []migrationTargetPlanProvenanceLock{
		{kind: "proposal", reference: "GDJ-0052"},
		{kind: "documentation", reference: "ADR-0054"},
	}
	const djangoRevision = "django@fe0a859f537d4238cf49fca39073513206f83122:"
	switch contract.ID {
	case "MIG-120":
		want = append(want,
			migrationTargetPlanProvenanceLock{"source", djangoRevision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan", "BSD-3-Clause"},
			migrationTargetPlanProvenanceLock{"test", djangoRevision + "tests/migrations/test_executor.py::ExecutorTests.test_run", "BSD-3-Clause"},
		)
	case "MIG-121":
		want = append(want,
			migrationTargetPlanProvenanceLock{"source", djangoRevision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan", "BSD-3-Clause"},
			migrationTargetPlanProvenanceLock{"test", djangoRevision + "tests/migrations/test_executor.py::ExecutorUnitTests.test_minimize_rollbacks_branchy", "BSD-3-Clause"},
			migrationTargetPlanProvenanceLock{"test", djangoRevision + "tests/migrations/test_executor.py::ExecutorTests.test_unrelated_applied_migrations_mutate_state", "BSD-3-Clause"},
		)
	case "MIG-122":
		want = append(want,
			migrationTargetPlanProvenanceLock{"source", djangoRevision + "django/db/migrations/executor.py::MigrationExecutor.migration_plan", "BSD-3-Clause"},
			migrationTargetPlanProvenanceLock{"test", djangoRevision + "tests/migrations/test_executor.py::ExecutorTests.test_run", "BSD-3-Clause"},
		)
	}
	if len(contract.Provenance) != len(want) {
		t.Fatalf("contract %s provenance = %#v, want %d entries", contract.ID, contract.Provenance, len(want))
	}
	for index, expected := range want {
		got := contract.Provenance[index]
		if got.Kind != expected.kind || got.Reference != expected.reference || got.License != expected.license || got.Derived == nil || *got.Derived {
			t.Fatalf("contract %s provenance %d = %#v, want %#v derived=false", contract.ID, index, got, expected)
		}
	}
}

func assertMigrationTargetPlanDeclaredPayloads(t *testing.T, contract Contract, observation Observation) {
	t.Helper()

	wantResult, wantDBState, wantMetrics := false, false, false
	for _, dimension := range contract.Comparison {
		switch dimension {
		case CompareResult:
			wantResult = true
		case CompareDBState:
			wantDBState = true
		case CompareMetrics:
			wantMetrics = true
		}
	}
	if (observation.Result != nil) != wantResult ||
		(observation.DBState != nil) != wantDBState ||
		(observation.Metrics != nil) != wantMetrics {
		t.Fatalf("contract %s payloads result/db_state/metrics = %t/%t/%t, want %t/%t/%t",
			contract.ID,
			observation.Result != nil,
			observation.DBState != nil,
			observation.Metrics != nil,
			wantResult,
			wantDBState,
			wantMetrics,
		)
	}
}

func assertMigrationTargetPlanArtifactLock(t *testing.T, root, name string, want migrationTargetPlanArtifactLock) {
	t.Helper()
	contents := mustReadMigrationTargetPlanFile(t, filepath.Join(root, filepath.FromSlash(name)))
	assertMigrationTargetPlanArtifactContents(t, name, contents, want)
}

func assertMigrationTargetPlanArtifactContents(t *testing.T, name string, contents []byte, want migrationTargetPlanArtifactLock) {
	t.Helper()
	if len(contents) != want.size {
		t.Fatalf("migration-target-plan artifact %s size = %d, want %d", name, len(contents), want.size)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
		t.Fatalf("migration-target-plan artifact %s sha256 = %q, want %q", name, got, want.sha256)
	}
}

func loadMigrationTargetPlanStaticArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, filepath.FromSlash(migrationTargetPlanManifestArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, filepath.FromSlash(migrationTargetPlanBaselineArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, baseline
}

func mustReadMigrationTargetPlanFile(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func cloneMigrationTargetPlanSuite(t *testing.T, suite ObservationSuite) ObservationSuite {
	t.Helper()
	document, err := json.Marshal(suite)
	if err != nil {
		t.Fatal(err)
	}
	var cloned ObservationSuite
	if err := json.Unmarshal(document, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func mutateMigrationTargetPlanValue(value *Value) bool {
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
		return mutateMigrationTargetPlanValue(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateMigrationTargetPlanValue(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateMigrationTargetPlanValue(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
