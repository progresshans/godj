package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type gdj0044ContractSet struct {
	name              string
	manifest          string
	oracle            string
	fixture           string
	adr               string
	deviationFixture  string
	deviationIDs      []string
	deviationDecision string
	ids               []string
	scenarios         []string
	phases            []Phase
	comparisons       [][]ComparisonDimension
}

func gdj0044ContractSets() []gdj0044ContractSet {
	return []gdj0044ContractSet{
		{
			name:              "parameter-routing",
			manifest:          "parameter-routing-manifest.json",
			oracle:            "parameter-routing-oracle.json",
			fixture:           "godj-parameter-routing-not-implemented.json",
			adr:               "ADR-0045",
			deviationFixture:  "godj-parameter-routing-deviation-expected.json",
			deviationIDs:      []string{"WEB-028", "WEB-029"},
			deviationDecision: "DEV-0006",
			ids:               []string{"WEB-028", "WEB-029", "WEB-030", "WEB-031", "WEB-032", "WEB-033", "WEB-034", "WEB-035"},
			scenarios: []string{
				"drf.parameter_routing.static_parameter_coexistence",
				"drf.parameter_routing.nonnegative_int64_parameter",
				"drf.parameter_routing.static_precedence_order_independent",
				"drf.parameter_routing.named_reverse_boundaries",
				"drf.parameter_routing.ambiguous_route_rejection",
				"drf.parameter_routing.invalid_route_and_resource_caps",
				"drf.parameter_routing.trailing_slash_and_invalid_path_404",
				"drf.parameter_routing.method_not_allowed_allow_header",
			},
			phases: []Phase{PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseConstruction, PhaseConstruction, PhaseConstruction, PhaseEvaluation, PhaseEvaluation},
			comparisons: [][]ComparisonDimension{
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
			},
		},
		{
			name:              "article-api",
			manifest:          "article-api-manifest.json",
			oracle:            "article-api-oracle.json",
			fixture:           "godj-article-api-not-implemented.json",
			adr:               "ADR-0046",
			deviationFixture:  "godj-article-api-deviation-expected.json",
			deviationIDs:      []string{"API-001", "API-003", "API-010"},
			deviationDecision: "DEV-0007",
			ids:               []string{"API-001", "API-002", "API-003", "API-004", "API-005", "API-006", "API-007", "API-008", "API-009", "API-010"},
			scenarios: []string{
				"drf.article_api.json_transport_boundary",
				"drf.article_api.article_serializer_semantics",
				"drf.article_api.session_permission_csrf_denial",
				"drf.article_api.list_filter_order",
				"drf.article_api.page_number_pagination",
				"drf.article_api.create_article",
				"drf.article_api.retrieve_article",
				"drf.article_api.full_update",
				"drf.article_api.partial_update",
				"drf.article_api.delete_article",
			},
			phases: []Phase{PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseCommit, PhaseEvaluation, PhaseCommit, PhaseCommit, PhaseCommit},
			comparisons: [][]ComparisonDimension{
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
			},
		},
	}
}

func TestGDJ0044ExactDRFProfileAndProvenance(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "drf-3.18.0-django-6.1-sqlite-darwin-arm64" || profile.Fingerprint.DjangoVersion != "6.1" || profile.Fingerprint.PythonVersion != "3.14.3" || profile.Lock.File != "conformance/reference/drf/uv.lock" || profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected GDJ-0044 profile: %#v", profile)
	}
	lock, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(profile.Lock.File)))
	if err != nil {
		t.Fatal(err)
	}
	lockText := string(lock)
	for _, marker := range []string{
		"name = \"djangorestframework\"",
		"version = \"3.18.0\"",
		"sha256:381fc44d3249c9565c5f723850855b734e99030eb30957a49f506d3fe11d7dcb",
		"sha256:2323a5111837e0b784dcb8323abc78ecc54fa2a5af7aff2677cf50cdd849477f",
	} {
		if !strings.Contains(lockText, marker) {
			t.Fatalf("nested DRF lock lacks %q", marker)
		}
	}

	for _, set := range gdj0044ContractSets() {
		manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifest))
		if err != nil {
			t.Fatal(err)
		}
		exactDRFReferences := 0
		for _, contract := range manifest.Contracts {
			foundADR := false
			for _, provenance := range contract.Provenance {
				if provenance.Derived == nil || *provenance.Derived {
					t.Fatalf("contract %s does not preserve independent provenance: %#v", contract.ID, provenance)
				}
				if provenance.Reference == set.adr {
					foundADR = true
				}
				if strings.Contains(provenance.Reference, "django-rest-framework@11875a38f483cea69d8ef2fd9ede6b96fb602ec4:") {
					exactDRFReferences++
					if provenance.License != "BSD-3-Clause" {
						t.Fatalf("contract %s exact DRF source lacks BSD provenance", contract.ID)
					}
				}
				if strings.HasPrefix(provenance.Reference, "http") && provenance.License != "BSD-3-Clause" {
					t.Fatalf("contract %s documentation provenance lacks BSD license: %#v", contract.ID, provenance)
				}
			}
			if !foundADR {
				t.Fatalf("contract %s lacks %s provenance", contract.ID, set.adr)
			}
		}
		if exactDRFReferences == 0 {
			t.Fatalf("%s manifest lacks exact DRF tag source provenance", set.name)
		}
	}
}

func TestGDJ0044ProductManifestsPreserveReferencesDeviationsAndPayloadFreeBaselines(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range gdj0044ContractSets() {
		set := set
		t.Run(set.name, func(t *testing.T) {
			t.Parallel()
			manifest, oracle, fixture := loadGDJ0044Set(t, root, set)
			deviation, err := LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", set.deviationFixture))
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
				t.Fatalf("oracle does not validate: %v", err)
			}
			if err := ValidateSuiteAgainst(profile, manifest, fixture); err != nil {
				t.Fatalf("not-implemented fixture does not validate: %v", err)
			}
			if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(fixture.Profile, profile.Snapshot()) {
				t.Fatal("suite profile is not the exact nested-lock snapshot")
			}
			if len(manifest.Contracts) != len(set.ids) || len(oracle.Contracts) != len(set.ids) || len(fixture.Contracts) != len(set.ids) {
				t.Fatalf("artifact lengths = %d/%d/%d, want %d", len(manifest.Contracts), len(oracle.Contracts), len(fixture.Contracts), len(set.ids))
			}
			if deviation.ProfileID != profile.ID || deviation.Decision != set.deviationDecision || len(deviation.Contracts) != len(set.deviationIDs) {
				t.Fatalf("deviation envelope = %#v, want profile %s decision %s contracts %#v", deviation, profile.ID, set.deviationDecision, set.deviationIDs)
			}
			for index, contract := range deviation.Contracts {
				if contract.ID != set.deviationIDs[index] {
					t.Fatalf("deviation contract %d = %q, want %q", index, contract.ID, set.deviationIDs[index])
				}
			}
			for index, contract := range manifest.Contracts {
				wantStatus := ContractPassing
				decision := ""
				if gdj0044Contains(set.deviationIDs, contract.ID) {
					wantStatus = ContractDeviation
					decision = set.deviationDecision
				}
				if contract.ID != set.ids[index] || contract.Scenario != set.scenarios[index] || contract.Phase != set.phases[index] || contract.Status != wantStatus || !reflect.DeepEqual(contract.Comparison, set.comparisons[index]) {
					t.Fatalf("contract %d = %#v", index, contract)
				}
				assertGDJ0044Provenance(t, contract, set.adr, decision)
				observed := oracle.Contracts[index]
				if observed.ID != contract.ID || observed.Status != StatusObserved || observed.Phase != contract.Phase {
					t.Fatalf("oracle contract %d = %#v", index, observed)
				}
				assertGDJ0044DeclaredPayloads(t, contract, observed)
				locked := fixture.Contracts[index]
				if locked.ID != contract.ID || locked.Status != StatusNotImplemented || locked.Phase != contract.Phase || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
					t.Fatalf("fixture contract %d is not payload-free: %#v", index, locked)
				}
			}
			differences, err := Compare(profile, manifest, oracle, fixture)
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) != len(manifest.Contracts) {
				t.Fatalf("oracle/fixture differences = %d, want %d", len(differences), len(manifest.Contracts))
			}
			for index, difference := range differences {
				if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
					t.Fatalf("difference %d = %#v", index, difference)
				}
			}
		})
	}
}

func TestGDJ0044DeclaredPayloadAndBindingMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range gdj0044ContractSets() {
		manifest, oracle, _ := loadGDJ0044Set(t, root, set)
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
				case CompareError:
					t.Fatalf("contract %s unexpectedly declares error comparison", contract.ID)
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
						t.Fatalf("contract %s mutation reported against %s", contract.ID, difference.ContractID)
					}
				}
			}
		}

		reordered := cloneSuite(t, oracle)
		reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
		if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
			t.Fatalf("%s oracle order mutation produced a false green", set.name)
		}
		changedProfile := cloneSuite(t, oracle)
		changedProfile.Profile.Lock.SHA256 = strings.Repeat("0", 64)
		if err := ValidateSuiteAgainst(profile, manifest, changedProfile); err == nil {
			t.Fatalf("%s profile mutation produced a false green", set.name)
		}
	}
}

func TestGDJ0044OraclesAreSecretFreeAndScenariosAreOracleBlind(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	for _, set := range gdj0044ContractSets() {
		contents, err := os.ReadFile(filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", set.oracle))
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(contents, &value); err != nil {
			t.Fatal(err)
		}
		assertGDJ0044SecretFreeJSON(t, set.name, value)
	}
	for _, name := range []string{
		"conformance/runners/django/article_api_scenarios.py",
		"conformance/runners/django/article_api_worker.py",
		"conformance/runners/django/article_api_fixture/api.py",
		"conformance/runners/django/article_api_fixture/urls.py",
		"conformance/runners/godj/gdj0044_parameter_routing_scenarios.go",
		"conformance/runners/godj/gdj0044_article_api_fixture.go",
		"conformance/runners/godj/gdj0044_article_api_scenarios.go",
	} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(contents)
		for _, forbidden := range []string{"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not-implemented"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("independent scenario source %s contains expected artifact marker %q", name, forbidden)
			}
		}
	}
}

func TestDRFReferenceUsesLockedRuntimeAndChecksumChecks(t *testing.T) {
	t.Parallel()
	root := conformanceRepositoryRoot(t)
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	ciText := string(ci)
	for _, required := range []string{
		"uv sync --project conformance/reference/drf --frozen",
		"working-directory: conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64",
		"--with djangorestframework==3.18.0",
		"rest_framework.VERSION == \"3.18.0\"",
	} {
		if !strings.Contains(ciText, required) {
			t.Fatalf("CI lacks GDJ-0044 reference fragment %q", required)
		}
	}
}

func loadGDJ0044Set(t *testing.T, root string, set gdj0044ContractSet) (Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	manifest := requireArtifact(t, filepath.Join(root, "conformance", "contracts", set.manifest), LoadManifest)
	oracle := requireArtifact(t, filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", set.oracle), LoadObservationSuite)
	fixture := requireArtifact(t, filepath.Join(root, "conformance", "fixtures", set.fixture), LoadObservationSuite)
	return manifest, oracle, fixture
}

func assertGDJ0044Provenance(t *testing.T, contract Contract, adr, decision string) {
	t.Helper()
	adrCount := 0
	decisionReferences := make([]string, 0, 1)
	for _, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s does not preserve independent provenance: %#v", contract.ID, provenance)
		}
		if provenance.Reference == adr {
			adrCount++
			if provenance.Kind != "documentation" || provenance.License != "" {
				t.Fatalf("contract %s ADR provenance = %#v, want unlicensed documentation", contract.ID, provenance)
			}
		}
		if provenance.Kind == "decision" {
			if provenance.License != "" {
				t.Fatalf("contract %s decision provenance carries a license: %#v", contract.ID, provenance)
			}
			decisionReferences = append(decisionReferences, provenance.Reference)
		}
	}
	if adrCount != 1 {
		t.Fatalf("contract %s ADR provenance count = %d, want exactly one %s", contract.ID, adrCount, adr)
	}
	if decision == "" && len(decisionReferences) != 0 {
		t.Fatalf("passing contract %s carries decision provenance %#v", contract.ID, decisionReferences)
	}
	if decision != "" && (len(decisionReferences) != 1 || decisionReferences[0] != decision) {
		t.Fatalf("deviation contract %s decision provenance = %#v, want exactly %s", contract.ID, decisionReferences, decision)
	}
}

func gdj0044Contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func assertGDJ0044DeclaredPayloads(t *testing.T, contract Contract, observation Observation) {
	t.Helper()
	declared := make(map[ComparisonDimension]bool, len(contract.Comparison))
	for _, dimension := range contract.Comparison {
		declared[dimension] = true
	}
	if (observation.Result != nil) != declared[CompareResult] || (observation.Error != nil) != declared[CompareError] || (observation.DBState != nil) != declared[CompareDBState] || (observation.Metrics != nil) != declared[CompareMetrics] {
		t.Fatalf("contract %s observation payloads do not exactly match comparisons: %#v", contract.ID, observation)
	}
}

func assertGDJ0044SecretFreeJSON(t *testing.T, artifact string, value any) {
	t.Helper()
	forbiddenKeys := map[string]bool{
		"cookie_value": true, "csrf_token": true, "html": true, "password": true,
		"password_hash": true, "session_id": true, "session_key": true, "token": true,
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbiddenKeys[key] {
				t.Fatalf("%s contains forbidden secret/raw key %q", artifact, key)
			}
			assertGDJ0044SecretFreeJSON(t, artifact, child)
		}
	case []any:
		for _, child := range typed {
			assertGDJ0044SecretFreeJSON(t, artifact, child)
		}
	case string:
		lower := strings.ToLower(typed)
		for _, forbidden := range []string{"reference-password", "csrfmiddlewaretoken", "sessionid", "set-cookie", "<html", "<form", "<!doctype"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s contains forbidden raw/secret string marker %q", artifact, forbidden)
			}
		}
	}
}
