package postgres

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/internal/queryupdatetest"
	"strings"
	"testing"
)

func TestPostgresDurationScaling(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.CheckDurationScaling(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true, func(t *testing.T, err error) {
		t.Helper()
		var native *pgconn.PgError
		if !errors.As(err, &native) || !strings.HasPrefix(native.Code, "22") {
			t.Fatal("invalid duration arithmetic lost its native data exception", err)
		}
	})
}
