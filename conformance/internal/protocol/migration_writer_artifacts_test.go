package protocol

import (
	"crypto/sha256"
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
	type artifactLock struct {
		size   int
		sha256 string
	}
	for name, want := range map[string]artifactLock{
		"conformance/contracts/migration-writer-manifest.json": {
			size:   9227,
			sha256: "90bce609ffb4f771007379495629a31efbf00594dca16f9efe875005e97f1c72",
		},
		"conformance/fixtures/godj-migration-writer-deviation-expected.json": {
			size:   7242,
			sha256: "74617f20f72ecd5b26284ae8cffb7a1c408cdef03e0933d457beeb82f9f4718e",
		},
		"conformance/fixtures/godj-migration-writer-not-implemented.json": {
			size:   1876,
			sha256: "b27563a864fe417df53a20092c44f169829e9798cb2e40348c8dbbdcf4715502",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-writer-oracle.json": {
			size:   25980,
			sha256: "9068d0e603d631ac8a4da5c564b1aa1037c0854a0935342e3518812bf452fd41",
		},
	} {
		contents := mustReadMigrationCommandFile(t, filepath.Join(root, filepath.FromSlash(name)))
		if len(contents) != want.size {
			t.Fatalf("migration-writer artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
			t.Fatalf("migration-writer artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}

	profile, manifest, oracle, baseline := loadMigrationWriterArtifacts(t)
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

	profile, manifest, oracle, _ := loadMigrationWriterArtifacts(t)
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneMigrationCommandSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateMigrationCommandValue(observation.Result)
			case CompareMetrics:
				changed = mutateMigrationCommandValue(observation.Metrics)
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

	reordered := cloneMigrationCommandSuite(t, oracle)
	reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
	if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
		t.Fatal("migration-writer contract reorder produced a false green")
	}
}

func TestMigrationWriterAuthorityAndCentralWiringRemainSeparated(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	decisionSource := string(mustReadMigrationCommandFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_writer_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented", "sqlite3",
	} {
		if strings.Contains(decisionSource, forbidden) {
			t.Fatalf("migration-writer decision source crosses forbidden boundary %q", forbidden)
		}
	}
	scenarioSource := string(mustReadMigrationCommandFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_writer_scenarios.py")))
	for _, forbidden := range []string{"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not_implemented", "not-implemented"} {
		if strings.Contains(scenarioSource, forbidden) {
			t.Fatalf("migration-writer Django source reads expected artifact boundary %q", forbidden)
		}
	}

	runner := string(mustReadMigrationCommandFile(t, filepath.Join(root, "conformance", "runners", "django", "runner.py")))
	for fragment, want := range map[string]int{
		"SCENARIOS as MIGRATION_WRITER_DECISION_SCENARIOS":                             1,
		"SCENARIOS as MIGRATION_WRITER_DJANGO_SCENARIOS":                               1,
		"    MIGRATION_WRITER_SCENARIOS,":                                              1,
		"DEFAULT_MIGRATION_WRITER_MANIFEST = (":                                        1,
		"DEFAULT_MIGRATION_WRITER_ORACLE = (":                                          1,
		"DEFAULT_MIGRATION_WRITER_MANIFEST.resolve(): DEFAULT_MIGRATION_WRITER_ORACLE": 1,
	} {
		if got := strings.Count(runner, fragment); got != want {
			t.Fatalf("Django runner fragment %q count = %d, want %d", fragment, got, want)
		}
	}

	makeText := string(mustReadMigrationCommandFile(t, filepath.Join(root, "Makefile")))
	for variable, value := range map[string]string{
		"MIGRATION_WRITER_MANIFEST":           "conformance/contracts/migration-writer-manifest.json",
		"MIGRATION_WRITER_ORACLE":             "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-writer-oracle.json",
		"MIGRATION_WRITER_NOT_IMPLEMENTED":    "conformance/fixtures/godj-migration-writer-not-implemented.json",
		"MIGRATION_WRITER_DEVIATION_EXPECTED": "conformance/fixtures/godj-migration-writer-deviation-expected.json",
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
	if got := strings.Count(referenceTarget, "$(MIGRATION_WRITER_MANIFEST)"); got != 2 {
		t.Fatalf("reference migration-writer manifest count = %d, want oracle and NI", got)
	}
	if got := strings.Count(referenceTarget, "go run ./conformance/cmd/contractcheck"); got != 52 {
		t.Fatalf("reference contractcheck count = %d, want 52", got)
	}
	for _, fragment := range []string{"MIGRATION_WRITER_MANIFEST", "MIGRATION_WRITER_ORACLE", "MIGRATION_WRITER_DEVIATION_EXPECTED"} {
		if !strings.Contains(productTarget, fragment) {
			t.Fatalf("Phase D product adapter is missing %s", fragment)
		}
	}
	if strings.Contains(productTarget, "MIGRATION_WRITER_NOT_IMPLEMENTED") {
		t.Fatal("Phase D product adapter uses the migration-writer not-implemented fixture")
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 25 {
		t.Fatalf("product adapter count = %d, want 25", got)
	}
	for name, target := range map[string]string{"oracle-check": oracleCheckTarget, "oracle-regenerate": oracleRegenerateTarget} {
		if got := strings.Count(target, "$(MIGRATION_WRITER_MANIFEST)"); got != 1 {
			t.Fatalf("%s migration-writer manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "python -m conformance.runners.django"); got != 26 {
			t.Fatalf("%s reference runner count = %d, want 26", name, got)
		}
	}

	workflow := string(mustReadMigrationCommandFile(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	if got := strings.Count(workflow, "conformance/fixtures/godj-migration-writer-not-implemented.json"); got != 2 {
		t.Fatalf("workflow migration-writer NI lock count = %d, want 2", got)
	}
	if got := strings.Count(workflow, "conformance/fixtures/godj-migration-writer-deviation-expected.json"); got != 2 {
		t.Fatalf("workflow migration-writer deviation lock count = %d, want 2", got)
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

func loadMigrationWriterArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-writer-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-writer-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-writer-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}
