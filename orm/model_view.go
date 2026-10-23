package orm

import (
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ModelView owns an explicit scalar selection and the complete model source
// from which it was projected. It contains neither the original model nor
// unselected field values. It is read data, never mutation or read authority.
// Primary-key presence is retained separately for an instance-bound form;
// it does not make the key an automatically selected output field.
type ModelView[M any] struct {
	model   ir.Model
	values  map[string]query.Value
	fields  []string
	key     query.Value
	present bool
	valid   bool
	_       [0]func(M)
}

func (ModelView[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("orm.ModelView{redacted}"))
}

// View projects an already-authorized model without I/O. Selection is explicit;
// an empty selection is a valid key-only view, distinct from the invalid zero
// value. Unknown, repeated or collection fields are rejected. No input policy,
// default, normalization or model validator is applied to stored values.
func (manager Manager[M]) View(value M, fields ...string) (ModelView[M], error) {
	if manager.prepared == nil {
		return ModelView[M]{}, invalidWritePlan("model view requires a model source")
	}
	selected := make(map[string]bool, len(fields))
	for _, name := range fields {
		if _, exists := manager.prepared.byName[name]; !exists || selected[name] {
			return ModelView[M]{}, invalidModelValue(name, "model view field is unknown or repeated")
		}
		selected[name] = true
	}
	values, err := manager.ModelValues(value)
	if err != nil {
		return ModelView[M]{}, err
	}
	key := values[manager.prepared.primaryKey.Name]
	view := ModelView[M]{model: manager.prepared.metadata, fields: slices.Clone(fields),
		values: make(map[string]query.Value, len(fields)), key: key, present: !key.IsNull(), valid: true}
	for _, name := range fields {
		view.values[name] = values[name]
	}
	return view, nil
}

func (view ModelView[M]) Model() (ir.Model, bool) {
	if !view.valid {
		return ir.Model{}, false
	}
	return view.model.Clone(), true
}

func (view ModelView[M]) Fields() []string { return slices.Clone(view.fields) }

// Values returns only the selected immutable scalars in an owned map. It never
// substitutes an unselected field's zero/default value for a missing value.
func (view ModelView[M]) Values() map[string]query.Value { return maps.Clone(view.values) }

func (view ModelView[M]) Get(name string) (query.Value, bool) {
	value, present := view.values[name]
	return value, view.valid && present
}

// PrimaryKey distinguishes an explicitly stored zero from an unsaved instance.
// For a valid unsaved view it returns SQL NULL and false.
func (view ModelView[M]) PrimaryKey() (query.Value, bool) {
	return view.key, view.valid && view.present
}

// ReadField can be used by model serializers and form projections after their
// complete model source has been checked. A detached field must still match
// this source, and an unselected field never becomes readable implicitly.
func (view ModelView[M]) ReadField(field ir.Field) (query.Value, bool) {
	value, present := view.Get(field.Name)
	if !present {
		return query.Value{}, false
	}
	for _, owned := range view.model.Fields {
		if owned.Name == field.Name {
			return value, owned.Equal(field)
		}
	}
	return query.Value{}, false
}
