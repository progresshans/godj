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

var systemStateIDs = []string{
	"SYS-001", "SYS-002", "SYS-003", "SYS-004", "SYS-005", "SYS-006",
	"SYS-007", "SYS-008", "SYS-009", "SYS-010", "SYS-011", "SYS-012",
}

var systemStateScenarios = []string{
	"godj.system_state.explicit_migration_gate",
	"godj.system_state.admin_bootstrap_gate",
	"django.system_state.credential_permission_restart",
	"django.system_state.rotated_session_restart",
	"godj.system_state.session_expiry_and_touch",
	"godj.system_state.capacity_reap_and_rotate_rollback",
	"godj.system_state.digest_only_current_codec",
	"django.system_state.logout_restart_denial",
	"django.system_state.csrf_restart",
	"django.system_state.admin_audit_fault_rollback",
	"django.system_state.audit_history_restart",
	"godj.system_state.commit_outcome_unknown",
}

var systemStatePhases = []Phase{
	PhaseEnvironment, PhaseCommit, PhaseEvaluation, PhaseCommit,
	PhaseCommit, PhaseRollback, PhaseEvaluation, PhaseCommit,
	PhaseCommit, PhaseRollback, PhaseEvaluation, PhaseCommit,
}

func TestSystemStateArtifactBytesAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size   int
		sha256 string
	}
	root := conformanceRepositoryRoot(t)
	wanted := map[string]artifactLock{
		"conformance/contracts/system-state-manifest.json":                     {7730, "f570cadb322ce7587a70fc4cbbf69bd7d9b1641b31719c42ed00509dc807af44"},
		"conformance/fixtures/godj-system-state-not-implemented.json":          {1838, "aa95b3551b576f74aa537eb52355ea550ff0fdc0b96e0afd2c155b332bf9dc6e"},
		"conformance/fixtures/godj-system-state-deviation-expected.json":       {1141, "a2877ae785b937b2b1c9ee3b567a7631403a5b5ca91485d2a6c942066c744869"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json": {13099, "4b1cf9a63308c2f9ad9ac385c24e35ffec8f94546d80ed933dcf32edcb5a34bb"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS":        {1791, "6c0a8332929a09579ca9b1cb45c2ae0f250bf9002370a72371c3cc1e6bc5753c"},
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != want.size {
			t.Fatalf("system-state artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want.sha256 {
			t.Fatalf("system-state artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
}

func TestSystemStateMixedAuthorityAndPayloadFreeBaselineAreExact(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline, deviation := loadSystemStateArtifacts(t)
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("system-state oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("system-state not-implemented baseline does not validate: %v", err)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("system-state suites do not bind the exact profile snapshot")
	}
	if len(manifest.Contracts) != 12 || len(oracle.Contracts) != 12 || len(baseline.Contracts) != 12 {
		t.Fatalf("system-state lengths = %d/%d/%d, want 12", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}
	djangoIDs := map[string]bool{
		"SYS-003": true, "SYS-004": true, "SYS-008": true,
		"SYS-009": true, "SYS-010": true, "SYS-011": true,
	}
	passing, deviations := 0, 0
	for index, contract := range manifest.Contracts {
		if contract.ID != systemStateIDs[index] || contract.Scenario != systemStateScenarios[index] || contract.Phase != systemStatePhases[index] {
			t.Fatalf("system-state contract %d binding = %#v", index, contract)
		}
		wantStatus := ContractPassing
		if contract.ID == "SYS-009" {
			wantStatus = ContractDeviation
			deviations++
		} else {
			passing++
		}
		if contract.Status != wantStatus {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, wantStatus)
		}
		wantComparisons := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
		if !reflect.DeepEqual(contract.Comparison, wantComparisons) {
			t.Fatalf("contract %s comparisons = %#v, want %#v", contract.ID, contract.Comparison, wantComparisons)
		}
		assertSystemStateProvenance(t, contract, djangoIDs[contract.ID])
		observed := oracle.Contracts[index]
		if observed.ID != contract.ID || observed.Status != StatusObserved || observed.Phase != contract.Phase || observed.Result == nil || observed.DBState == nil || observed.Metrics == nil || observed.Error != nil {
			t.Fatalf("system-state oracle contract %d = %#v", index, observed)
		}
		locked := baseline.Contracts[index]
		if locked.ID != contract.ID || locked.Status != StatusNotImplemented || locked.Phase != contract.Phase || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
			t.Fatalf("system-state baseline contract %d is not payload-free: %#v", index, locked)
		}
	}
	if passing != 11 || deviations != 1 {
		t.Fatalf("system-state product classification = %d passing + %d deviation, want 11 + 1", passing, deviations)
	}
	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 12 {
		t.Fatalf("system-state oracle/baseline differences = %d, want 12", len(differences))
	}
	for index, difference := range differences {
		if difference.ContractID != systemStateIDs[index] || difference.Path != "status" {
			t.Fatalf("system-state difference %d = %#v", index, difference)
		}
	}
	if deviation.ProfileID != profile.ID || deviation.Decision != "DEV-0008" || len(deviation.Contracts) != 1 || deviation.Contracts[0].ID != "SYS-009" {
		t.Fatalf("system-state deviation envelope = %#v", deviation)
	}
}

func TestSystemStateReviewedDeviationHasExactlyFourOracleBoundSelectors(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _, deviation := loadSystemStateArtifacts(t)
	wantSelectors := []DeviationChangePolicy{
		{Dimension: DeviationResult, Path: "pre_restart.accepted", Operation: DeviationReplace},
		{Dimension: DeviationResult, Path: "pre_restart.status", Operation: DeviationReplace},
		{Dimension: DeviationDBState, Path: "pre_restart.article_delta", Operation: DeviationReplace},
		{Dimension: DeviationMetrics, Path: "pre_restart_mutations", Operation: DeviationReplace},
	}
	changes := deviation.Contracts[0].Changes
	if len(changes) != len(wantSelectors) {
		t.Fatalf("SYS-009 deviation changes = %d, want %d", len(changes), len(wantSelectors))
	}
	actual := cloneSuite(t, oracle)
	contractIndex := 8
	for index, want := range wantSelectors {
		change := changes[index]
		if change.Dimension != want.Dimension || change.Path != want.Path || change.Operation != want.Operation {
			t.Fatalf("SYS-009 selector %d = %#v, want %#v", index, change, want)
		}
		root, err := deviationDimensionRoot(&oracle.Contracts[contractIndex], change.Dimension)
		if err != nil {
			t.Fatal(err)
		}
		location, err := locateDeviationValue(root, change.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(*location.value, change.Reference) {
			t.Fatalf("SYS-009 selector %d reference = %#v, oracle has %#v", index, change.Reference, *location.value)
		}
		contract := manifest.Contracts[contractIndex]
		if err := applyDeviationChange(&contract, &actual.Contracts[contractIndex], change); err != nil {
			t.Fatalf("apply SYS-009 selector %d: %v", index, err)
		}
	}
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 4 {
		t.Fatalf("reviewed SYS-009 product differences = %d, want 4: %#v", len(differences), differences)
	}
	fresh := objectField(t, actual.Contracts[contractIndex].Result, "fresh")
	*objectField(t, fresh, "accepted") = Boolean(false)
	differences, err = Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 5 {
		t.Fatalf("fresh-token scope escape differences = %d, want 5", len(differences))
	}
}

func TestSystemStateDeviationExpectationBuildsClosedProductAndRejectsSelectorEscape(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _, expectation := loadSystemStateArtifacts(t)
	policy := DeviationPolicy{
		Decision: "DEV-0008",
		Contracts: []DeviationContractPolicy{{
			ID: "SYS-009",
			Changes: []DeviationChangePolicy{
				{Dimension: DeviationResult, Path: "pre_restart.accepted", Operation: DeviationReplace},
				{Dimension: DeviationResult, Path: "pre_restart.status", Operation: DeviationReplace},
				{Dimension: DeviationDBState, Path: "pre_restart.article_delta", Operation: DeviationReplace},
				{Dimension: DeviationMetrics, Path: "pre_restart_mutations", Operation: DeviationReplace},
			},
		}},
	}
	effective, product, err := PrepareDeviationExpectation(profile, manifest, oracle, expectation, policy)
	if err != nil {
		t.Fatalf("prepare DEV-0008 expectation: %v", err)
	}
	differences, err := Compare(profile, effective, oracle, product)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 4 {
		t.Fatalf("DEV-0008 prepared differences = %d, want 4: %#v", len(differences), differences)
	}

	tests := []struct {
		name   string
		mutate func(*DeviationExpectation)
	}{
		{
			name: "missing selector",
			mutate: func(candidate *DeviationExpectation) {
				candidate.Contracts[0].Changes = candidate.Contracts[0].Changes[:3]
			},
		},
		{
			name: "extra selector",
			mutate: func(candidate *DeviationExpectation) {
				candidate.Contracts[0].Changes = append(candidate.Contracts[0].Changes, candidate.Contracts[0].Changes[0])
			},
		},
		{
			name: "fresh lane escape",
			mutate: func(candidate *DeviationExpectation) {
				candidate.Contracts[0].Changes[0].Path = "fresh.accepted"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneDeviationExpectation(t, expectation)
			test.mutate(&candidate)
			if _, _, err := PrepareDeviationExpectation(profile, manifest, oracle, candidate, policy); err == nil {
				t.Fatal("invalid DEV-0008 selector set produced a false green")
			}
		})
	}
}

func TestSystemStateDeclaredDimensionsAndBindingsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _, _ := loadSystemStateArtifacts(t)
	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateMigrationDefinitionSourceValue(observation.Result)
			case CompareDBState:
				changed = mutateMigrationDefinitionSourceValue(observation.DBState)
			case CompareMetrics:
				changed = mutateMigrationDefinitionSourceValue(observation.Metrics)
			default:
				t.Fatalf("contract %s unexpected comparison %q", contract.ID, dimension)
			}
			if !changed {
				t.Fatalf("contract %s %s has no mutable payload", contract.ID, dimension)
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
	reordered := cloneSuite(t, oracle)
	reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
	if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
		t.Fatal("system-state contract reorder produced a false green")
	}
}

func TestSystemStateReferenceIsSecretFreeAndScenarioSourcesAreArtifactBlind(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "system-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		t.Fatal(err)
	}
	assertSystemStateSecretFreeJSON(t, value)
	for _, name := range []string{
		"conformance/runners/django/system_state_decisions.py",
		"conformance/runners/django/system_state_scenarios.py",
		"conformance/runners/django/system_state_worker.py",
		"conformance/runners/django/system_state_fixture/urls.py",
		"conformance/runners/godj/gdj0045_system_state_scenarios.go",
		"conformance/runners/godj/gdj0045_system_state_worker.go",
		"conformance/systemstate/worker/protocol.go",
		"conformance/systemstate/worker/worker.go",
		"conformance/systemstate/worker/application.go",
		"conformance/systemstate/worker/actions.go",
		"conformance/systemstate/worker/cmd/main.go",
	} {
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not-implemented"} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("independent scenario source %s contains expected artifact marker %q", name, forbidden)
			}
		}
	}
}

func TestCurrentTwentyOneReferenceSetsHave231ContractsAndReject420OrderedCrossBindings(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	djangoProfile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	drfProfile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	type inventorySet struct {
		name     string
		profile  Profile
		manifest Manifest
		oracle   ObservationSuite
	}
	djangoSets := []struct{ name, manifest, oracle string }{
		{"read", "manifest.json", "oracle.json"},
		{"write-migration", "write-migration-manifest.json", "write-migration-oracle.json"},
		{"save-lifecycle", "save-lifecycle-manifest.json", "save-lifecycle-oracle.json"},
		{"query-cache", "query-cache-manifest.json", "query-cache-oracle.json"},
		{"migration-planning", "migration-planning-manifest.json", "migration-planning-oracle.json"},
		{"migration-execution", "migration-execution-manifest.json", "migration-execution-oracle.json"},
		{"migration-restart", "migration-restart-manifest.json", "migration-restart-oracle.json"},
		{"migration-state-reconstruction", "migration-state-reconstruction-manifest.json", "migration-state-reconstruction-oracle.json"},
		{"migration-lifecycle", "migration-lifecycle-manifest.json", "migration-lifecycle-oracle.json"},
		{"migration-definition-source", "migration-definition-source-manifest.json", "migration-definition-source-oracle.json"},
		{"migration-project-check", "migration-project-check-manifest.json", "migration-project-check-oracle.json"},
		{"relation", "relation-manifest.json", "relation-oracle.json"},
		{"query-breadth", "query-breadth-manifest.json", "query-breadth-oracle.json"},
		{"query-expression", "query-expression-manifest.json", "query-expression-oracle.json"},
		{"migration-relation", "migration-relation-manifest.json", "migration-relation-oracle.json"},
		{"template-form", "template-form-manifest.json", "template-form-oracle.json"},
		{"auth-session", "auth-session-manifest.json", "auth-session-oracle.json"},
		{"article-admin", "article-admin-manifest.json", "article-admin-oracle.json"},
		{"system-state", "system-state-manifest.json", "system-state.json"},
	}
	sets := make([]inventorySet, 0, 21)
	for _, source := range djangoSets {
		manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", source.manifest))
		if err != nil {
			t.Fatal(err)
		}
		oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", source.oracle))
		if err != nil {
			t.Fatal(err)
		}
		sets = append(sets, inventorySet{source.name, djangoProfile, manifest, oracle})
	}
	for _, source := range gdj0044ContractSets() {
		manifest, oracle, _ := loadGDJ0044Set(t, root, source)
		sets = append(sets, inventorySet{source.name, drfProfile, manifest, oracle})
	}
	ids, scenarios := map[string]string{}, map[string]string{}
	passing, deviations, locked, total := 0, 0, 0, 0
	for _, set := range sets {
		if err := ValidateSuiteAgainst(set.profile, set.manifest, set.oracle); err != nil {
			t.Fatalf("%s set does not validate: %v", set.name, err)
		}
		total += len(set.manifest.Contracts)
		for _, contract := range set.manifest.Contracts {
			if previous := ids[contract.ID]; previous != "" {
				t.Fatalf("contract %s shared by %s and %s", contract.ID, previous, set.name)
			}
			if previous := scenarios[contract.Scenario]; previous != "" {
				t.Fatalf("scenario %s shared by %s and %s", contract.Scenario, previous, set.name)
			}
			ids[contract.ID], scenarios[contract.Scenario] = set.name, set.name
			switch contract.Status {
			case ContractPassing:
				passing++
			case ContractDeviation:
				deviations++
			case ContractOracleLocked:
				locked++
			default:
				t.Fatalf("contract %s unexpected status %q", contract.ID, contract.Status)
			}
		}
	}
	if len(sets) != 21 || total != 231 || len(ids) != 231 || len(scenarios) != 231 || passing != 203 || deviations != 16 || locked != 12 {
		t.Fatalf("reference inventory = %d sets/%d contracts/%d IDs/%d scenarios = %d passing + %d deviation + %d oracle_locked", len(sets), total, len(ids), len(scenarios), passing, deviations, locked)
	}
	crossBindings := 0
	for manifestIndex, manifestSet := range sets {
		for suiteIndex, suiteSet := range sets {
			if manifestIndex == suiteIndex {
				continue
			}
			crossBindings++
			if err := ValidateSuiteAgainst(manifestSet.profile, manifestSet.manifest, suiteSet.oracle); err == nil {
				t.Fatalf("%s manifest accepted %s oracle", manifestSet.name, suiteSet.name)
			}
		}
	}
	if crossBindings != 420 {
		t.Fatalf("ordered cross-bindings = %d, want 420", crossBindings)
	}
}

func TestSystemStatePublishedProductMakeAndWorkflowWiringIsExact(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	makeContents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	makeText := string(makeContents)
	for variable, value := range map[string]string{
		"SYSTEM_STATE_MANIFEST":           "conformance/contracts/system-state-manifest.json",
		"SYSTEM_STATE_ORACLE":             "conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json",
		"SYSTEM_STATE_NOT_IMPLEMENTED":    "conformance/fixtures/godj-system-state-not-implemented.json",
		"SYSTEM_STATE_DEVIATION_EXPECTED": "conformance/fixtures/godj-system-state-deviation-expected.json",
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
	if got := strings.Count(referenceTarget, "$(SYSTEM_STATE_MANIFEST)"); got != 2 {
		t.Fatalf("reference system-state manifest count = %d, want oracle and NI", got)
	}
	if got := strings.Count(referenceTarget, "$(SYSTEM_STATE_ORACLE)"); got != 1 {
		t.Fatalf("reference system-state oracle count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "$(SYSTEM_STATE_NOT_IMPLEMENTED)"); got != 1 {
		t.Fatalf("reference system-state NI count = %d, want 1", got)
	}
	for variable, want := range map[string]int{
		"$(SYSTEM_STATE_MANIFEST)":           1,
		"$(SYSTEM_STATE_ORACLE)":             1,
		"$(SYSTEM_STATE_DEVIATION_EXPECTED)": 1,
	} {
		if got := strings.Count(productTarget, variable); got != want {
			t.Fatalf("product system-state variable %s count = %d, want %d", variable, got, want)
		}
	}
	if strings.Contains(productTarget, "$(SYSTEM_STATE_NOT_IMPLEMENTED)") {
		t.Fatal("product system-state adapter uses the not-implemented fixture")
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 20 {
		t.Fatalf("product adapter count = %d, want 20", got)
	}
	for name, target := range map[string]string{
		"oracle-check":      oracleCheckTarget,
		"oracle-regenerate": oracleRegenerateTarget,
	} {
		if got := strings.Count(target, "$(SYSTEM_STATE_MANIFEST)"); got != 1 {
			t.Fatalf("%s system-state manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "$(SYSTEM_STATE_ORACLE)"); got != 1 {
			t.Fatalf("%s system-state oracle count = %d, want 1", name, got)
		}
	}
	if !strings.Contains(oracleCheckTarget, "--output $(SYSTEM_STATE_ORACLE) --check") {
		t.Fatal("oracle-check does not require an exact system-state no-rewrite check")
	}

	workflowContents, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(workflowContents)
	for _, artifact := range []string{
		"conformance/fixtures/godj-system-state-not-implemented.json",
		"conformance/fixtures/godj-system-state-deviation-expected.json",
	} {
		if got := strings.Count(workflow, artifact); got != 2 {
			t.Fatalf("system-state no-rewrite artifact %q count = %d, want both reference gates", artifact, got)
		}
	}
	for _, required := range []string{
		"working-directory: conformance/oracles/django-6.1-sqlite-darwin-arm64",
		"run: sha256sum --check SHA256SUMS",
		"len(SCENARIOS) == 231",
		"len(payload) == 860151",
		"b2671e3c39a1a4b98428f323e2331354cba1b744f2e9b38a477f8cb107df3232",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("system-state workflow lacks exact hash/payload fragment %q", required)
		}
	}
	for _, obsolete := range []string{
		"len(SCENARIOS) == 219",
		"len(payload) == 846764",
		"6561aff4fcb64ab1d348f8ac7d8d7642c87453febd81925fbcd769ee409cdb0f",
	} {
		if strings.Contains(workflow, obsolete) {
			t.Fatalf("system-state workflow retains obsolete reference inventory fragment %q", obsolete)
		}
	}
}

func loadSystemStateArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite, DeviationExpectation) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "system-state-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "system-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-system-state-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	deviation, err := LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-system-state-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline, deviation
}

func assertSystemStateProvenance(t *testing.T, contract Contract, djangoAuthority bool) {
	t.Helper()
	adrCount, apiBoundaryCount, djangoCount, devCount := 0, 0, 0, 0
	for _, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s provenance is not independent: %#v", contract.ID, provenance)
		}
		if provenance.Kind == "documentation" && provenance.Reference == "ADR-0047" && provenance.License == "" {
			adrCount++
		}
		if provenance.Kind == "documentation" && provenance.Reference == "ADR-0046" && provenance.License == "" {
			apiBoundaryCount++
		}
		if strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
			djangoCount++
			if provenance.License != "BSD-3-Clause" {
				t.Fatalf("contract %s Django provenance lacks BSD license: %#v", contract.ID, provenance)
			}
		}
		if provenance.Reference == "DEV-0008" {
			devCount++
			if provenance.Kind != "decision" || provenance.License != "" {
				t.Fatalf("contract %s DEV-0008 provenance = %#v", contract.ID, provenance)
			}
		}
		if provenance.Kind == "decision" && provenance.Reference != "DEV-0008" {
			t.Fatalf("contract %s carries unrelated decision provenance: %#v", contract.ID, provenance)
		}
	}
	if adrCount != 1 {
		t.Fatalf("contract %s ADR-0047 documentation count = %d, want 1", contract.ID, adrCount)
	}
	if contract.ID == "SYS-008" && apiBoundaryCount != 1 {
		t.Fatalf("SYS-008 Accepted ADR-0046 count = %d, want 1", apiBoundaryCount)
	}
	if contract.ID != "SYS-008" && apiBoundaryCount != 0 {
		t.Fatalf("contract %s unexpectedly carries ADR-0046", contract.ID)
	}
	if djangoAuthority && djangoCount == 0 {
		t.Fatalf("contract %s lacks exact Django authority", contract.ID)
	}
	if !djangoAuthority && djangoCount != 0 {
		t.Fatalf("contract %s decision authority carries %d Django references", contract.ID, djangoCount)
	}
	if contract.ID == "SYS-009" && devCount != 1 {
		t.Fatalf("SYS-009 DEV-0008 decision count = %d, want 1", devCount)
	}
	if contract.ID != "SYS-009" && devCount != 0 {
		t.Fatalf("contract %s unexpectedly carries DEV-0008", contract.ID)
	}
}

func assertSystemStateSecretFreeJSON(t *testing.T, value any) {
	t.Helper()
	forbiddenKeys := map[string]bool{
		"cookie_value": true, "csrf_token": true, "html": true, "password": true,
		"password_hash": true, "session_id": true, "session_key": true, "token": true,
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbiddenKeys[key] {
				t.Fatalf("system-state oracle contains forbidden secret/raw key %q", key)
			}
			assertSystemStateSecretFreeJSON(t, child)
		}
	case []any:
		for _, child := range typed {
			assertSystemStateSecretFreeJSON(t, child)
		}
	case string:
		lower := strings.ToLower(typed)
		for _, forbidden := range []string{"system-state-reference-credential", "csrfmiddlewaretoken", "sessionid", "set-cookie", "<html", "<form", "<!doctype"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("system-state oracle contains forbidden raw/secret marker %q", forbidden)
			}
		}
	}
}
