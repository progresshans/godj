package projectspec

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestSchemaResourcePerContainerAndStringBoundaries(t *testing.T) {
	field := ir.Field{Name: "name", GoName: "Name", Column: "name", Kind: ir.FieldChar, MaxLength: 1}
	model := ir.Model{Name: "entry", GoName: "Entry", DBTable: "app_entry", Fields: []ir.Field{field}}
	schema := ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "app", Models: []ir.Model{model}}

	modelsAtMaximum := make([]ir.Model, MaxModelsPerApp)
	if err := ValidateSchemas([]ir.Schema{{AppLabel: "app", Models: modelsAtMaximum}}); err != nil {
		t.Fatalf("maximum model count rejected: %v", err)
	}
	modelsAboveMaximum := make([]ir.Model, MaxModelsPerApp+1)
	assertResourceLimit(t, ValidateSchemas([]ir.Schema{{AppLabel: "app", Models: modelsAboveMaximum}}), "models_per_app")

	fieldsAtMaximum := make([]ir.Field, MaxFieldsPerModel)
	if err := ValidateSchemas([]ir.Schema{{AppLabel: "app", Models: []ir.Model{{Fields: fieldsAtMaximum}}}}); err != nil {
		t.Fatalf("maximum field count rejected: %v", err)
	}
	fieldsAboveMaximum := make([]ir.Field, MaxFieldsPerModel+1)
	assertResourceLimit(t, ValidateSchemas([]ir.Schema{{AppLabel: "app", Models: []ir.Model{{Fields: fieldsAboveMaximum}}}}), "fields_per_model")

	schema.Models[0].Fields[0].Default = &ir.ScalarDefault{Kind: ir.ScalarString, String: strings.Repeat("x", MaxSchemaStringBytes)}
	if err := ValidateSchemas([]ir.Schema{schema}); err != nil {
		t.Fatalf("maximum schema string rejected: %v", err)
	}
	schema.Models[0].Fields[0].Default.String += "x"
	assertResourceLimit(t, ValidateSchemas([]ir.Schema{schema}), "schema_string_bytes")
	schema.Models[0].Fields[0].Default.String = string([]byte{0xff})
	assertResourceLimit(t, ValidateSchemas([]ir.Schema{schema}), "schema_string_utf8")
}

func TestSchemaResourceAggregateBoundariesWithoutLargeAllocations(t *testing.T) {
	schema := ir.Schema{AppLabel: "app", Models: []ir.Model{{Fields: []ir.Field{{}}}}}
	if budget, err := validateSchemas([]ir.Schema{schema}, resourceBudget{
		fields: MaxAggregateFields - 1,
		nodes:  0,
	}); err != nil || budget.fields != MaxAggregateFields {
		t.Fatalf("maximum aggregate fields rejected: budget=%+v err=%v", budget, err)
	}
	_, err := validateSchemas([]ir.Schema{schema}, resourceBudget{
		fields: MaxAggregateFields,
		nodes:  0,
	})
	assertResourceLimit(t, err, "aggregate_fields")

	empty := ir.Schema{AppLabel: "app"}
	if budget, err := validateSchemas([]ir.Schema{empty}, resourceBudget{nodes: MaxAggregateNodes - 1}); err != nil || budget.nodes != MaxAggregateNodes {
		t.Fatalf("maximum aggregate nodes rejected: budget=%+v err=%v", budget, err)
	}
	_, err = validateSchemas([]ir.Schema{empty}, resourceBudget{nodes: MaxAggregateNodes})
	assertResourceLimit(t, err, "aggregate_schema_nodes")
}

func TestSchemaResourceCountsDefaultAndRelationNodesAndDoesNotMutate(t *testing.T) {
	schema := ir.Schema{AppLabel: "app", Models: []ir.Model{{Fields: []ir.Field{{
		Default: &ir.ScalarDefault{Kind: ir.ScalarString},
		Relation: &ir.ForeignKeyRelation{
			Target: ir.ModelIdentity{AppLabel: "target", ModelName: "entry"},
		},
	}}}}}
	budget, err := validateSchemas([]ir.Schema{schema}, resourceBudget{})
	if err != nil {
		t.Fatal(err)
	}
	if budget.fields != 1 || budget.nodes != 7 {
		t.Fatalf("resource budget = %+v, want fields=1 nodes=7", budget)
	}
	if schema.Models[0].Fields[0].Default == nil || schema.Models[0].Fields[0].Relation == nil {
		t.Fatal("resource scan mutated caller schema")
	}
}

func assertResourceLimit(t *testing.T, err error, limit string) {
	t.Helper()
	var resource *ResourceError
	if !errors.As(err, &resource) || resource.Limit != limit {
		t.Fatalf("resource error = %v, want limit %q", err, limit)
	}
}
