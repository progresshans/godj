package model_test

import (
	"math"
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
			{Name: "published", GoName: "Published", Kind: ir.FieldBoolean, Default: &ir.Scalar{Kind: ir.ScalarBoolean}},
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
			name: "incomplete foreign key metadata",
			mutate: func(model *ir.Model) {
				model.Fields = append(model.Fields, ir.Field{Name: "author", GoName: "Author", Kind: ir.FieldForeignKey})
			},
		},
		{name: "unknown override", overrides: []formmodel.Override{formmodel.OverrideField("missing")}},
		{name: "primary key override", overrides: []formmodel.Override{formmodel.OverrideField("id")}},
		{
			name: "mismatched default",
			mutate: func(model *ir.Model) {
				model.Fields[1].Default = &ir.Scalar{Kind: ir.ScalarBoolean}
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

func TestIntegerProjectionPreservesNullabilityAndExactInitialDefault(t *testing.T) {
	model := ir.Model{Name: "counter", GoName: "Counter", Fields: []ir.Field{
		{Name: "count", GoName: "Count", Kind: ir.FieldInteger, Default: &ir.Scalar{Kind: ir.ScalarInteger, Integer: math.MinInt64}},
		{Name: "priority", GoName: "Priority", Kind: ir.FieldInteger, Nullable: true},
	}}
	spec, err := formmodel.NewSpec(model)
	if err != nil {
		t.Fatal(err)
	}
	fields := spec.Fields()
	if len(fields) != 2 || fields[0].Kind() != forms.FieldInteger || !fields[0].Required() || fields[0].Nullable() || fields[1].Required() || !fields[1].Nullable() {
		t.Fatal("integer metadata projection diverged")
	}
	model.Fields[0].Default.Integer = 0
	unbound, err := spec.Unbound(nil)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := unbound.Initial().Integer("count"); !ok || value != math.MinInt64 {
		t.Fatal("default lost precision or retained caller metadata")
	}
	if value, ok := unbound.Initial().Get("priority"); !ok || !value.IsNull() {
		t.Fatal("nullable initial invented zero")
	}
	bound, err := spec.Bind(forms.NewData(map[string][]string{"count": {"0"}, "priority": {""}}), nil)
	if err != nil || !bound.Valid() {
		t.Fatalf("integer form bind: %v", err)
	}
	if value, ok := bound.Cleaned().Integer("count"); !ok || value != 0 {
		t.Fatal("zero was replaced by the default")
	}
	if _, err := formmodel.NewSpec(model, formmodel.OverrideField("count", formmodel.WithRequired(false))); err == nil {
		t.Fatal("optional nonnullable integer has no representation for empty input")
	}
}
