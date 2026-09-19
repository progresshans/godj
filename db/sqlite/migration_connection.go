package sqlite

import (
	"context"
	"errors"
	"fmt"

	migrationbackend "github.com/progresshans/godj/migrations/backend"
)

// SQLite's generalized remake must run with FK actions suspended. Deferring
// checks still leaves DROP/rename counter failures and may execute referential
// actions. Only remakes and dropping a self-referencing table need this mode;
// ordinary create/add migrations retain immediate database enforcement.
func sqliteMigrationSuspendsForeignKeys(intent migrationbackend.MigrationIntent) bool {
	for _, operation := range intent.Operations {
		if operation.Kind == migrationbackend.MigrationRemoveField && sqliteRelationOperationChangesForeignKey(operation) {
			return true
		}
		if operation.Kind == migrationbackend.MigrationDeleteModel {
			for _, target := range operation.Targets {
				if target.TargetModel.DBTable == operation.Before.DBTable {
					return true
				}
			}
		}
	}
	return false
}

// releaseFencedMigrationConnection runs after a confirmed COMMIT/ROLLBACK or
// before BEGIN. Cleanup outlives caller cancellation but remains time-bounded.
// A setting failure never returns this connection to the application pool.
func releaseFencedMigrationConnection(
	ctx context.Context,
	connection migrationPinnedConnection,
	restoreForeignKeys bool,
	admission *relationTransactionAdmission,
) error {
	defer admission.release()
	if connection == nil {
		return nil
	}
	if restoreForeignKeys {
		cleanup, cancel := migrationDetachedCleanupContext(ctx)
		defer cancel()
		if _, err := connection.ExecContext(cleanup, `PRAGMA foreign_keys = ON`); err != nil {
			return errors.Join(fmt.Errorf("restore pinned SQLite foreign keys: %w", err), discardOrRetainMigrationConnection(connection, admission))
		}
		var enabled int
		if err := connection.QueryRowContext(cleanup, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
			return errors.Join(fmt.Errorf("read restored SQLite foreign keys: %w", err), discardOrRetainMigrationConnection(connection, admission))
		}
		if enabled != 1 {
			return errors.Join(fmt.Errorf("restored SQLite foreign keys readback is %d, want 1", enabled), discardOrRetainMigrationConnection(connection, admission))
		}
	}
	if err := connection.Close(); err != nil {
		return errors.Join(wrapCloseMigrationConnection(err), discardOrRetainMigrationConnection(connection, admission))
	}
	return nil
}

func discardOrRetainMigrationConnection(connection migrationPinnedConnection, admission *relationTransactionAdmission) error {
	defer admission.release()
	err := discardMigrationConnection(connection)
	if err != nil && admission != nil {
		// Retention is exclusive and visible to every backend entry point.
		// Backend.Close owns the retained connection if Raw could not discard it.
		err = errors.Join(err, admission.retain(connection))
	}
	return err
}
