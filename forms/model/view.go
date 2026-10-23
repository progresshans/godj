package model

import (
	"context"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// InitialView projects a source-bound read view into the form's selected
// initial values. Every selected model field must have been loaded; hidden
// fields do not fall back to defaults. It performs no I/O or input cleaning.
// The returned selection can be passed to Spec.Unbound for display.
func InitialView[M any](manager orm.Manager[M], spec forms.Spec, view orm.ModelView[M]) (map[string]forms.Value, error) {
	model, err := manager.Metadata()
	if err != nil {
		return nil, err
	}
	source, valid := view.Model()
	if !valid || !source.Equal(model) {
		return nil, &Error{Path: "model", Code: "mismatch"}
	}
	return InitialValues(model, spec, view, func(value orm.ModelView[M], field ir.Field) (query.Value, bool) {
		return value.ReadField(field)
	})
}

// BindView prepares edit intent from an explicitly selected read projection.
// It preserves the complete model source and original primary-key presence,
// including a stored zero key. Unloaded fields remain absent from Candidate;
// their defaults/empty values never impersonate current stored data. Model
// clean/validators must check presence for fields outside this view. Final
// current-state validation and writes still belong to the authorized writer.
// A collection requires its own complete current selection and is not loaded
// by this scalar adapter.
func BindView[M any](ctx context.Context, manager orm.Manager[M], spec forms.Spec, data forms.Data, view orm.ModelView[M], postClean PostClean) (BoundForm, error) {
	if _, err := InitialView(manager, spec, view); err != nil {
		return BoundForm{}, err
	}
	model, _ := view.Model()
	initial := make(map[string]forms.Value, len(view.Fields())+1)
	for name, scalar := range view.Values() {
		value, valid := formValue(scalar)
		if !valid {
			return BoundForm{}, &Error{Path: "initial." + name, Code: "type_mismatch"}
		}
		initial[name] = value
	}
	key, present := view.PrimaryKey()
	if !present {
		key = query.Null()
	}
	for _, field := range model.Fields {
		if field.PrimaryKey {
			value, valid := formValue(key)
			if !valid {
				return BoundForm{}, &Error{Path: "initial." + field.Name, Code: "type_mismatch"}
			}
			initial[field.Name] = value
		}
	}
	binding, formInitial, err := prepareModelBinding(model, spec.Fields(), initial, postClean)
	if err != nil {
		return BoundForm{}, err
	}
	for name := range binding.candidate {
		if _, loaded := initial[name]; !loaded {
			delete(binding.candidate, name)
		}
	}
	form, err := spec.Bind(ctx, data, formInitial)
	if err != nil {
		return BoundForm{}, err
	}
	return binding.finish(form)
}
