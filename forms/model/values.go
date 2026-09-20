package model

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// InitialValues projects only the form's selected fields from a typed model
// reader. The model and its nullable pointers are never retained by the result.
func InitialValues[M any](model ir.Model, spec forms.Spec, value M, read func(M, ir.Field) (query.Value, bool)) (map[string]forms.Value, error) {
	if read == nil || len(spec.Fields()) == 0 {
		return nil, &Error{Path: "initial", Code: "invalid"}
	}
	byName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		if _, duplicate := byName[field.Name]; duplicate {
			return nil, &Error{Path: "initial", Code: "duplicate"}
		}
		byName[field.Name] = field
	}
	result := make(map[string]forms.Value, len(spec.Fields()))
	for _, field := range spec.Fields() {
		metadata, found := byName[field.Name()]
		if !found {
			return nil, &Error{Path: "initial." + field.Name(), Code: "unknown_field"}
		}
		scalar, found := read(value, metadata.Clone())
		if !found {
			return nil, &Error{Path: "initial." + field.Name(), Code: "missing_value"}
		}
		switch {
		case scalar.IsNull() && field.Nullable():
			result[field.Name()] = forms.Null()
		case scalar.Kind() == query.ValueFloat && field.Kind() == forms.FieldFloat:
			value, _ := scalar.Float()
			result[field.Name()] = forms.Float(value)
		case scalar.Kind() == query.ValueDuration && field.Kind() == forms.FieldDuration:
			value, _ := scalar.Duration()
			result[field.Name()] = forms.Duration(value)
		case scalar.Kind() == query.ValueTime && field.Kind() == forms.FieldTime:
			value, _ := scalar.Time()
			result[field.Name()] = forms.Time(value)
		case scalar.Kind() == query.ValueDate && field.Kind() == forms.FieldDate:
			value, _ := scalar.Date()
			result[field.Name()] = forms.Date(value)
		case scalar.Kind() == query.ValueDateTime && field.Kind() == forms.FieldDateTime:
			value, _ := scalar.DateTime()
			result[field.Name()] = forms.DateTime(value)
		case scalar.Kind() == query.ValueInteger && field.Kind() == forms.FieldInteger:
			integer, _ := scalar.Integer()
			result[field.Name()] = forms.Integer(integer)
		case scalar.Kind() == query.ValueString && field.Kind() == forms.FieldChar:
			text, _ := scalar.String()
			result[field.Name()] = forms.String(text)
		case scalar.Kind() == query.ValueBoolean && field.Kind() == forms.FieldBoolean:
			boolean, _ := scalar.Boolean()
			result[field.Name()] = forms.Boolean(boolean)
		default:
			return nil, &Error{Path: "initial." + field.Name(), Code: "type_mismatch"}
		}
	}
	if _, err := spec.Unbound(result); err != nil {
		return nil, err
	}
	return result, nil
}
