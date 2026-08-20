package ir_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestCurrentScalarCanonicalHashIsDeterministic(t *testing.T) {
	t.Parallel()

	input := ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "godj_conformance",
		Models: []ir.Model{{
			Name:   "article",
			GoName: "Article",
			Fields: []ir.Field{
				{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
				{Name: "published", GoName: "Published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean}},
				{Name: "summary", GoName: "Summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 200},
			},
		}},
	}
	canonical, err := ir.CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	const want = "{\"format_version\":1,\"app_label\":\"godj_conformance\",\"models\":[{\"name\":\"article\",\"go_name\":\"Article\",\"db_table\":\"godj_conformance_article\",\"fields\":[{\"name\":\"id\",\"go_name\":\"ID\",\"column\":\"id\",\"kind\":\"auto\",\"primary_key\":true,\"nullable\":false},{\"name\":\"title\",\"go_name\":\"Title\",\"column\":\"title\",\"kind\":\"char\",\"primary_key\":false,\"nullable\":false,\"max_length\":200},{\"name\":\"published\",\"go_name\":\"Published\",\"column\":\"published\",\"kind\":\"boolean\",\"primary_key\":false,\"nullable\":false,\"default\":{\"kind\":\"boolean\"}},{\"name\":\"summary\",\"go_name\":\"Summary\",\"column\":\"summary\",\"kind\":\"char\",\"primary_key\":false,\"nullable\":true,\"max_length\":200}]}]}\n"
	if !bytes.Equal(canonical, []byte(want)) {
		t.Fatalf("current scalar canonical bytes changed\nwant: %s\n got: %s", want, canonical)
	}
	hash, err := ir.Hash(input)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash != "3e6ec104d26c21665690e9d4a20f547ae2f7212b2eb35f5e741d38a85274647d" {
		t.Fatalf("current scalar hash = %s", hash)
	}
}

func TestCurrentRelationNormalizesRoundTripsAndDoesNotAliasInput(t *testing.T) {
	t.Parallel()

	input := relationSchema()
	input.Models[0].Fields[1].Column = ""
	normalized, err := ir.Normalize(input)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if normalized.FormatVersion != ir.CurrentFormatVersion || normalized.Models[0].Fields[1].Column != "author_id" {
		t.Fatalf("normalized relation schema = %#v", normalized)
	}
	if input.Models[0].Fields[1].Column != "" {
		t.Fatal("Normalize mutated the caller input")
	}

	canonical, err := ir.CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	var decoded ir.Schema
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	roundTrip, err := ir.CanonicalJSON(decoded)
	if err != nil {
		t.Fatalf("round-trip CanonicalJSON() error = %v", err)
	}
	if !bytes.Equal(canonical, roundTrip) {
		t.Fatalf("relation canonical round trip changed\nfirst: %s\nagain: %s", canonical, roundTrip)
	}

	input.Models[0].Fields[1].Relation.Target.AppLabel = "mutated"
	input.Models[0].Fields[1].Relation.Reverse.Name = "mutated"
	if normalized.Models[0].Fields[1].Relation.Target.AppLabel != "authors" ||
		normalized.Models[0].Fields[1].Relation.Reverse.Name != "posts" {
		t.Fatalf("Normalize retained nested relation alias: %#v", normalized.Models[0].Fields[1].Relation)
	}
}

func TestCurrentRelationExplicitColumnAndScalarColumnDefaults(t *testing.T) {
	t.Parallel()

	input := relationSchema()
	input.Models[0].Fields[1].Column = "author_key"
	input.Models[0].Fields = append(input.Models[0].Fields, ir.Field{
		Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200,
	})
	got, err := ir.Normalize(input)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if got.Models[0].Fields[1].Column != "author_key" || got.Models[0].Fields[2].Column != "title" {
		t.Fatalf("normalized columns = %#v", got.Models[0].Fields)
	}
}

func TestCurrentRelationValidationMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*ir.Schema)
		path string
		code string
	}{
		{name: "unsupported version", edit: func(s *ir.Schema) { s.FormatVersion = 4 }, path: "format_version", code: "unsupported_version"},
		{name: "relation on scalar", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Kind = ir.FieldChar; s.Models[0].Fields[1].MaxLength = 20 }, path: "models[0].fields[1].relation", code: "unsupported"},
		{name: "missing relation", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation = nil }, path: "models[0].fields[1].relation", code: "required"},
		{name: "primary key", edit: func(s *ir.Schema) { s.Models[0].Fields[1].PrimaryKey = true }, path: "models[0].fields[1].primary_key", code: "unsupported"},
		{name: "max length", edit: func(s *ir.Schema) { s.Models[0].Fields[1].MaxLength = 1 }, path: "models[0].fields[1].max_length", code: "unsupported"},
		{name: "default", edit: func(s *ir.Schema) {
			s.Models[0].Fields[1].Default = &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: 1}
		}, path: "models[0].fields[1].default", code: "unsupported"},
		{name: "target app", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.Target.AppLabel = "Authors" }, path: "models[0].fields[1].relation.target.app_label", code: "invalid_identifier"},
		{name: "target model", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.Target.ModelName = "" }, path: "models[0].fields[1].relation.target.model_name", code: "invalid_identifier"},
		{name: "cardinality", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.Cardinality = ir.RelationOneToMany }, path: "models[0].fields[1].relation.cardinality", code: "unsupported"},
		{name: "reverse empty", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.Reverse = ir.ReverseRelation{} }, path: "models[0].fields[1].relation.reverse", code: "invalid"},
		{name: "reverse both", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.Reverse.Disabled = true }, path: "models[0].fields[1].relation.reverse", code: "invalid"},
		{name: "reverse identifier", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.Reverse.Name = "bad-name" }, path: "models[0].fields[1].relation.reverse.name", code: "invalid_identifier"},
		{name: "delete policy", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.OnDelete = ir.DeletePolicy("cascade") }, path: "models[0].fields[1].relation.on_delete", code: "unsupported"},
		{name: "set null required", edit: func(s *ir.Schema) { s.Models[0].Fields[1].Relation.OnDelete = ir.DeleteSetNull }, path: "models[0].fields[1].relation.on_delete", code: "invalid_nullability"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := relationSchema()
			test.edit(&input)
			_, err := ir.Normalize(input)
			var validation *ir.ValidationError
			if !errors.As(err, &validation) || validation.Path != test.path || validation.Code != test.code {
				t.Fatalf("Normalize() error = %#v, want path=%q code=%q", err, test.path, test.code)
			}
		})
	}
}

func TestFieldAndModelCloneDeepCopyDefaultAndRelation(t *testing.T) {
	t.Parallel()
	if got := (ir.Model{}).Clone(); got.Fields != nil {
		t.Fatalf("Model.Clone changed nil fields to non-nil: %#v", got.Fields)
	}

	field := relationSchema().Models[0].Fields[1]
	field.Default = &ir.ScalarDefault{Kind: ir.ScalarInteger, Integer: 7}
	fieldClone := field.Clone()
	modelClone := (ir.Model{Fields: []ir.Field{field}}).Clone()

	field.Default.Integer = 9
	field.Relation.Target.AppLabel = "mutated"
	field.Relation.Reverse.Name = "mutated"
	for name, cloned := range map[string]ir.Field{"field": fieldClone, "model": modelClone.Fields[0]} {
		if cloned.Default == field.Default || cloned.Default.Integer != 7 || cloned.Relation == field.Relation ||
			cloned.Relation.Target.AppLabel != "authors" || cloned.Relation.Reverse.Name != "posts" {
			t.Fatalf("%s clone = %#v", name, cloned)
		}
	}
}

func relationSchema() ir.Schema {
	return ir.Schema{
		FormatVersion: ir.CurrentFormatVersion,
		AppLabel:      "blog",
		Models: []ir.Model{{
			Name:   "post",
			GoName: "Post",
			Fields: []ir.Field{
				{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
				{
					Name: "author", GoName: "AuthorID", Column: "author_id", Kind: ir.FieldForeignKey,
					Relation: &ir.ForeignKeyRelation{
						Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
						Cardinality: ir.RelationManyToOne,
						Reverse:     ir.ReverseRelation{Name: "posts"},
						OnDelete:    ir.DeleteProtect,
					},
				},
			},
		}},
	}
}
