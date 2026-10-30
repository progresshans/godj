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
	state *modelInputState
	_     [0]func(M)
}

// Keep selected input behind an opaque pointer even when an invalid fmt verb
// bypasses Format. Every derivation copies this state before changing it.
type modelInputState struct {
	model       ir.Model
	values      map[string]query.Value
	collections map[string][]int64
	target      query.Value
	targetBound bool
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
	return ModelInput[M]{state: &modelInputState{model: m.prepared.metadata, values: owned}}, nil
}

// Get distinguishes omission from explicit NULL and non-null zero. A default
// supplied by a transport's full-input policy is present.
func (value ModelInput[M]) Get(name string) (query.Value, bool) {
	if value.state == nil {
		return query.Value{}, false
	}
	scalar, present := value.state.values[name]
	return scalar, present
}

func (value ModelInput[M]) Values() map[string]query.Value {
	if value.state == nil {
		return nil
	}
	return maps.Clone(value.state.values)
}

// Model returns the complete owned source policy, not persistence authority.
func (value ModelInput[M]) Model() (ir.Model, bool) {
	if value.state == nil {
		return ir.Model{}, false
	}
	return value.state.model.Clone(), true
}

// WithTargetKey binds server-prepared input to one instance identity. NULL
// means an unsaved instance; an explicit integer zero is a different target.
// It never makes the primary key writable or grants permission. A model form
// uses this to retain its initial object binding across a fresh write lookup.
// An existing binding cannot be replaced with a different key.
func (value ModelInput[M]) WithTargetKey(key query.Value) (ModelInput[M], error) {
	if value.state == nil {
		return ModelInput[M]{}, invalidWritePlan("model input is zero or invalid")
	}
	if value.state.targetBound && !value.state.target.Equal(key) {
		return ModelInput[M]{}, invalidWritePlan("model input target cannot be replaced")
	}
	var primaryKey *ir.Field
	for _, field := range value.state.model.Fields {
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
	state := *value.state
	state.target, state.targetBound = key, true
	return ModelInput[M]{state: &state}, nil
}

func (value ModelInput[M]) TargetKey() (query.Value, bool) {
	if value.state == nil {
		return query.Value{}, false
	}
	return value.state.target, value.state.targetBound
}

// WithCollections adds explicit membership intent separately from scalar
// changes. It preserves key order, duplicates, empty selections and omissions;
// current choice checks and relation writes remain the application's policy.
func (value ModelInput[M]) WithCollections(collections map[string][]int64) (ModelInput[M], error) {
	if value.state == nil {
		return ModelInput[M]{}, invalidWritePlan("model input is zero or invalid")
	}
	known := make(map[string]bool, len(value.state.model.ManyToMany))
	for _, field := range value.state.model.ManyToMany {
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
	state := *value.state
	state.collections = value.Collections()
	for _, name := range names {
		state.collections[name] = slices.Clone(collections[name])
	}
	return ModelInput[M]{state: &state}, nil
}

func (value ModelInput[M]) Collection(name string) ([]int64, bool) {
	if value.state == nil {
		return nil, false
	}
	keys, present := value.state.collections[name]
	return slices.Clone(keys), present
}

func (value ModelInput[M]) Collections() map[string][]int64 {
	if value.state == nil {
		return nil
	}
	result := make(map[string][]int64, len(value.state.collections))
	for name, keys := range value.state.collections {
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
	if value.state.targetBound {
		key := query.Null()
		if current != nil {
			values, err := m.ModelValues(*current)
			if err != nil {
				return PreparedModel[M]{}, err
			}
			key = values[m.prepared.primaryKey.Name]
		}
		if !value.state.target.Equal(key) {
			return PreparedModel[M]{}, invalidWritePlan("model input target does not match the current instance")
		}
	}
	return m.PrepareValues(current, value.state.values)
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
		if _, supplied := value.state.values[field.Name]; supplied {
			return ModelInput[M]{}, invalidModelValue(field.Name, "server-owned field is already supplied by input")
		}
	}
	state := *value.state
	state.values = maps.Clone(value.state.values)
	maps.Copy(state.values, owned)
	return ModelInput[M]{state: &state}, nil
}

func (m Manager[M]) checkInput(value ModelInput[M]) error {
	if value.state == nil || m.prepared == nil || !value.state.model.Equal(m.prepared.metadata) {
		return invalidWritePlan("model input source does not match manager metadata")
	}
	return nil
}
