package protocol

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRelationArtifactBytesAreLocked(t *testing.T) {
	t.Parallel()

	type artifactLock struct {
		size   int
		sha256 string
	}
	root := conformanceRepositoryRoot(t)
	wanted := map[string]artifactLock{
		"conformance/contracts/relation-manifest.json": {
			size:   10812,
			sha256: "640b24e9e543b66375ea1dafa45750a6d2716c1b3f1e2602afcd7e2a3b68f136",
		},
		"conformance/fixtures/godj-relation-not-implemented.json": {
			size:   1859,
			sha256: "2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209",
		},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json": {
			size:   33792,
			sha256: "6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290",
		},
	}
	for name, want := range wanted {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(contents) != want.size {
			t.Fatalf("relation artifact %s size = %d, want %d", name, len(contents), want.size)
		}
		got := fmt.Sprintf("%x", sha256.Sum256(contents))
		if got != want.sha256 {
			t.Fatalf("relation artifact %s checksum = %q, want %q", name, got, want.sha256)
		}
	}
}

func TestRelationManifestDiffIsExactlyReviewedREL005StatusTransition(t *testing.T) {
	t.Parallel()

	root := conformanceRepositoryRoot(t)
	contents, err := os.ReadFile(filepath.Join(root, "conformance", "contracts", "relation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	passing := []byte(`"status": "passing"`)
	if count := bytes.Count(contents, passing); count != 5 {
		t.Fatalf("relation manifest passing status count = %d, want exact 5", count)
	}
	restored := append([]byte(nil), contents...)
	const identifier = "REL-005"
	marker := []byte(`"id": "` + identifier + `"`)
	start := bytes.Index(restored, marker)
	if start < 0 {
		t.Fatalf("%s contract is missing", identifier)
	}
	relative := bytes.Index(restored[start:], passing)
	if relative < 0 {
		t.Fatalf("%s passing transition is missing", identifier)
	}
	transition := start + relative
	restored = append(restored[:transition], append([]byte(`"status": "oracle_locked"`), restored[transition+len(passing):]...)...)
	if len(restored) != 10818 {
		t.Fatalf("restored relation manifest size = %d, want prior GDJ-0026 size 10818", len(restored))
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(restored)); got != "e548332401932059a87920f90fb7a1300aa02e3c5775335e3b6eda90cc84293a" {
		t.Fatalf("restored relation manifest checksum = %q, want prior GDJ-0026 bytes", got)
	}
}

func TestRelationArtifactBoundaryIsLocked(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadRelationArtifacts(t)
	wantSlugs := []string{
		"django.relation.cross_app_metadata",
		"django.relation.unsaved_related_target",
		"django.relation.forward_lazy_cache",
		"django.relation.forward_lookup_join_reuse",
		"django.relation.reverse_accessor_and_lookup",
		"django.relation.nullable_access_and_isnull",
		"django.relation.protect_delete",
		"django.relation.set_null_delete",
		"django.relation.required_select_related",
		"django.relation.nullable_select_related",
		"django.relation.invalid_reverse_select_related",
		"django.relation.reverse_prefetch",
	}
	wantPhases := []Phase{
		PhaseMetadata,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseCommit,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
	}
	wantComparisons := [][]ComparisonDimension{
		{CompareResult},
		{CompareError, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareError, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
		{CompareError, CompareDBState, CompareMetrics},
		{CompareResult, CompareDBState, CompareMetrics},
	}
	if len(manifest.Contracts) != len(wantSlugs) {
		t.Fatalf("relation manifest has %d contracts, want %d", len(manifest.Contracts), len(wantSlugs))
	}
	if len(oracle.Contracts) != len(wantSlugs) || len(baseline.Contracts) != len(wantSlugs) {
		t.Fatalf("relation suite lengths = oracle %d/static %d, want %d", len(oracle.Contracts), len(baseline.Contracts), len(wantSlugs))
	}

	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("REL-%03d", index+1)
		if contract.ID != wantID {
			t.Fatalf("contract %d ID = %q, want %q", index, contract.ID, wantID)
		}
		if contract.Scenario != wantSlugs[index] {
			t.Fatalf("contract %s scenario = %q, want %q", contract.ID, contract.Scenario, wantSlugs[index])
		}
		wantStatus := ContractOracleLocked
		if index == 0 || index == 2 || index == 3 || index == 4 || index == 5 {
			wantStatus = ContractPassing
		}
		if contract.Status != wantStatus {
			t.Fatalf("contract %s status = %q, want %q", contract.ID, contract.Status, wantStatus)
		}
		if contract.Phase != wantPhases[index] {
			t.Fatalf("contract %s phase = %q, want %q", contract.ID, contract.Phase, wantPhases[index])
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparisons[index]) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparisons[index])
		}
		if len(contract.Provenance) == 0 {
			t.Fatalf("contract %s has no pinned provenance", contract.ID)
		}
		for _, provenance := range contract.Provenance {
			if provenance.Kind != "documentation" && provenance.Kind != "test" {
				t.Fatalf("contract %s provenance kind = %q, want documentation or test", contract.ID, provenance.Kind)
			}
			if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
				t.Fatalf("contract %s provenance is not pinned to Django 6.1: %#v", contract.ID, provenance)
			}
			if provenance.Derived == nil || *provenance.Derived || provenance.License != "BSD-3-Clause" {
				t.Fatalf("contract %s provenance = %#v, want derived=false/BSD-3-Clause", contract.ID, provenance)
			}
		}

		observation := oracle.Contracts[index]
		if observation.ID != contract.ID || observation.Status != StatusObserved || observation.Phase != contract.Phase {
			t.Fatalf("oracle contract %d = %#v, want %s observed/%s", index, observation, contract.ID, contract.Phase)
		}
		static := baseline.Contracts[index]
		if static.ID != contract.ID || static.Status != StatusNotImplemented || static.Phase != contract.Phase {
			t.Fatalf("static contract %d = %#v, want %s not_implemented/%s", index, static, contract.ID, contract.Phase)
		}
		assertRelationObservationDimensions(t, contract, observation)
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("relation oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("relation static fixture does not validate: %v", err)
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

func TestRelationStaticFixtureExitsOneWithTwelveOrderedMismatches(t *testing.T) {
	root := conformanceRepositoryRoot(t)
	arguments := []string{
		"run", "./conformance/cmd/observationcmp",
		"-profile", "conformance/profiles/django-6.1-sqlite-darwin-arm64.json",
		"-manifest", "conformance/contracts/relation-manifest.json",
		"-expected", "conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json",
		"-actual", "conformance/fixtures/godj-relation-not-implemented.json",
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
	if !strings.Contains(text, "observationcmp: 12 difference(s)") {
		t.Fatalf("observationcmp stderr = %q, want 12 differences", text)
	}
	previous := -1
	for number := 1; number <= 12; number++ {
		needle := fmt.Sprintf("REL-%03d status:", number)
		position := strings.Index(text, needle)
		if position <= previous {
			t.Fatalf("observationcmp stderr does not preserve contract order at %s: %q", needle, text)
		}
		previous = position
	}
}

func assertRelationObservationDimensions(t *testing.T, contract Contract, observation Observation) {
	t.Helper()
	wantResult := false
	wantError := false
	wantDBState := false
	wantMetrics := false
	for _, dimension := range contract.Comparison {
		switch dimension {
		case CompareResult:
			wantResult = true
		case CompareError:
			wantError = true
		case CompareDBState:
			wantDBState = true
		case CompareMetrics:
			wantMetrics = true
		}
	}
	if (observation.Result != nil) != wantResult || (observation.Error != nil) != wantError ||
		(observation.DBState != nil) != wantDBState || (observation.Metrics != nil) != wantMetrics {
		t.Fatalf("observation %s dimensions result/error/db_state/metrics = %t/%t/%t/%t, want %t/%t/%t/%t",
			contract.ID,
			observation.Result != nil, observation.Error != nil, observation.DBState != nil, observation.Metrics != nil,
			wantResult, wantError, wantDBState, wantMetrics,
		)
	}
}

func loadRelationArtifacts(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := conformanceRepositoryRoot(t)
	profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := LoadManifest(filepath.Join(root, "conformance", "contracts", "relation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "relation-oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadObservationSuite(filepath.Join(root, "conformance", "fixtures", "godj-relation-not-implemented.json"))
	if err != nil {
		t.Fatal(err)
	}
	return profile, manifest, oracle, baseline
}
