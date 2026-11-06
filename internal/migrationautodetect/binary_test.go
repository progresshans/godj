package migrationautodetect

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDetectBinaryAdditionAndInputPolicyFromExactHistory(t *testing.T) {
	field := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldBinary, Nullable: true, NonEditable: true, MaxLength: 4, Default: &ir.Scalar{Kind: ir.ScalarBinary}}
	before := mustProjectState(t, testSchema("content", testModel("packet", testChar("title", false, nil))))
	history := initialMigrationsFromState(t, before)
	desired := mustProjectState(t, testSchema("content", testModel("packet", testChar("title", false, nil), field)))
	added := mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}).Migrations()
	if len(added) != 1 || len(added[0].Operations) != 1 {
		t.Fatal("binary addition lost operation")
	}
	operation, ok := added[0].Operations[0].(migrations.AddField)
	if !ok || !operation.Field.Equal(field) {
		t.Fatal("binary empty default or editable policy lost")
	}
	history = append(history, added...)
	for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.NonEditable = false }, func(f *ir.Field) { f.MaxLength = 8 }, func(f *ir.Field) { f.DBIndex = true }, func(f *ir.Field) { f.NonEditable = true }, func(f *ir.Field) { f.MaxLength = 0 }} {
		previous := field.Clone()
		mutate(&field)
		desired = mustProjectState(t, testSchema("content", testModel("packet", testChar("title", false, nil), field)))
		loaded := mustLoadDefinitions(t, history...)
		changes := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}}).Migrations()
		if len(changes) != 1 || len(changes[0].Operations) != 1 {
			t.Fatal("binary policy lost explicit operation")
		}
		change, ok := changes[0].Operations[0].(migrations.AlterField)
		if !ok || !change.Before.Equal(previous) || !change.After.Equal(field) {
			t.Fatal("binary policy lost exact preimage")
		}
		assertGeneratedState(t, loaded, changes, desired)
		history = append(history, changes...)
		if !mustDetect(t, Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}).Empty() {
			t.Fatal("binary policy never reaches no-op")
		}
	}
	incompatible := field.Clone()
	incompatible.Kind = ir.FieldText
	incompatible.Default = nil
	desired = mustProjectState(t, testSchema("content", testModel("packet", testChar("title", false, nil), incompatible)))
	if _, err := Detect(Request{Definitions: mustLoadDefinitions(t, history...), Desired: desired, ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("binary bytes silently converted to text")
	}
}
