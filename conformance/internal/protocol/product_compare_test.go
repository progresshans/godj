package protocol

import (
	"strings"
	"testing"
)

func TestCompareProductAcceptsFourObservedAndOrderedOracleLockedRemainder(t *testing.T) {
	profile, manifest, expected, actual := productComparisonFixture(t)

	differences, err := CompareProduct(profile, manifest, expected, actual, []string{"REL-001", "REL-003", "REL-004", "REL-006"})
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 0 {
		t.Fatalf("differences = %#v, want none", differences)
	}
}

func TestCompareProductRejectsFalseGreenShapes(t *testing.T) {
	profile, manifest, expected, valid := productComparisonFixture(t)
	tests := []struct {
		name     string
		required []string
		mutate   func(*Manifest, *ObservationSuite)
		contains string
	}{
		{
			name:     "missing required ID",
			required: nil,
			mutate:   func(*Manifest, *ObservationSuite) {},
			contains: "manifest requires 4",
		},
		{
			name:     "unknown required ID",
			required: []string{"REL-999", "REL-003", "REL-004", "REL-006"},
			mutate:   func(*Manifest, *ObservationSuite) {},
			contains: "manifest requires \"REL-001\"",
		},
		{
			name:     "required contract not implemented",
			required: []string{"REL-001", "REL-003", "REL-004", "REL-006"},
			mutate: func(_ *Manifest, suite *ObservationSuite) {
				suite.Contracts[0] = Observation{ID: "REL-001", Status: StatusNotImplemented, Phase: PhaseMetadata}
			},
			contains: "must be observed",
		},
		{
			name:     "locked contract observed",
			required: []string{"REL-001", "REL-003", "REL-004", "REL-006"},
			mutate: func(_ *Manifest, suite *ObservationSuite) {
				suite.Contracts[1] = expected.Contracts[1]
			},
			contains: "must be not_implemented",
		},
		{
			name:     "red contract",
			required: []string{"REL-001", "REL-003", "REL-004", "REL-006"},
			mutate: func(changed *Manifest, _ *ObservationSuite) {
				changed.Contracts[1].Status = ContractRed
			},
			contains: "product-ineligible status",
		},
		{
			name:     "reordered observations",
			required: []string{"REL-001", "REL-003", "REL-004", "REL-006"},
			mutate: func(_ *Manifest, suite *ObservationSuite) {
				suite.Contracts[1], suite.Contracts[2] = suite.Contracts[2], suite.Contracts[1]
			},
			contains: "in that position",
		},
		{
			name:     "duplicate observations",
			required: []string{"REL-001", "REL-003", "REL-004", "REL-006"},
			mutate: func(_ *Manifest, suite *ObservationSuite) {
				suite.Contracts[2] = suite.Contracts[1]
			},
			contains: "duplicate id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedManifest := cloneManifest(t, manifest)
			changedSuite := cloneSuite(t, valid)
			test.mutate(&changedManifest, &changedSuite)
			if _, err := CompareProduct(profile, changedManifest, expected, changedSuite, test.required); err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("CompareProduct error = %v, want substring %q", err, test.contains)
			}
		})
	}
}

func TestCompareProductReportsObservedPayloadDifference(t *testing.T) {
	profile, manifest, expected, actual := productComparisonFixture(t)
	changed := "changed"
	actual.Contracts[0].Result.Text = &changed

	differences, err := CompareProduct(profile, manifest, expected, actual, []string{"REL-001", "REL-003", "REL-004", "REL-006"})
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) != 1 || differences[0].ContractID != "REL-001" || differences[0].Path != "result.value" {
		t.Fatalf("differences = %#v, want REL-001 result.value", differences)
	}
}

func productComparisonFixture(t *testing.T) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	profile := validProfile()
	manifest := validManifest()
	manifest.Contracts = append(manifest.Contracts, manifest.Contracts...)
	manifest.Contracts = manifest.Contracts[:8]
	for index := range manifest.Contracts {
		manifest.Contracts[index].ID = "REL-00" + string(rune('1'+index))
		manifest.Contracts[index].Scenario = "django.relation.case" + string(rune('a'+index))
		manifest.Contracts[index].Phase = PhaseMetadata
		manifest.Contracts[index].Comparison = []ComparisonDimension{CompareResult}
		manifest.Contracts[index].Status = ContractOracleLocked
	}
	manifest.Contracts[0].Status = ContractPassing
	manifest.Contracts[2].Status = ContractPassing
	manifest.Contracts[3].Status = ContractPassing
	manifest.Contracts[5].Status = ContractPassing

	expected := ObservationSuite{
		FormatVersion: FormatVersion,
		Profile:       profile.Snapshot(),
		Contracts:     make([]Observation, len(manifest.Contracts)),
	}
	actual := ObservationSuite{
		FormatVersion: FormatVersion,
		Profile:       profile.Snapshot(),
		Contracts:     make([]Observation, len(manifest.Contracts)),
	}
	for index, contract := range manifest.Contracts {
		result := String(contract.ID)
		expected.Contracts[index] = Observation{ID: contract.ID, Status: StatusObserved, Phase: contract.Phase, Result: &result}
		actual.Contracts[index] = Observation{ID: contract.ID, Status: StatusNotImplemented, Phase: contract.Phase}
	}
	actualResult := String(manifest.Contracts[0].ID)
	actual.Contracts[0] = Observation{ID: manifest.Contracts[0].ID, Status: StatusObserved, Phase: manifest.Contracts[0].Phase, Result: &actualResult}
	for _, index := range []int{2, 3, 5} {
		result := String(manifest.Contracts[index].ID)
		actual.Contracts[index] = Observation{ID: manifest.Contracts[index].ID, Status: StatusObserved, Phase: manifest.Contracts[index].Phase, Result: &result}
	}
	return profile, manifest, expected, actual
}
