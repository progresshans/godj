package ir

import "testing"

func TestModelRenameRebindsOwnedStorageAndPreservesExplicitAuthority(t *testing.T) {
	id := func(name string) ModelIdentity { return ModelIdentity{AppLabel: "store", ModelName: name} }
	model := func(name, goName string) Model {
		s, err := Normalize(Schema{FormatVersion: CurrentFormatVersion, AppLabel: "store", Models: []Model{{Name: name, GoName: goName, DBTable: "fixed_entries",
			Fields: []Field{{Name: "friend", GoName: "Friend", Kind: FieldForeignKey, Relation: &ForeignKeyRelation{
				Target: id(name), Cardinality: RelationOneToOne, OnDelete: DeleteProtect}}},
			ManyToMany: []ManyToManyField{{Name: "labels", GoName: "Labels", Target: id("tag")}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return s.Models[0]
	}
	old, next := model("entry", "Entry"), model("publication", "Publication")
	change, err := NewModelRename("store", old, next)
	if err != nil {
		t.Fatal(err)
	}
	external := Model{Name: "peer", Fields: []Field{{Relation: &ForeignKeyRelation{Target: id("entry_labels")}}},
		ManyToMany: []ManyToManyField{{Target: id("entry"), Through: &ThroughModel{Model: id("entry_labels"), SourceField: "source", TargetField: "target"}}}}
	copy, ok := change.RebindReferences(external)
	if !ok || copy.Fields[0].Relation.Target != id("publication_labels") || copy.ManyToMany[0].Target != id("publication") || copy.ManyToMany[0].Through.Model != id("publication_labels") ||
		copy.ManyToMany[0].Through.SourceField != "source" || external.Fields[0].Relation.Target != id("entry_labels") || external.ManyToMany[0].Through.Model != id("entry_labels") {
		t.Fatal("owned or retained relation was rebound incorrectly", copy, external)
	}
	other := ModelIdentity{AppLabel: "elsewhere", ModelName: "entry"}
	if change.Target(other) != other {
		t.Fatal("crossed app identity")
	}
	pinned := next.Clone()
	pinned.Fields[1].Relation.Reverse = old.Fields[1].Relation.Reverse
	pinned.ManyToMany[0].Reverse = old.ManyToMany[0].Reverse
	if _, err := NewModelRename("store", old, pinned); err != nil {
		t.Fatal("could not retain explicitly authored reverse names", err)
	}
	next.ManyToMany[0].Reverse.Name = "other"
	if _, err := NewModelRename("store", old, next); err == nil {
		t.Fatal("accepted an unrelated reverse-name change")
	}
}
