package godj

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGenerateMatchesLockedDjangoOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadLockedInputs(t)
	actual, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	differences, err := protocol.Compare(profile, manifest, expected, actual)
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(differences) != 0 {
		for _, difference := range differences {
			t.Logf("%s %s: %s (expected %s, actual %s)",
				difference.ContractID,
				difference.Path,
				difference.Message,
				difference.Expected,
				difference.Actual,
			)
		}
		t.Fatalf("GoDj suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestConstructionContractsAreObservedBeforeQueryIO(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadLockedInputs(t)
	suite, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, contractID := range []string{"QRY-008", "QRY-010"} {
		observation := findObservation(t, suite, contractID)
		if observation.Phase != protocol.PhaseConstruction {
			t.Fatalf("%s phase = %q, want construction", contractID, observation.Phase)
		}
		if observation.Error == nil {
			t.Fatalf("%s error is nil", contractID)
		}
	}

	observation := findObservation(t, suite, "QRY-009")
	if observation.Phase != protocol.PhaseConstruction {
		t.Fatalf("QRY-009 phase = %q, want construction", observation.Phase)
	}
	wantMetrics := protocol.Object(map[string]protocol.Value{
		"queries_during_construction": protocol.Integer("0"),
	})
	if observation.Metrics == nil {
		t.Fatal("QRY-009 metrics are nil")
	}
	if !reflect.DeepEqual(*observation.Metrics, wantMetrics) {
		t.Fatalf("QRY-009 metrics = %#v, want %#v", *observation.Metrics, wantMetrics)
	}
}

func TestGenerateHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadLockedInputs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Generate(ctx, profile, manifest)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v, want context.Canceled", err)
	}
}

func loadLockedInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func findObservation(t *testing.T, suite protocol.ObservationSuite, contractID string) protocol.Observation {
	t.Helper()
	for _, observation := range suite.Contracts {
		if observation.ID == contractID {
			return observation
		}
	}
	t.Fatalf("observation %s is missing", contractID)
	return protocol.Observation{}
}
