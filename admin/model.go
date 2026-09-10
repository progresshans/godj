package admin

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/templates"
)

// ModelProjector owns a resolved field allowlist and typed scalar reader.
// Row projection never discovers fields or rebuilds the metadata index.
type ModelProjector[M any] struct {
	fields      []ir.Field
	fieldByName map[string]ir.Field
	read        func(M, ir.Field) (query.Value, bool)
}

func NewModelProjector[M any](model ir.Model, read func(M, ir.Field) (query.Value, bool), names ...string) (ModelProjector[M], error) {
	if read == nil || len(names) == 0 {
		return ModelProjector[M]{}, &ConfigError{Path: "snapshot.fields", Code: "missing"}
	}
	byName := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		if _, duplicate := byName[field.Name]; duplicate {
			return ModelProjector[M]{}, &ConfigError{Path: "snapshot.fields", Code: "duplicate"}
		}
		byName[field.Name] = field
	}
	selectedByName := make(map[string]ir.Field, len(names))
	selected := make([]ir.Field, 0, len(names))
	for _, name := range names {
		field, found := byName[name]
		if !found {
			return ModelProjector[M]{}, &ConfigError{Path: "snapshot." + name, Code: "unknown_field"}
		}
		if _, duplicate := selectedByName[name]; duplicate {
			return ModelProjector[M]{}, &ConfigError{Path: "snapshot." + name, Code: "duplicate"}
		}
		owned := field.Clone()
		selected = append(selected, owned)
		selectedByName[name] = owned
	}
	return ModelProjector[M]{fields: selected, fieldByName: selectedByName, read: read}, nil
}

// Project returns a validated snapshot without retaining the model. The reader
// receives a detached field and cannot mutate the prepared allowlist.
func (projector ModelProjector[M]) Project(value M, id int64, label string) (Object, error) {
	if projector.read == nil {
		return Object{}, &ConfigError{Path: "snapshot.fields", Code: "missing"}
	}
	values := make(map[string]templates.Value, len(projector.fields))
	for _, field := range projector.fields {
		name := field.Name
		scalar, found := projector.read(value, field.Clone())
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
	}
	object, err := NewObject(id, label, values)
	if err != nil {
		return Object{}, err
	}
	if err := validateObject(object, projector.fieldByName); err != nil {
		return Object{}, err
	}
	return object, nil
}
