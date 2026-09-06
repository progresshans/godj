package admin

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

// ModelObject converts an explicit field allowlist using a typed scalar reader
// such as a generated descriptor's WriteFieldValue method. It does not retain
// the model, invoke model methods by reflection, or infer exposed fields.
func ModelObject[M any](model ir.Model, value M, read func(M, ir.Field) (query.Value, bool), id int64, label string, names ...string) (Object, error) {
	if read == nil || len(names) == 0 {
		return Object{}, &ConfigError{Path: "snapshot.fields", Code: "missing"}
	}
	byName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		if _, duplicate := byName[field.Name]; duplicate {
			return Object{}, &ConfigError{Path: "snapshot.fields", Code: "duplicate"}
		}
		byName[field.Name] = field
	}
	values := make(map[string]templates.Value, len(names))
	selected := make([]ir.Field, 0, len(names))
	for _, name := range names {
		field, found := byName[name]
		if !found {
			return Object{}, &ConfigError{Path: "snapshot." + name, Code: "unknown_field"}
		}
		if _, duplicate := values[name]; duplicate {
			return Object{}, &ConfigError{Path: "snapshot." + name, Code: "duplicate"}
		}
		scalar, found := read(value, field.Clone())
		if !found {
			return Object{}, &ConfigError{Path: "snapshot." + name, Code: "missing_value"}
		}
		var converted templates.Value
		switch scalar.Kind() {
		case query.ValueNull:
			converted = templates.Null()
		case query.ValueString:
			text, _ := scalar.String()
			converted = templates.String(text)
		case query.ValueBoolean:
			boolean, _ := scalar.Boolean()
			converted = templates.Bool(boolean)
		case query.ValueInteger:
			integer, _ := scalar.Integer()
			converted = templates.Integer(integer)
		default:
			return Object{}, &ConfigError{Path: "snapshot." + name, Code: "invalid_value"}
		}
		values[name] = converted
		selected = append(selected, field)
	}
	object, err := NewObject(id, label, values)
	if err != nil {
		return Object{}, err
	}
	if err := validateObject(object, selected); err != nil {
		return Object{}, err
	}
	return object, nil
}
