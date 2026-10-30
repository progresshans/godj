package ir_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func manySchema() ir.Schema {
	return ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "owners", Models: []ir.Model{
		{Name: "owner", GoName: "Owner", Fields: []ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldText}}, ManyToMany: []ir.ManyToManyField{{Name: "labels", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}}}},
	}}
}

func TestManyToManyColumnlessNormalizationStorageAndHash(t *testing.T) {
	input := manySchema()
	normalized, hash, err := ir.NormalizeAndHash(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Models[0].Fields) != 1 || input.Models[0].ManyToMany[0].Symmetry != "" {
		t.Fatal("normalization mutated caller")
	}
	owner := normalized.Models[0]
	if len(owner.Fields) != 2 || owner.Fields[1].Name != "title" || len(normalized.Models) != 1 {
		t.Fatal("columnless relation became storage on owner")
	}
	field := owner.ManyToMany[0]
	if field.Symmetry != ir.ManyToManyDirected || field.Reverse.Name != "owner_set" {
		t.Fatal("defaults", field)
	}
	again, err := ir.Normalize(normalized)
	if err != nil || !reflect.DeepEqual(again, normalized) {
		t.Fatal("normalization not idempotent", err)
	}
	wire, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip ir.Schema
	if err := json.Unmarshal(wire, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if got, err := ir.Hash(roundtrip); err != nil || got != hash {
		t.Fatal("wire lost semantic hash", err)
	}
	storage, err := ir.StorageSchema(normalized)
	if err != nil {
		t.Fatal(err)
	}
	link := storage.Models[1]
	if len(storage.Models) != 2 || link.Name != "owner_labels" || link.GoName != "OwnerLabelsLink" || link.DBTable != "owners_owner_labels" || len(link.Fields) != 3 {
		t.Fatal("derived model", link)
	}
	if !reflect.DeepEqual(link.UniqueConstraints, []ir.UniqueConstraint{{Name: "relation_pair", Fields: []string{"source", "target"}}}) {
		t.Fatal("pair constraint missing")
	}
	for index, target := range []ir.ModelIdentity{{AppLabel: "owners", ModelName: "owner"}, {AppLabel: "labels", ModelName: "label"}} {
		fk := link.Fields[index+1]
		if fk.Kind != ir.FieldForeignKey || fk.Nullable || fk.Relation.Target != target || fk.Relation.OnDelete != ir.DeleteCascade || !fk.Relation.Reverse.Disabled {
			t.Fatal("automatic edge", fk)
		}
	}
	storage.Models[0].ManyToMany[0].Target.ModelName = "changed"
	if normalized.Models[0].ManyToMany[0].Target.ModelName != "label" {
		t.Fatal("storage aliases declaration")
	}
	changed := normalized.Clone()
	changed.Models[0].ManyToMany[0].Reverse.Name = "other"
	if got, err := ir.Hash(changed); err != nil || got == hash || owner.Equal(changed.Models[0]) {
		t.Fatal("ManyToMany omitted from hash or model equality", err)
	}
}

func TestManyToManyExplicitThroughAndSelfDefaultsAreDetached(t *testing.T) {
	input := manySchema()
	through := &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "links", ModelName: "edge"}, SourceField: "owner", TargetField: "label"}
	input.Models[0].ManyToMany[0].Through = through
	normalized, err := ir.Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	clone := normalized.Clone()
	clone.Models[0].ManyToMany[0].Through.SourceField = "changed"
	through.TargetField = "changed"
	if normalized.Models[0].ManyToMany[0].Through.SourceField != "owner" || normalized.Models[0].ManyToMany[0].Through.TargetField != "label" {
		t.Fatal("through ownership leaked")
	}
	storage, err := ir.StorageSchema(normalized)
	if err != nil || len(storage.Models) != 1 {
		t.Fatal("explicit through synthesized storage", err)
	}
	input = manySchema()
	input.Models[0].ManyToMany[0].Target = ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}
	normalized, err = ir.Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	if field := normalized.Models[0].ManyToMany[0]; field.Symmetry != ir.ManyToManySymmetrical || !field.Reverse.Disabled {
		t.Fatal("self default", field)
	}
	input.Models[0].ManyToMany[0].Symmetry = ir.ManyToManyDirected
	normalized, err = ir.Normalize(input)
	if err != nil || normalized.Models[0].ManyToMany[0].Reverse.Name != "owner_set" {
		t.Fatal("directed self reverse", err)
	}
}

func TestManyToManyRejectsCollisionsAndMalformedShapesWithoutPartialSchema(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*ir.Schema)
	}{
		{"stored_name", func(s *ir.Schema) { s.Models[0].ManyToMany[0].Name = "title" }},
		{"stored_go_name", func(s *ir.Schema) { s.Models[0].ManyToMany[0].GoName = "Title" }},
		{"duplicate", func(s *ir.Schema) { s.Models[0].ManyToMany = append(s.Models[0].ManyToMany, s.Models[0].ManyToMany[0]) }},
		{"invalid_name", func(s *ir.Schema) { s.Models[0].ManyToMany[0].Name = "bad-name" }},
		{"invalid_go", func(s *ir.Schema) { s.Models[0].ManyToMany[0].GoName = "hidden" }},
		{"missing_target", func(s *ir.Schema) { s.Models[0].ManyToMany[0].Target = ir.ModelIdentity{} }},
		{"symmetry", func(s *ir.Schema) { s.Models[0].ManyToMany[0].Symmetry = ir.ManyToManySymmetrical }},
		{"unknown_symmetry", func(s *ir.Schema) { s.Models[0].ManyToMany[0].Symmetry = "unknown" }},
		{"reverse_both", func(s *ir.Schema) {
			s.Models[0].ManyToMany[0].Reverse = ir.ReverseRelation{Name: "owners", Disabled: true}
		}},
		{"through_identity", func(s *ir.Schema) {
			s.Models[0].ManyToMany[0].Through = &ir.ThroughModel{SourceField: "a", TargetField: "b"}
		}},
		{"through_same_field", func(s *ir.Schema) {
			s.Models[0].ManyToMany[0].Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "a", ModelName: "b"}, SourceField: "same", TargetField: "same"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := manySchema()
			test.edit(&input)
			before := input.Clone()
			got, err := ir.Normalize(input)
			if err == nil || !reflect.DeepEqual(got, ir.Schema{}) || !reflect.DeepEqual(input, before) {
				t.Fatal("invalid schema published or caller mutated", err)
			}
		})
	}
	for _, model := range []ir.Model{{Name: "owner_labels", GoName: "Other"}, {Name: "other", GoName: "OwnerLabelsLink"}, {Name: "other", GoName: "Other", DBTable: "owners_owner_labels"}} {
		input := manySchema()
		input.Models = append(input.Models, model)
		if got, err := ir.StorageSchema(input); err == nil || !reflect.DeepEqual(got, ir.Schema{}) {
			t.Fatal("automatic storage collision accepted", model, err)
		}
	}
}

func TestResolveManyToManyChecksThroughEndpointsAndReverseNamespaces(t *testing.T) {
	owners := manySchema()
	labels := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "labels", Models: []ir.Model{{Name: "label", GoName: "Label"}}}
	for _, schemas := range [][]ir.Schema{{owners, labels}, {labels, owners}} {
		bindings, err := ir.ResolveManyToMany(schemas...)
		if err != nil || len(bindings) != 1 || !bindings[0].Automatic || bindings[0].Through.SourceField != "source" {
			t.Fatal("automatic cross-app resolution", bindings, err)
		}
	}
	if got, err := ir.ResolveManyToMany(owners); err == nil || got != nil {
		t.Fatal("unresolved target accepted")
	}
	owners.Models[0].ManyToMany[0].Through = &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "links", ModelName: "edge"}, SourceField: "from_owner", TargetField: "to_label"}
	fk := func(name string, target ir.ModelIdentity) ir.Field {
		return ir.Field{Name: name, GoName: name + "ID", Kind: ir.FieldForeignKey, Nullable: true, Relation: &ir.ForeignKeyRelation{Target: target, Cardinality: ir.RelationManyToOne, Reverse: ir.ReverseRelation{Disabled: true}, OnDelete: ir.DeleteSetNull}}
	}
	links := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "links", Models: []ir.Model{{Name: "edge", GoName: "Edge", Fields: []ir.Field{fk("from_owner", ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}), fk("to_label", ir.ModelIdentity{AppLabel: "labels", ModelName: "label"})}}}}
	links.Models[0].Fields[0].GoName = "FromOwnerID"
	links.Models[0].Fields[1].GoName = "ToLabelID"
	bindings, err := ir.ResolveManyToMany(owners, labels, links)
	if err != nil || len(bindings) != 1 || bindings[0].Automatic || bindings[0].Through.SourceField != "from_owner" {
		t.Fatal("explicit nullable non-unique through rejected", err)
	}
	wrong := links.Clone()
	wrong.Models[0].Fields[1].Relation.Target = ir.ModelIdentity{AppLabel: "owners", ModelName: "owner"}
	if got, err := ir.ResolveManyToMany(owners, labels, wrong); err == nil || got != nil {
		t.Fatal("wrong through endpoint accepted")
	}
	labels.Models[0].Fields = []ir.Field{{Name: "owner_set", GoName: "OwnerSet", Kind: ir.FieldText}}
	if got, err := ir.ResolveManyToMany(owners, labels, links); err == nil || got != nil {
		t.Fatal("reverse name collision accepted")
	}
}
