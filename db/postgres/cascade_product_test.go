package postgres

import (
	"context"
	"testing"

	"github.com/progresshans/godj/internal/cascadetest"
)

func TestPostgresCascadeGeneratedDeletionMatchesDjango(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	cascadetest.RunReference(t, backend, "postgres", func(ctx context.Context) error {
		transaction, err := backend.database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer transaction.Rollback()
		left, err := quoteTable(namespace, "cascade_required_left")
		if err != nil {
			return err
		}
		right, err := quoteTable(namespace, "cascade_required_right")
		if err != nil {
			return err
		}
		for _, statement := range []string{"INSERT INTO " + left + " (id,right_id) VALUES (101,202)", "INSERT INTO " + right + " (id,left_id) VALUES (202,101)"} {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		return transaction.Commit()
	})
}
