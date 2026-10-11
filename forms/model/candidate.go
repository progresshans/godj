package model

import (
	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/query"
)

// ValidationValues snapshots selected scalar model values after the currently
// applied errors and exclusions. Recompute it after each validation stage; an
// earlier snapshot intentionally retains its earlier membership. Collections
// and command-only fields never enter database model uniqueness checks.
func (bound BoundForm) ValidationValues() (map[string]query.Value, error) {
	if !bound.form.Bound() {
		return nil, &Error{Path: "form", Code: "unbound"}
	}
	excluded := make(map[string]bool)
	for _, name := range bound.Excluded() {
		excluded[name] = true
	}
	values := make(map[string]query.Value, len(bound.model.Fields))
	for _, field := range bound.model.Fields {
		if excluded[field.Name] {
			continue
		}
		value, present := bound.candidate.Get(field.Name)
		if !present {
			return nil, &Error{Path: "candidate." + field.Name, Code: "missing_value"}
		}
		scalar, valid := queryValue(value)
		if !valid {
			return nil, &Error{Path: "candidate." + field.Name, Code: "type_mismatch"}
		}
		values[field.Name] = scalar
	}
	return values, nil
}

func queryValue(value forms.Value) (query.Value, bool) {
	switch value.Kind() {
	case forms.ValueNull:
		return query.Null(), true
	case forms.ValueString:
		v, ok := value.AsString()
		return query.String(v), ok
	case forms.ValueBoolean:
		v, ok := value.AsBoolean()
		return query.Boolean(v), ok
	case forms.ValueInteger:
		v, ok := value.AsInteger()
		return query.Integer(v), ok
	case forms.ValueDateTime:
		v, ok := value.AsDateTime()
		return query.DateTime(v), ok
	case forms.ValueDate:
		v, ok := value.AsDate()
		return query.Date(v), ok
	case forms.ValueTime:
		v, ok := value.AsTime()
		return query.Time(v), ok
	case forms.ValueDuration:
		v, ok := value.AsDuration()
		return query.Duration(v), ok
	case forms.ValueFloat:
		v, ok := value.AsFloat()
		return query.Float(v), ok
	case forms.ValueDecimal:
		v, ok := value.AsDecimal()
		return query.Decimal(v), ok
	case forms.ValueBinary:
		v, ok := value.AsBinary()
		return query.Binary(v), ok
	case forms.ValueUUID:
		v, ok := value.AsUUID()
		return query.UUID(v), ok
	case forms.ValueJSON:
		v, ok := value.AsJSON()
		return query.JSON(v), ok
	default:
		return query.Value{}, false
	}
}
