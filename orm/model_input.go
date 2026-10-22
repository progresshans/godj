package orm

import (
	"fmt"
	"maps"
	"slices"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// ModelInput owns scalar presence and separate collection intent for one
// typed model source. It is independent of HTML/JSON and never performs I/O.
// The zero value is invalid, unlike a prepared input with no changes.
type ModelInput[M any] struct {
	model       ir.Model
	values      map[string]query.Value
	collections map[string][]int64
	target      query.Value
	targetBound bool
	valid       bool
}

func (ModelInput[M]) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "orm.ModelInput{redacted}")
}

// Input snapshots explicitly supplied, already-cleaned scalar values. It
// checks field ownership, type and nullability without applying defaults or
// assigning a model. This low-level server API grants no input exposure or
// permission: transport adapters must enforce their own explicit allowlist.
// Primary keys, unknown/computed fields and collections are not scalar inputs.
func (m Manager[M]) Input(values map[string]query.Value) (ModelInput[M], error) {
	if _, err := m.modelValueDescriptor(); err != nil {
		return ModelInput[M]{}, err
	}
	owned, err := m.inputValues(values)
	if err != nil {
		return ModelInput[M]{}, err
	}
	return ModelInput[M]{model: m.prepared.metadata, values: owned, valid: true}, nil
}

// Get distinguishes omission from explicit NULL and non-null zero. A default
// supplied by a transport's full-input policy is present.
func (value ModelInput[M]) Get(name string) (query.Value, bool) {
	scalar, present := value.values[name]
	return scalar, present
}

func (value ModelInput[M]) Values() map[string]query.Value { return maps.Clone(value.values) }

// Model returns the complete owned source policy, not persistence authority.
func (value ModelInput[M]) Model() (ir.Model, bool) {
	if !value.valid {
		return ir.Model{}, false
	}
	return value.model.Clone(), true
}

// WithTargetKey binds server-prepared input to one instance identity. NULL
// means an unsaved instance; an explicit integer zero is a different target.
// It never makes the primary key writable or grants permission. A model form
// uses this to retain its initial object binding across a fresh write lookup.
// An existing binding cannot be replaced with a different key.
func (value ModelInput[M]) WithTargetKey(key query.Value) (ModelInput[M], error) {
	if !value.valid {
		return ModelInput[M]{}, invalidWritePlan("model input is zero or invalid")
	}
	if value.targetBound && !value.target.Equal(key) {
		return ModelInput[M]{}, invalidWritePlan("model input target cannot be replaced")
	}
	var primaryKey *ir.Field
	for _, field := range value.model.Fields {
		if !field.PrimaryKey {
			continue
		}
		if primaryKey != nil {
			return ModelInput[M]{}, invalidWritePlan("model input primary key is ambiguous")
		}
		copy := field
		primaryKey = &copy
	}
	if primaryKey == nil || !key.IsNull() && !mutationValueMatches(*primaryKey, key) {
		return ModelInput[M]{}, invalidWritePlan("model input target key has an invalid representation")
	}
	value.target, value.targetBound = key, true
	return value, nil
}

func (value ModelInput[M]) TargetKey() (query.Value, bool) { return value.target, value.targetBound }

// WithCollections adds explicit membership intent separately from scalar
// changes. It preserves key order, duplicates, empty selections and omissions;
// current choice checks and relation writes remain the application's policy.
func (value ModelInput[M]) WithCollections(collections map[string][]int64) (ModelInput[M], error) {
	if !value.valid {
		return ModelInput[M]{}, invalidWritePlan("model input is zero or invalid")
	}
	known := make(map[string]bool, len(value.model.ManyToMany))
	for _, field := range value.model.ManyToMany {
		known[field.Name] = true
	}
	names := make([]string, 0, len(collections))
	for name := range collections {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !known[name] {
			return ModelInput[M]{}, invalidModelValue(name, "model input collection is unknown")
		}
	}
	value.collections = value.Collections()
	for _, name := range names {
		value.collections[name] = slices.Clone(collections[name])
	}
	return value, nil
}

func (value ModelInput[M]) Collection(name string) ([]int64, bool) {
	keys, present := value.collections[name]
	return slices.Clone(keys), present
}

func (value ModelInput[M]) Collections() map[string][]int64 {
	result := make(map[string][]int64, len(value.collections))
	for name, keys := range value.collections {
		result[name] = slices.Clone(keys)
	}
	return result
}

// PrepareInput rechecks the complete source before preparing the caller's
// fresh authorized row. A ModelInput for the same Go type with different IR
// cannot redirect the write to another table or change hidden field policy.
// Collection intent is never silently persisted by scalar preparation.
func (m Manager[M]) PrepareInput(value ModelInput[M], current *M) (PreparedModel[M], error) {
	if err := m.checkInput(value); err != nil {
		return PreparedModel[M]{}, err
	}
	if value.targetBound {
		key := query.Null()
		if current != nil {
			values, err := m.ModelValues(*current)
			if err != nil {
				return PreparedModel[M]{}, err
			}
			key = values[m.prepared.primaryKey.Name]
		}
		if !value.target.Equal(key) {
			return PreparedModel[M]{}, invalidWritePlan("model input target does not match the current instance")
		}
	}
	return m.PrepareValues(current, value.values)
}

// WithServerValues adds explicitly server-owned scalar values before model
// preparation. A field already supplied by the input is rejected instead of
// silently choosing a winner. This makes accidental exposure of a server-owned
// category, owner or revision fail closed. Defaults remain separate from input
// presence, and unknown/computed fields can never become writable metadata.
func (m Manager[M]) WithServerValues(value ModelInput[M], values map[string]query.Value) (ModelInput[M], error) {
	if err := m.checkInput(value); err != nil {
		return ModelInput[M]{}, err
	}
	owned, err := m.inputValues(values)
	if err != nil {
		return ModelInput[M]{}, err
	}
	for _, field := range m.prepared.metadata.Fields {
		if _, present := owned[field.Name]; !present {
			continue
		}
		if _, supplied := value.values[field.Name]; supplied {
			return ModelInput[M]{}, invalidModelValue(field.Name, "server-owned field is already supplied by input")
		}
	}
	value.values = maps.Clone(value.values)
	maps.Copy(value.values, owned)
	return value, nil
}

func (m Manager[M]) checkInput(value ModelInput[M]) error {
	if !value.valid || m.prepared == nil || !value.model.Equal(m.prepared.metadata) {
		return invalidWritePlan("model input source does not match manager metadata")
	}
	return nil
}
