package backend

import "context"

// ForwardMigrationSQLRequest carries one complete, ordered forward migration
// intent together with its loader-owned identity. Renderers must treat the
// request as immutable. The result has exactly one slot per operation, in the
// same order. A slot contains one semicolon-free statement body, or is empty
// when that operation has no physical SQL on this backend. CreateModel/AddField
// require a body, choices-only AlterField requires an empty slot, and Decimal
// precision changes use an empty SQLite slot or a PostgreSQL ALTER statement.
// Keeping empty slots preserves operation identity in mixed migration plans.
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
