package sqlite

import (
	"context"
	"errors"
	"fmt"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

const readAppliedMigrationsSQL = `SELECT "godj_migrations"."app", "godj_migrations"."name" ` +
	`FROM "godj_migrations" ` +
	`ORDER BY "godj_migrations"."app", "godj_migrations"."name"`

var _ migrationbackend.AppliedMigrationReader = (*Backend)(nil)

// ReadAppliedMigrations returns a deterministic snapshot of the durable
// migration recorder. A missing recorder table is the canonical empty history
// and this read path never creates it.
func (b *Backend) ReadAppliedMigrations(ctx context.Context) (records []migrationbackend.AppliedMigration, resultErr error) {
	if b == nil || b.database == nil || b.closed.Load() {
		return nil, errors.New("read SQLite applied migrations: backend is nil or closed")
	}
	if ctx == nil {
		return nil, errors.New("read SQLite applied migrations: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read SQLite applied migrations: %w", err)
	}

	rows, err := b.database.QueryContext(ctx, readAppliedMigrationsSQL)
	if err != nil {
		if isSQLiteMissingTableError(err, migrationRecorderTable) {
			return []migrationbackend.AppliedMigration{}, nil
		}
		return nil, fmt.Errorf("read SQLite applied migrations: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			records = nil
			resultErr = errors.Join(resultErr, fmt.Errorf("close SQLite applied migration rows: %w", err))
		}
	}()

	records = make([]migrationbackend.AppliedMigration, 0)
	for rows.Next() {
		var record migrationbackend.AppliedMigration
		if err := rows.Scan(&record.App, &record.Name); err != nil {
			return nil, fmt.Errorf("scan SQLite applied migration: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite applied migrations: %w", err)
	}
	return records, nil
}
