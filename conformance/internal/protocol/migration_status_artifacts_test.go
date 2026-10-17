package protocol

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var migrationStatusScenarios = []string{
	"godj.migration.status.empty_catalog",
	"django.migration.status.fresh_unapplied",
	"django.migration.status.applied_prefix",
	"django.migration.status.fully_applied_restart",
	"django.migration.status.cross_app_branch_order",
	"godj.migration.status.unknown_record_visible",
	"godj.migration.status.inconsistent_known_history",
	"godj.migration.status.project_boundary",
}

var migrationStatusPhases = []Phase{
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEnvironment,
}

var migrationStatusComparisons = [][]ComparisonDimension{
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult},
	{CompareResult},
	{CompareResult},
	{CompareResult},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareError, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
}

func TestMigrationStatusReferenceArtifactsAreLockedValidatedAndPayloadSafe(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-status")
	if profile.ID != "django-6.1-sqlite-darwin-arm64" || profile.Fingerprint.DjangoVersion != "6.1" || profile.Fingerprint.PythonVersion != "3.14.3" || profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected migration-status profile: %#v", profile)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("migration-status suites do not preserve the exact profile snapshot")
	}
	if len(manifest.Contracts) != 8 || len(oracle.Contracts) != 8 || len(baseline.Contracts) != 8 {
		t.Fatalf("migration-status lengths = %d/%d/%d, want 8/8/8", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+111)
		if contract.ID != wantID || contract.Scenario != migrationStatusScenarios[index] || contract.Phase != migrationStatusPhases[index] || contract.Status != ContractPassing || !reflect.DeepEqual(contract.Comparison, migrationStatusComparisons[index]) {
			t.Fatalf("migration-status contract %d = %#v", index, contract)
		}
		wantProvenance := 2
		if index >= 1 && index <= 2 {
			wantProvenance = 4
		} else if index >= 3 && index <= 4 {
			wantProvenance = 3
		}
		if len(contract.Provenance) != wantProvenance {
			t.Fatalf("contract %s provenance = %#v, want %d entries", contract.ID, contract.Provenance, wantProvenance)
		}
		for provenanceIndex, want := range []struct{ kind, reference string }{{"proposal", "GDJ-0051"}, {"documentation", "ADR-0053"}} {
			got := contract.Provenance[provenanceIndex]
			if got.Kind != want.kind || got.Reference != want.reference || got.Derived == nil || *got.Derived || got.License != "" {
				t.Fatalf("contract %s provenance %d = %#v", contract.ID, provenanceIndex, got)
			}
		}
		if wantProvenance >= 3 {
			got := contract.Provenance[2]
			if got.Kind != "source" || !strings.HasPrefix(got.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") || got.Derived == nil || *got.Derived || got.License != "BSD-3-Clause" {
				t.Fatalf("contract %s Django source provenance = %#v", contract.ID, got)
			}
		}
		if wantProvenance == 4 {
			got := contract.Provenance[3]
			if got.Kind != "test" || !strings.HasPrefix(got.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") || got.Derived == nil || *got.Derived || got.License != "BSD-3-Clause" {
				t.Fatalf("contract %s Django test provenance = %#v", contract.ID, got)
			}
		}

		observed := oracle.Contracts[index]
		if observed.ID != wantID || observed.Phase != contract.Phase || observed.Status != StatusObserved {
			t.Fatalf("migration-status oracle contract %d = %#v", index, observed)
		}
		locked := baseline.Contracts[index]
		if locked.ID != wantID || locked.Phase != contract.Phase || locked.Status != StatusNotImplemented || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
			t.Fatalf("migration-status baseline contract %d is not payload-free: %#v", index, locked)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-status oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-status baseline does not validate: %v", err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 8 {
		t.Fatalf("migration-status oracle/baseline differences = %d, want 8: %#v", len(differences), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != fmt.Sprintf("MIG-%03d", index+111) || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("migration-status difference %d = %#v", index, difference)
		}
	}
}

func TestMigrationStatusDeclaredDimensionsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-status")
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

func TestMigrationStatusDjangoFixtureDetailsAreNotPublishedAsProductDimensions(t *testing.T) {
	t.Parallel()

	_, manifest, oracle, _ := loadContractArtifacts(t, "migration-status")
	for index := 1; index <= 4; index++ {
		contract := manifest.Contracts[index]
		if !reflect.DeepEqual(contract.Comparison, []ComparisonDimension{CompareResult}) {
			t.Fatalf("contract %s comparison = %v, want portable result only", contract.ID, contract.Comparison)
		}
		observation := oracle.Contracts[index]
		if observation.DBState != nil || observation.Metrics != nil {
			t.Fatalf("contract %s published Django fixture details as product payload", contract.ID)
		}
	}

}

func TestMigrationStatusAuthoritySourcesAreIndependentAndArtifactBlind(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	decision := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_status_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "sqlite3", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(decision, forbidden) {
			t.Fatalf("migration-status decision source crosses forbidden boundary %q", forbidden)
		}
	}
	djangoSource := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_status_scenarios.py")))
	for _, forbidden := range []string{
		"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(djangoSource, forbidden) {
			t.Fatalf("migration-status Django source crosses forbidden boundary %q", forbidden)
		}
	}
	if !strings.Contains(djangoSource, "command.show_list(fixture_connection)") {
		t.Fatal("migration-status Django source does not execute Command.show_list")
	}
	for _, name := range []string{
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-status-oracle.json",
		"conformance/runners/django/migration_status_decisions.py",
		"conformance/runners/django/migration_status_scenarios.py",
	} {
		contents := string(readArtifact(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-status source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
}
