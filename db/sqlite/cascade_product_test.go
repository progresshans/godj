package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/internal/cascadetest"
)

func TestSQLiteCascadeGeneratedDeletionMatchesDjango(t *testing.T) {
	backend := openMigrationHistoryFileBackend(t, filepath.Join(t.TempDir(), "cascade-product.sqlite"))
	cascadetest.RunReference(t, backend, "sqlite", func(ctx context.Context) error {
		transaction, err := backend.database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer transaction.Rollback()
		for _, statement := range []string{
			`INSERT INTO cascade_required_left (id,right_id) VALUES (101,202)`,
			`INSERT INTO cascade_required_right (id,left_id) VALUES (202,101)`,
		} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return transaction.Commit()
	})
}
