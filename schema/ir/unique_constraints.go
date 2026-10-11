package ir

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/progresshans/godj/internal/identifiers"
)

// UniqueConstraint is a named, immediate constraint over ordered logical
// fields. Physical column mapping and native ownership belong to the backend.
// Any SQL NULL member makes a tuple distinct under the supported policy.
type UniqueConstraint struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

func (constraint UniqueConstraint) Clone() UniqueConstraint {
	constraint.Fields = slices.Clone(constraint.Fields)
	return constraint
}

func (constraint UniqueConstraint) Equal(other UniqueConstraint) bool {
	return constraint.Name == other.Name && slices.Equal(constraint.Fields, other.Fields)
}

func normalizeUniqueConstraints(model *Model, path string) error {
	if len(model.UniqueConstraints) == 0 {
		model.UniqueConstraints = nil
		return nil
	}
	fields := make(map[string]struct{}, len(model.Fields))
	for _, field := range model.Fields {
		fields[field.Name] = struct{}{}
	}
	names := make(map[string]struct{}, len(model.UniqueConstraints))
	for index, constraint := range model.UniqueConstraints {
		constraintPath := fmt.Sprintf("%s.unique_constraints[%d]", path, index)
		if !identifiers.SQL(constraint.Name) {
			return validation(constraintPath+".name", "invalid_identifier", constraint.Name)
		}
		if duplicate(names, constraint.Name) {
			return validation(constraintPath+".name", "duplicate", constraint.Name)
		}
		if len(constraint.Fields) == 0 {
			return validation(constraintPath+".fields", "empty", "at least one field is required")
		}
		members := make(map[string]struct{}, len(constraint.Fields))
		for position, name := range constraint.Fields {
			fieldPath := fmt.Sprintf("%s.fields[%d]", constraintPath, position)
			if !identifiers.SQL(name) {
				return validation(fieldPath, "invalid_identifier", name)
			}
			if duplicate(members, name) {
				return validation(fieldPath, "duplicate", name)
			}
			if _, exists := fields[name]; !exists {
				return validation(fieldPath, "unknown_field", name)
			}
		}
	}
	// Declaration-list order does not change constraint identity. Each member
	// list remains ordered because it defines the physical index key order.
	slices.SortFunc(model.UniqueConstraints, func(left, right UniqueConstraint) int {
		return cmp.Compare(left.Name, right.Name)
	})
	return nil
}
