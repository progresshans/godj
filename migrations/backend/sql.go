package backend

import "context"

// ForwardMigrationSQLRequest carries one complete, ordered forward migration
// intent together with its loader-owned identity. Renderers must treat the
// request as immutable and must return one semicolon-free statement body for
// every operation in Intent.
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
