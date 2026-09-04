// Package projectspec owns the shared, allocation-before-clone resource
// boundary for whole-project schema generation and its private wire protocol.
package projectspec

import (
	"fmt"
	"unicode/utf8"

	"github.com/progresshans/godj/schema/ir"
)

const (
	MaxModelsPerApp      = 2_048
	MaxFieldsPerModel    = 2_048
	MaxAggregateFields   = 262_144
	MaxAggregateNodes    = 262_144
	MaxSchemaStringBytes = 4_096
)

// ResourceError identifies one deterministic schema resource boundary.
type ResourceError struct {
	Path    string
	Limit   string
	Maximum uint64
	Actual  uint64
}

func (err *ResourceError) Error() string {
	if err == nil {
		return "project schema resource limit"
	}
	return fmt.Sprintf(
		"project schema %s exceeds %s: got %d, maximum %d",
		err.Path,
		err.Limit,
		err.Actual,
		err.Maximum,
	)
}

type resourceBudget struct {
	fields uint64
	nodes  uint64
}

// ValidateSchemas scans caller-owned schemas without cloning, normalizing,
// sorting or retaining them. Callers must invoke it before any operation that
// allocates proportionally to model or field counts.
func ValidateSchemas(schemas []ir.Schema) error {
	_, err := validateSchemas(schemas, resourceBudget{})
	return err
}

func validateSchemas(schemas []ir.Schema, budget resourceBudget) (resourceBudget, error) {
	for appIndex := range schemas {
		appPath := fmt.Sprintf("apps[%d].schema", appIndex)
		schema := schemas[appIndex]
		if err := consumeNodes(&budget, appPath, 1); err != nil {
			return budget, err
		}
		if err := validateString(appPath+".app_label", schema.AppLabel); err != nil {
			return budget, err
		}
		if len(schema.Models) > MaxModelsPerApp {
			return budget, resourceError(appPath+".models", "models_per_app", MaxModelsPerApp, len(schema.Models))
		}
		for modelIndex := range schema.Models {
			modelPath := fmt.Sprintf("%s.models[%d]", appPath, modelIndex)
			model := schema.Models[modelIndex]
			if err := consumeNodes(&budget, modelPath, 1); err != nil {
				return budget, err
			}
			for _, value := range []struct {
				path  string
				value string
			}{
				{path: modelPath + ".name", value: model.Name},
				{path: modelPath + ".go_name", value: model.GoName},
				{path: modelPath + ".db_table", value: model.DBTable},
			} {
				if err := validateString(value.path, value.value); err != nil {
					return budget, err
				}
			}
			if len(model.Fields) > MaxFieldsPerModel {
				return budget, resourceError(modelPath+".fields", "fields_per_model", MaxFieldsPerModel, len(model.Fields))
			}
			if err := consumeFields(&budget, modelPath+".fields", uint64(len(model.Fields))); err != nil {
				return budget, err
			}
			if err := consumeNodes(&budget, modelPath+".fields", uint64(len(model.Fields))); err != nil {
				return budget, err
			}
			for fieldIndex := range model.Fields {
				fieldPath := fmt.Sprintf("%s.fields[%d]", modelPath, fieldIndex)
				field := model.Fields[fieldIndex]
				for _, value := range []struct {
					path  string
					value string
				}{
					{path: fieldPath + ".name", value: field.Name},
					{path: fieldPath + ".go_name", value: field.GoName},
					{path: fieldPath + ".column", value: field.Column},
					{path: fieldPath + ".kind", value: string(field.Kind)},
				} {
					if err := validateString(value.path, value.value); err != nil {
						return budget, err
					}
				}
				if field.Default != nil {
					if err := consumeNodes(&budget, fieldPath+".default", 1); err != nil {
						return budget, err
					}
					if err := validateString(fieldPath+".default.kind", string(field.Default.Kind)); err != nil {
						return budget, err
					}
					if err := validateString(fieldPath+".default.string", field.Default.String); err != nil {
						return budget, err
					}
				}
				if field.Relation != nil {
					if err := consumeNodes(&budget, fieldPath+".relation", 3); err != nil {
						return budget, err
					}
					for _, value := range []struct {
						path  string
						value string
					}{
						{path: fieldPath + ".relation.target.app_label", value: field.Relation.Target.AppLabel},
						{path: fieldPath + ".relation.target.model_name", value: field.Relation.Target.ModelName},
						{path: fieldPath + ".relation.cardinality", value: string(field.Relation.Cardinality)},
						{path: fieldPath + ".relation.reverse.name", value: field.Relation.Reverse.Name},
						{path: fieldPath + ".relation.on_delete", value: string(field.Relation.OnDelete)},
					} {
						if err := validateString(value.path, value.value); err != nil {
							return budget, err
						}
					}
				}
			}
		}
	}
	return budget, nil
}

func consumeFields(budget *resourceBudget, path string, count uint64) error {
	if budget.fields > MaxAggregateFields || count > MaxAggregateFields-budget.fields {
		return &ResourceError{
			Path: path, Limit: "aggregate_fields", Maximum: MaxAggregateFields,
			Actual: saturatedAdd(budget.fields, count),
		}
	}
	budget.fields += count
	return nil
}

func consumeNodes(budget *resourceBudget, path string, count uint64) error {
	if budget.nodes > MaxAggregateNodes || count > MaxAggregateNodes-budget.nodes {
		return &ResourceError{
			Path: path, Limit: "aggregate_schema_nodes", Maximum: MaxAggregateNodes,
			Actual: saturatedAdd(budget.nodes, count),
		}
	}
	budget.nodes += count
	return nil
}

func validateString(path, value string) error {
	if !utf8.ValidString(value) {
		return &ResourceError{Path: path, Limit: "schema_string_utf8", Maximum: MaxSchemaStringBytes, Actual: uint64(len(value))}
	}
	if len(value) > MaxSchemaStringBytes {
		return resourceError(path, "schema_string_bytes", MaxSchemaStringBytes, len(value))
	}
	return nil
}

func resourceError(path, limit string, maximum, actual int) *ResourceError {
	return &ResourceError{Path: path, Limit: limit, Maximum: uint64(maximum), Actual: uint64(actual)}
}

func saturatedAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}
