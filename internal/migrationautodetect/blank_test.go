package migrationautodetect

import (
	"errors"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDetectBlankPolicyKeepsLogicalOperationsAndOwnedStorage(t *testing.T) {
	for _, beforeBlank := range []bool{false, true} {
		name := "allow_blank"
		if beforeBlank {
			name = "require_input"
		}
		t.Run(name, func(t *testing.T) {
			model := testModel("article", testChar("title", false, nil))
			model.Fields[0].Blank = beforeBlank
			model.ManyToMany = []ir.ManyToManyField{{Name: "related", GoName: "Related", Blank: beforeBlank, Target: ir.ModelIdentity{AppLabel: "content", ModelName: "article"}}}
			base := mustProjectState(t, testSchema("content", model))
			history := mustLoadDefinitions(t, initialMigrationsFromState(t, base)...)
			schema, _ := base.Schema("content")
			modelIndex := -1
			for i := range schema.Models {
				if schema.Models[i].Name == "article" {
					modelIndex = i
				}
			}
			if modelIndex < 0 {
				t.Fatal("missing logical model")
			}
			fieldIndex := -1
			for i, field := range schema.Models[modelIndex].Fields {
				if field.Name == "title" {
					fieldIndex = i
				}
			}
			if fieldIndex < 0 {
				t.Fatal("missing scalar field")
			}
			schema.Models[modelIndex].Fields[fieldIndex].Blank = !beforeBlank
			schema.Models[modelIndex].ManyToMany[0].Blank = !beforeBlank
			desired := mustProjectState(t, schema)
			request := Request{Definitions: history, Desired: desired, ManagedApps: []string{"content"}}
			plan, err := Detect(request)
			if err != nil {
				t.Fatal(err)
			}
			changes := plan.Migrations()
			if len(changes) != 1 || len(changes[0].Operations) != 2 {
				t.Fatal("metadata change recreated owned storage", changes)
			}
			scalar, scalarOK := changes[0].Operations[0].(migrations.AlterField)
			many, manyOK := changes[0].Operations[1].(migrations.AlterManyToMany)
			if !scalarOK || !manyOK || scalar.Before.Blank != beforeBlank || scalar.After.Blank == beforeBlank || many.Before.Blank != beforeBlank || many.After.Blank == beforeBlank {
				t.Fatal("blank policy was lowered to a different operation")
			}
			assertGeneratedState(t, history, changes, desired)
			repeated, err := Detect(request)
			if err != nil || !reflect.DeepEqual(repeated.Migrations(), changes) {
				t.Fatal("blank plan is nondeterministic", err)
			}
			for _, mixed := range []string{"scalar_storage", "collection_binding"} {
				t.Run(mixed, func(t *testing.T) {
					changed := schema.Clone()
					if mixed == "scalar_storage" {
						changed.Models[modelIndex].Fields[fieldIndex].Nullable = true
					} else {
						changed.Models[modelIndex].ManyToMany[0].GoName = "Different"
					}
					candidate := mustProjectState(t, changed)
					plan, err := Detect(Request{Definitions: history, Desired: candidate, ManagedApps: []string{"content"}})
					var typed *Error
					if !errors.As(err, &typed) || typed.Code != CodeUnsupportedChange || !plan.Empty() {
						t.Fatal("mixed blank/storage change was disguised as metadata-only", err)
					}
				})
			}
		})
	}
}
