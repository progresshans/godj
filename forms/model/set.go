package model

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/progresshans/godj/forms"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// InstanceSet binds a bounded formset to an explicitly supplied set of current
// model instances. Current is an allowed identity set, not a query to execute.
// Construction, binding and preparation perform no database I/O. The caller
// still owns current authorization, database validation and final write scope.
type InstanceSet[M any] struct {
	set       forms.Set
	manager   orm.Manager[M]
	primary   string
	current   []setModelSnapshot[M]
	instances map[int]InstanceForm[M]
}

type setModelSnapshot[M any] struct {
	value  M
	values map[string]forms.Value
	key    int64
}

func (set InstanceSet[M]) FormSet() forms.Set { return set.set }
func (set InstanceSet[M]) Valid() bool        { return set.set.Valid() }

// Instance returns evaluated rows and read-only current snapshots. A read-only
// snapshot cannot produce persistence input. Unbound rows and unchanged extras
// do not manufacture a model candidate.
func (set InstanceSet[M]) Instance(index int) (InstanceForm[M], bool) {
	instance, present := set.instances[index]
	return instance, present
}

// WithErrors attaches a whole-request rejection. Authorization and identity
// failures must use this path, so DELETE cannot suppress them as row errors.
func (set InstanceSet[M]) WithErrors(failures validation.Errors) (InstanceSet[M], error) {
	bound, err := set.set.WithErrors(failures)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	set.set = bound
	return set, nil
}

// WithRowErrors attaches data-validation diagnostics to an evaluated row and
// keeps its typed candidate/exclusions in sync with the ordinary formset.
// Such diagnostics may be ignored for a deleted row; they are not admission.
func (set InstanceSet[M]) WithRowErrors(index int, failures validation.Errors, rejectedFields ...string) (InstanceSet[M], error) {
	instance, present := set.instances[index]
	if !present {
		return InstanceSet[M]{}, &Error{Path: "set.row", Code: "not_evaluated"}
	}
	updated, err := instance.WithErrors(failures, rejectedFields...)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	bound, err := set.set.WithFormErrors(index, failures, rejectedFields...)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	instances := make(map[int]InstanceForm[M], len(set.instances))
	for key, value := range set.instances {
		instances[key] = value
	}
	instances[index] = updated
	set.instances, set.set = instances, bound
	return set, nil
}

// Current returns a detached, server-owned current row. A new/extra row has no
// existing model, even if a client submitted a primary key for it.
func (set InstanceSet[M]) Current(index int) (M, bool, error) {
	var zero M
	if index < 0 || index >= set.set.TotalForms() {
		return zero, false, &Error{Path: "set.index", Code: "invalid"}
	}
	if index >= len(set.current) {
		return zero, false, nil
	}
	value, err := set.manager.ApplyValues(set.current[index].value, nil)
	return value, err == nil, err
}

// IdentityName/Identity provide the separate hidden primary-key input. Identity
// is never an editable model field or a command value in BoundForm.Input.
func (set InstanceSet[M]) IdentityName(index int) (string, error) {
	if index < 0 || index >= set.set.TotalForms() || set.primary == "" {
		return "", &Error{Path: "set.index", Code: "invalid"}
	}
	return set.set.Prefix() + "-" + strconv.Itoa(index) + "-" + set.primary, nil
}
func (set InstanceSet[M]) Identity(index int) (forms.Value, bool) {
	if index < 0 || index >= set.set.TotalForms() || index >= len(set.current) {
		return forms.Null(), false
	}
	return forms.Integer(set.current[index].key), true
}

// UnboundSet prepares display/identity snapshots without running validators.
// Selected ManyToMany fields require one pure reader over already-loaded data.
func UnboundSet[M any](manager orm.Manager[M], spec forms.SetSpec, current []M, related ...func(M, ir.ManyToManyField) ([]int64, bool)) (InstanceSet[M], error) {
	metadata, primary, snapshots, err := setSnapshots(manager, spec, current, related)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	initial, err := setFormInitial(manager, metadata, spec.FormSpec(), snapshots, related)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	set, err := spec.Unbound(initial)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	return InstanceSet[M]{set: set, manager: manager, primary: primary, current: snapshots}, nil
}

// BindSet maps submitted identities within the caller's current snapshot before
// binding. Existing rows may be reordered, but cannot be duplicated, omitted or
// replaced with identities outside that snapshot. Extra rows cannot supply an
// existing key. Identity failures invalidate the entire set, even for DELETE.
// PostClean is applied once to each evaluated row before set count validation;
// ordinary field/cross-field cleaning is not repeated by the model adapter.
func BindSet[M any](manager orm.Manager[M], spec forms.SetSpec, data forms.Data, current []M, postClean PostClean, related ...func(M, ir.ManyToManyField) ([]int64, bool)) (InstanceSet[M], error) {
	return bindSet(manager, spec, data, current, postClean, related, "")
}

func bindSet[M any](manager orm.Manager[M], spec forms.SetSpec, data forms.Data, current []M, postClean PostClean, related []func(M, ir.ManyToManyField) ([]int64, bool), parent string) (InstanceSet[M], error) {
	metadata, primary, snapshots, err := setSnapshots(manager, spec, current, related)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	postClean = postClean.Clone()
	if _, _, err := prepareModelBinding(metadata, spec.FormSpec().Fields(), nil, postClean); err != nil {
		return InstanceSet[M]{}, err
	}
	choices := make([]forms.Choice, len(snapshots))
	byKey := make(map[int64]setModelSnapshot[M], len(snapshots))
	for index, snapshot := range snapshots {
		choices[index] = forms.Choice{Value: forms.Integer(snapshot.key), Label: strconv.FormatInt(snapshot.key, 10)}
		byKey[snapshot.key] = snapshot
	}
	keyField, err := forms.ModelChoiceField(primary, forms.WithChoices(choices...))
	if err != nil {
		return InstanceSet[M]{}, err
	}
	keySpec, err := forms.NewSpec([]forms.Field{keyField})
	if err != nil {
		return InstanceSet[M]{}, err
	}
	var identityErrors []validation.Violation
	seen := make(map[int64]bool, len(snapshots))
	for index := range snapshots {
		name := spec.Config().Prefix + "-" + strconv.Itoa(index) + "-" + primary
		raw, present := data.Get(name)
		values := map[string][]string{}
		if present {
			values[primary] = raw
		}
		bound, err := keySpec.Bind(forms.NewData(values), nil)
		if err != nil {
			return InstanceSet[M]{}, err
		}
		key, parsed := bound.Cleaned().Integer(primary)
		if !bound.Valid() || !parsed || seen[key] {
			identityErrors = append(identityErrors, invalidSetIdentity(index))
			continue
		}
		seen[key] = true
		snapshots[index] = byKey[key]
	}
	initial, err := setFormInitial(manager, metadata, spec.FormSpec(), snapshots, related)
	if err != nil {
		return InstanceSet[M]{}, err
	}
	instances := make(map[int]InstanceForm[M])
	set, err := spec.BindWith(data, initial, forms.SetProcessor{Form: func(row forms.SetForm) (forms.Form, error) {
		var source M
		var values map[string]forms.Value
		hasInstance := row.Index() < len(snapshots)
		if hasInstance {
			snapshot := snapshots[row.Index()]
			source = snapshot.value
			values = copyFormValues(snapshot.values)
			for name, value := range initial[row.Index()] {
				values[name] = value
			}
		}
		if parent != "" {
			if values == nil {
				values = map[string]forms.Value{}
			}
			// The server parent wins over a model default, including NULL
			// while its generated key is still pending.
			values[parent], _ = row.Form().Initial().Get(parent)
		}
		binding, _, err := prepareModelBinding(metadata, row.Fields(), values, postClean)
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
		bound.parent = parent
		snapshot, err := manager.ApplyValues(source, nil)
		if err != nil {
			return forms.Form{}, err
		}
		instances[row.Index()] = InstanceForm[M]{bound: bound, manager: manager, instance: snapshot, hasInstance: hasInstance}
		return bound.Form(), nil
	}, Clean: func(set forms.Set) (forms.Set, error) {
		return validateSetUnique(metadata, set, instances, parent)
	}})
	if err != nil {
		return InstanceSet[M]{}, err
	}
	// Empty optional extras skipped row processing, but an injected identity
	// must still be rejected before any typed/persistence selection is offered.
	for index := len(snapshots); index < set.TotalForms(); index++ {
		name := spec.Config().Prefix + "-" + strconv.Itoa(index) + "-" + primary
		if raw, present := data.Get(name); present && (len(raw) != 1 || raw[0] != "") {
			identityErrors = append(identityErrors, invalidSetIdentity(index))
		}
	}
	if len(identityErrors) != 0 {
		set, err = set.WithErrors(validation.NewErrors(identityErrors...))
		if err != nil {
			return InstanceSet[M]{}, err
		}
	}
	return InstanceSet[M]{set: set, manager: manager, primary: primary, current: snapshots, instances: instances}, nil
}

func invalidSetIdentity(index int) validation.Violation {
	return validation.New(validation.NonField, "invalid_identity", validation.NewParam("index", strconv.Itoa(index)))
}

func copyFormValues(values map[string]forms.Value) map[string]forms.Value {
	result := make(map[string]forms.Value, len(values))
	for name, value := range values {
		result[name] = value
	}
	return result
}

func setSnapshots[M any](manager orm.Manager[M], spec forms.SetSpec, current []M, related []func(M, ir.ManyToManyField) ([]int64, bool)) (ir.Model, string, []setModelSnapshot[M], error) {
	metadata, err := manager.Metadata()
	if err != nil {
		return ir.Model{}, "", nil, err
	}
	if len(spec.FormSpec().Fields()) == 0 {
		return ir.Model{}, "", nil, &Error{Path: "set.spec", Code: "uninitialized"}
	}
	if len(current) > spec.Config().AbsoluteMax {
		return ir.Model{}, "", nil, &Error{Path: "set.current", Code: "absolute_max"}
	}
	if len(related) > 1 || len(related) == 1 && related[0] == nil {
		return ir.Model{}, "", nil, &Error{Path: "set.related", Code: "invalid_reader"}
	}
	primary := ""
	for _, field := range metadata.Fields {
		if spec.Config().CanOrder && field.Name == "ORDER" || spec.Config().CanDelete && field.Name == "DELETE" {
			return ir.Model{}, "", nil, &Error{Path: "set.model." + field.Name, Code: "reserved"}
		}
		if field.PrimaryKey {
			if primary != "" || field.Kind != ir.FieldAuto && field.Kind != ir.FieldInteger {
				return ir.Model{}, "", nil, &Error{Path: "set.model", Code: "unsupported_primary_key"}
			}
			primary = field.Name
		}
	}
	if primary == "" {
		return ir.Model{}, "", nil, &Error{Path: "set.model", Code: "missing_primary_key"}
	}
	if _, _, err := prepareModelBinding(metadata, spec.FormSpec().Fields(), nil, PostClean{}); err != nil {
		return ir.Model{}, "", nil, err
	}
	result := make([]setModelSnapshot[M], len(current))
	seen := make(map[int64]bool, len(current))
	for index, value := range current {
		snapshot, err := manager.ApplyValues(value, nil)
		if err != nil {
			return ir.Model{}, "", nil, err
		}
		values, err := manager.ModelValues(snapshot)
		if err != nil {
			return ir.Model{}, "", nil, err
		}
		key, present := values[primary].Integer()
		if !present || seen[key] {
			return ir.Model{}, "", nil, &Error{Path: "set.current", Code: "missing_or_duplicate_identity"}
		}
		seen[key] = true
		owned := make(map[string]forms.Value, len(values))
		for name, scalar := range values {
			input, valid := formValue(scalar)
			if !valid {
				return ir.Model{}, "", nil, &Error{Path: "set.current." + name, Code: "type_mismatch"}
			}
			owned[name] = input
		}
		result[index] = setModelSnapshot[M]{value: snapshot, values: owned, key: key}
	}
	return metadata, primary, result, nil
}

func setFormInitial[M any](manager orm.Manager[M], model ir.Model, spec forms.Spec, snapshots []setModelSnapshot[M], related []func(M, ir.ManyToManyField) ([]int64, bool)) ([]map[string]forms.Value, error) {
	many := make(map[string]ir.ManyToManyField, len(model.ManyToMany))
	for _, field := range model.ManyToMany {
		many[field.Name] = field
	}
	initial := make([]map[string]forms.Value, len(snapshots))
	for index, snapshot := range snapshots {
		values := make(map[string]forms.Value)
		for _, field := range spec.Fields() {
			if value, present := snapshot.values[field.Name()]; present {
				if field.Kind() == forms.FieldFile {
					var err error
					value, err = fileInitialValue(value)
					if err != nil {
						return nil, err
					}
				}
				values[field.Name()] = value
				continue
			}
			if metadata, collection := many[field.Name()]; collection {
				if len(related) != 1 {
					return nil, &Error{Path: "set.initial." + field.Name(), Code: "missing_relation_reader"}
				}
				owner, err := manager.ApplyValues(snapshot.value, nil)
				if err != nil {
					return nil, err
				}
				keys, present := related[0](owner, metadata.Clone())
				if !present {
					return nil, &Error{Path: "set.initial." + field.Name(), Code: "missing_value"}
				}
				values[field.Name()] = forms.Integers(keys...)
			}
		}
		initial[index] = values
	}
	return initial, nil
}

// PreparedSetRow is a typed candidate for one active editable row. Unchanged
// editable rows remain; read-only current rows cannot become write candidates.
type PreparedSetRow[M any] struct {
	index    int
	existing bool
	changed  []string
	prepared PreparedInstance[M]
}

func (row PreparedSetRow[M]) Index() int                    { return row.index }
func (row PreparedSetRow[M]) Existing() bool                { return row.existing }
func (row PreparedSetRow[M]) Changed() []string             { return slices.Clone(row.changed) }
func (row PreparedSetRow[M]) Prepared() PreparedInstance[M] { return row.prepared }
func (row PreparedSetRow[M]) Model() (M, error)             { return row.prepared.Model() }

type DeletedSetRow[M any] struct {
	index   int
	manager orm.Manager[M]
	current M
}

func (row DeletedSetRow[M]) Index() int        { return row.index }
func (row DeletedSetRow[M]) Model() (M, error) { return row.manager.ApplyValues(row.current, nil) }

// PreparedSet separates active typed candidates from existing deletion intents.
// It schedules no writes and opens no transaction. New rows marked DELETE and
// unchanged optional extras never become a persistence operation.
type PreparedSet[M any] struct {
	manager orm.Manager[M]
	rows    []PreparedSetRow[M]
	deleted []DeletedSetRow[M]
}

func (set PreparedSet[M]) Rows() []PreparedSetRow[M]   { return slices.Clone(set.rows) }
func (set PreparedSet[M]) Deleted() []DeletedSetRow[M] { return slices.Clone(set.deleted) }

func (set InstanceSet[M]) Prepare() (PreparedSet[M], error) {
	active, err := set.set.ActiveForms()
	if err != nil {
		return PreparedSet[M]{}, err
	}
	deleted, err := set.set.DeletedForms()
	if err != nil {
		return PreparedSet[M]{}, err
	}
	result := PreparedSet[M]{manager: set.manager, rows: make([]PreparedSetRow[M], 0, len(active)), deleted: make([]DeletedSetRow[M], 0, len(deleted))}
	for _, row := range active {
		if row.Form().ReadOnly() {
			continue
		}
		instance, present := set.instances[row.Index()]
		if !present {
			return PreparedSet[M]{}, &Error{Path: "set.row", Code: "not_evaluated"}
		}
		prepared, err := instance.Prepare()
		if err != nil {
			return PreparedSet[M]{}, err
		}
		result.rows = append(result.rows, PreparedSetRow[M]{index: row.Index(), existing: row.Index() < len(set.current), changed: row.Form().Changed(), prepared: prepared})
	}
	for _, row := range deleted {
		if row.Index() < len(set.current) {
			current, err := set.manager.ApplyValues(set.current[row.Index()].value, nil)
			if err != nil {
				return PreparedSet[M]{}, err
			}
			result.deleted = append(result.deleted, DeletedSetRow[M]{index: row.Index(), manager: set.manager, current: current})
		}
	}
	return result, nil
}

func (InstanceSet[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.InstanceSet{redacted}")
}
func (PreparedSet[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.PreparedSet{redacted}")
}
func (PreparedSetRow[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.PreparedSetRow{redacted}")
}
func (DeletedSetRow[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "model.DeletedSetRow{redacted}")
}
