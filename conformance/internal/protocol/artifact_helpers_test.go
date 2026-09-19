package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// requireArtifact loads a fresh value for each caller and reports the failing path.
func requireArtifact[T any](t *testing.T, path string, load func(string) (T, error)) T {
	t.Helper()
	value, err := load(path)
	if err != nil {
		t.Fatalf("load artifact %s: %v", path, err)
	}
	return value
}

func readArtifact(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

// JSON cloning intentionally accepts malformed suites used by negative controls.
// cloneSuite instead enforces the canonical protocol decoder on valid fixtures.
func cloneJSONSuite(t *testing.T, suite ObservationSuite) ObservationSuite {
	t.Helper()
	document, err := json.Marshal(suite)
	if err != nil {
		t.Fatal(err)
	}
	var cloned ObservationSuite
	if err := json.Unmarshal(document, &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}

func mutateFirstValue(value *Value) bool {
	if value == nil {
		return false
	}
	switch value.Type {
	case ValueBool:
		*value.Bool = !*value.Bool
		return true
	case ValueInt:
		if *value.Text == "0" {
			*value.Text = "1"
		} else {
			*value.Text = "0"
		}
		return true
	case ValueString, ValueDecimal, ValueDatetime, ValueUUID, ValueBytes:
		*value.Text += "_changed"
		return true
	case ValuePK:
		return mutateFirstValue(value.Nested)
	case ValueList:
		for index := range value.Items {
			if mutateFirstValue(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstValue(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}

func mutateFirstScalar(value *Value) bool {
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
		return mutateFirstScalar(value.Nested)
	case ValueList:
		if len(value.Items) == 0 {
			value.Items = append(value.Items, String("mutated"))
			return true
		}
		for index := range value.Items {
			if mutateFirstScalar(&value.Items[index]) {
				return true
			}
		}
	case ValueObject:
		for index := range value.Fields {
			if mutateFirstScalar(&value.Fields[index].Value) {
				return true
			}
		}
	}
	return false
}

// Each load produces fresh mutable values; actual adapters never use this helper.
func loadContractArtifacts(t *testing.T, name string) (Profile, Manifest, ObservationSuite, ObservationSuite) {
	t.Helper()
	root := filepath.Join(conformanceRepositoryRoot(t), "conformance")
	return requireArtifact(t, filepath.Join(root, "profiles", "django-6.1-sqlite-darwin-arm64.json"), LoadProfile),
		requireArtifact(t, filepath.Join(root, "contracts", name+"-manifest.json"), LoadManifest),
		requireArtifact(t, filepath.Join(root, "oracles", "django-6.1-sqlite-darwin-arm64", name+"-oracle.json"), LoadObservationSuite),
		requireArtifact(t, filepath.Join(root, "fixtures", "godj-"+name+"-not-implemented.json"), LoadObservationSuite)
}

func observationByID(t *testing.T, suite *ObservationSuite, contractID string) *Observation {
	t.Helper()
	for index := range suite.Contracts {
		if suite.Contracts[index].ID == contractID {
			return &suite.Contracts[index]
		}
	}
	t.Fatalf("observation %s is missing", contractID)
	return nil
}

func assertOnlyContractDiffers(t *testing.T, profile Profile, manifest Manifest, oracle, actual ObservationSuite, contractID string) {
	t.Helper()
	differences, err := Compare(profile, manifest, oracle, actual)
	if err != nil {
		t.Fatal(err)
	}
	if len(differences) == 0 {
		t.Fatal("payload mutation produced a false green")
	}
	for _, difference := range differences {
		if difference.ContractID != contractID {
			t.Fatalf("mutation reported against %q, want %q: %#v", difference.ContractID, contractID, differences)
		}
	}
}

func assertObservationDimensions(t *testing.T, contract Contract, observation Observation) {
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
		t.Fatalf(
			"observation %s dimensions result/error/db_state/metrics = %t/%t/%t/%t, want %t/%t/%t/%t",
			contract.ID,
			observation.Result != nil,
			observation.Error != nil,
			observation.DBState != nil,
			observation.Metrics != nil,
			wantResult,
			wantError,
			wantDBState,
			wantMetrics,
		)
	}
}
