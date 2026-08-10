package orm

import (
	"fmt"
	"strings"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type LookupInput struct {
	Key   string
	Value any
}

// LookupPolicy returns whether an otherwise supported field lookup may be
// accepted at a dynamic boundary. A nil policy allows all M1 lookups.
type LookupPolicy func(ir.Field, query.Lookup) bool

func ParseDynamic[M any](descriptor ModelDescriptor[M], policy LookupPolicy, inputs []LookupInput) ([]Predicate[M], error) {
	if descriptorIsNil(descriptor) {
		return nil, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "descriptor is nil"}
	}
	metadata := descriptor.Metadata()
	result := make([]Predicate[M], 0, len(inputs))
	for _, input := range inputs {
		fieldName, lookupName := splitLookup(input.Key)
		field, ok := findField(metadata.Fields, fieldName)
		if !ok {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnknownField,
				Field:    fieldName,
				Detail:   "field is not present in model metadata",
			}
		}
		if field.Kind == ir.FieldForeignKey || field.Relation != nil {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnsupportedLookup,
				Field:    field.Name,
				Lookup:   lookupName,
				Detail:   "relation fields require the project-bound dynamic relation API",
			}
		}
		lookup, ok := supportedLookup(field, lookupName)
		if !ok {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeUnsupportedLookup,
				Field:    field.Name,
				Lookup:   lookupName,
				Detail:   "lookup is not supported for this field kind",
			}
		}
		if policy != nil && !policy(field, lookup) {
			return nil, &query.Error{
				Category: query.CategoryField,
				Code:     query.CodeDisallowedLookup,
				Field:    field.Name,
				Lookup:   string(lookup),
				Detail:   "lookup was rejected by policy",
			}
		}
		value, err := dynamicValue(field, lookup, input.Value)
		if err != nil {
			return nil, err
		}
		result = append(result, Predicate[M]{condition: query.NewCondition(fieldReference(field), lookup, value)})
	}
	return result, nil
}

func splitLookup(key string) (string, string) {
	parts := strings.Split(key, "__")
	if len(parts) == 1 {
		return key, string(query.LookupExact)
	}
	return parts[0], strings.Join(parts[1:], "__")
}

func findField(fields []ir.Field, name string) (ir.Field, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return ir.Field{}, false
}

func supportedLookup(field ir.Field, name string) (query.Lookup, bool) {
	lookup := query.Lookup(name)
	switch lookup {
	case query.LookupExact:
		return lookup, true
	case query.LookupIsNull:
		return lookup, true
	case query.LookupIContains:
		return lookup, field.Kind == ir.FieldChar
	default:
		return "", false
	}
}

func dynamicValue(field ir.Field, lookup query.Lookup, raw any) (query.Value, error) {
	invalid := func(expected string) (query.Value, error) {
		return query.Value{}, &query.Error{
			Category: query.CategoryField,
			Code:     query.CodeInvalidValue,
			Field:    field.Name,
			Lookup:   string(lookup),
			Detail:   fmt.Sprintf("expected %s, got %T", expected, raw),
		}
	}
	if lookup == query.LookupIsNull {
		value, ok := raw.(bool)
		if !ok {
			return invalid("bool")
		}
		return query.Boolean(value), nil
	}
	switch field.Kind {
	case ir.FieldAuto:
		switch value := raw.(type) {
		case int:
			return query.Integer(int64(value)), nil
		case int64:
			return query.Integer(value), nil
		default:
			return invalid("int or int64")
		}
	case ir.FieldChar:
		value, ok := raw.(string)
		if !ok {
			return invalid("string")
		}
		return query.String(value), nil
	case ir.FieldBoolean:
		value, ok := raw.(bool)
		if !ok {
			return invalid("bool")
		}
		return query.Boolean(value), nil
	default:
		return invalid("supported field value")
	}
}
