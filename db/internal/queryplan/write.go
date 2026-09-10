package queryplan

import (
	"fmt"

	"github.com/progresshans/godj/query"
)

// Assignments preserves quote, duplicate-column, value-validation and database
// conversion order. Dialects own identifier equivalence and extra validation;
// a nil columnKey means quoted column names are compared exactly.
func Assignments(assignments []query.Assignment, validateValue func(query.FieldRef, query.Value) error, quoteIdentifier func(string) (string, error), columnKey func(string) string) ([]string, []any, error) {
	columns := make([]string, len(assignments))
	arguments := make([]any, len(assignments))
	seen := make(map[string]struct{}, len(assignments))
	for index, assignment := range assignments {
		field := assignment.Field()
		column, err := quoteIdentifier(field.Column())
		if err != nil {
			return nil, nil, err
		}
		key := field.Column()
		if columnKey != nil {
			key = columnKey(key)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, invalidPlan(fmt.Sprintf("field column %q is assigned more than once", field.Column()))
		}
		seen[key] = struct{}{}
		if err := validateValue(field, assignment.Value()); err != nil {
			return nil, nil, err
		}
		argument, err := assignment.Value().DatabaseValue()
		if err != nil {
			return nil, nil, err
		}
		columns[index], arguments[index] = column, argument
	}
	return columns, arguments, nil
}

func Key(field query.FieldRef, value query.Value, validateValue func(query.FieldRef, query.Value) error, quoteIdentifier func(string) (string, error)) (string, any, error) {
	if field.Nullable() || value.IsNull() {
		return "", nil, invalidPlan("mutation key cannot be nullable or NULL")
	}
	if err := validateValue(field, value); err != nil {
		return "", nil, err
	}
	column, err := quoteIdentifier(field.Column())
	if err != nil {
		return "", nil, err
	}
	argument, err := value.DatabaseValue()
	if err != nil {
		return "", nil, err
	}
	return column, argument, nil
}

func WriteValue(field query.FieldRef, value query.Value) error {
	if value.IsNull() {
		if !field.Nullable() {
			return invalidPlan(fmt.Sprintf("non-null field %q cannot be assigned NULL", field.Name()))
		}
		return nil
	}
	if !ValueMatchesField(value.Kind(), field.Kind()) {
		return invalidPlan(fmt.Sprintf("value kind %q does not match field %q", value.Kind(), field.Name()))
	}
	return nil
}
