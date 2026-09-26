package backend

import "context"

// ForwardMigrationSQLRequest carries one complete, ordered forward migration
// intent together with its loader-owned identity. Renderers must treat the
// request as immutable. The result has exactly one statement group per
// operation, in the same order. Each group contains zero or more ordered,
// semicolon-free, nonempty statement bodies. An operation without physical SQL
// has an empty group, preserving its identity in mixed migration plans.
// CreateModel/AddField and uniqueness changes require at least one statement;
// choices-only AlterField requires an empty group. Decimal precision changes
// use an empty SQLite group or a PostgreSQL ALTER statement. Relation delete
// policy changes may require backend-specific constraint edits or a table remake.
type ForwardMigrationSQLRequest struct {
	App    string
	Name   string
	Intent MigrationIntent
}

// MigrationSQLRenderer projects one forward migration intent to SQL without
// opening a database, reading applied history, or beginning a transaction.
// Implementations are cooperative with context cancellation.
type MigrationSQLRenderer interface {
	RenderForwardMigrationSQL(context.Context, ForwardMigrationSQLRequest) ([][]string, error)
}
