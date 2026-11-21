package projectspec

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyResourcesIncludeDerivedStorageBeforeClone(t *testing.T) {
	input := []ir.Schema{{AppLabel: "owners", Models: []ir.Model{{Name: "owner", GoName: "Owner", ManyToMany: []ir.ManyToManyField{{Name: "labels", GoName: "Labels", Target: ir.ModelIdentity{AppLabel: "labels", ModelName: "label"}}}}}}}
	before := input[0].Clone()
	budget, err := validateSchemas(input, resourceBudget{nodes: MaxAggregateNodes - 18, fields: MaxAggregateFields - 4})
	if err != nil || budget.nodes != MaxAggregateNodes || budget.fields != MaxAggregateFields {
		t.Fatal("automatic storage budget", budget, err)
	}
	if _, err := validateSchemas(input, resourceBudget{nodes: MaxAggregateNodes - 17}); err == nil {
		t.Fatal("derived nodes bypassed")
	}
	if _, err := validateSchemas(input, resourceBudget{fields: MaxAggregateFields - 3}); err == nil {
		t.Fatal("derived fields bypassed")
	}
	if !reflect.DeepEqual(input[0], before) {
		t.Fatal("resource scan mutated source")
	}
	input[0].Models = append(input[0].Models, make([]ir.Model, MaxModelsPerApp-1)...)
	if err := ValidateSchemas(input); err == nil {
		t.Fatal("derived model limit bypass")
	}
	input[0] = before.Clone()
	input[0].Models[0].Fields = make([]ir.Field, MaxFieldsPerModel)
	if err := ValidateSchemas(input); err == nil {
		t.Fatal("combined field limit bypass")
	}
	for _, value := range []string{strings.Repeat("x", MaxSchemaStringBytes+1), string([]byte{0xff})} {
		input[0] = before.Clone()
		input[0].Models[0].ManyToMany[0].Target.AppLabel = value
		if err := ValidateSchemas(input); err == nil {
			t.Fatal("bad relation string accepted")
		}
	}
	input[0] = before.Clone()
	input[0].Models[0].GoName = strings.Repeat("X", MaxSchemaStringBytes)
	if err := ValidateSchemas(input); err == nil {
		t.Fatal("derived Go symbol too long")
	}
}
