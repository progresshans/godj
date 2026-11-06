package ir

import "testing"

func TestCascadePolicyChangePreservesOtherFacetsAndInputOwnership(t *testing.T) {
	base := Field{Name: "owner", GoName: "OwnerID", Column: "owner_id", Kind: FieldForeignKey, Nullable: true,
		Relation: &ForeignKeyRelation{Target: ModelIdentity{AppLabel: "accounts", ModelName: "owner"},
			Cardinality: RelationManyToOne, Reverse: ReverseRelation{Name: "children"}, OnDelete: DeleteProtect}}
	for _, beforePolicy := range []DeletePolicy{DeleteProtect, DeleteSetNull, DeleteCascade} {
		for _, afterPolicy := range []DeletePolicy{DeleteProtect, DeleteSetNull, DeleteCascade} {
			if beforePolicy == afterPolicy {
				continue
			}
			before, after := base.Clone(), base.Clone()
			before.Relation.OnDelete, after.Relation.OnDelete = beforePolicy, afterPolicy
			for _, one := range []bool{false, true} {
				if one {
					after.Unique, after.Relation.Cardinality, after.Relation.Reverse = true, RelationOneToOne, ReverseRelation{Disabled: true}
				}
				previous, next := before.Clone(), after.Clone()
				kind, err := ClassifyFieldChange(before, after)
				if err != nil || kind != ChangeRelation || !before.Equal(previous) || !after.Equal(next) {
					t.Fatalf("policy %s -> %s (one=%t) changed classification or input ownership: %v %v", beforePolicy, afterPolicy, one, kind, err)
				}
			}
		}
	}
	for name, mutate := range map[string]func(*Field){
		"target":         func(f *Field) { f.Relation.Target.ModelName = "other" },
		"column":         func(f *Field) { f.Column = "other_id" },
		"nullability":    func(f *Field) { f.Nullable = false },
		"default":        func(f *Field) { f.Default = &Scalar{Kind: ScalarInteger, Integer: 1} },
		"unknown policy": func(f *Field) { f.Relation.OnDelete = "restrict" },
		"choices":        func(f *Field) { f.Choices = []Choice{{Value: Scalar{Kind: ScalarInteger, Integer: 1}, Label: "one"}} },
	} {
		t.Run(name, func(t *testing.T) {
			after := base.Clone()
			after.Relation.OnDelete = DeleteCascade
			mutate(&after)
			if _, err := ClassifyFieldChange(base, after); err == nil {
				t.Fatal("policy alteration absorbed another facet or invalid policy")
			}
		})
	}
}
