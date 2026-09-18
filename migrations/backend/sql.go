package backend

import "context"

// ForwardMigrationSQLRequest carries one complete, ordered forward migration
// intent together with its loader-owned identity. Renderers must treat the
// request as immutable and return ordered semicolon-free statement bodies.
// Current CreateModel/AddField each produce one body; choices-only AlterField
// produces none while remaining part of the complete validated intent.
type ForwardMigrationSQLRequest struct {
	App    string
	Name   string
	Intent MigrationIntent
}

// MigrationSQLRenderer projects one forward migration intent to SQL without
// opening a database, reading applied history, or beginning a transaction.
// Implementations are cooperative with context cancellation.
type MigrationSQLRenderer interface {
	RenderForwardMigrationSQL(context.Context, ForwardMigrationSQLRequest) ([]string, error)
}
