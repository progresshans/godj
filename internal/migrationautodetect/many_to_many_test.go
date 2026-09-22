package migrationautodetect

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestDetectManyToManyDoesNotPublishIncompleteStoragePlan(t *testing.T) {
	base := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
	history := mustLoadDefinitions(t, initialMigrationsFromState(t, base)...)
	schema, _ := base.Schema("content")
	schema.Models[0].ManyToMany = []ir.ManyToManyField{{Name: "related", GoName: "Related", Target: ir.ModelIdentity{AppLabel: "content", ModelName: "article"}}}
	desired := mustProjectState(t, schema)
	plan, err := Detect(Request{Definitions: history, Desired: desired, ManagedApps: []string{"content"}})
	var failure *Error
	if !plan.Empty() || !errors.As(err, &failure) || failure.Code != CodeUnsupportedChange || failure.Field != "related" {
		t.Fatal("incomplete collection migration accepted", err)
	}
}
