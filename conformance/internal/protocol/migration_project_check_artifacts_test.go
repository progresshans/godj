package protocol

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationProjectCheckArtifactBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "migration-project-check")
	wantSlugs := []string{
		"godj.migration.project_check.nested_project_success",
		"godj.migration.project_check.explicit_project_override",
		"godj.migration.project_check.empty_catalog",
		"godj.migration.project_check.canonical_filesystem_order",
		"godj.migration.project_check.unsafe_source_entry",
		"godj.migration.project_check.project_not_found",
		"godj.migration.project_check.project_protocol_incompatible",
		"godj.migration.project_check.project_build_failure_atomic",
		"godj.migration.project_check.definition_load_failure",
		"godj.migration.project_check.invalid_runner_response",
	}
	wantPhases := []Phase{
		PhaseEnvironment,
		PhaseEnvironment,
		PhaseConstruction,
		PhaseConstruction,
		PhaseConstruction,
		PhaseEnvironment,
		PhaseEnvironment,
		PhaseEnvironment,
		PhaseConstruction,
		PhaseEnvironment,
	}
	if len(manifest.Contracts) != len(wantSlugs) {
		t.Fatalf("migration-project-check manifest has %d contracts, want %d", len(manifest.Contracts), len(wantSlugs))
	}
	if len(oracle.Contracts) != len(wantSlugs) || len(baseline.Contracts) != len(wantSlugs) {
		t.Fatalf("migration-project-check suite lengths = oracle %d/static %d, want %d", len(oracle.Contracts), len(baseline.Contracts), len(wantSlugs))
	}
	wantMetricNames := []string{
		"ancestor_directories_inspected",
		"build_calls",
		"command_dispatches",
		"definition_sets_published",
		"definitions_published",
		"descriptor_reads",
		"direct_planner_calls",
		"directory_entries_seen",
		"documents_received",
		"exit_code",
		"failure",
		"godj_db_calls",
		"headers_validated",
		"load_calls",
		"operations_decoded",
		"partial_stdout_writes",
		"planner_construction",
		"revision_lifecycle_calls",
		"roots_opened",
		"runner_calls",
		"runner_response_writes",
		"source_reads",
		"user_stderr_writes",
		"user_stdout_writes",
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("MIG-%03d", index+65)
		if contract.ID != wantID {
			t.Fatalf("contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if contract.Scenario != wantSlugs[index] {
			t.Fatalf("contract %s scenario = %q, want %q", contract.ID, contract.Scenario, wantSlugs[index])
		}
		if contract.Status != ContractPassing {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		wantComparison := []ComparisonDimension{CompareError, CompareMetrics}
		if index < 4 {
			wantComparison = []ComparisonDimension{CompareResult, CompareMetrics}
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparison) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparison)
		}
		wantReferences := []string{"ADR-0021"}
		if index < 4 || contract.ID == "MIG-073" {
			wantReferences = append(wantReferences, "ADR-0035")
		}
		if len(contract.Provenance) != len(wantReferences) {
			t.Fatalf("contract %s provenance count = %d, want %d", contract.ID, len(contract.Provenance), len(wantReferences))
		}
		for provenanceIndex, provenance := range contract.Provenance {
			if provenance.Kind != "decision" || provenance.Reference != wantReferences[provenanceIndex] || provenance.Derived == nil || *provenance.Derived || provenance.License != "" {
				t.Fatalf("contract %s provenance %d = %#v, want decision/%s/derived=false", contract.ID, provenanceIndex, provenance, wantReferences[provenanceIndex])
			}
		}
		if oracle.Contracts[index].ID != contract.ID || oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %d = %#v, want %s observed", index, oracle.Contracts[index], contract.ID)
		}
		if baseline.Contracts[index].ID != contract.ID || baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("static contract %d = %#v, want %s not_implemented", index, baseline.Contracts[index], contract.ID)
		}
		if oracle.Contracts[index].DBState != nil {
			t.Fatalf("oracle contract %s db_state = %#v, want absent for DB-free reference", contract.ID, oracle.Contracts[index].DBState)
		}
		metrics := oracle.Contracts[index].Metrics
		if metrics == nil || metrics.Type != ValueObject {
			t.Fatalf("oracle contract %s metrics = %#v, want object", contract.ID, metrics)
		}
		gotMetricNames := make([]string, 0, len(metrics.Fields))
		for _, field := range metrics.Fields {
			gotMetricNames = append(gotMetricNames, field.Name)
		}
		if !reflect.DeepEqual(gotMetricNames, wantMetricNames) {
			t.Fatalf("oracle contract %s metric fields = %#v, want exact 24 fields %#v", contract.ID, gotMetricNames, wantMetricNames)
		}
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("migration-project-check oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("migration-project-check static fixture does not validate: %v", err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(wantSlugs) {
		t.Fatalf("oracle/static differences = %d, want %d: %#v", len(differences), len(wantSlugs), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" {
			t.Fatalf("difference %d = %#v, want ordered status-only mismatch", index, difference)
		}
	}
}

func TestMigrationProjectCheckStaticFixtureExitsOneWithTenOrderedMismatches(t *testing.T) {
	root := conformanceRepositoryRoot(t)
	arguments := []string{
		"run", "./conformance/cmd/observationcmp",
		"-profile", "conformance/profiles/django-6.1-sqlite-darwin-arm64.json",
		"-manifest", "conformance/contracts/migration-project-check-manifest.json",
		"-expected", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json",
		"-actual", "conformance/fixtures/godj-migration-project-check-not-implemented.json",
	}
	command := exec.Command("go", arguments...)
	command.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 1 {
		t.Fatalf("observationcmp error = %v, want exit 1; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("observationcmp stdout = %q, want empty", stdout.String())
	}
	text := stderr.String()
	if !strings.Contains(text, "observationcmp: 10 difference(s)") {
		t.Fatalf("observationcmp stderr = %q, want 10 differences", text)
	}
	previous := -1
	for number := 65; number <= 74; number++ {
		needle := fmt.Sprintf("MIG-%03d status:", number)
		position := strings.Index(text, needle)
		if position <= previous {
			t.Fatalf("observationcmp stderr does not preserve contract order at %s: %q", needle, text)
		}
		previous = position
	}
}

func TestMigrationProjectCheckRemainsInProductTarget(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	referenceTarget := migrationProjectCheckMakeTarget(t, text, "conformance-check:\n", "godj-conformance:")
	productTarget := migrationProjectCheckMakeTarget(t, text, "godj-conformance:", "oracle-check:\n")
	oracleCheckTarget := migrationProjectCheckMakeTarget(t, text, "oracle-check:\n", "oracle-regenerate:\n")
	oracleRegenerateTarget := migrationProjectCheckMakeTarget(t, text, "oracle-regenerate:\n", "ci:")
	if got := strings.Count(referenceTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 2 {
		t.Fatalf("reference target project-check manifest count = %d, want 2", got)
	}
	if got := strings.Count(productTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("product target project-check manifest count = %d, want 1", got)
	}
	if got := strings.Count(oracleCheckTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-check project-check manifest count = %d, want 1", got)
	}
	if got := strings.Count(oracleRegenerateTarget, "$(MIGRATION_PROJECT_CHECK_MANIFEST)"); got != 1 {
		t.Fatalf("oracle-regenerate project-check manifest count = %d, want 1", got)
	}
}

func migrationProjectCheckMakeTarget(t *testing.T, text, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(text, startMarker)
	end := strings.Index(text, endMarker)
	if start < 0 || end <= start {
		t.Fatalf("cannot isolate Makefile target %q..%q", startMarker, endMarker)
	}
	return text[start:end]
}
