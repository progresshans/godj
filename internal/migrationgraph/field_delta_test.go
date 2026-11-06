package migrationgraph

import (
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestChangedFieldPreservesEveryRetainedFieldAndOrder(t *testing.T) {
	base := graphTestModel(t, "graph", "node", ir.Field{Name: "label", GoName: "Label", Kind: ir.FieldText}).Model
	added := graphTestFK("parent", "graph", "node")
	for position := 0; position <= len(base.Fields); position++ {
		after := base.Clone()
		after.Fields = slices.Insert(after.Fields, position, added)
		for _, kind := range []MigrationOperationKind{MigrationAddField, MigrationRemoveField} {
			op := MigrationOperation{Kind: kind, Before: base, After: after}
			if kind == MigrationRemoveField {
				op.Before, op.After = after, base
			}
			got, err := op.ChangedField()
			if err != nil || !reflect.DeepEqual(got, added) {
				t.Fatalf("position %d kind %d: %v", position, kind, err)
			}
		}
		for _, mutate := range []func(*ir.Model){
			func(m *ir.Model) { m.DBTable += "_changed" },
			func(m *ir.Model) {
				for i := range m.Fields {
					if m.Fields[i].Name == "label" {
						m.Fields[i].Nullable = true
					}
				}
			},
			func(m *ir.Model) { m.Fields = append(m.Fields, added) },
			func(m *ir.Model) { slices.Reverse(m.Fields) },
		} {
			bad := after.Clone()
			mutate(&bad)
			if _, err := (MigrationOperation{Kind: MigrationAddField, Before: base, After: bad}).ChangedField(); err == nil {
				t.Fatalf("accepted invalid retained metadata at %d", position)
			}
		}
	}
	if _, err := (MigrationOperation{Kind: MigrationCreateModel, After: base}).ChangedField(); err == nil {
		t.Fatal("accepted non-field operation")
	}
}
