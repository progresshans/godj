package godj

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

type metricsProbeMutator struct {
	calls []string
}

func (mutator *metricsProbeMutator) Insert(context.Context, query.InsertPlan) (int64, error) {
	mutator.calls = append(mutator.calls, "INSERT")
	return 73, nil
}

func (mutator *metricsProbeMutator) Update(context.Context, query.UpdatePlan) (int64, error) {
	mutator.calls = append(mutator.calls, "UPDATE")
	return 4, nil
}

func (mutator *metricsProbeMutator) Delete(context.Context, query.DeletePlan) (int64, error) {
	mutator.calls = append(mutator.calls, "DELETE")
	return 2, nil
}

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

func TestGenerateMatchesLockedWriteMigrationOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadWriteMigrationInputs(t)
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
		t.Fatalf("GoDj write/migration suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateMatchesLockedSaveLifecycleOracle(t *testing.T) {
	t.Parallel()

	profile, manifest, expected := loadSaveLifecycleInputs(t)
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
		t.Fatalf("GoDj save lifecycle suite differs from locked Django oracle in %d place(s)", len(differences))
	}
}

func TestGenerateSaveLifecycleIsDeterministic(t *testing.T) {
	t.Parallel()

	profile, manifest, _ := loadSaveLifecycleInputs(t)
	first, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(first) error = %v", err)
	}
	second, err := Generate(context.Background(), profile, manifest)
	if err != nil {
		t.Fatalf("Generate(second) error = %v", err)
	}
	firstJSON, err := protocol.MarshalCanonical(first)
	if err != nil {
		t.Fatalf("MarshalCanonical(first) error = %v", err)
	}
	secondJSON, err := protocol.MarshalCanonical(second)
	if err != nil {
		t.Fatalf("MarshalCanonical(second) error = %v", err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("independent save lifecycle runs produced different canonical observations")
	}
}

func TestSaveMetricsAreDerivedFromObservedMutatorCalls(t *testing.T) {
	t.Parallel()

	delegate := &metricsProbeMutator{}
	recorder := &statementRecorder{}
	mutator := observedMutator(delegate, recorder)
	ctx := context.Background()

	insertID, err := mutator.Insert(ctx, query.NewInsertPlan("probe", nil))
	if err != nil || insertID != 73 {
		t.Fatalf("observed Insert() = (%d, %v), want (73, nil)", insertID, err)
	}
	updated, err := mutator.Update(ctx, query.NewUpdatePlan("probe", nil, query.FieldRef{}, query.Value{}))
	if err != nil || updated != 4 {
		t.Fatalf("observed Update() = (%d, %v), want (4, nil)", updated, err)
	}
	deleted, err := mutator.Delete(ctx, query.NewDeletePlan("probe", query.FieldRef{}, query.Value{}))
	if err != nil || deleted != 2 {
		t.Fatalf("observed Delete() = (%d, %v), want (2, nil)", deleted, err)
	}
	updated, err = mutator.Update(ctx, query.NewUpdatePlan("probe", nil, query.FieldRef{}, query.Value{}))
	if err != nil || updated != 4 {
		t.Fatalf("second observed Update() = (%d, %v), want (4, nil)", updated, err)
	}

	wantCalls := []string{"INSERT", "UPDATE", "DELETE", "UPDATE"}
	if !reflect.DeepEqual(delegate.calls, wantCalls) {
		t.Fatalf("delegate calls = %#v, want %#v", delegate.calls, wantCalls)
	}
	wantMetrics := protocol.Object(map[string]protocol.Value{
		"query_count": protocol.Integer("4"),
		"statement_kinds": protocol.List(
			protocol.String("INSERT"),
			protocol.String("UPDATE"),
			protocol.String("DELETE"),
			protocol.String("UPDATE"),
		),
	})
	if got := saveMetrics(recorder); !reflect.DeepEqual(got, wantMetrics) {
		t.Fatalf("save metrics = %#v, want metrics derived from calls %#v", got, wantMetrics)
	}

	emptyMetrics := protocol.Object(map[string]protocol.Value{
		"query_count":     protocol.Integer("0"),
		"statement_kinds": protocol.List(),
	})
	if got := saveMetrics(&statementRecorder{}); !reflect.DeepEqual(got, emptyMetrics) {
		t.Fatalf("independent empty recorder metrics = %#v, want %#v", got, emptyMetrics)
	}
}

func TestSaveResultObservationUsesRecorderForArbitraryContract(t *testing.T) {
	t.Parallel()

	const contractID = "MUTATION-PROBE-NOT-A-MANIFEST-CONTRACT"
	observation, err := withEmptyArticleDatabase(context.Background(), contractID, func(ctx context.Context, backend *sqlite.Backend) (protocol.Observation, error) {
		delegate := &metricsProbeMutator{}
		recorder := &statementRecorder{}
		mutator := observedMutator(delegate, recorder)
		if _, err := mutator.Delete(ctx, query.NewDeletePlan("probe", query.FieldRef{}, query.Value{})); err != nil {
			return protocol.Observation{}, err
		}
		if _, err := mutator.Insert(ctx, query.NewInsertPlan("probe", nil)); err != nil {
			return protocol.Observation{}, err
		}
		if _, err := mutator.Delete(ctx, query.NewDeletePlan("probe", query.FieldRef{}, query.Value{})); err != nil {
			return protocol.Observation{}, err
		}
		return saveResultObservation(ctx, backend, contractID, protocol.PhaseEvaluation, protocol.Null(), recorder)
	})
	if err != nil {
		t.Fatalf("arbitrary save result observation: %v", err)
	}
	if observation.ID != contractID {
		t.Fatalf("observation ID = %q, want %q", observation.ID, contractID)
	}
	wantMetrics := protocol.Object(map[string]protocol.Value{
		"query_count": protocol.Integer("3"),
		"statement_kinds": protocol.List(
			protocol.String("DELETE"),
			protocol.String("INSERT"),
			protocol.String("DELETE"),
		),
	})
	if observation.Metrics == nil || !reflect.DeepEqual(*observation.Metrics, wantMetrics) {
		t.Fatalf("arbitrary contract metrics = %#v, want recorder-derived %#v", observation.Metrics, wantMetrics)
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

func loadWriteMigrationInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "write-migration-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "write-migration-oracle.json"))
	if err != nil {
		t.Fatalf("LoadObservationSuite() error = %v", err)
	}
	return profile, manifest, expected
}

func loadSaveLifecycleInputs(t *testing.T) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	profile, err := protocol.LoadProfile(filepath.Join(root, "conformance", "profiles", "django-6.1-sqlite-darwin-arm64.json"))
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	manifest, err := protocol.LoadManifest(filepath.Join(root, "conformance", "contracts", "save-lifecycle-manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest() error = %v", err)
	}
	expected, err := protocol.LoadObservationSuite(filepath.Join(root, "conformance", "oracles", "django-6.1-sqlite-darwin-arm64", "save-lifecycle-oracle.json"))
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
