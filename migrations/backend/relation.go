package backend

import (
	"context"

	"github.com/progresshans/godj/schema/ir"
)

// RelationMigrationCapabilities reports the relation-aware schema changes a
// backend can perform atomically with revision-fenced migration history.
type RelationMigrationCapabilities struct {
	CreateModelForeignKeys            bool
	AddNullableForeignKey             bool
	AddRequiredForeignKeyToEmptyTable bool
	RemoveForeignKeyByTableRemake     bool
}

// RelationRevisionFencedBackend is the optional backend port for complete,
// relation-aware migration intents. Core may use it only after constructing a
// validated intent; implementing this interface does not alter the legacy
// revision-fenced entry point.
type RelationRevisionFencedBackend interface {
	RevisionFencedBackend
	RelationMigrationCapabilities() RelationMigrationCapabilities
}

// RelationRevisionFencedSession begins a relation-aware migration on the same
// revision-fenced session that supplied its applied-history snapshot.
type RelationRevisionFencedSession interface {
	RevisionFencedSession
	BeginRelationFencedMigration(context.Context, HistoryTransition, RelationMigrationIntent) (RevisionFencedTransaction, error)
}

// RelationMigrationOperationKind identifies one operation in the complete
// migration-step intent supplied to a relation-aware backend.
type RelationMigrationOperationKind uint8

const (
	RelationMigrationCreateModel RelationMigrationOperationKind = iota + 1
	RelationMigrationDeleteModel
	RelationMigrationAddField
	RelationMigrationRemoveField
)

// RelationMigrationIntent carries a complete, ordered migration step.
type RelationMigrationIntent struct {
	Operations []RelationMigrationOperation
}

// RelationMigrationOperation carries the exact before/after model snapshots
// and relation targets for one operation in a migration step.
type RelationMigrationOperation struct {
	OperationIndex int
	Kind           RelationMigrationOperationKind
	Before         ir.Model
	After          ir.Model
	Targets        []RelationMigrationTarget
}

// RelationMigrationTarget binds a source ForeignKey field to the exact target
// model and historical target key used by the migration step.
type RelationMigrationTarget struct {
	SourceField ir.Field
	TargetModel ir.Model
	TargetKey   ir.Field
}
