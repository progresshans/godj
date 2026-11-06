package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectChoiceAdditionReorderRemovalAndNoOpFromHistory(t *testing.T) {
	field := testChar("status", false, nil)
	base := mustProjectState(t, testSchema("content", testModel("article", field)))
	history := initialMigrationsFromState(t, base)
	for _, choices := range [][]ir.Choice{
		{{Value: ir.Scalar{Kind: ir.ScalarString, String: "open"}, Label: "Open"}, {Value: ir.Scalar{Kind: ir.ScalarString, String: "closed"}, Label: "Closed"}},
		{{Value: ir.Scalar{Kind: ir.ScalarString, String: "closed"}, Label: "Closed"}, {Value: ir.Scalar{Kind: ir.ScalarString, String: "open"}, Label: "Open again"}},
		nil,
	} {
		field.Choices = choices
		desired := mustProjectState(t, testSchema("content", testModel("article", field)))
		plan := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}})
		changes := plan.Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatalf("choice changes: %#v", changes)
		}
		operation, ok := changes[0].Operations[0].(migrations.AlterField)
		wanted, _ := desired.Model("content", "article")
		if !ok || !operation.After.Equal(wanted.Fields[1]) {
			t.Fatalf("choice operation: %#v", changes[0].Operations)
		}
		if len(changes[0].Dependencies) != 1 || changes[0].Dependencies[0] != history[len(history)-1].Key() {
			t.Fatal("choice change lost exact historical dependency")
		}
		history = append(history, changes...)
		repeated := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}})
		if !repeated.Empty() {
			t.Fatal("choice writer did not reach a stable no-op")
		}
	}
	field.MaxLength++
	if _, err := Detect(Request{Definitions: mustLoadDefinitions(t, history...), Desired: mustProjectState(t, testSchema("content", testModel("article", field))), ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("physical field change was hidden in metadata migration")
	}
}
