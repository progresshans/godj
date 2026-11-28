package admin_test

import (
	"testing"

	"github.com/progresshans/godj/admin/testdata/browser/modeldef"
	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/projectgenerate"
)

func TestAdminCollectionBrowserGeneratedSourceMatchesDeclaration(t *testing.T) {
	spec, err := modeldef.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	report, err := projectgenerate.Check(t.Context(), "testdata/browser", bundle)
	if err != nil || !report.Clean() {
		t.Fatal("collection browser generated source drift", report, err)
	}
}
