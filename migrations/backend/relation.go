package backend

import "github.com/progresshans/godj/internal/migrationgraph"

// MigrationCapabilities reports the schema changes a backend can perform
// atomically with revision-fenced migration history.
type MigrationCapabilities struct {
	CreateModelForeignKeys            bool
	AddNullableForeignKey             bool
	AddRequiredForeignKeyToEmptyTable bool
	RemoveForeignKey                  bool
	AlterFieldChoices                 bool
	AlterFieldDecimalPrecision        bool
	AlterFieldRelation                bool
	// UniqueConstraints covers mutation and physical verification of declared
	// field and named model uniqueness, including retained target and transitive models.
	UniqueConstraints bool
	// ExplicitManyToMany admits columnless changes using existing through models.
	// Automatic intermediary creation/removal/rename needs separate storage support.
	ExplicitManyToMany bool
}

// The public backend contract shares the pure historical metadata types with
// the state reconstructor. Database sessions and durability remain in backend.
type MigrationOperationKind = migrationgraph.MigrationOperationKind
type MigrationIntent = migrationgraph.MigrationIntent
type MigrationOperation = migrationgraph.MigrationOperation
type MigrationTarget = migrationgraph.MigrationTarget
type MigrationModel = migrationgraph.MigrationModel

const (
	MigrationAlterManyToMany  = migrationgraph.MigrationAlterManyToMany
	MigrationCreateModel      = migrationgraph.MigrationCreateModel
	MigrationDeleteModel      = migrationgraph.MigrationDeleteModel
	MigrationAddField         = migrationgraph.MigrationAddField
	MigrationRemoveField      = migrationgraph.MigrationRemoveField
	MigrationAlterField       = migrationgraph.MigrationAlterField
	MigrationAddConstraint    = migrationgraph.MigrationAddConstraint
	MigrationRemoveConstraint = migrationgraph.MigrationRemoveConstraint
)

func CloneMigrationModels(models []MigrationModel) []MigrationModel {
	return migrationgraph.CloneMigrationModels(models)
}

func CloneMigrationTargets(targets []MigrationTarget) []MigrationTarget {
	return migrationgraph.CloneMigrationTargets(targets)
}
