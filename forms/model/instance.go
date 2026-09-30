package model

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// InstanceForm binds a typed current instance to the same immutable model form
// candidate used by read checks. It owns a detached instance snapshot, never the
// caller's pointer. The caller still owns admission and final write authority.
type InstanceForm[M any] struct {
	bound       BoundForm
	manager     orm.Manager[M]
	instance    M
	hasInstance bool
}

func (InstanceForm[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.InstanceForm{redacted}")
}
func (form InstanceForm[M]) BoundForm() BoundForm { return form.bound }

// BindInstance obtains model metadata and initial scalar values from the typed
// manager. Nil means a new model using Schema IR defaults/unsaved values; a
// non-nil instance may itself be unsaved. Spec owns selected fields, command
// inputs and explicitly scoped relation choices. No database I/O occurs.
func BindInstance[M any](ctx context.Context, manager orm.Manager[M], spec forms.Spec, data forms.Data, instance *M, postClean PostClean) (InstanceForm[M], error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return InstanceForm[M]{}, err
	}
	var initial map[string]forms.Value
	var source M
	if instance != nil {
		source = *instance
	}
	snapshot, err := manager.ApplyValues(source, nil)
	if err != nil {
		return InstanceForm[M]{}, err
	}
	if instance != nil {
		values, err := manager.ModelValues(snapshot)
		if err != nil {
			return InstanceForm[M]{}, err
		}
		initial = make(map[string]forms.Value, len(values))
		for name, scalar := range values {
			value, valid := formValue(scalar)
			if !valid {
				return InstanceForm[M]{}, &Error{Path: "instance." + name, Code: "type_mismatch"}
			}
			initial[name] = value
		}
	}
	bound, err := Bind(ctx, metadata, spec, data, initial, postClean)
	if err != nil {
		return InstanceForm[M]{}, err
	}
	return InstanceForm[M]{bound: bound, manager: manager, instance: snapshot, hasInstance: instance != nil}, nil
}

func (form InstanceForm[M]) WithErrors(failures validation.Errors, rejectedFields ...string) (InstanceForm[M], error) {
	bound, err := form.bound.WithErrors(failures, rejectedFields...)
	if err != nil {
		return InstanceForm[M]{}, err
	}
	form.bound = bound
	return form, nil
}

// PrepareInstance connects an already-bound model form to an authorized typed
// current instance without binding again or repeating model clean. The complete
// model policy and primary-key presence/value must agree. A non-nil instance
// preserves its excluded fields; nil prepares a new model from IR defaults.
// Callers still own fresh row/authority checks and transaction admission.
func PrepareInstance[M any](manager orm.Manager[M], bound BoundForm, instance *M) (PreparedInstance[M], error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return PreparedInstance[M]{}, err
	}
	if !metadata.Equal(bound.model) {
		return PreparedInstance[M]{}, &Error{Path: "model", Code: "mismatch"}
	}
	var source M
	if instance != nil {
		source = *instance
	}
	snapshot, err := manager.ApplyValues(source, nil)
	if err != nil {
		return PreparedInstance[M]{}, err
	}
	prepared, err := (InstanceForm[M]{bound: bound, manager: manager, instance: snapshot, hasInstance: instance != nil}).Prepare()
	if err != nil {
		return PreparedInstance[M]{}, err
	}
	values, err := manager.ModelValues(prepared.value)
	if err != nil {
		return PreparedInstance[M]{}, err
	}
	for _, field := range metadata.Fields {
		if !field.PrimaryKey {
			continue
		}
		key, present := bound.candidate.Get(field.Name)
		scalar, valid := queryValue(key)
		if !present || !valid || !values[field.Name].Equal(scalar) {
			return PreparedInstance[M]{}, &Error{Path: "instance." + field.Name, Code: "mismatch"}
		}
	}
	return prepared, nil
}

// PreparedInstance keeps typed scalar preparation separate from selected
// collection writes and command inputs. Preparation is not a commit. Callers
// must preserve all intended scalar/collection operations in their authorized
// write scope; this value does not imply that any database check has run.
type PreparedInstance[M any] struct {
	manager     orm.Manager[M]
	value       M
	input       forms.Values
	collections forms.Values
	files       []pendingFile
}

func (PreparedInstance[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.PreparedInstance{redacted}")
}

// Model returns a detached model, including nullable pointees. An invalid zero
// PreparedInstance returns an error instead of an apparently prepared zero M.
func (prepared PreparedInstance[M]) Model() (M, error) {
	if err := prepared.requireStoredFiles(); err != nil {
		var zero M
		return zero, err
	}
	return prepared.manager.ApplyValues(prepared.value, nil)
}

func (prepared PreparedInstance[M]) Input() forms.Values       { return prepared.input }
func (prepared PreparedInstance[M]) Collections() forms.Values { return prepared.collections }

// Prepare constructs the typed scalar instance without I/O. It rejects NULL
// for a nonnullable Go field instead of silently converting it to a zero value.
// A new model also applies excluded defaults; an existing instance preserves
// every excluded field unless an image or PostClean owns a derived change.
// ManyToMany values stay separate for their later, explicit persistence phase.
func (form InstanceForm[M]) Prepare() (PreparedInstance[M], error) {
	input, err := form.bound.Input()
	if err != nil {
		return PreparedInstance[M]{}, err
	}
	values := make(map[string]query.Value)
	var files []pendingFile
	for _, field := range form.bound.model.Fields {
		if field.PrimaryKey {
			continue
		}
		value, present := input.Get(field.Name)
		if field.Kind.IsFile() && present {
			if file, ok := value.AsFile(); ok {
				if upload, pending := file.Upload(); pending {
					files = append(files, pendingFile{field: field.Clone(), upload: upload})
					if !form.hasInstance {
						stored := query.String("")
						if field.Nullable {
							stored = query.Null()
						}
						if field.Default != nil {
							stored = query.String(field.Default.String)
						}
						values[field.Name] = stored
					}
					continue
				}
				// Both explicit clear and a retained empty reference store "".
				values[field.Name] = query.String(file.Name())
				continue
			}
		}
		if !form.hasInstance {
			value, present = form.bound.candidate.Get(field.Name)
		}
		if !present {
			continue
		}
		scalar, valid := queryValue(value)
		if !valid {
			return PreparedInstance[M]{}, &Error{Path: "candidate." + field.Name, Code: "type_mismatch"}
		}
		values[field.Name] = scalar
	}
	value, err := form.manager.ApplyValues(form.instance, values)
	if err != nil {
		return PreparedInstance[M]{}, err
	}
	collections := make(map[string]forms.Value)
	for _, field := range form.bound.model.ManyToMany {
		if value, present := input.Get(field.Name); present {
			collections[field.Name] = value
		}
	}
	return PreparedInstance[M]{manager: form.manager, value: value, input: input, collections: forms.NewValues(collections), files: files}, nil
}

func formValue(value query.Value) (forms.Value, bool) {
	switch value.Kind() {
	case query.ValueNull:
		return forms.Null(), true
	case query.ValueString:
		v, ok := value.String()
		return forms.String(v), ok
	case query.ValueInteger:
		v, ok := value.Integer()
		return forms.Integer(v), ok
	case query.ValueBoolean:
		v, ok := value.Boolean()
		return forms.Boolean(v), ok
	case query.ValueDateTime:
		v, ok := value.DateTime()
		return forms.DateTime(v), ok
	case query.ValueDate:
		v, ok := value.Date()
		return forms.Date(v), ok
	case query.ValueTime:
		v, ok := value.Time()
		return forms.Time(v), ok
	case query.ValueDuration:
		v, ok := value.Duration()
		return forms.Duration(v), ok
	case query.ValueFloat:
		v, ok := value.Float()
		return forms.Float(v), ok
	case query.ValueDecimal:
		v, ok := value.Decimal()
		return forms.Decimal(v), ok
	case query.ValueUUID:
		v, ok := value.UUID()
		return forms.UUID(v), ok
	case query.ValueJSON:
		v, ok := value.JSON()
		return forms.JSON(v), ok
	default:
		return forms.Value{}, false
	}
}
