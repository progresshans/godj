package model_test

import (
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/modeldef"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/schema/ir"
)

func TestRelationModelFormsRequireExplicitMembershipAndPreserveMetadata(t *testing.T) {
	definition, err := modeldef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ticket", "service_report"} {
		t.Run(name, func(t *testing.T) {
			var model ir.Model
			for _, candidate := range definition.Models {
				if candidate.Name == name {
					model = candidate
				}
			}
			fieldName := "category"
			if name == "service_report" {
				fieldName = "ticket"
			}
			spec, err := formmodel.NewSpecForFields(model, []string{fieldName})
			if err != nil {
				t.Fatal(err)
			}
			field := spec.Fields()[0]
			if !field.ModelChoice() || field.Kind() != forms.FieldInteger || field.Widget() != forms.Select || !field.Required() || field.Nullable() {
				t.Fatal("relation structure lost", field)
			}
			input := forms.NewData(map[string][]string{fieldName: {"7"}})
			denied, err := spec.Bind(input, nil)
			if err != nil || denied.Valid() {
				t.Fatal("unpopulated relation accepted input", err)
			}
			scoped, err := spec.WithModelChoices(fieldName, forms.Choice{Value: forms.Integer(7), Label: "Visible"})
			if err != nil {
				t.Fatal(err)
			}
			bound, err := scoped.Bind(input, nil)
			if err != nil || !bound.Valid() {
				t.Fatal("explicit member rejected", err, bound.Errors())
			}
			for _, original := range model.Fields {
				if original.Name != fieldName {
					continue
				}
				nullable := model
				nullable.Fields = []ir.Field{original.Clone()}
				nullable.Fields[0].Nullable = true
				optional, err := formmodel.NewSpec(nullable)
				if err != nil {
					t.Fatal(err)
				}
				empty, err := optional.Bind(forms.NewData(nil), nil)
				if err != nil || !empty.Valid() {
					t.Fatal("nullable empty rejected", err, empty.Errors())
				}
				value, exists := empty.Cleaned().Get(fieldName)
				if !exists || !value.IsNull() {
					t.Fatal("nullable relation did not preserve NULL")
				}
				for _, mutate := range []func(*ir.Field){func(f *ir.Field) { f.Relation = nil }, func(f *ir.Field) { f.Relation.Cardinality = "bad" }, func(f *ir.Field) { f.Relation.Target.ModelName = "" }, func(f *ir.Field) { f.MaxLength = 1 }, func(f *ir.Field) { f.Relation.Cardinality = ir.RelationOneToOne; f.Unique = false }} {
					invalid := model
					invalid.Fields = []ir.Field{original.Clone()}
					mutate(&invalid.Fields[0])
					if _, err := formmodel.NewSpec(invalid); err == nil {
						t.Fatal("malformed relation metadata accepted")
					}
				}
			}
		})
	}
}
