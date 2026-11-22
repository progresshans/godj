package ir

import "testing"

func TestNullabilityChangePreservesFieldIdentityAndRelationPolicy(t *testing.T) {
	base := Field{Name: "external_id", GoName: "ExternalID", Column: "external_id", Kind: FieldChar, MaxLength: 80, Nullable: true}
	for _, unique := range []bool{false, true} {
		for _, fill := range []*Scalar{nil, {Kind: ScalarString, String: "보관"}} {
			required := base.Clone()
			required.Nullable, required.Unique, required.Default = false, unique, fill
			for _, pair := range [][2]Field{{base, required}, {required, base}} {
				if kind, err := ClassifyFieldChange(pair[0], pair[1]); err != nil || kind != ChangeNullability {
					t.Fatal("nullability transition", kind, err)
				}
			}
		}
	}
	for _, mutate := range []func(*Field){
		func(f *Field) { f.Column = "other" },
		func(f *Field) { f.MaxLength-- },
		func(f *Field) { f.Blank = true },
		func(f *Field) { f.DBIndex = true },
		func(f *Field) { f.PrimaryKey = true },
		func(f *Field) { f.Default = &Scalar{Kind: ScalarInteger, Integer: 1} },
	} {
		other := base.Clone()
		other.Nullable = false
		mutate(&other)
		if _, err := ClassifyFieldChange(base, other); err == nil {
			t.Fatal("nullability absorbed an invalid or unrelated change", other)
		}
	}
	ref := Field{Name: "owner", GoName: "OwnerID", Column: "owner_id", Kind: FieldForeignKey, Nullable: true, Relation: &ForeignKeyRelation{
		Target: ModelIdentity{AppLabel: "people", ModelName: "person"}, Cardinality: RelationManyToOne,
		Reverse: ReverseRelation{Name: "entries"}, OnDelete: DeleteSetNull,
	}}
	required := ref.Clone()
	required.Nullable = false
	if _, err := ClassifyFieldChange(ref, required); err == nil {
		t.Fatal("SET_NULL cannot target a required column")
	}
	ref.Relation.OnDelete, required.Relation.OnDelete = DeleteProtect, DeleteProtect
	if kind, err := ClassifyFieldChange(ref, required); err != nil || kind != ChangeNullability {
		t.Fatal("valid FK tightening rejected", kind, err)
	}
	required.Relation.Target.ModelName = "unrelated"
	if _, err := ClassifyFieldChange(ref, required); err == nil {
		t.Fatal("changed FK target through nullability")
	}
}

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
