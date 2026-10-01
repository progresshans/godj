package sqlite

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/identitytest"
	"github.com/progresshans/godj/internal/iexacttest"
	"github.com/progresshans/godj/query"
)

func TestSQLiteIExactBindsEscapedCompleteLiteral(t *testing.T) {
	field := query.NewFieldRef("title", "title", query.FieldString, true)
	plan, err := query.NewPlan("records", []query.FieldRef{field}).WithConditions(query.NewCondition(field, query.LookupIExact, query.String(`50%_\'`)))
	if err != nil {
		t.Fatal(err)
	}
	sql, args, err := Compile(plan)
	if err != nil || !strings.Contains(sql, `"title" LIKE ? ESCAPE '\'`) || !reflect.DeepEqual(args, []any{`50\%\_\\'`}) || strings.Contains(sql, "50") {
		t.Fatal("iexact literal not bound/escaped", sql, args, err)
	}
}

func TestSQLiteIExactMatchesDjango(t *testing.T) {
	backend, err := OpenMemory(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, sql := range iexacttest.Tables("") {
		if _, err := backend.database.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	iexacttest.RunQueries(t, backend, "sqlite")
}

func TestSQLiteIdentityCreationUsernamePolicy(t *testing.T) {
	identitytest.RunCreationUsernamePolicy(t, openSQLiteIdentityPair, "sqlite")
}
