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
	"SYS-013", "SYS-014", "SYS-015", "SYS-016", "SYS-017", "SYS-018",
	"SYS-019", "SYS-020", "SYS-021", "SYS-022", "SYS-023", "SYS-024",
	"SYS-025", "SYS-026", "SYS-027", "SYS-028", "SYS-029", "SYS-030",
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
	"godj.system_state.coordinated_atomic_fence",
	"godj.system_state.concurrent_admin_bootstrap",
	"godj.system_state.concurrent_session_capacity",
	"godj.system_state.concurrent_touch_monotonicity",
	"godj.system_state.concurrent_session_rotation",
	"godj.system_state.concurrent_article_audit",
	"godj.system_state.shared_csrf_key_ring",
	"godj.system_state.two_process_backend_restart",
	"godj.system_state.explicit_operator_provisioning",
	"godj.system_state.createsuperuser_argv_and_pre_io",
	"godj.system_state.tty_secret_transport",
	"godj.system_state.project_provision_ownership",
	"godj.system_state.operator_provision_cardinality",
	"godj.system_state.provision_outcome_ownership",
	"godj.system_state.open_existing_authenticator",
	"godj.system_state.credential_absent_public_only",
	"godj.system_state.operator_backend_login_restart",
	"godj.system_state.sensitive_child_cleanup",
}

var systemStatePhases = []Phase{
	PhaseEnvironment, PhaseCommit, PhaseEvaluation, PhaseCommit,
	PhaseCommit, PhaseRollback, PhaseEvaluation, PhaseCommit,
	PhaseCommit, PhaseRollback, PhaseEvaluation, PhaseCommit,
	PhaseCommit, PhaseCommit, PhaseCommit, PhaseCommit,
	PhaseCommit, PhaseRollback, PhaseEvaluation, PhaseEnvironment,
	PhaseConstruction, PhaseEnvironment, PhaseEnvironment, PhaseEnvironment,
	PhaseCommit, PhaseCommit, PhaseEvaluation, PhaseEnvironment,
	PhaseEnvironment, PhaseEnvironment,
}

func TestSystemStateArtifactBytesAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size   int
		sha256 string
	}
	root := conformanceRepositoryRoot(t)
	wanted := map[string]artifactLock{
		"conformance/contracts/system-state-manifest.json":                                                  {16420, "ddae48e95770eacf2e3b761c7c4931b53dbcb65020cc375f624413ac71e0996c"},
		"conformance/fixtures/godj-system-state-not-implemented.json":                                       {3167, "eff126dff0e7e9375a09722d054a2f663150cea1440241875b82f60650d9aa53"},
		"conformance/fixtures/godj-system-state-deviation-expected.json":                                    {1141, "a2877ae785b937b2b1c9ee3b567a7631403a5b5ca91485d2a6c942066c744869"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json":                              {37866, "2251157e801295b084a51a7879e496fab528d7360fcb8c55bdd7b0b368862913"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS":                                     {2279, "ad256f0bf1b0322c6480b285c701648954b41bf06ecc6c203d4ba8c6b6c6bf87"},
		"conformance/systemstate/attestations/postgresql-17.10-two-process-v1.json":                         {1134, "64aec6b72729d36b441cbb2fd0bb3da5ece455db26925e7822df861900bbdc2d"},
		"conformance/systemstate/attestations/SHA256SUMS":                                                   {103, "97b52f0ac44d3e94f9ea8afbd419a6df5c0a4a025fa942b7644a0698fe11ab94"},
		"conformance/projectoperatorproduct/attestations/postgresql-17.10-sqlite-external-operator-v1.json": {1811, "5488c684001f58b41c39090152a812da91369e71b7a647ba9238631065bb1d7b"},
		"conformance/projectoperatorproduct/attestations/SHA256SUMS":                                        {116, "7ff6a7b74638f8835006fc5e3d86e7946f9b23bc724e89170609c14f23821897"},
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
	if len(manifest.Contracts) != 30 || len(oracle.Contracts) != 30 || len(baseline.Contracts) != 30 {
		t.Fatalf("system-state lengths = %d/%d/%d, want 30", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}
	djangoIDs := map[string]bool{
		"SYS-003": true, "SYS-004": true, "SYS-008": true,
		"SYS-009": true, "SYS-010": true, "SYS-011": true,
		"SYS-029": true,
	}
	passing, deviations, lockedContracts := 0, 0, 0
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
	if passing != 29 || deviations != 1 || lockedContracts != 0 {
		t.Fatalf("system-state classification = %d passing + %d deviation + %d oracle_locked, want 29 + 1 + 0", passing, deviations, lockedContracts)
	}
	for _, field := range []string{"callback_cancellation", "confirmed_callback_error"} {
		if got := objectField(t, oracle.Contracts[12].Result, field); !reflect.DeepEqual(*got, String("rolled_back")) {
			t.Fatalf("SYS-013 %s = %#v, want rolled_back", field, got)
		}
	}
	rotation := oracle.Contracts[16].Result
	if got := objectField(t, rotation, "touch_first_then_rotate"); !reflect.DeepEqual(*got, Object(map[string]Value{
		"old_rows":         Integer("0"),
		"replacement_rows": Integer("1"),
	})) {
		t.Fatalf("SYS-017 touch-first outcome = %#v", got)
	}
	if got := objectField(t, rotation, "rotate_first_stale_old_id_touch"); !reflect.DeepEqual(*got, Object(map[string]Value{
		"old_bearer_resurrected": Boolean(false),
		"outcome":                String("not_found"),
	})) {
		t.Fatalf("SYS-017 rotate-first stale-touch outcome = %#v", got)
	}
	for _, field := range []string{"child_starts_on_rejection", "project_builds_on_rejection", "project_discoveries_on_rejection", "terminal_reads_on_rejection"} {
		if got := objectField(t, oracle.Contracts[21].Metrics, field); !reflect.DeepEqual(*got, Integer("0")) {
			t.Fatalf("SYS-022 %s = %#v, want 0", field, got)
		}
	}
	for _, field := range []string{"backend_opens_on_rejection", "project_reads_on_rejection", "writes_on_rejection"} {
		if got := objectField(t, oracle.Contracts[21].DBState, field); !reflect.DeepEqual(*got, Integer("0")) {
			t.Fatalf("SYS-022 %s = %#v, want 0", field, got)
		}
	}
	for _, field := range []string{"terminal_reads_before_project_build", "terminal_reads_before_project_selection"} {
		if got := objectField(t, oracle.Contracts[22].Metrics, field); !reflect.DeepEqual(*got, Integer("0")) {
			t.Fatalf("SYS-023 %s = %#v, want 0", field, got)
		}
	}
	cardinalityCases := objectField(t, oracle.Contracts[24].Result, "cases")
	if cardinalityCases.Type != ValueList || len(cardinalityCases.Items) != 6 {
		t.Fatalf("SYS-025 cases = %#v, want exact six-case decision", cardinalityCases)
	}
	if got := objectField(t, &cardinalityCases.Items[1], "loser_outcome"); !reflect.DeepEqual(*got, String("credential_already_exists")) {
		t.Fatalf("SYS-025 concurrent loser outcome = %#v, want credential_already_exists", got)
	}
	if got := objectField(t, &cardinalityCases.Items[3], "outcome"); !reflect.DeepEqual(*got, String("invalid_cardinality")) {
		t.Fatalf("SYS-025 two-or-more outcome = %#v, want invalid_cardinality", got)
	}
	if got := objectField(t, &cardinalityCases.Items[4], "outcome"); !reflect.DeepEqual(*got, String("corrupt_state")) {
		t.Fatalf("SYS-025 malformed/profile outcome = %#v, want corrupt_state", got)
	}
	if got := objectField(t, &cardinalityCases.Items[5], "outcome"); !reflect.DeepEqual(*got, String("credential_policy_mismatch")) {
		t.Fatalf("SYS-025 policy outcome = %#v, want credential_policy_mismatch", got)
	}
	ownershipCases := objectField(t, oracle.Contracts[25].Result, "cases")
	wantOwnershipCases := []string{
		"confirmed_rollback",
		"commit_outcome_unknown",
		"known_created_backend_close_failure",
		"known_created_workspace_cleanup_failure",
		"known_created_output_failure",
	}
	if ownershipCases.Type != ValueList || len(ownershipCases.Items) != len(wantOwnershipCases) {
		t.Fatalf("SYS-026 cases = %#v, want exact rollback/unknown/backend/workspace/output decision", ownershipCases)
	}
	for index, wantCase := range wantOwnershipCases {
		if got := objectField(t, &ownershipCases.Items[index], "case"); !reflect.DeepEqual(*got, String(wantCase)) {
			t.Fatalf("SYS-026 case %d = %#v, want %q", index, got, wantCase)
		}
		if got := objectField(t, &ownershipCases.Items[index], "retry"); !reflect.DeepEqual(*got, Boolean(false)) {
			t.Fatalf("SYS-026 case %q retry = %#v, want false", wantCase, got)
		}
	}
	for index := 2; index < len(ownershipCases.Items); index++ {
		if got := objectField(t, &ownershipCases.Items[index], "creation"); !reflect.DeepEqual(*got, String("preserved")) {
			t.Fatalf("SYS-026 known-created case %d creation = %#v, want preserved", index, got)
		}
		if got := objectField(t, &ownershipCases.Items[index], "known_created"); !reflect.DeepEqual(*got, Boolean(true)) {
			t.Fatalf("SYS-026 known-created case %d marker = %#v, want true", index, got)
		}
	}
	validationCases := objectField(t, oracle.Contracts[26].Result, "validation_cases")
	wantValidationOutcomes := []string{"authenticator_ready", "corrupt_state", "credential_policy_mismatch"}
	if validationCases.Type != ValueList || len(validationCases.Items) != len(wantValidationOutcomes) {
		t.Fatalf("SYS-027 validation cases = %#v, want exact valid/profile/policy decision", validationCases)
	}
	for index, wantOutcome := range wantValidationOutcomes {
		if got := objectField(t, &validationCases.Items[index], "outcome"); !reflect.DeepEqual(*got, String(wantOutcome)) {
			t.Fatalf("SYS-027 validation case %d outcome = %#v, want %q", index, got, wantOutcome)
		}
	}
	if got := objectField(t, oracle.Contracts[26].Metrics, "credential_mismatch_code_occurrences"); !reflect.DeepEqual(*got, Integer("0")) {
		t.Fatalf("SYS-027 retired credential_mismatch occurrences = %#v, want 0", got)
	}
	publicOnlyCases := objectField(t, oracle.Contracts[27].Result, "cases")
	wantPublicOnlyOutcomes := []string{
		"credential_absent",
		"schema_unavailable",
		"startup_failure",
		"startup_failure",
		"corrupt_state",
		"credential_policy_mismatch",
	}
	if publicOnlyCases.Type != ValueList || len(publicOnlyCases.Items) != len(wantPublicOnlyOutcomes) {
		t.Fatalf("SYS-028 cases = %#v, want exact clean/schema/table/dependent/corrupt/policy decision", publicOnlyCases)
	}
	for index, wantOutcome := range wantPublicOnlyOutcomes {
		if got := objectField(t, &publicOnlyCases.Items[index], "outcome"); !reflect.DeepEqual(*got, String(wantOutcome)) {
			t.Fatalf("SYS-028 case %d outcome = %#v, want %q", index, got, wantOutcome)
		}
		wantPublicOnly := index == 0
		if got := objectField(t, &publicOnlyCases.Items[index], "public_only"); !reflect.DeepEqual(*got, Boolean(wantPublicOnly)) {
			t.Fatalf("SYS-028 case %d public-only = %#v, want %t", index, got, wantPublicOnly)
		}
	}
	backendCases := objectField(t, oracle.Contracts[28].Result, "backend_cases")
	wantBackends := []string{"postgresql_17_10", "sqlite"}
	if backendCases.Type != ValueList || len(backendCases.Items) != len(wantBackends) {
		t.Fatalf("SYS-029 backend cases = %#v, want exact PostgreSQL/SQLite decision", backendCases)
	}
	for index, wantBackend := range wantBackends {
		backendCase := &backendCases.Items[index]
		if got := objectField(t, backendCase, "backend"); !reflect.DeepEqual(*got, String(wantBackend)) {
			t.Fatalf("SYS-029 backend case %d = %#v, want %q", index, got, wantBackend)
		}
		for _, field := range []string{"admin_authenticated", "api_authenticated", "distinct_process_restart", "provisioned", "provision_process_distinct_from_runtime"} {
			if got := objectField(t, backendCase, field); !reflect.DeepEqual(*got, Boolean(true)) {
				t.Fatalf("SYS-029 backend %q %s = %#v, want true", wantBackend, field, got)
			}
		}
	}
	loginMetrics := oracle.Contracts[28].Metrics
	if got := objectField(t, loginMetrics, "distinct_processes_per_backend"); !reflect.DeepEqual(*got, Integer("3")) {
		t.Fatalf("SYS-029 distinct processes per backend = %#v, want 3", got)
	}
	if got := objectField(t, loginMetrics, "provision_calls_per_backend"); !reflect.DeepEqual(*got, Integer("1")) {
		t.Fatalf("SYS-029 provision calls per backend = %#v, want 1", got)
	}
	if got := objectField(t, loginMetrics, "provision_processes_per_backend"); !reflect.DeepEqual(*got, Integer("1")) {
		t.Fatalf("SYS-029 provision processes per backend = %#v, want 1", got)
	}
	if got := objectField(t, loginMetrics, "runtime_processes_per_backend"); !reflect.DeepEqual(*got, Integer("2")) {
		t.Fatalf("SYS-029 runtime processes per backend = %#v, want 2", got)
	}
	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 30 {
		t.Fatalf("system-state oracle/baseline differences = %d, want 30", len(differences))
	}
	for index, difference := range differences {
		if difference.ContractID != systemStateIDs[index] || difference.Path != "status" {
			t.Fatalf("system-state difference %d = %#v", index, difference)
		}
	}
	if deviation.ProfileID != profile.ID || deviation.Decision != "DEV-0008" || len(deviation.Contracts) != 1 || deviation.Contracts[0].ID != "SYS-009" {
		t.Fatalf("system-state deviation envelope = %#v", deviation)
	}
	legacyBaseline := baseline
	legacyBaseline.Contracts = append([]Observation(nil), baseline.Contracts[:12]...)
	legacyBaselineBytes, err := MarshalCanonical(legacyBaseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyBaselineBytes) != 1527 || fmt.Sprintf("%x", sha256.Sum256(legacyBaselineBytes)) != "6b5549d00484c69ec8bf4186bf9a4b99ff5adf1b2040250159be49dc3f3f4533" {
		t.Fatalf("legacy SYS-001..012 NI canonical bytes drifted: size=%d sha256=%x", len(legacyBaselineBytes), sha256.Sum256(legacyBaselineBytes))
	}
	preGDJ0055Baseline := baseline
	preGDJ0055Baseline.Contracts = append([]Observation(nil), baseline.Contracts[:20]...)
	preGDJ0055BaselineBytes, err := MarshalCanonical(preGDJ0055Baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(preGDJ0055BaselineBytes) != 2026 || fmt.Sprintf("%x", sha256.Sum256(preGDJ0055BaselineBytes)) != "3a486bb24eddf660c9b10207031ed9d491f833fb2d18a02127b5260163ad7321" {
		t.Fatalf("pre-GDJ-0055 SYS-001..020 NI canonical bytes drifted: size=%d sha256=%x", len(preGDJ0055BaselineBytes), sha256.Sum256(preGDJ0055BaselineBytes))
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
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios.go",
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios_sessions.go",
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios_article.go",
		"conformance/runners/godj/gdj0046_system_state_multi_runtime_scenarios_csrf.go",
		"conformance/runners/godj/gdj0046_system_state_two_process_execution.go",
		"conformance/runners/godj/gdj0046_system_state_two_process_scenario.go",
		"conformance/runners/godj/gdj0055_operator_backend_scenario.go",
		"conformance/runners/godj/gdj0055_operator_process_scenarios.go",
		"conformance/runners/godj/gdj0055_operator_runtime_scenarios.go",
		"conformance/runners/godj/gdj0055_operator_scenarios.go",
		"conformance/runners/godj/inputs.go",
		"conformance/runners/godj/runner.go",
		"examples/article/siteappconformance/composition.go",
		"conformance/systemstate/restart/multiruntime_postgres_unix_test.go",
		"conformance/systemstate/multiruntimeworker/cmd/main.go",
		"conformance/systemstate/multiruntimeworker/inspection.go",
		"conformance/systemstate/multiruntimeworker/protocol.go",
		"conformance/systemstate/multiruntimeworker/scenario_other.go",
		"conformance/systemstate/multiruntimeworker/scenario_unix.go",
		"conformance/systemstate/multiruntimeworker/worker.go",
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
		for _, forbidden := range []string{
			"conformance/contracts",
			"conformance/oracles",
			"conformance/fixtures",
			"conformance/systemstate/attestations",
			"conformance/projectoperatorproduct/attestations",
			"not-implemented",
		} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("independent scenario source %s contains expected artifact marker %q", name, forbidden)
			}
		}
	}
}

func TestCurrentTwentySevenReferenceSetsHave311ContractsAndReject702OrderedCrossBindings(t *testing.T) {
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
		{"migration-command", "migration-command-manifest.json", "migration-command-oracle.json"},
		{"migration-writer", "migration-writer-manifest.json", "migration-writer-oracle.json"},
		{"migration-status", "migration-status-manifest.json", "migration-status-oracle.json"},
		{"migration-target-plan", "migration-target-plan-manifest.json", "migration-target-plan-oracle.json"},
		{"migration-sql-rendering", "migration-sql-rendering-manifest.json", "migration-sql-rendering-oracle.json"},
		{"relation", "relation-manifest.json", "relation-oracle.json"},
		{"query-breadth", "query-breadth-manifest.json", "query-breadth-oracle.json"},
		{"query-expression", "query-expression-manifest.json", "query-expression-oracle.json"},
		{"migration-relation", "migration-relation-manifest.json", "migration-relation-oracle.json"},
		{"template-form", "template-form-manifest.json", "template-form-oracle.json"},
		{"auth-session", "auth-session-manifest.json", "auth-session-oracle.json"},
		{"article-admin", "article-admin-manifest.json", "article-admin-oracle.json"},
		{"system-state", "system-state-manifest.json", "system-state.json"},
	}
	sets := make([]inventorySet, 0, 27)
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
	apiAuthenticationManifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "api-authentication-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	apiAuthenticationOracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", "api-authentication-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	sets = append(sets, inventorySet{"api-authentication", drfProfile, apiAuthenticationManifest, apiAuthenticationOracle})
	ids, scenarios := map[string]string{}, map[string]string{}
	lockedIDs := make([]string, 0, 12)
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
				lockedIDs = append(lockedIDs, contract.ID)
			default:
				t.Fatalf("contract %s unexpected status %q", contract.ID, contract.Status)
			}
		}
	}
	if len(sets) != 27 || total != 311 || len(ids) != 311 || len(scenarios) != 311 || passing != 274 || deviations != 25 || locked != 12 {
		t.Fatalf("reference inventory = %d sets/%d contracts/%d IDs/%d scenarios = %d passing + %d deviation + %d oracle_locked", len(sets), total, len(ids), len(scenarios), passing, deviations, locked)
	}
	if want := []string{
		"MIG-075", "MIG-076", "MIG-077", "MIG-078", "MIG-079", "MIG-080",
		"MIG-081", "MIG-082", "MIG-083", "MIG-084", "MIG-085", "MIG-086",
	}; !reflect.DeepEqual(lockedIDs, want) {
		t.Fatalf("remaining oracle_locked contracts = %v, want %v", lockedIDs, want)
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
	if crossBindings != 702 {
		t.Fatalf("ordered cross-bindings = %d, want 702", crossBindings)
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
		"SYSTEM_STATE_MANIFEST":                 "conformance/contracts/system-state-manifest.json",
		"SYSTEM_STATE_ORACLE":                   "conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json",
		"SYSTEM_STATE_NOT_IMPLEMENTED":          "conformance/fixtures/godj-system-state-not-implemented.json",
		"SYSTEM_STATE_DEVIATION_EXPECTED":       "conformance/fixtures/godj-system-state-deviation-expected.json",
		"SYSTEM_STATE_POSTGRES_ATTESTATION":     "conformance/systemstate/attestations/postgresql-17.10-two-process-v1.json",
		"PROJECT_OPERATOR_POSTGRES_ATTESTATION": "conformance/projectoperatorproduct/attestations/postgresql-17.10-sqlite-external-operator-v1.json",
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
		"$(SYSTEM_STATE_MANIFEST)":                 1,
		"$(SYSTEM_STATE_ORACLE)":                   1,
		"$(SYSTEM_STATE_DEVIATION_EXPECTED)":       1,
		"$(SYSTEM_STATE_POSTGRES_ATTESTATION)":     1,
		"$(PROJECT_OPERATOR_POSTGRES_ATTESTATION)": 1,
	} {
		if got := strings.Count(productTarget, variable); got != want {
			t.Fatalf("product system-state variable %s count = %d, want %d", variable, got, want)
		}
	}
	if strings.Contains(productTarget, "$(SYSTEM_STATE_NOT_IMPLEMENTED)") {
		t.Fatal("product system-state adapter uses the not-implemented fixture")
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 26 {
		t.Fatalf("product adapter count = %d, want 26", got)
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
		"len(SCENARIOS) == 311",
		"len(payload) == 1081058",
		"b8d53e874169009fcd4650c79f2a007e18307d2fddd07a07d970f28bce2ed3f5",
		"working-directory: conformance/systemstate/attestations",
		"working-directory: conformance/projectoperatorproduct/attestations",
		"GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE",
		"GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE",
		"TestSystemStatePostgresTwoProcessCoordinationRestartSentinel",
		"TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation",
		"TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct",
		"cmp \"$GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE\"",
		"cmp \"$GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE\"",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("system-state workflow lacks exact hash/payload fragment %q", required)
		}
	}
	for _, obsolete := range []string{
		"len(SCENARIOS) == 231",
	} {
		if strings.Contains(workflow, obsolete) {
			t.Fatalf("system-state workflow retains obsolete reference inventory fragment %q", obsolete)
		}
	}
	postgresStart := strings.Index(workflow, "\n  postgresql-product:\n")
	postgresEnd := strings.Index(workflow, "\n  sqlite-matrix:\n")
	if postgresStart < 0 || postgresEnd <= postgresStart {
		t.Fatal("cannot isolate PostgreSQL product workflow job")
	}
	postgresJob := workflow[postgresStart:postgresEnd]
	previous := -1
	for _, fragment := range []string{
		"image: postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f",
		"- name: Assert exact PostgreSQL service profile",
		"- name: Run and inventory PostgreSQL actual product tests",
		"GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE:",
		"GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE:",
		"TestSystemStatePostgresTwoProcessCoordinationRestartSentinel",
		"TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation",
		"TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct",
		"cmp \"$GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE\"",
		"cmp \"$GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE\"",
		"cd conformance/projectoperatorproduct/attestations",
	} {
		current := strings.Index(postgresJob, fragment)
		if current <= previous {
			t.Fatalf("PostgreSQL attestation workflow fragment %q is missing or out of order", fragment)
		}
		previous = current
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
	adrCount, multiRuntimeADRCount, operatorADRCount, apiBoundaryCount, djangoCount, devCount := 0, 0, 0, 0, 0, 0
	for _, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s provenance is not independent: %#v", contract.ID, provenance)
		}
		if provenance.Kind == "documentation" && provenance.Reference == "ADR-0047" && provenance.License == "" {
			adrCount++
		}
		if provenance.Kind == "documentation" && provenance.Reference == "ADR-0048" && provenance.License == "" {
			multiRuntimeADRCount++
		}
		if provenance.Kind == "documentation" && provenance.Reference == "ADR-0056" && provenance.License == "" {
			operatorADRCount++
		}
		if provenance.Kind == "documentation" && provenance.Reference == "ADR-0046" && provenance.License == "" {
			apiBoundaryCount++
		}
		if strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
			djangoCount++
			if provenance.License != "BSD-3-Clause" {
				t.Fatalf("contract %s Django provenance lacks BSD license: %#v", contract.ID, provenance)
			}
			if strings.Contains(provenance.Reference, "createsuperuser") || strings.Contains(provenance.Reference, "management/commands") {
				t.Fatalf("contract %s fabricates Django management-command authority: %#v", contract.ID, provenance)
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
		if provenance.Kind == "proposal" {
			t.Fatalf("contract %s carries unrelated proposal provenance: %#v", contract.ID, provenance)
		}
	}
	legacy := contract.ID <= "SYS-012"
	multiRuntime := contract.ID >= "SYS-013" && contract.ID <= "SYS-020"
	operator := contract.ID >= "SYS-021" && contract.ID <= "SYS-030"
	if legacy && (adrCount != 1 || multiRuntimeADRCount != 0 || operatorADRCount != 0) {
		t.Fatalf("legacy contract %s authority = %d ADR-0047 + %d ADR-0048, want 1 + 0", contract.ID, adrCount, multiRuntimeADRCount)
	}
	if multiRuntime && (len(contract.Provenance) != 1 || adrCount != 0 || multiRuntimeADRCount != 1 || operatorADRCount != 0) {
		t.Fatalf("multi-runtime contract %s authority = %#v, want exact Accepted ADR-0048", contract.ID, contract.Provenance)
	}
	if operator {
		wantProvenance := 1
		wantDjango := 0
		if contract.ID == "SYS-029" {
			wantProvenance = 5
			wantDjango = 4
		}
		if len(contract.Provenance) != wantProvenance || adrCount != 0 || multiRuntimeADRCount != 0 || operatorADRCount != 1 || djangoCount != wantDjango {
			t.Fatalf("operator contract %s authority = %#v, want ADR-0056 documentation with %d pinned Django login references", contract.ID, contract.Provenance, wantDjango)
		}
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
