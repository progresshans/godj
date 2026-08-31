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

	root := conformanceRepositoryRoot(t)
	type artifactLock struct {
		size   int
		sha256 string
	}
	for name, want := range map[string]artifactLock{
		"conformance/contracts/migration-status-manifest.json": {
			size:   5263,
			sha256: "dcb86295e683ea083cc57dca155284f9b26018d5d5a30c9606141bee8946fcc6",
		},
		"conformance/fixtures/godj-migration-status-not-implemented.json": {
			size:   1566,
			sha256: "0dd4dd08b13b9497ea541b7de4a85448cf6e0358b899c095c6eaafaf290f6cc6",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-status-oracle.json": {
			size:   39478,
			sha256: "5a7a7827b37594b5084a25567fedd65152bfb05b5783cdf9e052bdc4d6d9355f",
		},
	} {
		contents := mustReadMigrationStatusFile(t, filepath.Join(root, filepath.FromSlash(name)))
		if len(contents) != want.size {
			t.Fatalf("migration-status artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
			t.Fatalf("migration-status artifact %s sha256 = %q, want %q", name, got, want.sha256)
		}
	}

	profile, manifest, oracle, baseline := loadMigrationStatusArtifacts(t)
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

	profile, manifest, oracle, _ := loadMigrationStatusArtifacts(t)
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneMigrationStatusSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateMigrationStatusValue(observation.Result)
			case CompareError:
				if observation.Error != nil {
					observation.Error.Code += "_changed"
					changed = true
				}
			case CompareDBState:
				changed = mutateMigrationStatusValue(observation.DBState)
			case CompareMetrics:
				changed = mutateMigrationStatusValue(observation.Metrics)
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

	_, manifest, oracle, _ := loadMigrationStatusArtifacts(t)
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

	root := conformanceRepositoryRoot(t)
	work := string(mustReadMigrationStatusFile(t, filepath.Join(root, "work", "0051-project-linked-showmigrations.md")))
	for _, fragment := range []string{
		"MIG-112..115는 portable `result`만 reference comparison",
		"Durable no-mutation과 fresh-process proof는 SQLite/PostgreSQL product test",
	} {
		if !strings.Contains(work, fragment) {
			t.Fatalf("work packet does not preserve product-proof boundary %q", fragment)
		}
	}
}

func TestMigrationStatusAuthoritySourcesAreIndependentAndArtifactBlind(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	decision := string(mustReadMigrationStatusFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_status_decisions.py")))
	for _, forbidden := range []string{
		"from django", "import django", "sqlite3", "conformance/contracts", "conformance/oracles",
		"conformance/fixtures", "not_implemented", "not-implemented",
	} {
		if strings.Contains(decision, forbidden) {
			t.Fatalf("migration-status decision source crosses forbidden boundary %q", forbidden)
		}
	}
	djangoSource := string(mustReadMigrationStatusFile(t, filepath.Join(root, "conformance", "runners", "django", "migration_status_scenarios.py")))
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
		contents := string(mustReadMigrationStatusFile(t, filepath.Join(root, filepath.FromSlash(name))))
		for _, forbidden := range []string{"postgres://", "password=", root} {
			if strings.Contains(contents, forbidden) {
				t.Fatalf("migration-status source %s leaks forbidden value %q", name, forbidden)
			}
		}
	}
}

func TestMigrationStatusPublishedReferenceAndProductWiringIsExact(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	runner := string(mustReadMigrationStatusFile(t, filepath.Join(root, "conformance", "runners", "django", "runner.py")))
	for fragment, want := range map[string]int{
		"SCENARIOS as MIGRATION_STATUS_DECISION_SCENARIOS":                             1,
		"SCENARIOS as MIGRATION_STATUS_DJANGO_SCENARIOS":                               1,
		"MIGRATION_STATUS_SCENARIOS = {":                                               1,
		"    MIGRATION_STATUS_SCENARIOS,":                                              1,
		"DEFAULT_MIGRATION_STATUS_MANIFEST = (":                                        1,
		"DEFAULT_MIGRATION_STATUS_ORACLE = (":                                          1,
		"DEFAULT_MIGRATION_STATUS_MANIFEST.resolve(): DEFAULT_MIGRATION_STATUS_ORACLE": 1,
	} {
		if got := strings.Count(runner, fragment); got != want {
			t.Fatalf("Django runner fragment %q count = %d, want %d", fragment, got, want)
		}
	}

	makeText := string(mustReadMigrationStatusFile(t, filepath.Join(root, "Makefile")))
	for variable, value := range map[string]string{
		"MIGRATION_STATUS_MANIFEST":        "conformance/contracts/migration-status-manifest.json",
		"MIGRATION_STATUS_ORACLE":          "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-status-oracle.json",
		"MIGRATION_STATUS_NOT_IMPLEMENTED": "conformance/fixtures/godj-migration-status-not-implemented.json",
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
	if got := strings.Count(referenceTarget, "$(MIGRATION_STATUS_MANIFEST)"); got != 2 {
		t.Fatalf("reference migration-status manifest count = %d, want oracle and NI", got)
	}
	if got := strings.Count(referenceTarget, "$(MIGRATION_STATUS_ORACLE)"); got != 1 {
		t.Fatalf("reference migration-status oracle count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "$(MIGRATION_STATUS_NOT_IMPLEMENTED)"); got != 1 {
		t.Fatalf("reference migration-status NI count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "go run ./conformance/cmd/contractcheck"); got != 54 {
		t.Fatalf("reference contractcheck count = %d, want 54", got)
	}
	for variable, want := range map[string]int{
		"$(MIGRATION_STATUS_MANIFEST)":        1,
		"$(MIGRATION_STATUS_ORACLE)":          1,
		"$(MIGRATION_STATUS_NOT_IMPLEMENTED)": 0,
	} {
		if got := strings.Count(productTarget, variable); got != want {
			t.Fatalf("product migration-status variable %s count = %d, want %d", variable, got, want)
		}
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 26 {
		t.Fatalf("product adapter count = %d, want 26", got)
	}
	for name, target := range map[string]string{"oracle-check": oracleCheckTarget, "oracle-regenerate": oracleRegenerateTarget} {
		if got := strings.Count(target, "$(MIGRATION_STATUS_MANIFEST)"); got != 1 {
			t.Fatalf("%s migration-status manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "$(MIGRATION_STATUS_ORACLE)"); got != 1 {
			t.Fatalf("%s migration-status oracle count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "python -m conformance.runners.django"); got != 27 {
			t.Fatalf("%s reference runner count = %d, want 27", name, got)
		}
	}

	workflow := string(mustReadMigrationStatusFile(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	if got := strings.Count(workflow, "conformance/fixtures/godj-migration-status-not-implemented.json"); got != 2 {
		t.Fatalf("workflow migration-status NI lock count = %d, want 2", got)
	}
}

func loadMigrationStatusArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "migration-status-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "migration-status-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-migration-status-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}

func mustReadMigrationStatusFile(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func cloneMigrationStatusSuite(t *testing.T, suite ObservationSuite) ObservationSuite {
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

func mutateMigrationStatusValue(value *Value) bool {
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
		return mutateMigrationStatusValue(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateMigrationStatusValue(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateMigrationStatusValue(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
