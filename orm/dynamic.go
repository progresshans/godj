package orm

import (
	"fmt"
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"strings"
	"time"

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
		var condition query.Condition
		var err error
		if lookup == query.LookupIn {
			values, valueErr := dynamicMembership(field, input.Value)
			if valueErr != nil {
				return nil, valueErr
			}
			condition, err = query.NewInCondition(fieldReference(field), values)
		} else {
			value, valueErr := dynamicValue(field, lookup, input.Value)
			if valueErr != nil {
				return nil, valueErr
			}
			condition = query.NewCondition(fieldReference(field), lookup, value)
		}
		predicate := predicateFromCondition[M](condition, err)
		if predicate.err != nil {
			return nil, predicate.err
		}
		result = append(result, predicate)
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
	case query.LookupIn:
		return lookup, field.Kind == ir.FieldAuto || field.Kind == ir.FieldInteger || field.Kind == ir.FieldChar || field.Kind == ir.FieldText || field.Kind == ir.FieldBoolean || field.Kind == ir.FieldDateTime || field.Kind == ir.FieldDate || (field.Kind == ir.FieldTime || field.Kind == ir.FieldDuration || field.Kind == ir.FieldFloat || field.Kind == ir.FieldDecimal)
	case query.LookupExact:
		return lookup, true
	case query.LookupGreaterThan, query.LookupGreaterThanOrEqual, query.LookupLessThan, query.LookupLessThanOrEqual:
		return lookup, field.Kind == ir.FieldAuto || field.Kind == ir.FieldInteger || field.Kind == ir.FieldDateTime || field.Kind == ir.FieldDate || (field.Kind == ir.FieldTime || field.Kind == ir.FieldDuration || field.Kind == ir.FieldFloat || field.Kind == ir.FieldDecimal) || field.Kind == ir.FieldChar || field.Kind == ir.FieldText
	case query.LookupIsNull:
		return lookup, true
	case query.LookupIContains:
		return lookup, field.Kind == ir.FieldChar || field.Kind == ir.FieldText
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
	case ir.FieldAuto, ir.FieldInteger:
		switch value := raw.(type) {
		case int:
			return query.Integer(int64(value)), nil
		case int64:
			return query.Integer(value), nil
		default:
			return invalid("int or int64")
		}
	case ir.FieldDecimal:
		value, ok := raw.(decimal.Decimal)
		if !ok || !value.Valid() {
			return invalid("valid decimal.Decimal")
		}
		return query.Decimal(value), nil
	case ir.FieldFloat:
		value, ok := raw.(float64)
		if !ok {
			return invalid("float64")
		}
		return query.Float(value), nil
	case ir.FieldDuration:
		value, ok := raw.(duration.Duration)
		if !ok || !value.Valid() {
			return invalid("valid duration.Duration")
		}
		return query.Duration(value), nil
	case ir.FieldTime:
		value, ok := raw.(clock.Time)
		if !ok || !value.Valid() {
			return invalid("valid clock.Time")
		}
		return query.Time(value), nil
	case ir.FieldDate:
		value, ok := raw.(calendar.Date)
		if !ok || !value.Valid() {
			return invalid("valid calendar.Date")
		}
		return query.Date(value), nil
	case ir.FieldDateTime:
		value, ok := raw.(time.Time)
		if !ok {
			return invalid("time.Time")
		}
		result := query.DateTime(value)
		if result.Kind() != query.ValueDateTime {
			return invalid("time.Time in UTC years 1 through 9999")
		}
		return result, nil
	case ir.FieldChar, ir.FieldText:
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
