package model

import (
	"context"
	"fmt"
	"strconv"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// SetPreflight owns one submitted identity mapping and detached current model
// cohort. Only management and identity fields have been parsed. It runs no row,
// model, cross-field or set validator and performs no I/O. Current is available
// only after the entire admission succeeds, so a malformed row cannot select
// another row's authorization scope for a subsequent choice resolver.
type SetPreflight[M any] struct {
	input     forms.SetPreflight
	manager   orm.Manager[M]
	metadata  ir.Model
	fields    []forms.Field
	defaults  map[string]forms.Value
	primary   string
	prefix    string
	current   []setModelSnapshot[M]
	initial   []map[string]forms.Value
	postClean PostClean
	parent    string
	errors    validation.Errors
	valid     bool
}

func PreflightSet[M any](ctx context.Context, manager orm.Manager[M], spec forms.SetSpec, data forms.Data, current []M, postClean PostClean, related ...func(M, ir.ManyToManyField) ([]int64, bool)) (SetPreflight[M], error) {
	return preflightSet(ctx, manager, spec, data, current, postClean, related, "")
}

func preflightSet[M any](ctx context.Context, manager orm.Manager[M], spec forms.SetSpec, data forms.Data, current []M, postClean PostClean, related []func(M, ir.ManyToManyField) ([]int64, bool), parent string) (SetPreflight[M], error) {
	metadata, primary, snapshots, err := setSnapshots(manager, spec, current, related)
	if err != nil {
		return SetPreflight[M]{}, err
	}
	postClean = postClean.Clone()
	binding, _, err := prepareModelBinding(metadata, spec.FormSpec().Fields(), nil, postClean)
	if err != nil {
		return SetPreflight[M]{}, err
	}
	input, err := spec.Preflight(ctx, data, len(snapshots))
	if err != nil {
		return SetPreflight[M]{}, err
	}
	choices := make([]forms.Choice, len(snapshots))
	byKey := make(map[int64]setModelSnapshot[M], len(snapshots))
	for index, snapshot := range snapshots {
		choices[index] = forms.Choice{Value: forms.Integer(snapshot.key), Label: strconv.FormatInt(snapshot.key, 10)}
		byKey[snapshot.key] = snapshot
	}
	keyField, err := forms.ModelChoiceField(primary, forms.WithChoices(choices...))
	if err != nil {
		return SetPreflight[M]{}, err
	}
	keySpec, err := forms.NewSpec([]forms.Field{keyField})
	if err != nil {
		return SetPreflight[M]{}, err
	}
	var identityErrors []validation.Violation
	seen := make(map[int64]bool, len(snapshots))
	prefix := spec.Config().Prefix
	for index := range snapshots {
		name := prefix + "-" + strconv.Itoa(index) + "-" + primary
		raw, present := data.Get(name)
		values := map[string][]string{}
		if present {
			values[primary] = raw
		}
		bound, err := keySpec.Bind(ctx, forms.NewData(values), nil)
		if err != nil {
			return SetPreflight[M]{}, err
		}
		key, parsed := bound.Cleaned().Integer(primary)
		if !bound.Valid() || !parsed || seen[key] {
			identityErrors = append(identityErrors, invalidSetIdentity(index))
			continue
		}
		seen[key] = true
		snapshots[index] = byKey[key]
	}
	// Even unchanged or deleted extra rows must not inject an existing key.
	for index := len(snapshots); index < input.TotalForms(); index++ {
		name := prefix + "-" + strconv.Itoa(index) + "-" + primary
		if raw, present := data.Get(name); present && (len(raw) != 1 || raw[0] != "") {
			identityErrors = append(identityErrors, invalidSetIdentity(index))
		}
	}
	initial, err := setFormInitial(manager, metadata, spec.FormSpec(), snapshots, related)
	if err != nil {
		return SetPreflight[M]{}, err
	}
	if err := ctx.Err(); err != nil {
		return SetPreflight[M]{}, err
	}
	return SetPreflight[M]{input: input, manager: manager, metadata: metadata, fields: binding.fields, defaults: binding.candidate, primary: primary, prefix: prefix, current: snapshots, initial: initial, postClean: postClean, parent: parent, errors: validation.NewErrors(identityErrors...), valid: true}, nil
}

func (input SetPreflight[M]) AdmissionValid() bool {
	return input.valid && input.input.AdmissionValid() && input.errors.Empty()
}
func (input SetPreflight[M]) TotalForms() int       { return input.input.TotalForms() }
func (input SetPreflight[M]) InitialForms() int     { return input.input.InitialForms() }
func (input SetPreflight[M]) Submitted() forms.Data { return input.input.Submitted() }

// RowData exposes unprefixed raw row input only for the admitted identity
// mapping. Check ReadOnly before using raw relation keys for selection I/O.
func (input SetPreflight[M]) RowData(index int) (forms.Data, error) {
	if !input.AdmissionValid() {
		return forms.Data{}, &Error{Path: "set.preflight", Code: "not_admitted"}
	}
	return input.input.RowData(index)
}

// ChoiceKeys identifies the keys that a selected relation field needs to
// resolve without cleaning the form or running validators. Read-only rows use
// only server initial values. Writable rows use the original raw input, plus
// the current/model-default key that omitted optional input would retain.
// Required omission and explicitly empty input do not acquire default keys.
// A false second result preserves malformed input for ordinary Bind errors.
// Returned keys are a lookup request, never current selection authority.
func (input SetPreflight[M]) ChoiceKeys(index int, fieldName string) ([]int64, bool, error) {
	data, err := input.RowData(index)
	if err != nil {
		return nil, false, err
	}
	var field forms.Field
	for _, candidate := range input.fields {
		if candidate.Name() == fieldName {
			field = candidate
			break
		}
	}
	if !field.ModelChoice() || fieldName == input.parent {
		return nil, false, &Error{Path: "set.choices." + fieldName, Code: "not_selected_relation"}
	}
	readonly, err := input.ReadOnly(index)
	if err != nil {
		return nil, false, err
	}
	if readonly {
		value, present := input.initial[index][fieldName]
		if !present {
			value, _ = field.Default()
		}
		return modelChoiceValueKeys(field, value)
	}
	keys, valid, err := field.ModelChoiceKeys(data)
	if err != nil || !valid || len(keys) != 0 || field.Required() || field.Kind() != forms.FieldInteger {
		return keys, valid, err
	}
	for _, metadata := range input.metadata.Fields {
		if metadata.Name != fieldName || !retainsOmittedDefault(metadata, field, data, forms.Null()) {
			continue
		}
		value := input.defaults[fieldName]
		if index < len(input.current) {
			value = input.current[index].values[fieldName]
		}
		return modelChoiceValueKeys(field, value)
	}
	return keys, valid, nil
}

func modelChoiceValueKeys(field forms.Field, value forms.Value) ([]int64, bool, error) {
	if value.IsNull() {
		return nil, true, nil
	}
	if field.Kind() == forms.FieldIntegerList {
		if keys, valid := value.AsIntegers(); valid {
			return keys, true, nil
		}
	} else if key, valid := value.AsInteger(); valid {
		return []int64{key}, true, nil
	}
	return nil, false, &Error{Path: "set.choices." + field.Name(), Code: "invalid_initial_value"}
}

func (input SetPreflight[M]) ReadOnly(index int) (bool, error) {
	return input.input.ReadOnly(index)
}

// Current returns a detached authorized-cohort row at its submitted index. It
// rejects the whole malformed preflight, rather than exposing a fallback row.
func (input SetPreflight[M]) Current(index int) (M, bool, error) {
	var zero M
	if !input.AdmissionValid() {
		return zero, false, &Error{Path: "set.preflight", Code: "not_admitted"}
	}
	if index < 0 || index >= input.TotalForms() {
		return zero, false, &Error{Path: "set.index", Code: "invalid"}
	}
	if index >= len(input.current) {
		return zero, false, nil
	}
	value, err := input.manager.ApplyValues(input.current[index].value, nil)
	return value, err == nil, err
}

func (input SetPreflight[M]) Identity(index int) (forms.Value, bool) {
	if !input.AdmissionValid() || index < 0 || index >= input.TotalForms() || index >= len(input.current) {
		return forms.Null(), false
	}
	return forms.Integer(input.current[index].key), true
}

func (input SetPreflight[M]) IdentityName(index int) (string, error) {
	if !input.valid || index < 0 || index >= input.TotalForms() {
		return "", &Error{Path: "set.index", Code: "invalid"}
	}
	return input.prefix + "-" + strconv.Itoa(index) + "-" + input.primary, nil
}

// Bind runs ordinary row/model/set validation once against this exact input
// and current cohort. Choices is mandatory: its zero value permits no selected
// relation key, and missing row/field entries cannot inherit global choices.
// Invalid admission remains a whole-set failure, including for deleted rows.
func (input SetPreflight[M]) Bind(ctx context.Context, choices forms.RowModelChoices) (InstanceSet[M], error) {
	return input.bind(ctx, []forms.RowModelChoices{choices})
}

func (input SetPreflight[M]) bind(ctx context.Context, choices []forms.RowModelChoices) (InstanceSet[M], error) {
	if !input.valid {
		return InstanceSet[M]{}, &Error{Path: "set.preflight", Code: "uninitialized"}
	}
	instances := make(map[int]InstanceForm[M])
	set, err := input.input.BindWith(ctx, input.initial, forms.SetProcessor{Form: func(row forms.SetForm) (forms.Form, error) {
		var source M
		var values map[string]forms.Value
		hasInstance := row.Index() < len(input.current)
		if hasInstance {
			snapshot := input.current[row.Index()]
			source = snapshot.value
			values = copyFormValues(snapshot.values)
			for name, value := range input.initial[row.Index()] {
				values[name] = value
			}
		}
		if input.parent != "" {
			if values == nil {
				values = map[string]forms.Value{}
			}
			// Server ownership also applies while a new parent's key is pending.
			values[input.parent], _ = row.Form().Initial().Get(input.parent)
		}
		binding, _, err := prepareModelBinding(input.metadata, row.Fields(), values, input.postClean)
		if err != nil {
			return forms.Form{}, err
		}
		var bound BoundForm
		if row.Form().ReadOnly() {
			bound = BoundForm{form: row.Form(), candidate: forms.NewValues(binding.candidate), model: binding.model, fields: binding.fields}
		} else {
			bound, err = binding.finish(row.Form())
			if err != nil {
				return forms.Form{}, err
			}
		}
		bound.parent = input.parent
		snapshot, err := input.manager.ApplyValues(source, nil)
		if err != nil {
			return forms.Form{}, err
		}
		instances[row.Index()] = InstanceForm[M]{bound: bound, manager: input.manager, instance: snapshot, hasInstance: hasInstance}
		return bound.Form(), nil
	}, Clean: func(set forms.Set) (forms.Set, error) {
		return validateSetUnique(input.metadata, set, instances, input.parent)
	}}, choices...)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	if !input.errors.Empty() {
		set, err = set.WithErrors(input.errors)
		if err != nil {
			return InstanceSet[M]{}, err
		}
	}
	return InstanceSet[M]{set: set, manager: input.manager, primary: input.primary, current: input.current, instances: instances}, nil
}

func (SetPreflight[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.SetPreflight{redacted}")
}
