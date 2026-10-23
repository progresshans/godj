package identityfixture_test

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/identityfixture/modeldef"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/identitytest"
	"github.com/progresshans/godj/internal/projectgenerate"
)

func TestImportedIdentityGeneratedFixtureMatchesDeclaration(t *testing.T) {
	spec, err := modeldef.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range bundle.Files() {
		if file.Owner == "app:godj_identity" {
			t.Fatal("consumer attempted to publish library models")
		}
	}
	report, err := projectgenerate.Check(t.Context(), ".", bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("imported model fixture drift: %+v, %v", report, err)
	}
}

func TestImportedIdentitySQLiteRelationsAndDeleteOwnership(t *testing.T) {
	backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "identity.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	identitytest.RunProduct(t, backend)
}
