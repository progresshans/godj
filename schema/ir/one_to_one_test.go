package ir

import "testing"

func TestOneToOneFieldChangePreservesStorageIdentity(t *testing.T) {
	before := Field{Name: "ticket", GoName: "TicketID", Column: "ticket_id", Kind: FieldForeignKey,
		Relation: &ForeignKeyRelation{Target: ModelIdentity{AppLabel: "tickets", ModelName: "ticket"},
			Cardinality: RelationManyToOne, Reverse: ReverseRelation{Name: "reports"}, OnDelete: DeleteProtect}}
	for _, unique := range []bool{false, true} {
		before.Unique = unique
		after := before.Clone()
		after.Unique = true
		after.Relation.Cardinality = RelationOneToOne
		after.Relation.Reverse.Name = "report"
		oldSnapshot, newSnapshot := before.Clone(), after.Clone()
		for _, pair := range [][2]Field{{before, after}, {after, before}} {
			kind, err := ClassifyFieldChange(pair[0], pair[1])
			if err != nil || kind != ChangeRelation {
				t.Fatalf("relation delta = %v, %v", kind, err)
			}
		}
		if !before.Equal(oldSnapshot) || !after.Equal(newSnapshot) {
			t.Fatal("classification mutated borrowed fields")
		}
		for name, mutate := range map[string]func(*Field){
			"target":         func(f *Field) { f.Relation.Target.ModelName = "other" },
			"delete":         func(f *Field) { f.Relation.OnDelete = DeleteSetNull },
			"nullable":       func(f *Field) { f.Nullable = true },
			"column":         func(f *Field) { f.Column = "other_id" },
			"name":           func(f *Field) { f.GoName = "OtherID" },
			"unique missing": func(f *Field) { f.Unique = false },
		} {
			t.Run(name, func(t *testing.T) {
				mixed := after.Clone()
				mutate(&mixed)
				if _, err := ClassifyFieldChange(before, mixed); err == nil {
					t.Fatal("mixed or invalid relation change accepted")
				}
			})
		}
	}
	one := before.Clone()
	one.Unique = true
	one.Relation.Cardinality = RelationOneToOne
	invalid := one.Clone()
	invalid.Unique = false
	if _, err := ClassifyFieldChange(one, invalid); err == nil {
		t.Fatal("raw delta can drop required one-to-one uniqueness")
	}
}
