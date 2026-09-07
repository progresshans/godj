package queryplan

import (
	"fmt"

	"github.com/progresshans/godj/query"
)

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
