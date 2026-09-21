package onetoonefixture_test

import (
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/onetoonefixture"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/internal/onetoonetest"
	"github.com/progresshans/godj/internal/projectgenerate"
	"github.com/progresshans/godj/query"
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

func TestOneToOneSQLiteReverseLookupsMatchDjango(t *testing.T) {
	backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "lookups.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	onetoonetest.RunReverseLookups(t, backend, "sqlite", func(plan query.Plan) (string, error) {
		statement, _, err := sqlite.Compile(plan)
		return statement, err
	}, backend.QueryCount)
}

func TestOneToOneReversePresenceMethodRejectsFieldNameCollision(t *testing.T) {
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	for a := range spec.Apps {
		for m := range spec.Apps[a].Schema.Models {
			model := &spec.Apps[a].Schema.Models[m]
			if model.Name != "review" {
				continue
			}
			for f := range model.Fields {
				if model.Fields[f].Name == "title" {
					model.Fields[f].GoName = "IsNull"
					changed = true
				}
			}
		}
	}
	if !changed {
		t.Fatal("negative fixture did not change the declaration")
	}
	if _, err := codegen.GenerateProject(spec); err == nil {
		t.Fatal("OneToOne field/method collision accepted")
	}
}
