package backend

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestChangedFieldSealsOneDeltaAndReturnsDetachedPrecision(t *testing.T) {
	before := ir.Model{Name: "cost", GoName: "Cost", DBTable: "costs_cost", Fields: []ir.Field{{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldDecimal, Decimal: &ir.DecimalSpec{MaxDigits: 5, DecimalPlaces: 2}}}}
	after := before.Clone()
	after.Fields[0].Decimal.MaxDigits = 7
	old, next, kind, err := ChangedField(before, after)
	if err != nil || kind != ir.ChangeDecimalPrecision || old.Decimal.MaxDigits != 5 || next.Decimal.MaxDigits != 7 {
		t.Fatal("incorrect complete field delta", err)
	}
	old.Decimal.MaxDigits, next.Decimal.MaxDigits = 1000, 1000
	if before.Fields[0].Decimal.MaxDigits != 5 || after.Fields[0].Decimal.MaxDigits != 7 {
		t.Fatal("returned precision aliases sealed input")
	}
	for _, mutation := range []func(*ir.Model){
		func(m *ir.Model) { m.DBTable = "other" },
		func(m *ir.Model) { m.Fields = nil },
		func(m *ir.Model) { m.Fields[0].Nullable = true },
	} {
		changed := after.Clone()
		mutation(&changed)
		if _, _, _, err := ChangedField(before, changed); err == nil {
			t.Fatal("unsealed model transition accepted")
		}
	}
	if _, _, _, err := ChangedField(before, before); err == nil {
		t.Fatal("unchanged model accepted")
	}
	before.Fields = append(before.Fields, before.Fields[0].Clone())
	before.Fields[1].Name, before.Fields[1].GoName, before.Fields[1].Column = "other", "Other", "other"
	after = before.Clone()
	for index := range after.Fields {
		after.Fields[index].Decimal.MaxDigits++
	}
	if _, _, _, err := ChangedField(before, after); err == nil {
		t.Fatal("multiple field transitions accepted")
	}
}
