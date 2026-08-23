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
// migration-step intent supplied to a relation-aware backend.
type MigrationOperationKind uint8

const (
	MigrationCreateModel MigrationOperationKind = iota + 1
	MigrationDeleteModel
	MigrationAddField
	MigrationRemoveField
)

// MigrationIntent carries a complete, ordered migration step. Scalar
// operations use the same shape with an empty Targets slice.
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
