package database

import (
	"context"
	"errors"

	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

// Check explicitly performs I/O against the selected database and closes every
// connection it opens. SQLite uses an existing file in read-only mode; PostgreSQL
// uses the same connection/profile/schema validation as Open. No migration,
// account creation or repair is performed. This is not model/schema drift or
// whole-database integrity verification.
func Check(ctx context.Context, config Config) error {
	if ctx == nil {
		return &Error{Field: "context", Detail: "is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch config.kind {
	case KindSQLite:
		if err := sqlite.Check(ctx, config.sqliteDatabase); err != nil {
			if canceled := ctx.Err(); canceled != nil {
				return canceled
			}
			if errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
				return &Error{Field: "sqlite_database", Detail: "read-only checks do not support these connection options"}
			}
			return &Error{Field: "database", Detail: "reading the existing SQLite database failed"}
		}
		return nil
	case KindPostgres:
		backend, err := Open(ctx, config)
		if err != nil {
			return err
		}
		if err := backend.Close(); err != nil {
			return &Error{Field: "database", Detail: "closing the checked backend failed"}
		}
		return ctx.Err()
	default:
		return &Error{Field: "database", Detail: "no database was selected"}
	}
}
