package forms

import "github.com/progresshans/godj/validation"

// WithErrors returns a new bound form with additional post-clean diagnostics.
// Fields with new errors leave cleaned data; non-field errors retain it. Initial
// values and change detection remain available, and the caller can redisplay
// its original submitted data. Unknown fields and unbound forms fail explicitly.
// This operation performs no validation callbacks or persistence.
func (f Form) WithErrors(diagnostics validation.Errors) (Form, error) {
	if !f.bound {
		return Form{}, &ConfigError{Path: "form", Code: "unbound"}
	}
	for _, diagnostic := range diagnostics.All() {
		if diagnostic.Field() == validation.NonField {
			continue
		}
		if _, known := f.initial.Get(string(diagnostic.Field())); !known {
			return Form{}, &ConfigError{Path: "errors." + string(diagnostic.Field()), Code: "unknown_field"}
		}
	}
	if diagnostics.Empty() {
		return f, nil
	}
	cleaned := cloneValueMap(f.cleaned.values)
	for _, diagnostic := range diagnostics.All() {
		delete(cleaned, string(diagnostic.Field()))
	}
	order := make([]string, 0, len(cleaned))
	for _, name := range f.cleaned.order {
		if _, present := cleaned[name]; present {
			order = append(order, name)
		}
	}
	f.cleaned = Values{order: order, values: cleaned}
	f.errors = validation.Join(f.errors, diagnostics)
	f.valid = false
	return f, nil
}
