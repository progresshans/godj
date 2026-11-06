package godj

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func TestGenerateMatchesReferencesAndPreservesDeterminism(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		deterministic bool
	}{
		{name: ""},
		{name: "write-migration"},
		{name: "save-lifecycle", deterministic: true},
		{name: "query-cache", deterministic: true},
		{name: "migration-planning", deterministic: true},
		{name: "migration-restart", deterministic: true},
		{name: "migration-state-reconstruction", deterministic: true},
		{name: "migration-definition-source"},
		{name: "migration-project-check", deterministic: true},
	} {
		name := test.name
		if name == "" {
			name = "core"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			profile, manifest, expected := loadReferenceInputs(t, test.name)
			actual, err := Generate(t.Context(), profile, manifest)
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			differences, err := protocol.Compare(profile, manifest, expected, actual)
			if err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if len(differences) != 0 {
				for _, difference := range differences {
					t.Logf("%s %s: %s (expected %s, actual %s)", difference.ContractID, difference.Path,
						difference.Message, difference.Expected, difference.Actual)
				}
				t.Fatalf("actual differs from the reference in %d place(s)", len(differences))
			}
			if !test.deterministic {
				return
			}
			// The reference comparison also supplies the first independent run.
			// Do not cache or manufacture the second actual from that result.
			second, err := Generate(t.Context(), profile, manifest)
			if err != nil {
				t.Fatalf("Generate(second) error = %v", err)
			}
			firstJSON, err := protocol.MarshalCanonical(actual)
			if err != nil {
				t.Fatal(err)
			}
			secondJSON, err := protocol.MarshalCanonical(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstJSON, secondJSON) {
				t.Fatal("independent runs produced different canonical observations")
			}
		})
	}
}

func loadReferenceInputs(t *testing.T, name string) (protocol.Profile, protocol.Manifest, protocol.ObservationSuite) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "conformance")
	manifest, oracle := "manifest.json", "oracle.json"
	if name != "" {
		manifest, oracle = name+"-manifest.json", name+"-oracle.json"
	}
	return loadFixture(t, filepath.Join(root, "profiles", "django-6.1-sqlite-darwin-arm64.json"), protocol.LoadProfile),
		loadFixture(t, filepath.Join(root, "contracts", manifest), protocol.LoadManifest),
		loadFixture(t, filepath.Join(root, "oracles", "django-6.1-sqlite-darwin-arm64", oracle), protocol.LoadObservationSuite)
}
