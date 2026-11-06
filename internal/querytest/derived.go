package querytest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/query"
)

func DerivedColumn(t *testing.T, scope query.ResultScope, index int) query.ScalarExpression {
	t.Helper()
	ref, err := scope.Column(index)
	if err != nil {
		t.Fatal(err)
	}
	value, err := query.SelectedExpression(ref)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// DerivedCompiler checks scope and phase boundaries through both physical
// compilers. Native rows, codecs and failure ownership have separate consumers.
func DerivedCompiler(t *testing.T, compile func(query.Plan) (string, []any, error), postgres bool) {
	t.Helper()
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	shape, err := query.NewProjectionResult(query.FieldResult(amount))
	if err != nil {
		t.Fatal(err)
	}
	input, err := Conditions(t, query.NewPlan("items", []query.FieldRef{amount}),
		query.NewCondition(amount, query.LookupGreaterThanOrEqual, query.Integer(4))).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithOrderings(query.NewOrdering(amount, query.Descending))
	input, err = input.WithLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.WithOffset(1)
	if err != nil {
		t.Fatal(err)
	}
	plan, scope, err := query.NewDerivedPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	value := DerivedColumn(t, scope, 0)
	t.Run("input_before_case_filter_and_page", func(t *testing.T) {
		condition, err := query.NewScalarCondition(value, query.LookupGreaterThan, query.Integer(5))
		if err != nil {
			t.Fatal(err)
		}
		predicate, err := query.NewExpression(condition)
		if err != nil {
			t.Fatal(err)
		}
		ten, err := query.LiteralExpression(query.Integer(10))
		if err != nil {
			t.Fatal(err)
		}
		plus, err := query.Arithmetic(query.ArithmeticAdd, value, ten)
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(predicate, plus)
		if err != nil {
			t.Fatal(err)
		}
		fallback, err := query.LiteralExpression(query.Integer(-7))
		if err != nil {
			t.Fatal(err)
		}
		computed, err := query.CaseScalar(fallback, branch)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := query.ScalarResult(computed)
		if err != nil {
			t.Fatal(err)
		}
		result, err := query.NewProjectionResult(selected)
		if err != nil {
			t.Fatal(err)
		}
		derived, err := plan.WithResultShape(result)
		if err != nil {
			t.Fatal(err)
		}
		where, err := query.NewScalarCondition(value, query.LookupGreaterThanOrEqual, query.Integer(9))
		if err != nil {
			t.Fatal(err)
		}
		derived = Conditions(t, derived, where)
		order, err := query.NewResultOrdering(selected, query.Ascending)
		if err != nil {
			t.Fatal(err)
		}
		derived = derived.WithOrderings(order)
		derived, err = derived.WithLimit(1)
		if err != nil {
			t.Fatal(err)
		}
		statement, arguments, err := compile(derived)
		if err != nil {
			t.Fatal(err)
		}
		want := []any{int64(4), int64(2), int64(1), int64(5), int64(10), int64(-7), int64(9), int64(1)}
		if !reflect.DeepEqual(arguments, want) {
			t.Fatalf("stage arguments = %#v, want %#v", arguments, want)
		}
		if !strings.HasPrefix(statement, `WITH "godj_results" ("c0") AS (`) || strings.Index(statement, "OFFSET") > strings.Index(statement, "CASE") || !strings.Contains(statement, ` FROM "godj_results" WHERE `) || !strings.Contains(statement, "ORDER BY 1 ASC") {
			t.Fatal("input slice, output CASE or result filter crossed a stage", statement)
		}
		if postgres {
			for _, fragment := range []string{"LIMIT $2 OFFSET $3", " > $4", "CAST($5 AS bigint)", "CAST($6 AS bigint)", " >= $7", "LIMIT $8"} {
				if !strings.Contains(statement, fragment) {
					t.Fatal("numbered parameter changed traversal", fragment, statement)
				}
			}
		} else if strings.Count(statement, "?") != len(want) || !strings.Contains(statement, "godj_int64(") {
			t.Fatal("positional parameters or int64 guards lost", statement)
		}
	})
	t.Run("terminal_aggregate_owns_sliced_input", func(t *testing.T) {
		selected, err := query.ScalarResult(value)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := query.SumResult(selected)
		if err != nil {
			t.Fatal(err)
		}
		aggregate, err := query.NewAggregateResult(sum, query.CountAllResult())
		if err != nil {
			t.Fatal(err)
		}
		derived, err := plan.WithResultShape(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		statement, arguments, err := compile(derived)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(statement, "SUM(") || !strings.Contains(statement, "COUNT(*)") || !reflect.DeepEqual(arguments, []any{int64(4), int64(2), int64(1)}) {
			t.Fatal("terminal aggregate discarded its input slice", statement, arguments)
		}
		zeroInput, err := input.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		zero, _, err := query.NewDerivedPlan(zeroInput)
		if err != nil {
			t.Fatal(err)
		}
		count, err := query.NewAggregateResult(query.CountAllResult())
		if err != nil {
			t.Fatal(err)
		}
		zero, err = zero.WithResultShape(count)
		if err != nil {
			t.Fatal(err)
		}
		if !zero.EmptyResult() {
			t.Fatal("zero-row input did not expose its empty aggregate source")
		}
		statement, arguments, err = compile(zero)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(statement, `"items"`) || !strings.Contains(statement, "WHERE 0 = 1") || !strings.Contains(statement, "COUNT(*)") || !reflect.DeepEqual(arguments, []any{nil}) {
			t.Fatal("folded empty input retained native producer or lost COUNT", statement, arguments)
		}
		// A second stage reads that aggregate's one COUNT=0 row. Empty
		// source analysis must not mistake it for a zero-row projection.
		second, _, err := query.NewDerivedPlan(zero)
		if err != nil {
			t.Fatal(err)
		}
		if second.EmptyResult() {
			t.Fatal("aggregate COUNT row disappeared in a later stage")
		}
		statement, arguments, err = compile(second)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(statement, `"items"`) || strings.Contains(statement, "WHERE 0 = 1") || !reflect.DeepEqual(arguments, []any{int64(0)}) {
			t.Fatal("empty aggregate input did not retain its one synthetic row", statement, arguments)
		}
	})
	t.Run("cte_names_do_not_shadow_case_insensitive_sources", func(t *testing.T) {
		field := query.NewFieldRef("value", "godj_results_0", query.FieldInteger, false)
		result, err := query.NewProjectionResult(query.FieldResult(field))
		if err != nil {
			t.Fatal(err)
		}
		table := "GODJ_RESULTS"
		if postgres {
			table = "godj_results"
		} // PostgreSQL retains its lowercase identifier contract.
		base, err := query.NewPlan(table, []query.FieldRef{field}).WithResultShape(result)
		if err != nil {
			t.Fatal(err)
		}
		first, _, err := query.NewDerivedPlan(base)
		if err != nil {
			t.Fatal(err)
		}
		second, _, err := query.NewDerivedPlan(first)
		if err != nil {
			t.Fatal(err)
		}
		statement, _, err := compile(second)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(statement, `WITH "godj_results_2"`) || !strings.Contains(statement, `WITH "godj_results_1"`) {
			t.Fatal("derived CTE captured a physical source or another stage", statement)
		}
	})
	t.Run("group_page_names_preserve_nested_input", func(t *testing.T) {
		field := query.NewFieldRef("rank", "godj_page", query.FieldInteger, true)
		selection, err := query.NewProjectionResult(query.FieldResult(field))
		if err != nil {
			t.Fatal(err)
		}
		input, err := query.NewPlan("godj_groups", []query.FieldRef{field}).WithResultShape(selection)
		if err != nil {
			t.Fatal(err)
		}
		plan, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		key, err := query.ScalarResult(DerivedColumn(t, scope, 0))
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewGroupedResult([]query.ResultExpression{key}, []query.ResultExpression{query.CountAllResult()})
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithLimit(0)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = plan.WithGroupMode(query.GroupPage)
		if err != nil {
			t.Fatal(err)
		}
		statement, arguments, err := compile(plan)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(statement, `WITH "godj_groups_0" AS (`) || !strings.Contains(statement, `LEFT OUTER JOIN "godj_page_0"`) || !reflect.DeepEqual(arguments, []any{int64(0)}) {
			t.Fatal("page envelope captured its input namespace or lost a zero page", statement, arguments)
		}
	})
}
