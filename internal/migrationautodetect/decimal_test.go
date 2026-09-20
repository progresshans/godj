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
	field.Decimal.MaxDigits = 13
	changed := mustProjectState(t, testSchema("content", testModel("article", testChar("title", false, nil), field)))
	if _, err := Detect(Request{Definitions: reloaded, Desired: changed, ManagedApps: []string{"content"}}); err == nil {
		t.Fatal("unsupported precision alteration silently accepted")
	}
}
