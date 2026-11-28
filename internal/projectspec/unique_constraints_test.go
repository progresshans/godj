package projectspec

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestNamedUniqueConstraintResourceAccountingBeforeClone(t *testing.T) {
	input := []ir.Schema{{AppLabel: "labels", Models: []ir.Model{{UniqueConstraints: []ir.UniqueConstraint{{Name: "scope_name", Fields: []string{"category", "name"}}}}}}}
	before := input[0].Clone()
	// Schema, model, constraint and both member references each consume a node.
	budget, err := validateSchemas(input, resourceBudget{nodes: MaxAggregateNodes - 5})
	if err != nil || budget.nodes != MaxAggregateNodes {
		t.Fatal("constraint maximum rejected", err)
	}
	if _, err := validateSchemas(input, resourceBudget{nodes: MaxAggregateNodes - 4}); err == nil {
		t.Fatal("constraint references bypassed aggregate budget")
	}
	if !reflect.DeepEqual(input[0], before) {
		t.Fatal("resource scan mutated caller-owned metadata")
	}
	input[0].Models[0].UniqueConstraints[0].Fields = make([]string, MaxFieldsPerModel)
	if err := ValidateSchemas(input); err != nil {
		t.Fatal(err)
	}
	input[0].Models[0].UniqueConstraints[0].Fields = make([]string, MaxFieldsPerModel+1)
	assertResourceLimit(t, ValidateSchemas(input), "constraint_fields")
	input[0].Models[0].UniqueConstraints[0].Fields = []string{"name"}
	for _, value := range []string{strings.Repeat("x", MaxSchemaStringBytes+1), string([]byte{0xff})} {
		input[0].Models[0].UniqueConstraints[0].Name = value
		if err := ValidateSchemas(input); err == nil {
			t.Fatal("invalid constraint name bypassed string budget")
		}
		input[0].Models[0].UniqueConstraints[0].Name = "scope_name"
		input[0].Models[0].UniqueConstraints[0].Fields[0] = value
		if err := ValidateSchemas(input); err == nil {
			t.Fatal("invalid constraint member bypassed string budget")
		}
		input[0].Models[0].UniqueConstraints[0].Fields[0] = "name"
	}
}
