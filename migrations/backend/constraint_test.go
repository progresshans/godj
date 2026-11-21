package backend

import (
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestNamedConstraintMembersBindLogicalFieldsAndOwnNestedMetadata(t *testing.T) {
	model := ir.Model{Fields: []ir.Field{{Name: "id", Column: "record_id", PrimaryKey: true}, {Name: "value", Column: "stored_value", Choices: []ir.Choice{{Label: "Original"}}}}}
	constraint := ir.UniqueConstraint{Name: "ordered", Fields: []string{"value", "id"}}
	fields, err := UniqueConstraintFields(model, constraint)
	if err != nil || len(fields) != 2 || fields[0].Column != "stored_value" || fields[1].Column != "record_id" {
		t.Fatal("logical members lost order or storage mapping", err)
	}
	fields[0].Choices[0].Label = "changed"
	if model.Fields[1].Choices[0].Label != "Original" {
		t.Fatal("resolved fields expose nested model state")
	}
	for _, members := range [][]string{nil, {"stored_value"}, {"missing"}, {"value", "value"}} {
		constraint.Fields = members
		if fields, err := UniqueConstraintFields(model, constraint); err == nil || fields != nil {
			t.Fatal("invalid member list produced a partial physical key")
		}
	}
}
