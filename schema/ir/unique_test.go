package ir

import "testing"

func TestUniqueFieldChangesRemainAnExactIndependentFacet(t *testing.T) {
	before := Field{Name: "reference", GoName: "Reference", Column: "reference", Kind: FieldChar, MaxLength: 24, Nullable: true,
		Default: &Scalar{Kind: ScalarString, String: "default"}, Choices: []Choice{{Value: Scalar{Kind: ScalarString, String: "default"}, Label: "Default"}}}
	after := before.Clone()
	after.Unique = true
	for _, pair := range [][2]Field{{before, after}, {after, before}} {
		if kind, err := ClassifyFieldChange(pair[0], pair[1]); err != nil || kind != ChangeUnique {
			t.Fatal("unique add/remove lost its own delta", kind, err)
		}
	}
	for name, mutate := range map[string]func(*Field){
		"choices":  func(f *Field) { f.Choices[0].Label = "Other" },
		"nullable": func(f *Field) { f.Nullable = false },
		"default":  func(f *Field) { f.Default.String = "other" },
		"column":   func(f *Field) { f.Column = "renamed" },
		"kind":     func(f *Field) { f.Kind = FieldText; f.MaxLength = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			mixed := after.Clone()
			mutate(&mixed)
			if _, err := ClassifyFieldChange(before, mixed); err == nil {
				t.Fatal("unique absorbed another field change")
			}
		})
	}
	decimal := Field{Name: "cost", GoName: "Cost", Column: "cost", Kind: FieldDecimal, Decimal: &DecimalSpec{MaxDigits: 8, DecimalPlaces: 2}}
	mixed := decimal.Clone()
	mixed.Unique, mixed.Decimal.MaxDigits = true, 9
	if _, err := ClassifyFieldChange(decimal, mixed); err == nil {
		t.Fatal("unique absorbed a precision change")
	}
	pk := Field{Name: "id", GoName: "ID", Column: "id", Kind: FieldAuto, PrimaryKey: true}
	redundant := pk.Clone()
	redundant.Unique = true
	if _, err := ClassifyFieldChange(pk, redundant); err == nil {
		t.Fatal("primary key emitted a redundant unique alteration")
	}
	if before.Unique || before.Choices[0].Label != "Default" || before.Default.String != "default" {
		t.Fatal("classification mutated its borrowed inputs")
	}
}
