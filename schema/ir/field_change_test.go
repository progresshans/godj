package ir

import "testing"

func TestFieldChangeClassifiesExactFacetsWithoutBorrowMutation(t *testing.T) {
	before := Field{Name: "cost", GoName: "Cost", Column: "cost", Kind: FieldDecimal, Nullable: true, Decimal: &DecimalSpec{MaxDigits: 5, DecimalPlaces: 2}}
	for _, precision := range []DecimalSpec{{7, 2}, {5, 3}, {4, 1}, {1000, 1000}} {
		after := before.Clone()
		after.Decimal = &precision
		oldSnapshot, newSnapshot := before.Clone(), after.Clone()
		kind, err := ClassifyFieldChange(before, after)
		if err != nil || kind != ChangeDecimalPrecision || !before.Equal(oldSnapshot) || !after.Equal(newSnapshot) {
			t.Fatalf("precision delta or input ownership changed: %v %v", kind, err)
		}
	}
	for name, mutate := range map[string]func(*Field){
		"identity":          func(f *Field) { f.Column = "other" },
		"nullable":          func(f *Field) { f.Nullable = false },
		"default":           func(f *Field) { f.Default = &Scalar{Kind: ScalarDecimal, Decimal: "0"} },
		"kind":              func(f *Field) { f.Kind = FieldFloat },
		"length":            func(f *Field) { f.MaxLength = 10 },
		"invalid precision": func(f *Field) { f.Decimal.MaxDigits = 1001 },
		"missing precision": func(f *Field) { f.Decimal = nil },
	} {
		t.Run(name, func(t *testing.T) {
			after := before.Clone()
			after.Decimal.MaxDigits = 7
			mutate(&after)
			if _, err := ClassifyFieldChange(before, after); err == nil {
				t.Fatal("mixed or invalid field change accepted")
			}
		})
	}
	if _, err := ClassifyFieldChange(before, before.Clone()); err == nil {
		t.Fatal("unchanged field accepted")
	}
	text := Field{Name: "label", GoName: "Label", Column: "label", Kind: FieldChar, MaxLength: 16}
	selected := text.Clone()
	selected.Choices = []Choice{{Value: Scalar{Kind: ScalarString, String: "a"}, Label: "A"}}
	if kind, err := ClassifyFieldChange(text, selected); err != nil || kind != ChangeChoices {
		t.Fatal("choices-only change lost its distinct meaning", err)
	}
}
