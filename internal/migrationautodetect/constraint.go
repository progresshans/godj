package migrationautodetect

import (
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func constraintChanges(app string, before, after ir.Model) (removed, added []migrations.Operation) {
	old := make(map[string]ir.UniqueConstraint, len(before.UniqueConstraints))
	current := make(map[string]ir.UniqueConstraint, len(after.UniqueConstraints))
	for _, constraint := range before.UniqueConstraints {
		old[constraint.Name] = constraint
	}
	for _, constraint := range after.UniqueConstraints {
		current[constraint.Name] = constraint
	}
	for _, constraint := range before.UniqueConstraints {
		if value, exists := current[constraint.Name]; !exists || !value.Equal(constraint) {
			removed = append(removed, migrations.RemoveConstraint{AppLabel: app, ModelName: before.Name, Constraint: constraint.Clone()})
		}
	}
	for _, constraint := range after.UniqueConstraints {
		if value, exists := old[constraint.Name]; !exists || !value.Equal(constraint) {
			added = append(added, migrations.AddConstraint{AppLabel: app, ModelName: after.Name, Constraint: constraint.Clone()})
		}
	}
	return removed, added
}

func constraintMembersPresent(constraint ir.UniqueConstraint, available map[string]bool) bool {
	for _, name := range constraint.Fields {
		if !available[name] {
			return false
		}
	}
	return true
}
