package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/conformance/nullableforwardproduct"
)

func TestPostgreSQLNullableForwardBooleanPredicatesMatchDjango(t *testing.T) {
	databaseURL := postgresIntegrationURL(t)
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(redactConnectionError(err))
	}
	schema := fmt.Sprintf("godj_nullable_forward_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Error(redactConnectionError(err))
		}
	})
	author := quoted + `."nullable_reference_author"`
	post := quoted + `."nullable_reference_post"`
	for _, statement := range []string{
		`CREATE TABLE ` + author + `(id BIGINT PRIMARY KEY, name TEXT NOT NULL, rank BIGINT NOT NULL, bio TEXT NOT NULL, seen_at TIMESTAMP WITH TIME ZONE NOT NULL)`,
		`CREATE TABLE ` + post + `(id BIGINT PRIMARY KEY, title TEXT NOT NULL, author_id BIGINT NOT NULL REFERENCES ` + author + `(id), reviewer_id BIGINT NULL REFERENCES ` + author + `(id))`,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	first := time.Date(2026, 9, 19, 0, 0, 0, 123456000, time.UTC)
	second := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	for _, row := range [][]any{{int64(1), "Ada", int64(0), " space\nline ", first}, {int64(2), "Bob", int64(-1), "plain", second}, {int64(3), "Cleo", int64(9223372036854775807), "", first}} {
		if _, err := admin.Exec(ctx, `INSERT INTO `+author+` VALUES($1,$2,$3,$4,$5)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range [][]any{{int64(1), "keep", int64(1), nil}, {int64(2), "drop", int64(2), nil}, {int64(3), "keep", int64(1), int64(1)}, {int64(4), "drop", int64(2), int64(1)}, {int64(5), "keep", int64(1), int64(2)}, {int64(6), "drop", int64(2), int64(2)}, {int64(7), "keep", int64(3), int64(3)}} {
		if _, err := admin.Exec(ctx, `INSERT INTO `+post+` VALUES($1,$2,$3,$4)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	backend, err := Open(ctx, Config{URL: databaseURL, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
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
			ids, count, err := nullableforwardproduct.Evaluate(ctx, backend, plan)
			if err != nil || !reflect.DeepEqual(ids, observation.IDs) || count != observation.Count {
				t.Fatalf("ids=%v count=%d err=%v", ids, count, err)
			}
			statement, _, err := compilePlan(schema, plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(observation.Name, "reviewer_id") && strings.Count(statement, "LEFT OUTER JOIN") != strings.Count(observation.SQL[0], "LEFT OUTER JOIN") {
				t.Fatalf("JOIN semantics differ: %s", statement)
			}
		})
	}
}
