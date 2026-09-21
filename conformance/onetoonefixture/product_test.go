package onetoonefixture_test

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/onetoonefixture"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/onetoonetest"
	"github.com/progresshans/godj/internal/projectgenerate"
)

func TestOneToOneGeneratedFixtureMatchesDeclaration(t *testing.T) {
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	report, err := projectgenerate.Check(t.Context(), ".", bundle)
	if err != nil || !report.Clean() {
		t.Fatalf("generated one-to-one fixture drift: %+v, %v", report, err)
	}
}

func TestOneToOneSQLiteGeneratedProduct(t *testing.T) {
	backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "one-to-one.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	onetoonetest.RunProduct(t, backend)
}
