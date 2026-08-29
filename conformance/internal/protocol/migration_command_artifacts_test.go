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

var migrationCommandScenarios = []string{
	"godj.migration.command.fresh_latest",
	"godj.migration.command.applied_prefix_tail",
	"godj.migration.command.fully_applied_fresh_noop",
	"godj.migration.command.definition_preflight_before_backend",
	"godj.migration.command.inconsistent_history_preflight",
	"godj.migration.command.capability_preflight_before_begin",
	"godj.migration.command.middle_failure_durable_prefix",
	"godj.migration.command.fresh_resume_after_failure",
	"godj.migration.command.commit_outcome_unknown",
	"godj.migration.command.concurrent_latest_fenced",
	"godj.migration.command.backend_configuration_secret_boundary",
	"godj.migration.command.interrupt_rollback_cleanup",
}

var migrationCommandPhases = []Phase{
	PhaseCommit,
	PhaseCommit,
	PhaseCommit,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseRollback,
	PhaseCommit,
	PhaseCommit,
	PhaseCommit,
	PhaseEnvironment,
	PhaseRollback,
}

var migrationCommandComparisons = [][]ComparisonDimension{
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareError, CompareDBState, CompareMetrics},
	{CompareError, CompareDBState, CompareMetrics},
	{CompareError, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareError, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareError, CompareDBState, CompareMetrics},
}

func TestMigrationCommandArtifactsAreLockedValidatedAndPayloadSafe(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	type artifactLock struct {
		size   int
		sha256 string
	}
	for name, want := range map[string]artifactLock{
		"conformance/contracts/migration-command-manifest.json": {
			size:   6166,
			sha256: "d2846327e4d8cbf82a25568e41b198c67878bb7853958729969eb7077ca4c0e1",
		},
		"conformance/fixtures/godj-migration-command-not-implemented.json": {
			size:   1838,
			sha256: "8680d5e8ce7cf11604af69da1e96a64f580f64074277a2a015af8ad250bb0016",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-command-oracle.json": {
			size:   12690,
			sha256: "30b1b5c109c9da98a3fce2236ee9faf1f6fe9f4ae31ebdd640b74728160313ee",
		},
	} {
		contents := mustReadMigrationCommandFile(t, filepath.Join(root, filepath.FromSlash(name)))
		if len(contents) != want.size {
			t.Fatalf("migration-command artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
			t.Fatalf("migration-command artifact %s sha256 = %q, want %q", name, got, want.sha256)
		}
	}

	profile, manifest, oracle, baseline := loadMigrationCommandArtifacts(t)
	if profile.ID != "django-6.1-sqlite-darwin-arm64" || profile.Fingerprint.DjangoVersion != "6.1" || profile.Fingerprint.PythonVersion != "3.14.3" || profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected migration-command profile: %#v", profile)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("migration-command suites do not preserve the exact profile snapshot")
	}
	if len(manifest.Contracts) != 12 || len(oracle.Contracts) != 12 || len(baseline.Contracts) != 12 {
		t.Fatalf("migration-command lengths = %d/%d/%d, want 12/12/12", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+87)
		if contract.ID != wantID || contract.Scenario != migrationCommandScenarios[index] || contract.Phase != migrationCommandPhases[index] || contract.Status != ContractPassing || !reflect.DeepEqual(contract.Comparison, migrationCommandComparisons[index]) {
			t.Fatalf("migration-command contract %d = %#v", index, contract)
		}
		if len(contract.Provenance) != 2 {
			t.Fatalf("contract %s provenance = %#v, want proposal and decision", contract.ID, contract.Provenance)
		}
		for provenanceIndex, want := range []struct{ kind, reference string }{{"proposal", "GDJ-0049"}, {"decision", "ADR-0051"}} {
			got := contract.Provenance[provenanceIndex]
			if got.Kind != want.kind || got.Reference != want.reference || got.Derived == nil || *got.Derived || got.License != "" {
				t.Fatalf("contract %s provenance %d = %#v", contract.ID, provenanceIndex, got)
			}
		}

		observed := oracle.Contracts[index]
		if observed.ID != wantID || observed.Phase != contract.Phase || observed.Status != StatusObserved {
			t.Fatalf("migration-command oracle contract %d = %#v", index, observed)
		}
		locked := baseline.Contracts[index]
		if locked.ID != wantID || locked.Phase != contract.Phase || locked.Status != StatusNotImplemented || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
			t.Fatalf("migration-command baseline contract %d is not payload-free: %#v", index, locked)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-command oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-command baseline does not validate: %v", err)
	}

	for _, name := range []string{
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-command-oracle.json",
		"conformance/runners/django/migration_command_decisions.py",
	} {
		contents := string(mustReadMigrationCommandFile(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-command source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
	decisionSource := string(mustReadMigrationCommandFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_command_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(decisionSource, forbidden) {
			t.Fatalf("migration-command decision source crosses forbidden boundary %q", forbidden)
		}
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 12 {
		t.Fatalf("migration-command oracle/baseline differences = %d, want 12: %#v", len(differences), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != fmt.Sprintf("MIG-%03d", index+87) || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("migration-command difference %d = %#v", index, difference)
		}
	}
}

func TestMigrationCommandDeclaredDimensionsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadMigrationCommandArtifacts(t)
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneMigrationCommandSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateMigrationCommandValue(observation.Result)
			case CompareError:
				if observation.Error != nil {
					observation.Error.Code += "_changed"
					changed = true
				}
			case CompareDBState:
				changed = mutateMigrationCommandValue(observation.DBState)
			case CompareMetrics:
				changed = mutateMigrationCommandValue(observation.Metrics)
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
}

func TestMigrationCommandPublishedCentralWiringIsExact(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	runner := string(mustReadMigrationCommandFile(t, filepath.Join(root, "conformance", "runners", "django", "runner.py")))
	for fragment, want := range map[string]int{
		"SCENARIOS as MIGRATION_COMMAND_DECISION_SCENARIOS":                              1,
		"    MIGRATION_COMMAND_DECISION_SCENARIOS,":                                      1,
		"DEFAULT_MIGRATION_COMMAND_MANIFEST = (":                                         1,
		"DEFAULT_MIGRATION_COMMAND_ORACLE = (":                                           1,
		"DEFAULT_MIGRATION_COMMAND_MANIFEST.resolve(): DEFAULT_MIGRATION_COMMAND_ORACLE": 1,
	} {
		if got := strings.Count(runner, fragment); got != want {
			t.Fatalf("Django runner fragment %q count = %d, want %d", fragment, got, want)
		}
	}

	makeText := string(mustReadMigrationCommandFile(t, filepath.Join(root, "Makefile")))
	for variable, value := range map[string]string{
		"MIGRATION_COMMAND_MANIFEST":        "conformance/contracts/migration-command-manifest.json",
		"MIGRATION_COMMAND_ORACLE":          "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-command-oracle.json",
		"MIGRATION_COMMAND_NOT_IMPLEMENTED": "conformance/fixtures/godj-migration-command-not-implemented.json",
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
	if got := strings.Count(referenceTarget, "$(MIGRATION_COMMAND_MANIFEST)"); got != 2 {
		t.Fatalf("reference migration-command manifest count = %d, want oracle and NI", got)
	}
	if got := strings.Count(referenceTarget, "$(MIGRATION_COMMAND_ORACLE)"); got != 1 {
		t.Fatalf("reference migration-command oracle count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "$(MIGRATION_COMMAND_NOT_IMPLEMENTED)"); got != 1 {
		t.Fatalf("reference migration-command NI count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "go run ./conformance/cmd/contractcheck"); got != 48 {
		t.Fatalf("reference contractcheck count = %d, want 48", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_COMMAND_MANIFEST)"); got != 1 {
		t.Fatalf("product migration-command manifest count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_COMMAND_ORACLE)"); got != 1 {
		t.Fatalf("product migration-command oracle count = %d, want 1", got)
	}
	if strings.Contains(productTarget, "MIGRATION_COMMAND_NOT_IMPLEMENTED") {
		t.Fatal("product migration-command target reads the payload-free reference baseline")
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 22 {
		t.Fatalf("product adapter count = %d, want 22", got)
	}
	for name, target := range map[string]string{
		"oracle-check":      oracleCheckTarget,
		"oracle-regenerate": oracleRegenerateTarget,
	} {
		if got := strings.Count(target, "$(MIGRATION_COMMAND_MANIFEST)"); got != 1 {
			t.Fatalf("%s migration-command manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "$(MIGRATION_COMMAND_ORACLE)"); got != 1 {
			t.Fatalf("%s migration-command oracle count = %d, want 1", name, got)
		}
	}
	for name, target := range map[string]string{
		"oracle-check":      oracleCheckTarget,
		"oracle-regenerate": oracleRegenerateTarget,
	} {
		if got := strings.Count(target, "python -m conformance.runners.django"); got != 24 {
			t.Fatalf("%s reference runner count = %d, want 24", name, got)
		}
	}

	workflow := string(mustReadMigrationCommandFile(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	for fragment, want := range map[string]int{
		"conformance/fixtures/godj-migration-command-not-implemented.json": 2,
		"test \"$(grep -c '^test_' \"$log\")\" -eq 276":                    1,
		"grep -Fq 'Ran 276 tests' \"$log\"":                                1,
		"assert len(SCENARIOS) == 273":                                     1,
		"assert len(payload) == 932413":                                    1,
		"f5bfe7756008225452e5d9bbc86381a12b1e5de2e32eafd8f1afcd40d866fcd2": 1,
	} {
		if got := strings.Count(workflow, fragment); got != want {
			t.Fatalf("workflow fragment %q count = %d, want %d", fragment, got, want)
		}
	}
}

func TestMigrationCommandDoesNotReviveRetiredMigrationRelationBytes(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	type artifactLock struct {
		size   int
		sha256 string
	}
	locks := map[string]artifactLock{
		"conformance/contracts/migration-relation-manifest.json":                                     {7858, "ec90feaf988e5c014a9cc08d00f6744993af146f2e5d5c4cd86d1ed6e18f25a9"},
		"conformance/fixtures/godj-migration-relation-not-implemented.json":                          {1846, "f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json":          {120502, "5beadac7a80d0903d552e0bf9d5fae85b139ce0754d9163184d907fcf0da5968"},
		"conformance/runners/django/migration_relation_scenarios.py":                                 {36448, "70c33f9554e38fd7e839685d7b47f6064b07f8c7bdaf6f86e1bbfb0a0bbbf84c"},
		"conformance/runners/godj/migration_relation_scenarios.go":                                   {12102, "9d463f7e7b67ca1fe921530f6504eb6e3c225defc65c503df55d09fa3ba221e3"},
		"conformance/runners/django/tests/test_migration_relation_scenarios.py":                      {29396, "570c369ce5a23d473b0fee9fbdc2c3ee4dc36601bbadff598b1aed3628746829"},
		"conformance/runners/django/migration_relation_fixture/__init__.py":                          {67, "baf7af31b1ca827850c3226ca7dd97e242cecc35af531eb4258297f7c4cc9ce6"},
		"conformance/runners/django/migration_relation_fixture/apps.py":                              {194, "820fccf07b3d805bd246ab234559b9ecb9cfa9d8cd59765b67f5b66619dfea2b"},
		"conformance/runners/django/migration_relation_fixture/migrations/__init__.py":               {71, "44cf2a4bbe2bd1360aa7dc09079e45f271240f6a9e36e06253ba4bcb9b04a601"},
		"conformance/runners/django/migration_relation_fixture/migrations/0001_initial.py":           {1090, "899553d0865f8cb0c53fc44d688a3d60720117d3eaa140fc3ba2576fc3b3e4b2"},
		"conformance/runners/django/migration_relation_fixture/migrations/0002_nullable_relation.py": {548, "230a39c2c0b9ce101d8f18cee6f5b268063a29cf037230c5a9de0bafc886de95"},
	}
	if len(locks) != 11 {
		t.Fatalf("retired migration-relation lock count = %d, want 11", len(locks))
	}
	for name, want := range locks {
		contents := mustReadMigrationCommandFile(t, filepath.Join(root, filepath.FromSlash(name)))
		if len(contents) != want.size || fmt.Sprintf("%x", sha256.Sum256(contents)) != want.sha256 {
			t.Fatalf("retired migration-relation bytes changed: %s", name)
		}
	}
}

func loadMigrationCommandArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-command-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-command-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-command-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func mustReadMigrationCommandFile(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func cloneMigrationCommandSuite(t *testing.T, suite ObservationSuite) ObservationSuite {
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

func mutateMigrationCommandValue(value *Value) bool {
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
		return mutateMigrationCommandValue(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateMigrationCommandValue(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateMigrationCommandValue(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
