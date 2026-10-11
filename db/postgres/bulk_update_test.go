package postgres

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/internal/bulkupdatetest"
	"github.com/progresshans/godj/query"
)

func TestPostgresBulkUpdateCompiler(t *testing.T) {
	bulkupdatetest.CheckCompiler(t, func(plan query.BulkUpdatePlan) (string, []any, error) { return compileBulkUpdate("app", plan) }, `"app"."update_items"`, `SELECT "id" FROM "app"."update_items" WHERE ("name" = $1)`, true, postgresBulkParameters)
	plan := bulkupdatetest.Plan(t, bulkupdatetest.Source(), []query.FieldRef{bulkupdatetest.Amount}, []int64{1}, [][]query.Value{{query.Integer(2)}})
	if statement, args, err := compileBulkUpdate(`wrong"schema`, plan); statement != "" || args != nil || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("invalid schema reached SQL", statement, args, err)
	}
}

func TestPostgresNativeBulkUpdateAndScope(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	quote := func(name string) string {
		t.Helper()
		value, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	parent, labels, table, links, function := quote("godj_bulk_targets"), quote("godj_bulk_targets_0"), quote("update_items"), quote("update_links"), quote("update_skip")
	for _, statement := range []string{
		"CREATE TABLE " + parent + " (id BIGINT PRIMARY KEY, name TEXT NOT NULL)",
		"INSERT INTO " + parent + " VALUES (1,'allowed'),(2,'denied')",
		"CREATE TABLE " + labels + " (id BIGINT PRIMARY KEY, name TEXT NOT NULL)",
		"INSERT INTO " + labels + " VALUES (1,'red'),(2,'blue')",
		"CREATE TABLE " + table + " (id BIGINT PRIMARY KEY, name TEXT NOT NULL UNIQUE, amount BIGINT NOT NULL CHECK(amount>=0), parent_id BIGINT REFERENCES " + parent + "(id) DEFERRABLE INITIALLY DEFERRED, note TEXT)",
		"CREATE TABLE " + links + " (id BIGINT PRIMARY KEY, owner_id BIGINT NOT NULL REFERENCES " + table + "(id), label_id BIGINT NOT NULL REFERENCES " + labels + "(id))",
		"CREATE FUNCTION " + function + "() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN IF NEW.name=''skip-native'' THEN RETURN NULL; END IF; RETURN NEW; END'",
		"CREATE TRIGGER update_skip BEFORE UPDATE ON " + table + " FOR EACH ROW EXECUTE FUNCTION " + function + "()",
	} {
		if _, err := backend.database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	bulkupdatetest.Check(t, backend, backend.database, table, links, true)
}

func TestPostgresNativeBulkUpdateScalarCodecs(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	bulkupdatetest.CheckCodecs(t, backend, backend.database, func(name string) string {
		quoted, err := quoteTable(namespace, name)
		if err != nil {
			t.Fatal(err)
		}
		return quoted
	}, true)
}
