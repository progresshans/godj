package migrationgraph

import (
	"slices"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestNamedConstraintDeltaRejectsHiddenModelFieldAndRetainedConstraintChanges(t *testing.T) {
	base := ir.Model{Name: "label", GoName: "Label", DBTable: "scoped_label", Fields: []ir.Field{
		{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true},
		{Name: "name", GoName: "Name", Column: "stored_name", Kind: ir.FieldText},
	}, UniqueConstraints: []ir.UniqueConstraint{{Name: "a", Fields: []string{"id"}}, {Name: "z", Fields: []string{"name"}}}}
	for _, name := range []string{"_first", "middle", "zz_last"} {
		constraint := ir.UniqueConstraint{Name: name, Fields: []string{"id", "name"}}
		larger := base.Clone()
		larger.UniqueConstraints = append(larger.UniqueConstraints, constraint.Clone())
		slices.SortFunc(larger.UniqueConstraints, func(a, b ir.UniqueConstraint) int {
			if a.Name < b.Name {
				return -1
			}
			if a.Name > b.Name {
				return 1
			}
			return 0
		})
		for _, kind := range []MigrationOperationKind{MigrationAddConstraint, MigrationRemoveConstraint} {
			operation := MigrationOperation{Kind: kind, Before: base.Clone(), After: larger.Clone()}
			if kind == MigrationRemoveConstraint {
				operation.Before, operation.After = operation.After, operation.Before
			}
			got, err := operation.ChangedConstraint()
			if err != nil || !got.Equal(constraint) {
				t.Fatal("single ordered delta rejected", kind, name, err)
			}
			for _, mutate := range []func(*ir.Model){
				func(m *ir.Model) { m.DBTable = "other" }, func(m *ir.Model) { m.Name = "other" }, func(m *ir.Model) { m.GoName = "Other" },
				func(m *ir.Model) { m.Fields[1].Column = "other" }, func(m *ir.Model) { m.Fields[1].Unique = true }, func(m *ir.Model) { slices.Reverse(m.Fields) },
				func(m *ir.Model) {
					for index := range m.UniqueConstraints {
						if m.UniqueConstraints[index].Name == "a" {
							m.UniqueConstraints[index].Fields = []string{"id", "name"}
						}
					}
				}, func(m *ir.Model) { slices.Reverse(m.UniqueConstraints) },
			} {
				bad := operation
				bad.Before = bad.Before.Clone()
				mutate(&bad.Before)
				if _, err := bad.ChangedConstraint(); err == nil {
					t.Fatal("constraint delta accepted a hidden change", kind, name)
				}
			}
		}
	}
	if _, err := (MigrationOperation{Kind: MigrationAlterField, Before: base, After: base}).ChangedConstraint(); err == nil {
		t.Fatal("wrong operation kind accepted")
	}
}
