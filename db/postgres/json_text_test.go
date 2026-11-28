package postgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestPostgresJSONTextCompilationKeepsNativeTypesAndPreflightsEmptyQueries(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	payload := query.NewFieldRef("payload", "payload", query.FieldJSON, true)
	path, _ := query.NewJSONPath(query.JSONKey("source"))
	for _, nested := range []bool{false, true} {
		condition := query.NewCondition(payload, query.LookupIContains, query.String(`50%_\`))
		if nested {
			var err error
			condition, err = condition.WithJSONPath(path)
			if err != nil {
				t.Fatal(err)
			}
		}
		plan, err := query.NewPlan("records", []query.FieldRef{id, payload}).WithConditions(condition)
		if err != nil {
			t.Fatal(err)
		}
		sql, args, err := compilePlan("app", plan)
		if err != nil {
			t.Fatal(err)
		}
		want := []any{`%50\%\_\\%`}
		if nested {
			want = append([]any{`strict $."source"`}, want...)
		}
		if !reflect.DeepEqual(args, want) || !strings.Contains(sql, "UPPER(") || !strings.Contains(sql, " LIKE UPPER(") {
			t.Fatal("JSON native text parameters", sql, args, want)
		}
		if nested && !strings.Contains(sql, ` #>> '{}'::text[]`) || !nested && !strings.Contains(sql, `UPPER("payload"::text)`) {
			t.Fatal("root/path text domain changed", sql)
		}
	}
	bad := query.NewCondition(payload, query.LookupIContains, query.String("a\x00b"))
	base, err := query.NewPlan("records", []query.FieldRef{id, payload}).WithConditions(bad)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := base.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	emptyCondition, err := query.NewInCondition(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := base.WithConditions(bad, emptyCondition)
	if err != nil {
		t.Fatal(err)
	}
	aggregate, _ := query.NewAggregateResult(query.CountAllResult())
	for _, plan := range []query.Plan{base, zero, empty} {
		if _, _, err := compilePlan("app", plan); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal("native NUL text reached empty I/O", err)
		}
		count, err := plan.WithResultShape(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := compilePlan("app", count); !errors.Is(err, &query.Error{Code: query.CodeInvalidValue}) {
			t.Fatal("native NUL text reached Count", err)
		}
	}
}
