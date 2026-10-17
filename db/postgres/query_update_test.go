package postgres

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/progresshans/godj/internal/queryupdatetest"
	"github.com/progresshans/godj/query"
	"testing"
)

func TestPostgresQueryUpdateCompiler(t *testing.T) {
	queryupdatetest.CheckCompiler(t, func(plan query.QueryUpdatePlan) (string, []any, error) { return compileQueryUpdate("app", plan) }, true)
}

func TestPostgresTemporalReadsAndUpdates(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.CheckTemporal(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true, func(t *testing.T, err error) {
		t.Helper()
		var native *pgconn.PgError
		if !errors.As(err, &native) || !strings.HasPrefix(native.Code, "22") {
			t.Fatal("temporal failure lost its native data-exception SQLSTATE", err)
		}
	})
}

func TestPostgresDurationRatios(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.CheckDurationRatios(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true, func(t *testing.T, err error, state string) {
		t.Helper()
		var native *pgconn.PgError
		if !errors.As(err, &native) || native.Code != state {
			t.Fatal("ratio failure lost its native SQLSTATE", state, err)
		}
	})
}

func TestPostgresNativeQueryUpdateAndExpressions(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.Check(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true)
}
func TestPostgresQueryUpdateScalarCodecs(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	queryupdatetest.CheckCodecs(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true)
}
