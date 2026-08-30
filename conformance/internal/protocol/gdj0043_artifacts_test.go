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

type gdj0043ContractSet struct {
	name                 string
	manifestName         string
	oracleName           string
	fixtureName          string
	adr                  string
	deviationFixtureName string
	deviationIDs         []string
	deviationDecision    string
	ids                  []string
	scenarios            []string
	phases               []Phase
	comparisons          [][]ComparisonDimension
}

func gdj0043ContractSets() []gdj0043ContractSet {
	return []gdj0043ContractSet{
		{
			name:                 "template-form",
			manifestName:         "template-form-manifest.json",
			oracleName:           "template-form-oracle.json",
			fixtureName:          "godj-template-form-not-implemented.json",
			adr:                  "ADR-0043",
			deviationFixtureName: "godj-template-form-deviation-expected.json",
			deviationIDs:         []string{"WEB-022", "WEB-027"},
			deviationDecision:    "DEV-0003",
			ids:                  []string{"WEB-021", "WEB-022", "WEB-023", "WEB-024", "WEB-025", "WEB-026", "WEB-027", "FRM-001", "FRM-002", "FRM-003", "FRM-004", "FRM-005"},
			scenarios: []string{
				"django.template_form.scalar_and_missing",
				"django.template_form.dotted_lookup_precedence",
				"django.template_form.autoescape_and_safe",
				"django.template_form.if_for_and_empty",
				"django.template_form.closed_filters",
				"django.template_form.construction_failures",
				"django.template_form.callable_exposure",
				"django.template_form.unbound_and_bound_empty",
				"django.template_form.valid_article_clean",
				"django.template_form.field_error_codes",
				"django.template_form.cross_field_validation",
				"django.template_form.model_form_write_boundary",
			},
			phases: []Phase{PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseConstruction, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseCommit},
			comparisons: [][]ComparisonDimension{
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
			},
		},
		{
			name:                 "auth-session",
			manifestName:         "auth-session-manifest.json",
			oracleName:           "auth-session-oracle.json",
			fixtureName:          "godj-auth-session-not-implemented.json",
			adr:                  "ADR-0044",
			deviationFixtureName: "godj-auth-session-deviation-expected.json",
			deviationIDs:         []string{"AUT-004", "AUT-005"},
			deviationDecision:    "DEV-0004",
			ids:                  []string{"AUT-001", "AUT-002", "AUT-003", "AUT-004", "AUT-005", "AUT-006", "AUT-007", "AUT-008"},
			scenarios: []string{
				"django.auth_session.anonymous_request",
				"django.auth_session.valid_login_rotation",
				"django.auth_session.rejected_login",
				"django.auth_session.logout_flush",
				"django.auth_session.cookie_policy",
				"django.auth_session.permission_and_safe_next",
				"django.auth_session.csrf_rejection",
				"django.auth_session.csrf_acceptance_and_rotation",
			},
			phases: []Phase{PhaseEvaluation, PhaseCommit, PhaseEvaluation, PhaseCommit, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseCommit},
			comparisons: [][]ComparisonDimension{
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareMetrics}, {CompareResult, CompareMetrics}, {CompareResult, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
			},
		},
		{
			name:                 "article-admin",
			manifestName:         "article-admin-manifest.json",
			oracleName:           "article-admin-oracle.json",
			fixtureName:          "godj-article-admin-not-implemented.json",
			adr:                  "ADR-0044",
			deviationFixtureName: "godj-article-admin-deviation-expected.json",
			deviationIDs:         []string{"ADM-002"},
			deviationDecision:    "DEV-0005",
			ids:                  []string{"ADM-001", "ADM-002", "ADM-003", "ADM-004", "ADM-005", "ADM-006", "ADM-007", "ADM-008", "ADM-009", "ADM-010"},
			scenarios: []string{
				"django.article_admin.access_matrix",
				"django.article_admin.stable_list",
				"django.article_admin.search_boundary",
				"django.article_admin.change_form_shape",
				"django.article_admin.invalid_edit",
				"django.article_admin.valid_add",
				"django.article_admin.valid_edit",
				"django.article_admin.delete_boundaries",
				"django.article_admin.semantic_history",
				"django.article_admin.publish_action",
			},
			phases: []Phase{PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseEvaluation, PhaseCommit, PhaseCommit, PhaseCommit, PhaseEvaluation, PhaseCommit},
			comparisons: [][]ComparisonDimension{
				{CompareResult, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics}, {CompareResult, CompareDBState, CompareMetrics},
				{CompareResult, CompareDBState, CompareMetrics},
			},
		},
	}
}

func TestGDJ0043ArtifactBytesAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size   int
		sha256 string
	}
	root := conformanceRepositoryRoot(t)
	wanted := map[string]artifactLock{
		"conformance/contracts/template-form-manifest.json":                            {7584, "4bc189cf71976b6d2ca301a97c6dfc5ee33463dec936f6c13d4181d56e1b1a41"},
		"conformance/contracts/auth-session-manifest.json":                             {4932, "72d4a054839d0b2aa4723ffb44b6f526c34808b182c39905a92d59890a2c79c5"},
		"conformance/contracts/article-admin-manifest.json":                            {6296, "b4ef4017c8af84e9f7428f541dda5276295f1e9f27c2af1b99bc049b306f451e"},
		"conformance/fixtures/godj-template-form-deviation-expected.json":              {1472, "0f9b10539677c07aa18c058f0e78925b3388299be853506324325c2af11d2ff3"},
		"conformance/fixtures/godj-auth-session-deviation-expected.json":               {1184, "f494fb64ae084d40564eaddad7e30792d5ed2f23799c8670e2a150ddb31a665d"},
		"conformance/fixtures/godj-article-admin-deviation-expected.json":              {915, "47d6f144259fbf82046d1e2821b2bd374c245cda30c084b627c5418500b54fc3"},
		"conformance/fixtures/godj-template-form-not-implemented.json":                 {1863, "b1e426264c53dc4885f70aa0f6d2f2231ade201da0fa9fd980d11400960cc1f5"},
		"conformance/fixtures/godj-auth-session-not-implemented.json":                  {1553, "f55fafcf0d979bfb6ba9c534dd89bf8123d1afd740570e786432ae7468b4f618"},
		"conformance/fixtures/godj-article-admin-not-implemented.json":                 {1699, "6cf265bc2b92565791c5f9d75f42fb0f86d5c88e9978df44b25d895538447a46"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/template-form-oracle.json": {12873, "968218e75b3244e8f72a9a106e967d4e9ab066db756913d8108b7371d4ecd6fa"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/auth-session-oracle.json":  {6916, "9eb0bfd37e7aeabac9250374af250ba0b74d2cf4c657cd2543e5dc9626fc36dc"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/article-admin-oracle.json": {17645, "869f871fe826b07442810892197bec2d59e0202e413d327154f6d166b7803378"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS":                {2279, "9d2180400e5ffd339593d11feb95fdaf9b1532eaa14fdee8cfdc2d9c88f6e71d"},
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != want.size {
			t.Fatalf("GDJ-0043 artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want.sha256 {
			t.Fatalf("GDJ-0043 artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
}

func TestGDJ0043ProductManifestsPreserveLockedReferencesAndPayloadFreeBaselines(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "django-6.1-sqlite-darwin-arm64" || profile.Fingerprint.DjangoVersion != "6.1" || profile.Lock.ManagerVersion != "0.10.12" {
		t.Fatalf("unexpected GDJ-0043 reference profile: %#v", profile)
	}

	for _, set := range gdj0043ContractSets() {
		set := set
		t.Run(set.name, func(t *testing.T) {
			t.Parallel()
			manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifestName))
			if err != nil {
				t.Fatal(err)
			}
			oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", set.oracleName))
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", set.fixtureName))
			if err != nil {
				t.Fatal(err)
			}
			deviation, err := LoadDeviationExpectation(filepath.Join(root, "conformance", "fixtures", set.deviationFixtureName))
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
				t.Fatalf("%s oracle does not validate: %v", set.name, err)
			}
			if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
				t.Fatalf("%s static baseline does not validate: %v", set.name, err)
			}
			if !reflect.DeepEqual(oracle.Profile, profile.Snapshot()) || !reflect.DeepEqual(baseline.Profile, profile.Snapshot()) {
				t.Fatalf("%s suite profile is not the exact pinned snapshot", set.name)
			}
			if len(manifest.Contracts) != len(set.ids) || len(oracle.Contracts) != len(set.ids) || len(baseline.Contracts) != len(set.ids) {
				t.Fatalf("%s artifact counts = %d/%d/%d, want %d", set.name, len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts), len(set.ids))
			}
			if deviation.ProfileID != profile.ID || deviation.Decision != set.deviationDecision || len(deviation.Contracts) != len(set.deviationIDs) {
				t.Fatalf("%s deviation envelope = %#v, want profile %s decision %s contracts %#v", set.name, deviation, profile.ID, set.deviationDecision, set.deviationIDs)
			}
			for index, contract := range deviation.Contracts {
				if contract.ID != set.deviationIDs[index] {
					t.Fatalf("%s deviation contract %d = %q, want %q", set.name, index, contract.ID, set.deviationIDs[index])
				}
			}
			for index, contract := range manifest.Contracts {
				if contract.ID != set.ids[index] || contract.Scenario != set.scenarios[index] || contract.Phase != set.phases[index] {
					t.Fatalf("%s contract %d = %s/%s/%s, want %s/%s/%s", set.name, index, contract.ID, contract.Scenario, contract.Phase, set.ids[index], set.scenarios[index], set.phases[index])
				}
				wantStatus := ContractPassing
				decision := ""
				if gdj0043Contains(set.deviationIDs, contract.ID) {
					wantStatus = ContractDeviation
					decision = set.deviationDecision
				}
				if contract.Status != wantStatus {
					t.Fatalf("%s contract %s status = %q, want %q", set.name, contract.ID, contract.Status, wantStatus)
				}
				if !reflect.DeepEqual(contract.Comparison, set.comparisons[index]) {
					t.Fatalf("%s contract %s comparison = %#v, want %#v", set.name, contract.ID, contract.Comparison, set.comparisons[index])
				}
				assertGDJ0043Provenance(t, contract, set.adr, decision)
				observed := oracle.Contracts[index]
				if observed.ID != contract.ID || observed.Status != StatusObserved || observed.Phase != contract.Phase {
					t.Fatalf("%s oracle contract %d = %#v", set.name, index, observed)
				}
				assertGDJ0043DeclaredPayloads(t, contract, observed)
				locked := baseline.Contracts[index]
				if locked.ID != contract.ID || locked.Status != StatusNotImplemented || locked.Phase != contract.Phase || locked.Result != nil || locked.Error != nil || locked.DBState != nil || locked.Metrics != nil {
					t.Fatalf("%s baseline contract %d is not payload-free: %#v", set.name, index, locked)
				}
			}
			differences, err := Compare(profile, manifest, oracle, baseline)
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) != len(manifest.Contracts) {
				t.Fatalf("%s oracle/static differences = %d, want %d", set.name, len(differences), len(manifest.Contracts))
			}
			for index, difference := range differences {
				if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" || difference.Expected != string(StatusObserved) || difference.Actual != string(StatusNotImplemented) {
					t.Fatalf("%s difference %d = %#v", set.name, index, difference)
				}
			}
		})
	}
}

func TestGDJ0043KeepsManifestLimitAndTemplateReviewedDeviations(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "template-form-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	web022 := manifest.Contracts[1]
	web022Decisions := gdj0043DecisionProvenance(web022)
	if web022.ID != "WEB-022" || web022.Scenario != "django.template_form.dotted_lookup_precedence" || web022.Status != ContractDeviation || len(web022Decisions) != 1 || web022Decisions[0] != "DEV-0003" {
		t.Fatalf("WEB-022 reviewed deviation boundary = %#v", web022)
	}
	web027 := manifest.Contracts[6]
	decisions := gdj0043DecisionProvenance(web027)
	if web027.ID != "WEB-027" || web027.Scenario != "django.template_form.callable_exposure" || web027.Status != ContractDeviation || len(decisions) != 1 || decisions[0] != "DEV-0003" {
		t.Fatalf("WEB-027 reviewed deviation boundary = %#v", web027)
	}
	tooShort := manifest
	tooShort.Contracts = append([]Contract(nil), manifest.Contracts[:7]...)
	if err := tooShort.Validate(); err == nil || !strings.Contains(err.Error(), "8 to 12") {
		t.Fatalf("seven-contract manifest error = %v", err)
	}
	tooLong := manifest
	tooLong.Contracts = append(append([]Contract(nil), manifest.Contracts...), manifest.Contracts[0])
	if err := tooLong.Validate(); err == nil || !strings.Contains(err.Error(), "8 to 12") {
		t.Fatalf("thirteen-contract manifest error = %v", err)
	}
}

func TestGDJ0043DeclaredPayloadAndBindingMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range gdj0043ContractSets() {
		manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", set.manifestName))
		if err != nil {
			t.Fatal(err)
		}
		oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", set.oracleName))
		if err != nil {
			t.Fatal(err)
		}
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
					t.Fatalf("GDJ-0043 contract %s unexpectedly declares error comparison", contract.ID)
				}
				if !changed {
					t.Fatalf("contract %s declared %s without a mutable payload", contract.ID, dimension)
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
						t.Fatalf("contract %s mutation reported against %s: %#v", contract.ID, difference.ContractID, differences)
					}
				}
			}
		}

		reordered := cloneSuite(t, oracle)
		reordered.Contracts[0], reordered.Contracts[1] = reordered.Contracts[1], reordered.Contracts[0]
		if err := ValidateSuiteAgainst(profile, manifest, reordered); err == nil {
			t.Fatalf("%s oracle order mutation produced a false green", set.name)
		}
		changedPhase := cloneSuite(t, oracle)
		changedPhase.Contracts[0].Phase = differentPhase(changedPhase.Contracts[0].Phase)
		if err := ValidateSuiteAgainst(profile, manifest, changedPhase); err == nil {
			t.Fatalf("%s oracle phase mutation produced a false green", set.name)
		}
		changedProfile := cloneSuite(t, oracle)
		changedProfile.Profile.Fingerprint.SQLiteSourceID += " changed"
		if err := ValidateSuiteAgainst(profile, manifest, changedProfile); err == nil {
			t.Fatalf("%s oracle profile mutation produced a false green", set.name)
		}
	}
}

func TestGDJ0043OraclesContainNoRawHTMLOrSecretFields(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	for _, set := range gdj0043ContractSets() {
		contents, err := os.ReadFile(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", set.oracleName))
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(contents, &value); err != nil {
			t.Fatal(err)
		}
		assertGDJ0043SecretFreeJSON(t, set.name, value)
	}
}

func TestGDJ0043ScenarioSourcesDoNotReadExpectedArtifacts(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	paths := []string{
		"conformance/runners/django/template_form_scenarios.py",
		"conformance/runners/django/auth_admin_scenarios.py",
		"conformance/runners/django/auth_admin_worker.py",
		"conformance/runners/django/auth_admin_fixture/admin.py",
		"conformance/runners/django/auth_admin_fixture/models.py",
		"conformance/runners/godj/gdj0043_template_form_scenarios.go",
		"conformance/runners/godj/gdj0043_auth_session_scenarios.go",
		"conformance/runners/godj/gdj0043_article_admin_scenarios.go",
	}
	for _, name := range paths {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(contents)
		for _, forbidden := range []string{"conformance/contracts", "conformance/oracles", "conformance/fixtures", "not-implemented"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("independent scenario source %s reads expected artifact marker %q", name, forbidden)
			}
		}
	}
}

func assertGDJ0043Provenance(t *testing.T, contract Contract, adr, decision string) {
	t.Helper()
	if len(contract.Provenance) < 2 {
		t.Fatalf("contract %s provenance = %#v, want ADR documentation plus Django reference", contract.ID, contract.Provenance)
	}
	first := contract.Provenance[0]
	if first.Kind != "documentation" || first.Reference != adr || first.Derived == nil || *first.Derived || first.License != "" {
		t.Fatalf("contract %s ADR provenance = %#v, want %s documentation", contract.ID, first, adr)
	}
	djangoReference := false
	decisionReferences := make([]string, 0, 1)
	for _, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s unexpectedly marks copied source provenance: %#v", contract.ID, provenance)
		}
		if provenance.Kind == "decision" {
			if provenance.License != "" {
				t.Fatalf("contract %s decision provenance carries a license: %#v", contract.ID, provenance)
			}
			decisionReferences = append(decisionReferences, provenance.Reference)
		}
		if strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
			djangoReference = true
			if provenance.License != "BSD-3-Clause" {
				t.Fatalf("contract %s Django provenance lacks BSD license: %#v", contract.ID, provenance)
			}
		}
	}
	if !djangoReference {
		t.Fatalf("contract %s lacks exact Django source provenance", contract.ID)
	}
	if decision == "" && len(decisionReferences) != 0 {
		t.Fatalf("passing contract %s carries decision provenance %#v", contract.ID, decisionReferences)
	}
	if decision != "" && (len(decisionReferences) != 1 || decisionReferences[0] != decision) {
		t.Fatalf("deviation contract %s decision provenance = %#v, want exactly %s", contract.ID, decisionReferences, decision)
	}
}

func gdj0043Contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func gdj0043DecisionProvenance(contract Contract) []string {
	result := make([]string, 0, 1)
	for _, provenance := range contract.Provenance {
		if provenance.Kind == "decision" {
			result = append(result, provenance.Reference)
		}
	}
	return result
}

func assertGDJ0043DeclaredPayloads(t *testing.T, contract Contract, observation Observation) {
	t.Helper()
	for _, dimension := range contract.Comparison {
		switch dimension {
		case CompareResult:
			if observation.Result == nil {
				t.Fatalf("contract %s declares result without payload", contract.ID)
			}
		case CompareError:
			if observation.Error == nil {
				t.Fatalf("contract %s declares error without payload", contract.ID)
			}
		case CompareDBState:
			if observation.DBState == nil {
				t.Fatalf("contract %s declares db_state without payload", contract.ID)
			}
		case CompareMetrics:
			if observation.Metrics == nil {
				t.Fatalf("contract %s declares metrics without payload", contract.ID)
			}
		}
	}
}

func assertGDJ0043SecretFreeJSON(t *testing.T, artifact string, value any) {
	t.Helper()
	forbiddenKeys := map[string]struct{}{
		"cookie_value": {}, "csrf_token": {}, "html": {}, "password": {}, "password_hash": {}, "session_id": {}, "session_key": {}, "token": {},
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, forbidden := forbiddenKeys[key]; forbidden {
				t.Fatalf("%s oracle contains forbidden secret/raw key %q", artifact, key)
			}
			assertGDJ0043SecretFreeJSON(t, artifact, child)
		}
	case []any:
		for _, child := range typed {
			assertGDJ0043SecretFreeJSON(t, artifact, child)
		}
	case string:
		lower := strings.ToLower(typed)
		for _, forbidden := range []string{"reference-password", "csrfmiddlewaretoken", "sessionid", "set-cookie", "<html", "<form", "<!doctype"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s oracle contains forbidden raw/secret string marker %q", artifact, forbidden)
			}
		}
	}
}
