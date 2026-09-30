package migrationautodetect

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectURLAndBoundedStringKindChangesFromHistory(t *testing.T) {
	field := testChar("address", true, nil)
	field.MaxLength = 200
	base := mustProjectState(t, testSchema("content", testModel("contact", field)))
	history := initialMigrationsFromState(t, base)
	for _, kind := range []ir.FieldKind{ir.FieldURL, ir.FieldEmail, ir.FieldURL, ir.FieldChar} {
		before := field.Clone()
		field.Kind = kind
		desired := mustProjectState(t, testSchema("content", testModel("contact", field)))
		plan := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}})
		changes := plan.Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("URL kind change did not produce one migration operation")
		}
		operation, ok := changes[0].Operations[0].(migrations.AlterField)
		wanted, _ := desired.Model("content", "contact")
		if !ok || operation.Before.Kind != before.Kind || !operation.After.Equal(wanted.Fields[1]) {
			t.Fatal("URL automatic plan lost historical kind")
		}
		if len(changes[0].Dependencies) != 1 || changes[0].Dependencies[0] != history[len(history)-1].Key() {
			t.Fatal("URL kind change lost its historical dependency")
		}
		history = append(history, changes...)
		if repeated := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}); !repeated.Empty() {
			t.Fatal("URL migration did not reach a stable no-op")
		}
	}
	field.Kind, field.MaxLength = ir.FieldURL, 201
	if _, err := Detect(Request{Definitions: mustLoadDefinitions(t, history...), Desired: mustProjectState(t, testSchema("content", testModel("contact", field))), ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("URL kind change silently included a different storage length")
	}
}
