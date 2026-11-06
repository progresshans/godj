package postgres

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/internal/identitytest"
	"github.com/progresshans/godj/internal/iexacttest"
	"github.com/progresshans/godj/query"
)

func TestPostgresIExactBindsUnescapedCompleteLiteral(t *testing.T) {
	field := query.NewFieldRef("title", "title", query.FieldString, true)
	plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupIExact, query.String(`50%_\'`)))
	if err != nil {
		t.Fatal(err)
	}
	sql, args, err := compilePlan("app", plan)
	if err != nil || !strings.Contains(sql, `UPPER("title"::text) = UPPER($1)`) || !reflect.DeepEqual(args, []any{`50%_\'`}) || strings.Contains(sql, "50") {
		t.Fatal("iexact literal was escaped or not bound", sql, args, err)
	}
}

func TestPostgresIExactMatchesDjango(t *testing.T) {
	url := postgresIntegrationURL(t)
	namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
	backend := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
	for _, sql := range iexacttest.Tables(pgx.Identifier{namespace}.Sanitize() + ".") {
		if _, err := backend.database.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	iexacttest.RunQueries(t, backend, "postgres")
}

func TestPostgresIdentityCreationUsernamePolicy(t *testing.T) {
	url := postgresIntegrationURL(t)
	identitytest.RunCreationUsernamePolicy(t, func(t *testing.T) (identitytest.TransitionBackend, identitytest.TransitionBackend) {
		namespace := postgresMigrationIntegrationSchema(t, t.Context(), url)
		first := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		second := openPostgresMigrationIntegrationBackend(t, t.Context(), url, namespace)
		first.database.SetMaxOpenConns(1)
		second.database.SetMaxOpenConns(1)
		return first, second
	}, "postgres")
}
