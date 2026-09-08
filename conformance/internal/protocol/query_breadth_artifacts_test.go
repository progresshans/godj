package protocol

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestQueryBreadthPassingManifestKeepsExplicitNotImplementedBaseline(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "query-breadth")
	wantScenarios := []string{
		"django.query.breadth.ordered_projection",
		"django.query.breadth.source_fields_outside_projection",
		"django.query.breadth.projection_cache_independence",
		"django.query.breadth.distinct_projection",
		"django.query.breadth.stable_offset_limit",
		"django.query.breadth.invalid_offset_pre_io",
		"django.query.breadth.cold_count_and_warm_cache",
		"django.query.breadth.sliced_distinct_count",
		"django.query.breadth.empty_count_and_nullable_max",
		"django.query.breadth.filtered_count_and_max",
		"django.query.breadth.terminal_failure_ownership",
		"django.query.breadth.backend_parity_reference",
	}
	wantPhases := []Phase{
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseConstruction,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
		PhaseEvaluation,
	}
	wantComparison := []ComparisonDimension{CompareResult, CompareDBState, CompareMetrics}
	if len(manifest.Contracts) != 12 || len(oracle.Contracts) != 12 || len(baseline.Contracts) != 12 {
		t.Fatalf("query-breadth artifact counts = manifest %d/oracle %d/static %d, want 12 each", len(manifest.Contracts), len(oracle.Contracts), len(baseline.Contracts))
	}
	if err := ValidateSuiteAgainst(profile, manifest, oracle); err != nil {
		t.Fatalf("Django query-breadth oracle does not validate: %v", err)
	}
	if err := ValidateSuiteAgainst(profile, manifest, baseline); err != nil {
		t.Fatalf("query-breadth not-implemented baseline does not validate: %v", err)
	}
	for index, contract := range manifest.Contracts {
		wantID := fmt.Sprintf("QRY-%03d", index+22)
		if contract.ID != wantID || oracle.Contracts[index].ID != wantID || baseline.Contracts[index].ID != wantID {
			t.Fatalf("query-breadth contract %d identifiers = %q/%q/%q, want %q", index, contract.ID, oracle.Contracts[index].ID, baseline.Contracts[index].ID, wantID)
		}
		if contract.Scenario != wantScenarios[index] {
			t.Fatalf("contract %s scenario = %q, want %q", contract.ID, contract.Scenario, wantScenarios[index])
		}
		if contract.Phase != wantPhases[index] || oracle.Contracts[index].Phase != wantPhases[index] || baseline.Contracts[index].Phase != wantPhases[index] {
			t.Fatalf("contract %s phases = %q/%q/%q, want %q", contract.ID, contract.Phase, oracle.Contracts[index].Phase, baseline.Contracts[index].Phase, wantPhases[index])
		}
		if contract.Status != ContractPassing {
			t.Fatalf("manifest contract %s status = %q, want %q", contract.ID, contract.Status, ContractPassing)
		}
		if oracle.Contracts[index].Status != StatusObserved {
			t.Fatalf("oracle contract %s status = %q, want %q", contract.ID, oracle.Contracts[index].Status, StatusObserved)
		}
		if baseline.Contracts[index].Status != StatusNotImplemented {
			t.Fatalf("baseline contract %s status = %q, want %q", contract.ID, baseline.Contracts[index].Status, StatusNotImplemented)
		}
		if !reflect.DeepEqual(contract.Comparison, wantComparison) {
			t.Fatalf("contract %s comparison = %#v, want %#v", contract.ID, contract.Comparison, wantComparison)
		}
		assertQueryBreadthProvenance(t, contract)
	}

	differences, err := Compare(profile, manifest, oracle, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != len(manifest.Contracts) {
		t.Fatalf("got %d differences, want one for each of %d contracts: %#v", len(differences), len(manifest.Contracts), differences)
	}
	for index, difference := range differences {
		if difference.ContractID != manifest.Contracts[index].ID || difference.Path != "status" {
			t.Fatalf("difference %d does not preserve manifest order or explicit not-implemented status: %#v", index, difference)
		}
	}
}

func TestQueryBreadthPayloadMutationsCannotFalseGreen(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, _ := loadContractArtifacts(t, "query-breadth")
	for index, contract := range manifest.Contracts {
		index, contract := index, contract
		t.Run(contract.ID+" result", func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			if !mutateFirstScalar(actual.Contracts[index].Result) {
				t.Fatalf("%s result has no mutable scalar", contract.ID)
			}
			assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
		})
		t.Run(contract.ID+" metrics", func(t *testing.T) {
			actual := cloneSuite(t, oracle)
			if !mutateFirstScalar(actual.Contracts[index].Metrics) {
				t.Fatalf("%s metrics have no mutable scalar", contract.ID)
			}
			assertOnlyContractDiffers(t, profile, manifest, oracle, actual, contract.ID)
		})
	}

	t.Run("database state", func(t *testing.T) {
		actual := cloneSuite(t, oracle)
		if !mutateFirstScalar(actual.Contracts[0].DBState) {
			t.Fatal("QRY-022 database state has no mutable scalar")
		}
		assertOnlyContractDiffers(t, profile, manifest, oracle, actual, "QRY-022")
	})
}

func TestQueryBreadthArtifactsRejectOrderPhaseAndProfileMutations(t *testing.T) {
	t.Parallel()

	profile, manifest, oracle, baseline := loadContractArtifacts(t, "query-breadth")
	for _, artifact := range []struct {
		name  string
		suite ObservationSuite
	}{
		{name: "oracle", suite: oracle},
		{name: "not-implemented baseline", suite: baseline},
	} {
		artifact := artifact
		t.Run(artifact.name+" order", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0], changed.Contracts[1] = changed.Contracts[1], changed.Contracts[0]
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-breadth contract reordering produced a false green")
			}
		})
		t.Run(artifact.name+" phase", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Contracts[0].Phase = differentPhase(changed.Contracts[0].Phase)
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-breadth phase mutation produced a false green")
			}
		})
		t.Run(artifact.name+" profile", func(t *testing.T) {
			changed := cloneSuite(t, artifact.suite)
			changed.Profile.Fingerprint.SQLiteSourceID += " changed"
			if err := ValidateSuiteAgainst(profile, manifest, changed); err == nil {
				t.Fatal("query-breadth profile mutation produced a false green")
			}
		})
	}
}

func assertQueryBreadthProvenance(t *testing.T, contract Contract) {
	t.Helper()
	decisionCount := 0
	djangoCount := 0
	for index, provenance := range contract.Provenance {
		if provenance.Derived == nil || *provenance.Derived {
			t.Fatalf("contract %s provenance %d derived = %#v, want false", contract.ID, index, provenance.Derived)
		}
		if provenance.Kind == "decision" {
			decisionCount++
			if provenance.Reference != "ADR-0039" || provenance.License != "" {
				t.Fatalf("contract %s decision provenance = %#v", contract.ID, provenance)
			}
			continue
		}
		djangoCount++
		if !strings.HasPrefix(provenance.Reference, "django@fe0a859f537d4238cf49fca39073513206f83122:") {
			t.Fatalf("contract %s provenance %d reference = %q", contract.ID, index, provenance.Reference)
		}
		if provenance.License != "BSD-3-Clause" {
			t.Fatalf("contract %s provenance %d license = %q, want BSD-3-Clause", contract.ID, index, provenance.License)
		}
	}
	if decisionCount != 1 || djangoCount == 0 {
		t.Fatalf("contract %s provenance counts = decision %d/Django %d, want 1/at least 1", contract.ID, decisionCount, djangoCount)
	}
}
