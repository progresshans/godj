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

func TestGDJ0044ArtifactBytesAndExistingDjangoReferenceAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size int
		hash string
	}
	wanted := map[string]artifactLock{
		"pyproject.toml": {227, "3076234a966a3bdbb3a0d775576764709632e2e160594040b1fee65d8ad591bd"},
		"uv.lock":        {3162, "ad825e872092be26169a6706c0d9643e88875d877f24bffc5c1a3471d82b1fb7"},
		"conformance/profiles/django-6.1-sqlite-darwin-arm64.json":                                    {879, "8b557bf935575f5366f4ebdc07441a8f4a3e2097f8af4a42450eb0fde12a5041"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS":                               {1887, "cf7029cbc39627e57c1e3f991d5f28895781fba69b2353727e02f51cc14e3daa"},
		"conformance/reference/drf/pyproject.toml":                                                    {319, "46b3482056a64d2c9ac84320f047089c9c406d14d8bec7cf0e7a7b43f71be8b3"},
		"conformance/reference/drf/uv.lock":                                                           {4199, "efc431a1585aaecd9099d40194980771b395bbe261370619f29b5ccf58728f8f"},
		"conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json":                         {916, "6c0243b8ad398cca45e1ae1edfd99c321bd75e5ef6d0763cef76a5193c99ef1f"},
		"conformance/contracts/parameter-routing-manifest.json":                                       {4689, "85365b3670df5fa5a0d51241dd958d25816d9a285a5070e416120423793a264e"},
		"conformance/contracts/article-api-manifest.json":                                             {6618, "5047ca955ba5b2099f0d8bf2f6f0ed09944e2fcc705eb4ccd1c5bd6fa500a4e1"},
		"conformance/fixtures/godj-parameter-routing-deviation-expected.json":                         {2174, "f9d084d178cccdf5928830813810f4d28c930d4414c73091a06dcd825ed38f60"},
		"conformance/fixtures/godj-article-api-deviation-expected.json":                               {2003, "54758fdf850d4a61f65b764131a444c276ad7bb311a65d86b8b4a1780c979623"},
		"conformance/fixtures/godj-parameter-routing-not-implemented.json":                            {1608, "7a7e3f3c433f837fb3240f97a75ef66022cf8887c6322d960dc3291eb48776b1"},
		"conformance/fixtures/godj-article-api-not-implemented.json":                                  {1736, "fdb05cf9ff8e257c60b210dff29ec012a834110f22b703664f943e6740c2a27d"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/parameter-routing-oracle.json": {12663, "4aded47e2a0db9524a18625174e8d8815b69911e5310323fbe17bad34899cc53"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/article-api-oracle.json":       {46466, "f63f06ac26a1cedac0ea3e7fe9339b163b2571cdbc2a7fea87f8debef690ab56"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/SHA256SUMS":                    {283, "429b5f8a1c7ce554f5fa676b0e5c32fdf528cf4888128063a901f3c4d89cda8a"},
	}
	root := conformanceRepositoryRoot(t)
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != want.size {
			t.Fatalf("artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.hash {
			t.Fatalf("artifact %s sha256 = %q, want %q", name, got, want.hash)
		}
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

func TestGDJ0044ReferenceAndProductWiringPublishExactAdapters(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	conformanceStart := strings.Index(text, "conformance-check:\n")
	productStart := strings.Index(text, "godj-conformance:\n")
	oracleCheckStart := strings.Index(text, "oracle-check:\n")
	oracleRegenerateStart := strings.Index(text, "oracle-regenerate:\n")
	ciStart := strings.Index(text, "\nci:")
	if conformanceStart < 0 || productStart <= conformanceStart || oracleCheckStart <= productStart || oracleRegenerateStart <= oracleCheckStart || ciStart <= oracleRegenerateStart {
		t.Fatal("cannot isolate Makefile conformance targets")
	}
	referenceTarget := text[conformanceStart:productStart]
	productTarget := text[productStart:oracleCheckStart]
	oracleCheckTarget := text[oracleCheckStart:oracleRegenerateStart]
	oracleRegenerateTarget := text[oracleRegenerateStart:ciStart]
	for _, variable := range []string{"$(PARAMETER_ROUTING_MANIFEST)", "$(ARTICLE_API_MANIFEST)"} {
		if got := strings.Count(referenceTarget, variable); got != 2 {
			t.Fatalf("reference target %s count = %d, want oracle and baseline", variable, got)
		}
		if got := strings.Count(productTarget, variable); got != 1 {
			t.Fatalf("product target %s count = %d, want one product adapter", variable, got)
		}
		if got := strings.Count(oracleCheckTarget, variable); got != 1 {
			t.Fatalf("oracle-check %s count = %d, want 1", variable, got)
		}
		if got := strings.Count(oracleRegenerateTarget, variable); got != 1 {
			t.Fatalf("oracle-regenerate %s count = %d, want 1", variable, got)
		}
	}
	if got := strings.Count(productTarget, "go run ./conformance/cmd/godjcheck"); got != 22 {
		t.Fatalf("product adapter count = %d, want 22", got)
	}
	if got := strings.Count(oracleCheckTarget, "--project conformance/reference/drf --frozen"); got != 3 {
		t.Fatalf("nested DRF oracle-check command count = %d, want 3", got)
	}

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
		"len(SCENARIOS) == 261",
		"len(payload) == 906009",
		"4c7d628245af5a9eab06a353e5498c869e784c001a358f8ecec4c59c823e3767",
	} {
		if !strings.Contains(ciText, required) {
			t.Fatalf("CI lacks GDJ-0044 reference fragment %q", required)
		}
	}
}

func TestGDJ0044CheckpointTwentyReferenceSetsHave219ContractsAndReject380OrderedCrossBindings(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	oldProfile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
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
	sets := make([]inventorySet, 0, 20)
	old := []struct{ name, manifest, oracle string }{
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
	}
	for _, source := range old {
		manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", source.manifest))
		if err != nil {
			t.Fatal(err)
		}
		oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", source.oracle))
		if err != nil {
			t.Fatal(err)
		}
		sets = append(sets, inventorySet{source.name, oldProfile, manifest, oracle})
	}
	for _, source := range gdj0044ContractSets() {
		manifest, oracle, _ := loadGDJ0044Set(t, root, source)
		sets = append(sets, inventorySet{source.name, drfProfile, manifest, oracle})
	}

	ids := make(map[string]string)
	scenarios := make(map[string]string)
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
				t.Fatalf("contract %s has unexpected status %q", contract.ID, contract.Status)
			}
		}
	}
	if len(sets) != 20 || total != 219 || len(ids) != 219 || len(scenarios) != 219 || passing != 192 || deviations != 15 || locked != 12 {
		t.Fatalf("GDJ-0044 checkpoint inventory = %d sets/%d contracts/%d IDs/%d scenarios = %d passing + %d deviation + %d oracle_locked", len(sets), total, len(ids), len(scenarios), passing, deviations, locked)
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
	if crossBindings != 380 {
		t.Fatalf("ordered cross-bindings = %d, want 380", crossBindings)
	}
}

func loadGDJ0044Set(t *testing.T, root string, set gdj0044ContractSet) (Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifest))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "drf-3.18.0-django-6.1-sqlite-darwin-arm64", set.oracle))
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", set.fixture))
	if err != nil {
		t.Fatal(err)
	}
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
