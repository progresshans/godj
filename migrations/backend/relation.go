package backend

import "github.com/progresshans/godj/schema/ir"

// MigrationCapabilities reports the schema changes a backend can perform
// atomically with revision-fenced migration history.
type MigrationCapabilities struct {
	CreateModelForeignKeys            bool
	AddNullableForeignKey             bool
	AddRequiredForeignKeyToEmptyTable bool
	RemoveForeignKey                  bool
}

// MigrationOperationKind identifies one operation in the complete
// migration-step intent supplied to a relation-aware backend or pure SQL
// renderer.
type MigrationOperationKind uint8

const (
	MigrationCreateModel MigrationOperationKind = iota + 1
	MigrationDeleteModel
	MigrationAddField
	MigrationRemoveField
)

// MigrationIntent carries a complete, ordered migration step. Scalar
// operations use the same shape with an empty Targets slice. Mutation
// backends and pure SQL renderers consume detached copies of the same intent.
type MigrationIntent struct {
	Operations []MigrationOperation
}

// MigrationOperation carries the exact before/after model snapshots
// and relation targets for one operation in a migration step.
type MigrationOperation struct {
	OperationIndex int
	Kind           MigrationOperationKind
	Before         ir.Model
	After          ir.Model
	Targets        []MigrationTarget
}

// MigrationTarget binds a source ForeignKey field to the exact target
// model and historical target key used by the migration step.
type MigrationTarget struct {
	SourceField ir.Field
	TargetModel ir.Model
	TargetKey   ir.Field
}

// Clone returns a detached execution-intent snapshot. Nil and empty slices
// remain distinct because the intent validators use nil as a missing value.
func (intent MigrationIntent) Clone() MigrationIntent {
	if intent.Operations == nil {
		return MigrationIntent{}
	}
	clone := MigrationIntent{Operations: make([]MigrationOperation, len(intent.Operations))}
	for index, operation := range intent.Operations {
		operation.Before = cloneIntentModel(operation.Before)
		operation.After = cloneIntentModel(operation.After)
		operation.Targets = CloneMigrationTargets(operation.Targets)
		clone.Operations[index] = operation
	}
	return clone
}

func CloneMigrationTargets(targets []MigrationTarget) []MigrationTarget {
	if targets == nil {
		return nil
	}
	cloned := make([]MigrationTarget, len(targets))
	for index, target := range targets {
		cloned[index] = MigrationTarget{
			SourceField: target.SourceField.Clone(),
			TargetModel: cloneIntentModel(target.TargetModel),
			TargetKey:   target.TargetKey.Clone(),
		}
	}
	return cloned
}

func cloneIntentModel(model ir.Model) ir.Model {
	clone := model
	if model.Fields != nil {
		clone.Fields = make([]ir.Field, len(model.Fields))
		for index, field := range model.Fields {
			clone.Fields[index] = field.Clone()
		}
	}
	return clone
}
