package backend

import (
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

// UniqueConstraintFields resolves ordered logical members to owned field
// snapshots. A constraint being added need not be in the source model yet.
// Model normalization and resource admission precede physical compilation.
func UniqueConstraintFields(model ir.Model, constraint ir.UniqueConstraint) ([]ir.Field, error) {
	if len(constraint.Fields) == 0 {
		return nil, fmt.Errorf("unique constraint %q has no members", constraint.Name)
	}
	fields := make(map[string]ir.Field, len(model.Fields))
	for _, field := range model.Fields {
		fields[field.Name] = field
	}
	result := make([]ir.Field, len(constraint.Fields))
	seen := make(map[string]bool, len(constraint.Fields))
	for index, name := range constraint.Fields {
		field, exists := fields[name]
		if !exists || seen[name] {
			return nil, fmt.Errorf("unique constraint %q has a missing or repeated logical member %q", constraint.Name, name)
		}
		seen[name] = true
		result[index] = field.Clone()
	}
	return result, nil
}
