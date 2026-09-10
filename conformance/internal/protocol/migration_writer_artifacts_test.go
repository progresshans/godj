package protocol

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var migrationWriterScenarios = []string{
	"django.migration.writer.no_changes_clean",
	"django.migration.writer.fresh_initial",
	"django.migration.writer.repeat_after_initial_noop",
	"godj.migration.writer.deterministic_candidate",
	"django.migration.writer.relation_dependency_topology",
	"django.migration.writer.additive_model_and_field_tail",
	"django.migration.writer.dry_run_no_mutation",
	"django.migration.writer.check_clean_and_drift",
	"godj.migration.writer.unsupported_delta_fail_closed",
	"godj.migration.writer.snapshot_and_protocol_boundary",
	"godj.migration.writer.atomic_concurrent_publication",
	"godj.migration.writer.interruption_recovery_and_roundtrip",
}

var migrationWriterPhases = []Phase{
	PhaseConstruction,
	PhaseConstruction,
	PhaseConstruction,
	PhaseConstruction,
	PhaseConstruction,
	PhaseConstruction,
	PhaseEnvironment,
	PhaseEnvironment,
	PhaseConstruction,
	PhaseEnvironment,
	PhaseCommit,
	PhaseRollback,
}

func TestMigrationWriterArtifactsAreLockedAndPhaseAReferenceRemainsPayloadFree(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-writer")
	deviation, err := LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-migration-writer-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	for index, contract := range deviation.Contracts {
		if contract.ID != fmt.Sprintf("MIG-%03d", index+103) {
			t.Fatalf("migration-writer deviation contract %d = %q", index, contract.ID)
		}
		changes += len(contract.Changes)
	}
	if deviation.ProfileID != profile.ID || deviation.Decision != "DEV-0010" || len(deviation.Contracts) != 5 || changes != 19 {
		t.Fatalf("migration-writer deviation fixture = profile:%q decision:%q contracts:%d changes:%d", deviation.ProfileID, deviation.Decision, len(deviation.Contracts), changes)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("migration-writer suites do not preserve the exact profile snapshot")
	}
	if len(manifest.Contracts) != 12 || len(oracle.Contracts) != 12 || len(baseline.Contracts) != 12 {
		t.Fatalf("migration-writer lengths = %d/%d/%d, want 12/12/12", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}

	djangoAuthority := map[string]bool{
		"MIG-099": true,
		"MIG-100": true,
		"MIG-101": true,
		"MIG-103": true,
		"MIG-104": true,
		"MIG-105": true,
		"MIG-106": true,
	}
	wantComparisons := []ComparisonDimension{CompareResult, CompareMetrics}
	wantStatuses := []ContractStatus{
		ContractPassing,
		ContractPassing,
		ContractPassing,
		ContractPassing,
		ContractDeviation,
		ContractDeviation,
		ContractDeviation,
		ContractDeviation,
		ContractDeviation,
		ContractPassing,
		ContractPassing,
		ContractPassing,
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+99)
		if contract.ID != wantID || contract.Scenario != migrationWriterScenarios[index] || contract.Phase != migrationWriterPhases[index] || contract.Status != wantStatuses[index] || !reflect.DeepEqual(contract.Comparison, wantComparisons) {
			t.Fatalf("migration-writer contract %d = %#v", index, contract)
		}
		assertMigrationWriterProvenance(t, contract, djangoAuthority[contract.ID])

		observed := oracle.Contracts[index]
		if observed.ID != wantID || observed.Phase != contract.Phase || observed.Status != StatusObserved || observed.Result == nil || observed.Metrics == nil || observed.Error != nil || observed.DBState != nil {
			t.Fatalf("migration-writer oracle contract %d = %#v", index, observed)
		}
		locked := baseline.Contracts[index]
		if locked.ID != wantID || locked.Phase != contract.Phase || locked.Status != StatusNotImplemented || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
			t.Fatalf("migration-writer baseline contract %d is not payload-free: %#v", index, locked)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-writer oracle does not validate: %v", err)
	}
	referenceManifest := manifest
	referenceManifest.Contracts = append([]Contract(nil), manifest.Contracts...)
	for index := range referenceManifest.Contracts {
		referenceManifest.Contracts[index].Status = ContractOracleLocked
	}
	if err := ValidateSuiteAgainst(profile, referenceManifest, baseline); err != nil {
		t.Fatalf("migration-writer baseline does not validate: %v", err)
	}

	differences, err := Compare(profile, referenceManifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 12 {
		t.Fatalf("migration-writer oracle/baseline differences = %d, want 12: %#v", len(differences), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != fmt.Sprintf("MIG-%03d", index+99) || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("migration-writer difference %d = %#v", index, difference)
		}
	}
}

func TestMigrationWriterDeclaredDimensionsAndBindingsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "migration-writer")
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneJSONSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateFirstValue(observation.Result)
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
			if len(differences) == 0 || differences[0].ContractID != contract.ID {
				t.Fatalf("contract %s %s mutation false green: %#v", contract.ID, dimension, differences)
			}
		}
	}

	reordered := cloneJSONSuite(t, oracle)
	reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
	if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
		t.Fatal("migration-writer contract reorder produced a false green")
	}
}

func TestMigrationWriterObservationAuthorityRemainsIndependent(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	decisionSource := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_writer_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented", "sqlite3",
	} {
		if strings.Contains(decisionSource, forbidden) {
			t.Fatalf("migration-writer decision source crosses forbidden boundary %q", forbidden)
		}
	}
	scenarioSource := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "migration_writer_scenarios.py")))
	for _, forbidden := range []string{"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not_implemented", "not-implemented"} {
		if strings.Contains(scenarioSource, forbidden) {
			t.Fatalf("migration-writer Django source reads expected artifact boundary %q", forbidden)
		}
	}

}

func assertMigrationWriterProvenance(t *testing.T, contract Contract, djangoAuthority bool) {
	t.Helper()
	if len(contract.Provenance) < 2 {
		t.Fatalf("contract %s provenance = %#v", contract.ID, contract.Provenance)
	}
	for index, want := range []struct{ kind, reference string }{{"proposal", "GDJ-0050"}, {"documentation", "ADR-0052"}} {
		got := contract.Provenance[index]
		if got.Kind != want.kind || got.Reference != want.reference || got.Derived == nil || *got.Derived || got.License != "" {
			t.Fatalf("contract %s provenance %d = %#v", contract.ID, index, got)
		}
	}
	start := 2
	if contract.Status == ContractDeviation {
		if len(contract.Provenance) <= start {
			t.Fatalf("deviation contract %s has no DEV-0010 provenance", contract.ID)
		}
		decision := contract.Provenance[start]
		if decision.Kind != "decision" || decision.Reference != "DEV-0010" || decision.Derived == nil || *decision.Derived || decision.License != "" {
			t.Fatalf("contract %s deviation provenance = %#v", contract.ID, decision)
		}
		start++
	}
	if !djangoAuthority {
		if len(contract.Provenance) != start {
			t.Fatalf("GoDj-owned contract %s has Django provenance: %#v", contract.ID, contract.Provenance)
		}
		return
	}
	if len(contract.Provenance) <= start {
		t.Fatalf("Django-owned contract %s has no pinned Django provenance", contract.ID)
	}
	for _, provenance := range contract.Provenance[start:] {
		if provenance.Kind != "source" && provenance.Kind != "test" && provenance.Kind != "documentation" {
			t.Fatalf("contract %s unexpected Django provenance kind %q", contract.ID, provenance.Kind)
		}
		if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") || provenance.License != "BSD-3-Clause" || provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s unpinned Django provenance: %#v", contract.ID, provenance)
		}
	}
}
