package protocol

import (
	"fmt"
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

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-command")
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
		contents := string(readArtifact(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-command source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
	decisionSource := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_command_decisions.py")))
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

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-command")
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneJSONSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateFirstValue(observation.Result)
			case CompareError:
				if observation.Error != nil {
					observation.Error.Code += "_changed"
					changed = true
				}
			case CompareDBState:
				changed = mutateFirstValue(observation.DBState)
			case CompareMetrics:
				changed = mutateFirstValue(observation.Metrics)
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
	runner := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "runner.py")))
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

	makeText := string(readArtifact(t, filepath.Join(root, "Makefile")))
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
	productStart := strings.Index(makeText, "godj-conformance:")
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
	if got := strings.Count(productTarget, "$(MIGRATION_COMMAND_MANIFEST)"); got != 1 {
		t.Fatalf("product migration-command manifest count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_COMMAND_ORACLE)"); got != 1 {
		t.Fatalf("product migration-command oracle count = %d, want 1", got)
	}
	if strings.Contains(productTarget, "MIGRATION_COMMAND_NOT_IMPLEMENTED") {
		t.Fatal("product migration-command target reads the payload-free reference baseline")
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

}
