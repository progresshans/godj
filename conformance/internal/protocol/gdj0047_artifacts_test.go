package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
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
	type artifactLock struct {
		size   int
		sha256 string
	}
	for name, want := range map[string]artifactLock{
		"conformance/contracts/api-authentication-manifest.json": {
			size:   7224,
			sha256: "038d5b694ae16d2464965d2b967830a2b0a4818055b6d906ae5320b5abe122d0",
		},
		"conformance/fixtures/godj-api-authentication-not-implemented.json": {
			size:   1746,
			sha256: "9562a10f8d729777d35abf0c852a0e90cc98607bfc375252ecec5933dc625434",
		},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/api-authentication-oracle.json": {
			size:   23698,
			sha256: "73262bd3dbc505a110c4b500920f8f1c4df61be34c29c695343323431dbacef3",
		},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/SHA256SUMS": {
			size:   283,
			sha256: "429b5f8a1c7ce554f5fa676b0e5c32fdf528cf4888128063a901f3c4d89cda8a",
		},
		"conformance/fixtures/godj-api-authentication-deviation-expected.json": {
			size:   2291,
			sha256: "85a9a8b2261e7265b00a33c2cf5b63b9e5b5cd963b2ac7e894dd77988206fc4b",
		},
	} {
		contents := mustReadGDJ0047File(t, filepath.Join(root, filepath.FromSlash(name)))
		if len(contents) != want.size {
			t.Fatalf("GDJ-0047 artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
			t.Fatalf("GDJ-0047 artifact %s sha256 = %q, want %q", name, got, want.sha256)
		}
	}
	oraclePath := filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", "api-authentication-oracle.json")
	oracleBytes := mustReadGDJ0047File(t, oraclePath)
	for _, forbidden := range []string{
		"gdj-phase-a-raw-bearer-canary",
		"gdj-phase-a-unknown-bearer",
		root,
	} {
		if strings.Contains(string(oracleBytes), forbidden) {
			t.Fatalf("API authentication oracle leaks forbidden source value %q", forbidden)
		}
	}
	decisionSource := string(mustReadGDJ0047File(t, filepath.Join(root, "conformance", "runners", "django", "api_authentication_decisions.py")))
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
				changed = mutateFirstGDJ0047Scalar(observation.Result)
			case CompareDBState:
				changed = mutateFirstGDJ0047Scalar(observation.DBState)
			case CompareMetrics:
				changed = mutateFirstGDJ0047Scalar(observation.Metrics)
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

func TestGDJ0047PublishedMakeAndWorkflowWiringIsExact(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	makeText := string(mustReadGDJ0047File(t, filepath.Join(root, "Makefile")))
	for variable, value := range map[string]string{
		"API_AUTHENTICATION_MANIFEST":           "conformance/contracts/api-authentication-manifest.json",
		"API_AUTHENTICATION_ORACLE":             "conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/api-authentication-oracle.json",
		"API_AUTHENTICATION_NOT_IMPLEMENTED":    "conformance/fixtures/godj-api-authentication-not-implemented.json",
		"API_AUTHENTICATION_DEVIATION_EXPECTED": "conformance/fixtures/godj-api-authentication-deviation-expected.json",
	} {
		definition := variable + " := " + value
		if got := strings.Count(makeText, definition); got != 1 {
			t.Fatalf("Makefile definition %q count = %d, want 1", definition, got)
		}
	}
	referenceTarget := gdj0047MakeTarget(t, makeText, "conformance-check:\n", "godj-conformance:\n")
	productTarget := gdj0047MakeTarget(t, makeText, "godj-conformance:\n", "oracle-check:\n")
	oracleCheckTarget := gdj0047MakeTarget(t, makeText, "oracle-check:\n", "oracle-regenerate:\n")
	oracleRegenerateTarget := gdj0047MakeTarget(t, makeText, "oracle-regenerate:\n", "\nci:")
	if got := strings.Count(referenceTarget, "$(API_AUTHENTICATION_MANIFEST)"); got != 2 {
		t.Fatalf("reference API authentication manifest count = %d, want oracle and baseline", got)
	}
	if got := strings.Count(referenceTarget, "$(API_AUTHENTICATION_ORACLE)"); got != 1 {
		t.Fatalf("reference API authentication oracle count = %d, want 1", got)
	}
	if got := strings.Count(referenceTarget, "$(API_AUTHENTICATION_NOT_IMPLEMENTED)"); got != 1 {
		t.Fatalf("reference API authentication baseline count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "$(API_AUTHENTICATION_MANIFEST)"); got != 1 {
		t.Fatalf("product API authentication manifest count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "$(API_AUTHENTICATION_ORACLE)"); got != 1 {
		t.Fatalf("product API authentication oracle count = %d, want 1", got)
	}
	if got := strings.Count(productTarget, "$(API_AUTHENTICATION_DEVIATION_EXPECTED)"); got != 1 {
		t.Fatalf("product API authentication deviation count = %d, want 1", got)
	}
	if strings.Contains(productTarget, "$(API_AUTHENTICATION_NOT_IMPLEMENTED)") {
		t.Fatal("historical API authentication not-implemented fixture entered the product target")
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 21 {
		t.Fatalf("product adapter count = %d, want 21", got)
	}
	for name, target := range map[string]string{"oracle-check": oracleCheckTarget, "oracle-regenerate": oracleRegenerateTarget} {
		if got := strings.Count(target, "$(API_AUTHENTICATION_MANIFEST)"); got != 1 {
			t.Fatalf("%s API authentication manifest count = %d, want 1", name, got)
		}
		if got := strings.Count(target, "$(API_AUTHENTICATION_ORACLE)"); got != 1 {
			t.Fatalf("%s API authentication oracle count = %d, want 1", name, got)
		}
	}
	if !strings.Contains(oracleCheckTarget, "--output $(API_AUTHENTICATION_ORACLE) --check") {
		t.Fatal("oracle-check does not require an exact API authentication no-rewrite check")
	}

	workflow := string(mustReadGDJ0047File(t, filepath.Join(root, ".github", "workflows", "ci.yml")))
	if got := strings.Count(workflow, "conformance/fixtures/godj-api-authentication-not-implemented.json"); got != 2 {
		t.Fatalf("workflow API authentication baseline count = %d, want both reference gates", got)
	}
	if got := strings.Count(workflow, "conformance/fixtures/godj-api-authentication-deviation-expected.json"); got != 2 {
		t.Fatalf("workflow API authentication deviation count = %d, want both artifact rewrite gates", got)
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

func gdj0047MakeTarget(t *testing.T, text, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(text, startMarker)
	if start < 0 {
		t.Fatalf("Makefile target marker %q is missing", startMarker)
	}
	end := strings.Index(text[start+len(startMarker):], endMarker)
	if end < 0 {
		t.Fatalf("Makefile target end marker %q is missing", endMarker)
	}
	return text[start : start+len(startMarker)+end]
}

func mustReadGDJ0047File(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func mutateFirstGDJ0047Scalar(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
	case ValueNull:
		*value = String("mutated")
		return true
	case ValueBool:
		changed := !*value.Bool
		value.Bool = &changed
		return true
	case ValueInt:
		changed := "999999"
		if *value.Text == changed {
			changed = "999998"
		}
		value.Text = &changed
		return true
	case ValueString, ValueDecimal, ValueDatetime, ValueUUID, ValueBytes:
		if value.Type != ValueString {
			*value = String("mutated")
			return true
		}
		changed := *value.Text + " changed"
		value.Text = &changed
		return true
	case ValuePK:
		return mutateFirstGDJ0047Scalar(value.Nested)
	case ValueList:
		if len(value.Items) == 0 {
			value.Items = append(value.Items, String("mutated"))
			return true
		}
		for index := range value.Items {
			if mutateFirstGDJ0047Scalar(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstGDJ0047Scalar(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}
