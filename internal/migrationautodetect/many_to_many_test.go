package migrationautodetect

import (
	"github.com/progresshans/godj/migrations"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestDetectAutomaticManyToManyPublishesOwnedStoragePlan(t *testing.T) {
	base := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil))))
	history := mustLoadDefinitions(t, initialMigrationsFromState(t, base)...)
	schema, _ := base.Schema("content")
	schema.Models[0].ManyToMany = []ir.ManyToManyField{{Name: "related", GoName: "Related", Target: ir.ModelIdentity{AppLabel: "content", ModelName: "article"}}}
	desired := mustProjectState(t, schema)
	plan, err := Detect(Request{Definitions: history, Desired: desired, ManagedApps: []string{"content"}})
	if err != nil {
		t.Fatal(err)
	}
	changes := plan.Migrations()
	if len(changes) != 1 || len(changes[0].Operations) != 1 {
		t.Fatal("missing owned relation change", changes)
	}
	if _, ok := changes[0].Operations[0].(migrations.AddManyToMany); !ok {
		t.Fatal("automatic storage escaped logical ownership", changes)
	}
	assertGeneratedState(t, history, changes, desired)
}
