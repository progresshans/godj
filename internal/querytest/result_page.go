package querytest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
)

func ResultPageCompiler(t *testing.T, compile func(query.Plan) (string, []any, error), postgres bool) {
	t.Helper()
	id := query.NewFieldRef("id", "godj_page_slice", query.FieldInteger, false)
	value := query.NewFieldRef("value", "value", query.FieldInteger, true)
	shape, err := query.NewProjectionResult(query.FieldResult(value))
	if err != nil {
		t.Fatal(err)
	}
	input, err := Conditions(t, query.NewPlan("GODJ_PAGE_ROWS", []query.FieldRef{id, value}),
		query.NewCondition(id, query.LookupGreaterThan, query.Integer(7))).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithOrderings(query.NewOrdering(id, query.Descending))
	page, err := query.NewPagePlan(input, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	statement, arguments, err := compile(page)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(arguments, []any{int64(7), int64(2), int64(1)}) {
		t.Fatal("page argument traversal", arguments)
	}
	for _, part := range []string{`WITH "godj_page_rows_0" ("c0", "c1") AS MATERIALIZED (`, `"godj_page_slice_0" AS (SELECT "c0", "c1", 1 AS "godj_present"`,
		`FROM "godj_page_rows_0" ORDER BY "c1" DESC, "c0" ASC NULLS LAST`,
		`SELECT "godj_totals"."godj_total", "godj_page_slice_0"."godj_present", "godj_page_slice_0"."c0" FROM`,
		`LEFT OUTER JOIN "godj_page_slice_0" ON 1 = 1 ORDER BY "godj_page_slice_0"."c1" DESC, "godj_page_slice_0"."c0" ASC NULLS LAST`} {
		if !strings.Contains(statement, part) {
			t.Fatalf("page SQL missing %s: %s", part, statement)
		}
	}
	if strings.Contains(statement, "GROUP BY") || strings.Contains(statement, "DISTINCT") {
		t.Fatal("paging changed multiplicity", statement)
	}
	if postgres {
		if !strings.Contains(statement, "LIMIT $2 OFFSET $3") {
			t.Fatal("page placeholders", statement)
		}
	} else if strings.Count(statement, "?") != 3 {
		t.Fatal("page placeholders", statement)
	}
	zero, err := query.NewPagePlan(input, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, arguments, err = compile(zero)
	if err != nil || zero.EmptyResult() || !reflect.DeepEqual(arguments, []any{int64(7), int64(0), int64(0)}) {
		t.Fatal("zero page lost totals", arguments, err)
	}
}
