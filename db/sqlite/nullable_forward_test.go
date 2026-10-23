package sqlite_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/conformance/nullableforwardproduct"
	"github.com/progresshans/godj/db/sqlite"
)

func TestSQLiteNullableForwardBooleanPredicatesMatchDjango(t *testing.T) {
	ctx := t.Context()
	backend, err := sqlite.OpenMemory(ctx, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE nullable_reference_author(id INTEGER PRIMARY KEY, name TEXT NOT NULL, rank INTEGER NOT NULL, bio TEXT NOT NULL, seen_at DATETIME NOT NULL)`,
		`CREATE TABLE nullable_reference_post(id INTEGER PRIMARY KEY, title TEXT NOT NULL, author_id INTEGER NOT NULL REFERENCES nullable_reference_author(id), reviewer_id INTEGER NULL REFERENCES nullable_reference_author(id))`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	first := time.Date(2026, 9, 19, 0, 0, 0, 123456000, time.UTC).Format("2006-01-02 15:04:05.000000")
	second := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05.000000")
	for _, row := range [][]any{{1, "Ada", int64(0), " space\nline ", first}, {2, "Bob", int64(-1), "plain", second}, {3, "Cleo", int64(9223372036854775807), "", first}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO nullable_reference_author VALUES(?,?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{{1, "keep", 1, nil}, {2, "drop", 2, nil}, {3, "keep", 1, 1}, {4, "drop", 2, 1}, {5, "keep", 1, 2}, {6, "drop", 2, 2}, {7, "keep", 3, 3}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO nullable_reference_post VALUES(?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("../../orm/testdata/nullable-forward-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference nullableforwardproduct.Reference
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 73 {
		t.Fatal("reference roster incomplete")
	}
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			plan, err := nullableforwardproduct.Plan(reference.Leaves, observation.Expression)
			if err != nil {
				t.Fatal(err)
			}
			before := backend.QueryCount()
			ids, count, err := nullableforwardproduct.Evaluate(ctx, backend, plan)
			if err != nil || !reflect.DeepEqual(ids, observation.IDs) || count != observation.Count || backend.QueryCount() != before+2 {
				t.Fatalf("ids=%v count=%d err=%v", ids, count, err)
			}
			statement, _, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatal(err)
			}
			// Target-PK lookup JOIN elision is not required. Named target
			// fields must use the observed optional/required join semantics.
			if !strings.Contains(observation.Name, "reviewer_id") && strings.Count(statement, "LEFT OUTER JOIN") != strings.Count(observation.SQL[0], "LEFT OUTER JOIN") {
				t.Fatalf("JOIN semantics differ: %s", statement)
			}
		})
	}
}
