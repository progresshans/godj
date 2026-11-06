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

func TestSQLiteForwardScalarLookupsMatchDjango(t *testing.T) {
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
		`CREATE TABLE forward_lookup_person(id INTEGER PRIMARY KEY, name TEXT NOT NULL, nickname TEXT NULL, score INTEGER NULL, bio TEXT NULL, seen_at DATETIME NULL, active BOOLEAN NOT NULL)`,
		`CREATE TABLE forward_lookup_post(id INTEGER PRIMARY KEY, title TEXT NOT NULL, author_id INTEGER NOT NULL REFERENCES forward_lookup_person(id), reviewer_id INTEGER NULL REFERENCES forward_lookup_person(id))`,
	} {
		if _, err := backend.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	first := time.Date(2026, 9, 19, 0, 0, 0, 123456000, time.UTC).Format("2006-01-02 15:04:05.000000")
	second := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC).Format("2006-01-02 15:04:05.000000")
	for _, row := range [][]any{{int64(1), "Ada", nil, nil, nil, nil, true}, {int64(2), "Bob", "", int64(0), "rate 50%_ done", first, false}, {int64(3), "Cleo", "ADA", int64(-1), "plain", second, true}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO forward_lookup_person VALUES(?,?,?,?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{{1, "keep", 1, nil}, {2, "drop", 2, nil}, {3, "keep", 1, 1}, {4, "drop", 2, 1}, {5, "keep", 1, 2}, {6, "drop", 2, 2}, {7, "keep", 3, 3}} {
		if _, err := backend.ExecContext(ctx, `INSERT INTO forward_lookup_post VALUES(?,?,?,?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile("../../orm/testdata/forward-lookups-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference nullableforwardproduct.Reference
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || len(reference.Observations) != 748 {
		t.Fatal("reference roster incomplete")
	}
	for _, observation := range reference.Observations {
		t.Run(observation.Name, func(t *testing.T) {
			plan, err := nullableforwardproduct.LookupPlan(reference.Leaves, observation.Expression)
			if err != nil {
				t.Fatal(err)
			}
			before := backend.QueryCount()
			ids, count, err := nullableforwardproduct.Evaluate(ctx, backend, plan)
			if err != nil || !reflect.DeepEqual(ids, observation.IDs) || count != observation.Count || backend.QueryCount() != before+uint64(len(observation.SQL)+len(observation.CountSQL)) {
				t.Fatalf("ids=%v count=%d err=%v", ids, count, err)
			}
			statement, _, err := sqlite.Compile(plan)
			if err != nil {
				t.Fatal(err)
			}
			// Named target lookups use the observed join semantics. Empty
			// queries are fully compiled before their I/O can be skipped.
			if len(observation.SQL) > 0 && strings.Count(statement, "LEFT OUTER JOIN") != strings.Count(observation.SQL[0], "LEFT OUTER JOIN") {
				t.Fatalf("JOIN semantics differ: %s", statement)
			}
		})
	}
}
