package orm

import (
	"slices"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// Metadata returns a detached copy of the manager's construction-time policy.
// It performs no I/O and is not permission to read or write model instances.
func (m Manager[M]) Metadata() (ir.Model, error) {
	if m.prepared == nil {
		return ir.Model{}, invalidWritePlan("descriptor is nil")
	}
	return m.prepared.metadata.Clone(), nil
}

// ModelValues reads an owned scalar snapshot without database I/O. An absent
// primary key is SQL NULL; a present zero key remains an integer zero. Relation
// collections are not concrete model fields and require their own read scope.
func (m Manager[M]) ModelValues(value M) (map[string]query.Value, error) {
	descriptor, err := m.modelValueDescriptor()
	if err != nil {
		return nil, err
	}
	snapshot := descriptor.CloneWriteModel(value)
	result := make(map[string]query.Value, len(m.prepared.metadata.Fields))
	for _, field := range m.prepared.metadata.Fields {
		var scalar query.Value
		var valid bool
		if field.PrimaryKey {
			scalar, valid = descriptor.PrimaryKey(snapshot)
			if !valid {
				result[field.Name] = query.Null()
				continue
			}
		} else {
			scalar, valid = descriptor.WriteFieldValue(snapshot, field.Clone())
		}
		if !valid || !mutationValueMatches(field, scalar) {
			return nil, invalidModelValue(field.Name, "model snapshot has an invalid field representation")
		}
		result[field.Name] = scalar
	}
	return result, nil
}

// ApplyValues prepares a detached typed model from explicitly supplied scalar
// changes. It performs no I/O, runs no model/form validators and grants no write
// permission. Unknown fields, primary keys and unrepresentable NULL/type values
// fail before assignment. Defaults remain the caller's candidate responsibility.
func (m Manager[M]) ApplyValues(current M, values map[string]query.Value) (M, error) {
	var zero M
	write, err := m.modelValueDescriptor()
	if err != nil {
		return zero, err
	}
	descriptor, ok := write.(ValueAssignmentDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return zero, invalidWritePlan("descriptor does not implement model value assignment")
	}
	// Snapshot and validate all caller entries before invoking assignment code.
	// Error order and application order do not depend on Go map iteration.
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	owned := make(map[string]query.Value, len(values))
	for _, name := range names {
		index, found := m.prepared.byName[name]
		if !found || m.prepared.metadata.Fields[index].PrimaryKey {
			return zero, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: name, Detail: "model value field is not writable metadata"}
		}
		field := m.prepared.metadata.Fields[index]
		value := values[name]
		if !mutationValueMatches(field, value) {
			return zero, invalidModelValue(name, "model input does not match field type or nullability")
		}
		owned[name] = value
	}
	baseline := descriptor.CloneWriteModel(current)
	result := descriptor.CloneWriteModel(current)
	if len(owned) == 0 {
		return result, nil
	}
	assignments := make([]query.Assignment, 0, len(owned))
	for _, field := range m.prepared.metadata.Fields {
		value, present := owned[field.Name]
		if !present {
			continue
		}
		if !descriptor.SetFieldValue(&result, field.Clone(), value) {
			return zero, invalidModelValue(field.Name, "descriptor rejected model value assignment")
		}
		assignments = append(assignments, NewAssignment(field, value))
	}
	mutation := NewPatchMutation(result, m.prepared.metadata.DBTable, assignments)
	if err := validateMutation(mutation, MutationPatch, m.prepared, descriptor, &baseline); err != nil {
		return zero, err
	}
	beforeKey, beforePresent := descriptor.PrimaryKey(baseline)
	afterKey, afterPresent := descriptor.PrimaryKey(result)
	if beforePresent != afterPresent || !beforeKey.Equal(afterKey) {
		return zero, invalidWritePlan("model assignment changed primary key state")
	}
	return descriptor.CloneWriteModel(result), nil
}

func (m Manager[M]) modelValueDescriptor() (WriteDescriptor[M], error) {
	if m.prepared == nil || !m.prepared.writeValid {
		return nil, invalidWritePlan("model values require writable descriptor metadata")
	}
	descriptor, ok := m.descriptor.(WriteDescriptor[M])
	if !ok || interfaceIsNil(descriptor) {
		return nil, invalidWritePlan("descriptor does not implement write key state")
	}
	return descriptor, nil
}

func invalidModelValue(field, detail string) error {
	return &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field, Detail: detail}
}
