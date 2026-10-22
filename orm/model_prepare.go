package orm

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// InitialModelValues returns the model's unsaved scalar state. Defaults are
// exact Schema IR constants, including JSON null. Without a default, nullable
// fields and required non-text fields are NULL; nonnullable text/file and
// binary fields use their empty values. A required NULL is an incomplete
// candidate, never an instruction to write a Go zero. The absent key is NULL.
// Collections, uploads, input normalization and permission are not inferred.
func InitialModelValues(model ir.Model) (map[string]query.Value, error) {
	values := make(map[string]query.Value, len(model.Fields))
	for _, field := range model.Fields {
		if _, duplicate := values[field.Name]; duplicate {
			return nil, invalidModelValue(field.Name, "model field is duplicated")
		}
		if _, err := ir.NormalizeField(field); err != nil {
			return nil, invalidModelValue(field.Name, "model field metadata is invalid")
		}
		value := query.Null()
		if field.Default != nil {
			var err error
			value, err = query.ValueFromScalar(*field.Default)
			if err != nil || !mutationValueMatches(field, value) {
				return nil, invalidModelValue(field.Name, "model default is invalid")
			}
		} else if !field.Nullable && field.Kind == ir.FieldBinary {
			value = query.Binary(binaryvalue.Value{})
		} else if !field.Nullable && (field.Kind == ir.FieldChar || field.Kind == ir.FieldText || field.Kind == ir.FieldEmail || field.Kind == ir.FieldURL || field.Kind == ir.FieldSlug || field.Kind.IsFile()) {
			value = query.String("")
		}
		values[field.Name] = value
	}
	return values, nil
}

// PreparedModel owns a typed candidate and its baseline. Preparation performs
// no I/O or full_clean and confers no authority. The caller must obtain a fresh
// authorized current row and perform current choice/unique/business checks in
// the write scope. Each Model call returns a detached copy.
type PreparedModel[M any] struct {
	manager  Manager[M]
	value    M
	baseline map[string]query.Value
	changed  []string
	valid    bool
}

func (PreparedModel[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "orm.PreparedModel{redacted}")
}

// PrepareValues applies explicit scalar changes to a detached current row.
// Nil constructs a new row using every IR default, including excluded fields.
// A non-nil unsaved row preserves its explicitly supplied values. Unknown
// fields, keys and unrepresentable NULL/type values fail before publication.
// Values must already have passed the caller's input and business validation.
func (m Manager[M]) PrepareValues(current *M, values map[string]query.Value) (PreparedModel[M], error) {
	metadata, err := m.Metadata()
	if err != nil {
		return PreparedModel[M]{}, err
	}
	var baseline map[string]query.Value
	var source M
	if current == nil {
		baseline, err = InitialModelValues(metadata)
	} else {
		source, err = m.ApplyValues(*current, nil)
		if err == nil {
			baseline, err = m.ModelValues(source)
		}
	}
	if err != nil {
		return PreparedModel[M]{}, err
	}
	assignments := maps.Clone(values)
	if current == nil {
		assignments = maps.Clone(baseline)
		delete(assignments, m.prepared.primaryKey.Name)
		maps.Copy(assignments, values)
	}
	value, err := m.ApplyValues(source, assignments)
	if err != nil {
		return PreparedModel[M]{}, err
	}
	prepared := PreparedModel[M]{manager: m, value: value, baseline: baseline, valid: true}
	prepared.changed, err = prepared.Changes(value)
	if err != nil {
		return PreparedModel[M]{}, err
	}
	return prepared, nil
}

func (prepared PreparedModel[M]) Model() (M, error) {
	if !prepared.valid {
		var zero M
		return zero, invalidWritePlan("prepared model is zero or invalid")
	}
	return prepared.manager.ApplyValues(prepared.value, nil)
}

// ChangedFields reports actual changes from the preparation baseline in IR
// order. A new row reports every scalar field, since an insert writes them all.
func (prepared PreparedModel[M]) ChangedFields() []string { return slices.Clone(prepared.changed) }

// Changes includes explicit server edits made after Model, without restoring
// excluded values from an older input snapshot. It rejects changing a current
// row's primary key. An initially unsaved row may retain its generated key on
// subsequent saves through the same caller-owned pointer.
func (prepared PreparedModel[M]) Changes(value M) ([]string, error) {
	if !prepared.valid {
		return nil, invalidWritePlan("prepared model is zero or invalid")
	}
	values, err := prepared.manager.ModelValues(value)
	if err != nil {
		return nil, err
	}
	key := prepared.manager.prepared.primaryKey.Name
	beforeKey := prepared.baseline[key]
	if !beforeKey.IsNull() && !beforeKey.Equal(values[key]) {
		return nil, invalidModelValue(key, "prepared model primary key changed")
	}
	var changed []string
	for _, field := range prepared.manager.prepared.metadata.Fields {
		if !field.PrimaryKey && (beforeKey.IsNull() || !prepared.baseline[field.Name].Equal(values[field.Name])) {
			changed = append(changed, field.Name)
		}
	}
	return changed, nil
}

// ValidateUnique checks the prepared candidate's actual changes using the
// same field/constraint policy as generated create and patch inputs. Omitted
// members of a touched constraint come from the candidate's retained current
// values. The check is advisory, must run after current authorization, and
// never replaces native uniqueness enforcement at Save.
func (prepared PreparedModel[M]) ValidateUnique(ctx context.Context, backend db.Queryer, instance M) (validation.Errors, error) {
	changed, err := prepared.Changes(instance)
	if err != nil {
		return validation.Errors{}, err
	}
	descriptor, model, err := prepared.manager.writeConfiguration(ctx, backend)
	if err != nil {
		return validation.Errors{}, err
	}
	value := descriptor.CloneWriteModel(instance)
	values, err := prepared.manager.ModelValues(value)
	if err != nil {
		return validation.Errors{}, err
	}
	assignments := make([]query.Assignment, 0, len(changed))
	for _, name := range changed {
		assignments = append(assignments, NewAssignment(model.metadata.Fields[model.byName[name]], values[name]))
	}
	key, present := descriptor.PrimaryKey(value)
	mutation := NewPatchMutation(value, model.metadata.DBTable, assignments)
	if !present {
		mutation = NewCreateMutation(value, model.metadata.DBTable, assignments)
	}
	return validateUniqueMutation(ctx, backend, preparedWrite[M]{descriptor: descriptor, model: model, mutation: mutation, key: key})
}

// Save writes only actual changes to a keyed row, without INSERT fallback if
// it disappeared. An unsaved row uses force-insert. An unchanged keyed row is
// a no-op. This method opens no transaction and performs no authorization or
// uniqueness check; a successful call inside a transaction remains provisional.
// As with Manager.Save, a generated key stays on the caller's pointer even if
// a later relation write or outer transaction fails.
func (prepared PreparedModel[M]) Save(ctx context.Context, backend db.Mutator, instance *M) error {
	if instance == nil {
		return invalidWritePlan("prepared model instance is nil")
	}
	changed, err := prepared.Changes(*instance)
	if err != nil {
		return err
	}
	values, err := prepared.manager.ModelValues(*instance)
	if err != nil {
		return err
	}
	if values[prepared.manager.prepared.primaryKey.Name].IsNull() {
		return prepared.manager.Save(ctx, backend, instance, ForceInsert[M]())
	}
	return prepared.manager.Save(ctx, backend, instance, UpdateFieldNames[M](changed...))
}
