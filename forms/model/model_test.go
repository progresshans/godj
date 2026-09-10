package model_test

import (
	"testing"

	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
)

func articleModel() ir.Model {
	return ir.Model{
		Name:   "article",
		GoName: "Article",
		Fields: []ir.Field{
			{Name: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true},
			{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200},
			{Name: "published", GoName: "Published", Kind: ir.FieldBoolean, Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean}},
			{Name: "summary", GoName: "Summary", Kind: ir.FieldChar, Nullable: true, MaxLength: 500},
		},
	}
}

func TestNewSpecProjectsIRInDeclarationOrder(t *testing.T) {
	model := articleModel()
	spec, err := formmodel.NewSpec(model, formmodel.OverrideField("title", formmodel.WithLabel("Headline")))
	if err != nil {
		t.Fatal(err)
	}
	model.Fields[1].Name = "mutated"
	fields := spec.Fields()
	if len(fields) != 3 {
		t.Fatalf("field count = %d", len(fields))
	}
	if fields[0].Name() != "title" || fields[0].Label() != "Headline" || fields[0].Kind() != forms.FieldChar ||
		!fields[0].Required() || fields[0].MaxLength() != 200 {
		t.Fatalf("title = name %q label %q kind %v required %v max %d",
			fields[0].Name(), fields[0].Label(), fields[0].Kind(), fields[0].Required(), fields[0].MaxLength())
	}
	if fields[1].Name() != "published" || fields[1].Kind() != forms.FieldBoolean || fields[1].Required() {
		t.Fatalf("published = %#v", fields[1])
	}
	if value, ok := fields[1].Default(); !ok {
		t.Fatal("published default absent")
	} else if boolean, ok := value.AsBoolean(); !ok || boolean {
		t.Fatalf("published default = %#v", value)
	}
	if fields[2].Name() != "summary" || fields[2].Required() || !fields[2].Nullable() || fields[2].MaxLength() != 500 {
		t.Fatalf("summary = %#v", fields[2])
	}
}

func TestNewSpecRejectsUnsupportedAndUnknownOverrides(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*ir.Model)
		overrides []formmodel.Override
	}{
		{
			name: "foreign key",
			mutate: func(model *ir.Model) {
				model.Fields = append(model.Fields, ir.Field{Name: "author", GoName: "Author", Kind: ir.FieldForeignKey})
			},
		},
		{name: "unknown override", overrides: []formmodel.Override{formmodel.OverrideField("missing")}},
		{name: "primary key override", overrides: []formmodel.Override{formmodel.OverrideField("id")}},
		{
			name: "mismatched default",
			mutate: func(model *ir.Model) {
				model.Fields[1].Default = &ir.ScalarDefault{Kind: ir.ScalarBoolean}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := articleModel()
			if test.mutate != nil {
				test.mutate(&model)
			}
			if _, err := formmodel.NewSpec(model, test.overrides...); err == nil {
				t.Fatal("NewSpec succeeded")
			}
		})
	}
}

func TestOverridesCannotChangeStorageAuthority(t *testing.T) {
	spec, err := formmodel.NewSpec(
		articleModel(),
		formmodel.OverrideField("summary", formmodel.WithRequired(true), formmodel.WithLabel("Abstract")),
	)
	if err != nil {
		t.Fatal(err)
	}
	fields := spec.Fields()
	if !fields[2].Required() || fields[2].Label() != "Abstract" || !fields[2].Nullable() || fields[2].MaxLength() != 500 {
		t.Fatalf("summary = label %q required %v nullable %v max %d",
			fields[2].Label(), fields[2].Required(), fields[2].Nullable(), fields[2].MaxLength())
	}
}
