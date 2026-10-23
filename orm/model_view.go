package orm

import (
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ModelView owns an explicit value selection and the complete model source
// from which it was projected. It contains neither the original model nor
// unselected field values. It is read data, never mutation or read authority.
// Primary-key presence is retained separately for an instance-bound form;
// it does not make the key an automatically selected output field.
type ModelView[M any] struct {
	state *modelViewState
	_     [0]func(M)
}

// An opaque state also protects selected data when a malformed diagnostic
// verb bypasses Format and recursively reflects the public value.
type modelViewState struct {
	model       ir.Model
	values      map[string]query.Value
	fields      []string
	collections map[string][]int64
	key         query.Value
	present     bool
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
	view := ModelView[M]{state: &modelViewState{model: manager.prepared.metadata, fields: slices.Clone(fields),
		values: make(map[string]query.Value, len(fields)), key: key, present: !key.IsNull()}}
	for _, name := range fields {
		view.state.values[name] = values[name]
	}
	return view, nil
}

func (view ModelView[M]) Model() (ir.Model, bool) {
	if view.state == nil {
		return ir.Model{}, false
	}
	return view.state.model.Clone(), true
}

// Fields returns only the selected scalar names, in projection order.
func (view ModelView[M]) Fields() []string {
	if view.state == nil {
		return nil
	}
	return slices.Clone(view.state.fields)
}

// Values returns only the selected immutable scalars in an owned map. It never
// substitutes an unselected field's zero/default value for a missing value.
func (view ModelView[M]) Values() map[string]query.Value {
	if view.state == nil {
		return nil
	}
	return maps.Clone(view.state.values)
}

func (view ModelView[M]) Get(name string) (query.Value, bool) {
	if view.state == nil {
		return query.Value{}, false
	}
	value, present := view.state.values[name]
	return value, present
}

// PrimaryKey distinguishes an explicitly stored zero from an unsaved instance.
// For a valid unsaved view it returns SQL NULL and false.
func (view ModelView[M]) PrimaryKey() (query.Value, bool) {
	if view.state == nil {
		return query.Value{}, false
	}
	return view.state.key, view.state.present
}

// ReadField can be used by model serializers and form projections after their
// complete model source has been checked. A detached field must still match
// this source, and an unselected field never becomes readable implicitly.
func (view ModelView[M]) ReadField(field ir.Field) (query.Value, bool) {
	value, present := view.Get(field.Name)
	if !present {
		return query.Value{}, false
	}
	for _, owned := range view.state.model.Fields {
		if owned.Name == field.Name {
			return value, owned.Equal(field)
		}
	}
	return query.Value{}, false
}

// WithCollections returns a new view with explicitly loaded collection keys.
// It performs no I/O, normalization or authority check. The caller must have
// read complete authorized selections in the same scope as the scalar row.
// An absent field stays absent, while a present nil slice means loaded empty.
// Stored order and duplicates are preserved; current choice validation and
// membership limits belong to the reader/writer, not to this data projection.
func (view ModelView[M]) WithCollections(collections map[string][]int64) (ModelView[M], error) {
	if view.state == nil {
		return ModelView[M]{}, invalidWritePlan("model view is zero or invalid")
	}
	known := make(map[string]bool, len(view.state.model.ManyToMany))
	for _, field := range view.state.model.ManyToMany {
		known[field.Name] = true
	}
	names := make([]string, 0, len(collections))
	for name := range collections {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !known[name] {
			return ModelView[M]{}, invalidModelValue(name, "model view collection is unknown")
		}
	}
	state := *view.state
	state.collections = view.Collections()
	for _, name := range names {
		state.collections[name] = slices.Clone(collections[name])
	}
	return ModelView[M]{state: &state}, nil
}

func (view ModelView[M]) Collection(name string) ([]int64, bool) {
	if view.state == nil {
		return nil, false
	}
	keys, present := view.state.collections[name]
	return slices.Clone(keys), present
}

func (view ModelView[M]) Collections() map[string][]int64 {
	if view.state == nil {
		return nil
	}
	result := make(map[string][]int64, len(view.state.collections))
	for name, keys := range view.state.collections {
		result[name] = slices.Clone(keys)
	}
	return result
}

// ReadCollection applies the same exact declaration check as ReadField.
func (view ModelView[M]) ReadCollection(field ir.ManyToManyField) ([]int64, bool) {
	if view.state == nil {
		return nil, false
	}
	for _, owned := range view.state.model.ManyToMany {
		if owned.Name == field.Name && owned.Equal(field) {
			return view.Collection(field.Name)
		}
	}
	return nil, false
}
