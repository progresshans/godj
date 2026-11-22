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
	AlterFieldStringSemantics         bool
	// AlterFieldBlank covers stored and columnless model input policy changes.
	AlterFieldBlank bool
	// AlterFieldInputPolicy covers editable and Binary input-length metadata.
	AlterFieldInputPolicy      bool
	AlterFieldDecimalPrecision bool
	AlterFieldRelation         bool
	// AlterFieldNullability covers tightening or relaxing a column and applying
	// its explicit scalar default to existing NULLs when tightening.
	AlterFieldNullability bool
	// DefinitionHistory binds a recorder write to the loaded migration and its
	// declared ancestors in the same transaction as the migration body.
	DefinitionHistory bool
	// HistoricalData runs application code with row access to the operation's
	// historical models on the same connection and transaction as its recorder.
	HistoricalData bool
	RenameField    bool
	// UniqueConstraints covers mutation and physical verification of declared
	// field and named model uniqueness, including retained target and transitive models.
	UniqueConstraints bool
	// ColumnIndexes covers declared ordinary column indexes and their retained
	// catalog ownership, including uniqueness transitions that replace an index.
	ColumnIndexes bool
	// ExplicitManyToMany admits columnless changes using existing through models.
	// Automatic intermediary creation/removal/rename needs separate storage support.
	ExplicitManyToMany bool
	// AutomaticManyToMany covers derived link storage and retained catalog validation.
	AutomaticManyToMany bool
}

// The public backend contract shares the pure historical metadata types with
// the state reconstructor. Database sessions and durability remain in backend.
type MigrationOperationKind = migrationgraph.MigrationOperationKind
type MigrationIntent = migrationgraph.MigrationIntent
type MigrationOperation = migrationgraph.MigrationOperation
type MigrationTarget = migrationgraph.MigrationTarget
type MigrationModel = migrationgraph.MigrationModel
type MigrationModelChange = migrationgraph.ModelChange

const (
	MigrationAlterManyToMany  = migrationgraph.MigrationAlterManyToMany
	MigrationRunData          = migrationgraph.MigrationRunData
	MigrationRenameField      = migrationgraph.MigrationRenameField
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
