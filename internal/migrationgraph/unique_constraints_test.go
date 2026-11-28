package migrationgraph_test

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/internal/migrationgraph"
	"github.com/progresshans/godj/schema/ir"
)

func TestNamedUniqueIntentCloneOwnsAllModelBoundaries(t *testing.T) {
	model := ir.Model{Name: "label", Fields: []ir.Field{}, UniqueConstraints: []ir.UniqueConstraint{{Name: "scope", Fields: []string{"category", "name"}}}}
	intent := migrationgraph.MigrationIntent{Operations: []migrationgraph.MigrationOperation{{
		Before: model, After: model,
		Targets:       []migrationgraph.MigrationTarget{{TargetModel: model}},
		RelatedModels: []migrationgraph.MigrationModel{{AppLabel: "other", Model: model}},
	}}}
	clone := intent.Clone()
	if !reflect.DeepEqual(intent, clone) {
		t.Fatal("intent clone changed metadata shape")
	}
	models := []ir.Model{clone.Operations[0].Before, clone.Operations[0].After, clone.Operations[0].Targets[0].TargetModel, clone.Operations[0].RelatedModels[0].Model}
	for index := range models {
		models[index].UniqueConstraints[0].Fields[0] = "changed"
		if model.UniqueConstraints[0].Fields[0] != "category" {
			t.Fatal("cloned constraint retains input member storage")
		}
		for following := index + 1; following < len(models); following++ {
			if models[following].UniqueConstraints[0].Fields[0] != "category" {
				t.Fatal("separate cloned boundaries share member storage")
			}
		}
	}
}

func TestNamedUniqueFieldDeltaRejectsHiddenConstraintEdits(t *testing.T) {
	before := ir.Model{Name: "label", GoName: "Label", DBTable: "label", Fields: []ir.Field{{Name: "value", GoName: "Value", Column: "value", Kind: ir.FieldInteger}}, UniqueConstraints: []ir.UniqueConstraint{{Name: "scope", Fields: []string{"value"}}}}
	after := before.Clone()
	after.Fields = append(after.Fields, ir.Field{Name: "extra", GoName: "Extra", Column: "extra", Kind: ir.FieldInteger})
	for _, reverse := range []bool{false, true} {
		operation := migrationgraph.MigrationOperation{Kind: migrationgraph.MigrationAddField, Before: before, After: after.Clone()}
		if reverse {
			operation.Kind = migrationgraph.MigrationRemoveField
			operation.Before, operation.After = operation.After, operation.Before
		}
		if field, err := operation.ChangedField(); err != nil || field.Name != "extra" {
			t.Fatal("ordinary field change lost retained constraint", err)
		}
		if reverse {
			operation.Before.UniqueConstraints[0].Fields[0] = "extra"
		} else {
			operation.After.UniqueConstraints[0].Fields[0] = "extra"
		}
		if _, err := operation.ChangedField(); err == nil {
			t.Fatal("field delta implicitly retargeted a named constraint")
		}
	}
}
