package protocol

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var gdj0047IDs = []string{
	"AUT-009", "AUT-010", "AUT-011", "AUT-012", "AUT-013",
	"AUT-014", "AUT-015", "AUT-016", "API-011", "API-012",
}

var gdj0047Scenarios = []string{
	"godj.api_authentication.common_authentication_boundary",
	"godj.api_authentication.bounded_bearer_header",
	"drf.api_authentication.missing_and_unsupported",
	"drf.api_authentication.invalid_and_valid_token",
	"drf.api_authentication.permission_denial",
	"drf.api_authentication.unsafe_without_csrf",
	"drf.api_authentication.profile_isolation",
	"godj.api_authentication.secret_and_failure_boundary",
	"godj.api_authentication.article_route_reuse",
	"godj.api_authentication.denial_mutation_boundary",
}

var gdj0047Phases = []Phase{
	PhaseConstruction,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseCommit,
	PhaseEvaluation,
	PhaseEvaluation,
	PhaseCommit,
	PhaseEvaluation,
}

var gdj0047Comparisons = [][]ComparisonDimension{
	{CompareResult, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
	{CompareResult, CompareDBState, CompareMetrics},
}

var gdj0047Statuses = []ContractStatus{
	ContractPassing,
	ContractPassing,
	ContractPassing,
	ContractDeviation,
	ContractDeviation,
	ContractPassing,
	ContractDeviation,
	ContractPassing,
	ContractPassing,
	ContractPassing,
}

func TestGDJ0047PublishedArtifactsAreExactAndPayloadSafe(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)

	oraclePath := filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", "api-authentication-oracle.json")
	oracleBytes := readArtifact(t, oraclePath)
	for _, forbidden := range []string{
		"gdj-phase-a-raw-bearer-canary",
		"gdj-phase-a-unknown-bearer",
		root,
	} {
		if strings.Contains(string(oracleBytes), forbidden) {
			t.Fatalf("API authentication oracle leaks forbidden source value %q", forbidden)
		}
	}
	decisionSource := string(readArtifact(t, filepath.Join(root, "conformance", "runners", "django", "api_authentication_decisions.py")))
	for _, forbidden := range []string{
		"conformance/contracts",
		"conformance/oracles",
		"conformance/fixtures",
		"not-implemented",
		"not_implemented",
	} {
		if strings.Contains(decisionSource, forbidden) {
			t.Fatalf("API authentication decision source reads or names locked artifact boundary %q", forbidden)
		}
	}
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "api-authentication-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-api-authentication-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("API authentication oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("API authentication baseline does not validate: %v", err)
	}
	if profile.ID != "drf-3.18.0-django-6.1-sqlite-darwin-arm64" || profile.Fingerprint.DjangoVersion != "6.1" || profile.Fingerprint.PythonVersion != "3.14.3" || profile.Lock.File != "conformance/reference/drf/uv.lock" || profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected GDJ-0047 profile: %#v", profile)
	}
	if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
		t.Fatal("API authentication suites do not preserve the exact profile snapshot")
	}
	if len(manifest.Contracts) != 10 || len(oracle.Contracts) != 10 || len(baseline.Contracts) != 10 {
		t.Fatalf("API authentication artifact lengths = %d/%d/%d, want 10/10/10", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}

	for index, contract := range manifest.Contracts {
		if contract.ID != gdj0047IDs[index] || contract.Scenario != gdj0047Scenarios[index] || contract.Phase != gdj0047Phases[index] || contract.Status != gdj0047Statuses[index] || !reflect.DeepEqual(contract.Comparison, gdj0047Comparisons[index]) {
			t.Fatalf("API authentication contract %d = %#v", index, contract)
		}
		assertGDJ0047Authority(t, contract)

		observed := oracle.Contracts[index]
		if observed.ID != contract.ID || observed.Status != StatusObserved || observed.Phase != contract.Phase {
			t.Fatalf("API authentication oracle contract %d = %#v", index, observed)
		}
		assertGDJ0044DeclaredPayloads(t, contract, observed)

		locked := baseline.Contracts[index]
		if locked.ID != contract.ID || locked.Status != StatusNotImplemented || locked.Phase != contract.Phase || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
			t.Fatalf("API authentication baseline contract %d is not payload-free: %#v", index, locked)
		}
	}

	deviation, err := LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", "godj-api-authentication-deviation-expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	policy := DeviationPolicy{
		Decision: "DEV-0009",
		Contracts: []DeviationContractPolicy{
			{ID: "AUT-012", Changes: []DeviationChangePolicy{
				{Dimension: DeviationResult, Path: "[0].response.error_codes.detail", Operation: DeviationReplace},
				{Dimension: DeviationResult, Path: "[0].response.www_authenticate", Operation: DeviationReplace},
				{Dimension: DeviationResult, Path: "[1].response.error_codes.detail", Operation: DeviationReplace},
				{Dimension: DeviationResult, Path: "[1].response.www_authenticate", Operation: DeviationReplace},
			}},
			{ID: "AUT-013", Changes: []DeviationChangePolicy{
				{Dimension: DeviationResult, Path: "www_authenticate", Operation: DeviationReplace},
			}},
			{ID: "AUT-015", Changes: []DeviationChangePolicy{
				{Dimension: DeviationResult, Path: "[1].response.error_codes.detail", Operation: DeviationReplace},
				{Dimension: DeviationResult, Path: "[1].response.www_authenticate", Operation: DeviationReplace},
			}},
		},
	}
	effective, expectedProduct, err := PrepareDeviationExpectation(profile, manifest, oracle, deviation, policy)
	if err != nil {
		t.Fatalf("prepare DEV-0009 product expectation: %v", err)
	}
	if len(effective.Contracts) != 10 || len(expectedProduct.Contracts) != 10 {
		t.Fatalf("DEV-0009 effective/product lengths = %d/%d, want 10/10", len(effective.Contracts), len(expectedProduct.Contracts))
	}
	if differences, err := Compare(profile, effective, expectedProduct, expectedProduct); err != nil || len(differences) != 0 {
		t.Fatalf("DEV-0009 expected product is not self-consistent: differences=%#v error=%v", differences, err)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 10 {
		t.Fatalf("API authentication oracle/baseline differences = %d, want 10", len(differences))
	}
	for index, difference := range differences {
		if difference.ContractID != gdj0047IDs[index] || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
			t.Fatalf("API authentication difference %d = %#v", index, difference)
		}
	}

	for index, contract := range manifest.Contracts {
		for _, dimension := range contract.Comparison {
			actual := cloneSuite(t, oracle)
			observation := &actual.Contracts[index]
			var changed bool
			switch dimension {
			case CompareResult:
				changed = mutateFirstScalar(observation.Result)
			case CompareDBState:
				changed = mutateFirstScalar(observation.DBState)
			case CompareMetrics:
				changed = mutateFirstScalar(observation.Metrics)
			}
			if !changed {
				t.Fatalf("contract %s declared %s without a mutable payload", contract.ID, dimension)
			}
			mutationDifferences, err := Compare(profile, manifest, oracle, actual)
			if err != nil {
				t.Fatal(err)
			}
			if len(mutationDifferences) == 0 {
				t.Fatalf("contract %s %s mutation produced a false green", contract.ID, dimension)
			}
			for _, difference := range mutationDifferences {
				if difference.ContractID != contract.ID {
					t.Fatalf("contract %s %s mutation reported against %s", contract.ID, dimension, difference.ContractID)
				}
			}
		}
	}
}

func assertGDJ0047Authority(t *testing.T, contract Contract) {
	t.Helper()

	proposalScenario := strings.HasPrefix(contract.Scenario, "godj.api_authentication.")
	adrCount, proposalCount, drfAuthorityCount, deviationDecisionCount := 0, 0, 0, 0
	for _, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s does not preserve independent provenance: %#v", contract.ID, provenance)
		}
		if provenance.Reference == "ADR-0049" {
			adrCount++
			if provenance.Kind != "documentation" || provenance.License != "" {
				t.Fatalf("contract %s ADR-0049 provenance = %#v, want documentation", contract.ID, provenance)
			}
		}
		if provenance.Reference == "GDJ-0047" {
			proposalCount++
			if provenance.Kind != "proposal" || provenance.License != "" {
				t.Fatalf("contract %s GDJ-0047 provenance = %#v, want proposal", contract.ID, provenance)
			}
		}
		if provenance.Kind == "decision" {
			if provenance.Reference != "DEV-0009" || contract.Status != ContractDeviation {
				t.Fatalf("contract %s has unapproved decision provenance: %#v", contract.ID, provenance)
			}
			deviationDecisionCount++
		}
		if strings.Contains(provenance.Reference, "django-rest-framework@11875a38f483cea69d8ef2fd9ede6b96fb602ec4:") || strings.HasPrefix(provenance.Reference, "https://www.django-rest-framework.org/") {
			drfAuthorityCount++
			if provenance.License != "BSD-3-Clause" {
				t.Fatalf("contract %s DRF provenance lacks BSD-3-Clause: %#v", contract.ID, provenance)
			}
		}
		if strings.HasPrefix(provenance.Reference, "https://datatracker.ietf.org/doc/html/rfc") && (provenance.Kind != "documentation" || provenance.License != "") {
			t.Fatalf("contract %s RFC provenance = %#v, want unlicensed documentation authority", contract.ID, provenance)
		}
	}
	if adrCount != 1 {
		t.Fatalf("contract %s ADR-0049 provenance count = %d, want 1", contract.ID, adrCount)
	}
	if proposalScenario && proposalCount != 1 {
		t.Fatalf("GoDj proposal contract %s GDJ-0047 provenance count = %d, want 1", contract.ID, proposalCount)
	}
	if !proposalScenario && proposalCount != 0 {
		t.Fatalf("DRF observation contract %s carries GDJ-0047 proposal provenance", contract.ID)
	}
	if proposalScenario && drfAuthorityCount != 0 {
		t.Fatalf("GoDj proposal contract %s carries %d DRF provenance entries", contract.ID, drfAuthorityCount)
	}
	if !proposalScenario && drfAuthorityCount == 0 {
		t.Fatalf("DRF observation contract %s lacks exact DRF authority", contract.ID)
	}
	if contract.Status == ContractDeviation && deviationDecisionCount != 1 {
		t.Fatalf("deviation contract %s DEV-0009 provenance count = %d, want 1", contract.ID, deviationDecisionCount)
	}
	if contract.Status == ContractPassing && deviationDecisionCount != 0 {
		t.Fatalf("passing contract %s carries DEV-0009 provenance", contract.ID)
	}
}
