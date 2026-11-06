package sqlite

import (
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/schema/ir"
)

// The existing scalar-target fixtures deliberately reuse one Author/Target
// model. Carry each retained FK explicitly in the current intent. Tests for
// different or nested targets construct their graph bindings independently.
func sqliteRelationTestMutationTargets(before, after ir.Model, kind migrationbackend.MigrationOperationKind, target migrationbackend.MigrationTarget) []migrationbackend.MigrationTarget {
	retained, boundary := before, after
	if kind == migrationbackend.MigrationRemoveField {
		retained, boundary = after, before
	}
	if len(boundary.Fields) == 0 || boundary.Fields[len(boundary.Fields)-1].Kind != ir.FieldForeignKey {
		return []migrationbackend.MigrationTarget{target}
	}
	targets := make([]migrationbackend.MigrationTarget, 0)
	for _, field := range retained.Fields {
		if field.Kind == ir.FieldForeignKey {
			targets = append(targets, migrationbackend.MigrationTarget{SourceField: field,
				TargetModel: target.TargetModel, TargetKey: target.TargetKey})
		}
	}
	return append(targets, target)
}
