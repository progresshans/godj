package model

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ValidateInitialValues checks the representation of supplied model values,
// including fields excluded from input. It does not run input validators or
// grant permission to select, bind or persist any field. Current input length,
// choices and editability do not prevent displaying an existing stored value.
func ValidateInitialValues(model ir.Model, values map[string]forms.Value) error {
	known := make(map[string]bool, len(model.Fields)+len(model.ManyToMany))
	fields := make([]forms.Field, 0, len(values))
	for _, field := range model.Fields {
		if known[field.Name] {
			return &Error{Path: "initial." + field.Name, Code: "duplicate"}
		}
		known[field.Name] = true
		if _, found := values[field.Name]; !found {
			continue
		}
		// Only a provided stored value is being checked. A default belongs to
		// preparing new input and must not be applied or revalidated here.
		field.Default = nil
		var projected forms.Field
		var err error
		if field.Kind == ir.FieldAuto {
			projected, err = forms.IntegerField(field.Name)
		} else {
			projected, err = projectField(field, overrideConfig{})
		}
		if err != nil {
			return err
		}
		fields = append(fields, projected)
	}
	for _, field := range model.ManyToMany {
		if known[field.Name] {
			return &Error{Path: "initial." + field.Name, Code: "duplicate"}
		}
		known[field.Name] = true
		if _, found := values[field.Name]; !found {
			continue
		}
		projected, err := projectManyToMany(field, overrideConfig{})
		if err != nil {
			return err
		}
		fields = append(fields, projected)
	}
	for name := range values {
		if !known[name] {
			return &Error{Path: "initial." + name, Code: "unknown_field"}
		}
	}
	if len(fields) == 0 {
		return nil
	}
	spec, err := forms.NewSpec(fields)
	if err != nil {
		return err
	}
	_, err = spec.Unbound(values)
	return err
}

// InitialValues projects only the form's selected fields from a typed model
// reader. The model and its nullable pointers are never retained by the result.
// A selected ManyToMany requires one explicit, pure collection reader. Load
// relations with a context before projection; this callback must not hide I/O.
func InitialValues[M any](model ir.Model, spec forms.Spec, value M, read func(M, ir.Field) (query.Value, bool), related ...func(M, ir.ManyToManyField) ([]int64, bool)) (map[string]forms.Value, error) {
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
	if len(related) > 1 || len(related) == 1 && related[0] == nil {
		return nil, &Error{Path: "initial.relations", Code: "invalid"}
	}
	many := make(map[string]ir.ManyToManyField, len(model.ManyToMany))
	for _, field := range model.ManyToMany {
		if _, exists := byName[field.Name]; exists {
			return nil, &Error{Path: "initial", Code: "duplicate"}
		}
		if _, exists := many[field.Name]; exists {
			return nil, &Error{Path: "initial", Code: "duplicate"}
		}
		many[field.Name] = field.Clone()
	}
	result := make(map[string]forms.Value, len(spec.Fields()))
	for _, field := range spec.Fields() {
		if metadata, found := many[field.Name()]; found {
			if field.Kind() != forms.FieldIntegerList || len(related) != 1 {
				return nil, &Error{Path: "initial." + field.Name(), Code: "missing_relation_reader"}
			}
			keys, present := related[0](value, metadata.Clone())
			if !present {
				return nil, &Error{Path: "initial." + field.Name(), Code: "missing_value"}
			}
			result[field.Name()] = forms.Integers(keys...)
			continue
		}
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
		case scalar.Kind() == query.ValueString && field.IsFile():
			text, _ := scalar.String()
			value, err := forms.ExistingFile(text)
			if err != nil {
				return nil, err
			}
			result[field.Name()] = value
		case scalar.Kind() == query.ValueJSON && field.Kind() == forms.FieldJSON:
			document, _ := scalar.JSON()
			result[field.Name()] = forms.JSON(document)
		case scalar.Kind() == query.ValueBinary && field.Kind() == forms.FieldBinary:
			value, _ := scalar.Binary()
			result[field.Name()] = forms.Binary(value)
		case scalar.Kind() == query.ValueUUID && field.Kind() == forms.FieldUUID:
			identifier, _ := scalar.UUID()
			result[field.Name()] = forms.UUID(identifier)
		case scalar.Kind() == query.ValueDecimal && field.Kind() == forms.FieldDecimal:
			number, _ := scalar.Decimal()
			result[field.Name()] = forms.Decimal(number)
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
		case scalar.Kind() == query.ValueString && (field.Kind() == forms.FieldChar || field.Kind() == forms.FieldEmail || field.Kind() == forms.FieldURL || field.Kind() == forms.FieldSlug):
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
