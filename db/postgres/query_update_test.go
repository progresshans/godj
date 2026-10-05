package postgres

import (
	"github.com/progresshans/godj/internal/queryupdatetest"
	"github.com/progresshans/godj/query"
	"testing"
)

func TestPostgresQueryUpdateCompiler(t *testing.T) {
	queryupdatetest.CheckCompiler(t, func(plan query.QueryUpdatePlan) (string, []any, error) { return compileQueryUpdate("app", plan) }, true)
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
