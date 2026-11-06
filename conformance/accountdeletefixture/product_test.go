package accountdeletefixture_test

import (
	"testing"

	"github.com/progresshans/godj/codegen"
	fixture "github.com/progresshans/godj/conformance/accountdeletefixture"
	"github.com/progresshans/godj/internal/projectgenerate"
)

func TestAccountDeletionGeneratedFixtureMatchesDeclaration(t *testing.T) {
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
		t.Fatal("generated account deletion fixture drift", report, err)
	}
}
