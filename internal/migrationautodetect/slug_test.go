package migrationautodetect

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDetectSlugIndexAndUnicodeChangesFromExactHistory(t *testing.T) {
	field := testChar("address", true, nil)
	field.MaxLength, field.Column = 50, "address"
	base := mustProjectState(t, testSchema("content", testModel("contact", field)))
	history := initialMigrationsFromState(t, base)
	for _, policy := range []struct {
		kind                   ir.FieldKind
		index, unicode, unique bool
	}{
		{ir.FieldSlug, true, false, false}, {ir.FieldSlug, true, true, false},
		{ir.FieldSlug, false, true, false}, {ir.FieldSlug, true, true, false},
		{ir.FieldSlug, true, true, true}, {ir.FieldSlug, true, true, false}, {ir.FieldChar, false, false, false},
	} {
		before := field.Clone()
		field.Kind, field.DBIndex, field.AllowUnicode, field.Unique = policy.kind, policy.index, policy.unicode, policy.unique
		desired := mustProjectState(t, testSchema("content", testModel("contact", field)))
		plan := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}})
		changes := plan.Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("slug transition is not one explicit operation")
		}
		operation, ok := changes[0].Operations[0].(migrations.AlterField)
		wanted, _ := desired.Model("content", "contact")
		if !ok || !operation.Before.Equal(before) || !operation.After.Equal(wanted.Fields[1]) {
			t.Fatal("automatic plan lost exact slug preimages")
		}
		if len(changes[0].Dependencies) != 1 || changes[0].Dependencies[0] != history[len(history)-1].Key() {
			t.Fatal("transition lost history dependency")
		}
		history = append(history, changes...)
		if repeated := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}); !repeated.Empty() {
			t.Fatal("slug/index plan failed to reach no-op")
		}
	}
	field.Kind, field.DBIndex, field.MaxLength = ir.FieldSlug, true, 51
	if _, err := Detect(Request{Definitions: mustLoadDefinitions(t, history...), Desired: mustProjectState(t, testSchema("content", testModel("contact", field))), ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("index conversion hid an unsupported storage length change")
	}
}
