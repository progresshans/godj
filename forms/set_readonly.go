package forms

import "context"

// bindReadOnlyRow retains the server's already-resolved display values. It
// never evaluates editable field/cross validators against absent or forged
// input. Explicit formset controls have their own normal cleaning lifecycle.
func bindReadOnlyRow(ctx context.Context, spec Spec, submitted Data, initial Values, controls []Field) (Form, error) {
	if len(controls) == 0 {
		return Form{binding: &formBindingToken{}, submitted: submitted, bound: true,
			readOnly: true, valid: true, initial: initial, cleaned: initial}, nil
	}
	controlSpec, err := NewSpec(controls)
	if err != nil {
		return Form{}, err
	}
	controlInitial := make(map[string]Value, len(controls))
	for _, field := range controls {
		if value, present := initial.Get(field.name); present {
			controlInitial[field.name] = value
		}
	}
	control, err := controlSpec.Bind(ctx, submitted, controlInitial)
	if err != nil {
		return Form{}, err
	}
	values := make(map[string]Value, len(spec.fields))
	order := make([]string, 0, len(spec.fields))
	for _, field := range spec.fields {
		value, present := initial.Get(field.name)
		if _, utility := controlSpec.index[field.name]; utility {
			value, present = control.Cleaned().Get(field.name)
		}
		if present {
			values[field.name] = value
			order = append(order, field.name)
		}
	}
	return Form{binding: control.binding, submitted: submitted, bound: true,
		readOnly: true, valid: control.Valid(), errors: control.Errors(), initial: initial,
		cleaned: Values{order: order, values: values}, changed: control.Changed()}, nil
}
