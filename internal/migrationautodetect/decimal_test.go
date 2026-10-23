package migrationautodetect

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestDetectDecimalAdditionNoOpAndPrecisionChange(t *testing.T) {
	field := ir.Field{Name: "cost", GoName: "Cost", Column: "cost", Kind: ir.FieldDecimal, Nullable: true, Decimal: &ir.DecimalSpec{MaxDigits: 12, DecimalPlaces: 2}}
	initial := initialMigrationsFromState(t, mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil)))))
	loaded := mustLoadDefinitions(t, initial...)
	desired := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil), field)))
	plan := mustDetect(t, Request{Definitions: loaded, Desired: desired, ManagedApps: []string{"content"}})
	got := plan.Migrations()
	if len(got) != 1 || len(got[0].Operations) != 1 {
		t.Fatal("decimal addition did not produce one operation")
	}
	operation, ok := got[0].Operations[0].(migrations.AddField)
	if !ok || !operation.Field.Equal(field) {
		t.Fatal("decimal addition lost precision")
	}
	assertGeneratedState(t, loaded, got, desired)
	all := append(initial, got...)
	reloaded := mustLoadDefinitions(t, all...)
	repeat := mustDetect(t, Request{Definitions: reloaded, Desired: desired, ManagedApps: []string{"content"}})
	if !repeat.Empty() {
		t.Fatal("decimal no-op generated migration")
	}
	old := field.Clone()
	field.Decimal.MaxDigits = 13
	field.Decimal.DecimalPlaces = 3
	changed := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil), field)))
	precision := mustDetect(t, Request{Definitions: reloaded, Desired: changed, ManagedApps: []string{"content"}}).Migrations()
	if len(precision) != 1 || len(precision[0].Operations) != 1 {
		t.Fatal("precision change lost operation")
	}
	alter, ok := precision[0].Operations[0].(migrations.AlterField)
	if !ok || !alter.Before.Equal(old) || !alter.After.Equal(field) {
		t.Fatal("precision change lost exact preimage or target")
	}
	assertGeneratedState(t, reloaded, precision, changed)
	updated := mustLoadDefinitions(t, append(all, precision...)...)
	if !mustDetect(t, Request{Definitions: updated, Desired: changed, ManagedApps: []string{"content"}}).Empty() {
		t.Fatal("repeated precision generated a migration")
	}
	field.Nullable = false
	mixed := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil), field)))
	if _, err := Detect(Request{Definitions: reloaded, Desired: mixed, ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("precision change admitted simultaneous nullability change")
	}
}
