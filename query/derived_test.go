package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func derivedInput(t *testing.T) (query.Plan, query.FieldRef) {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	shape, err := query.NewProjectionResult(query.FieldResult(id))
	if err != nil {
		t.Fatal(err)
	}
	input, err := query.NewPlan("items", []query.FieldRef{id}).WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	return input, id
}

func TestDerivedPlanOwnsOrderedInput(t *testing.T) {
	input, id := derivedInput(t)
	input, err := input.WithConditions(query.NewCondition(id, query.LookupGreaterThan, query.Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	input = input.WithOrderings(query.NewOrdering(id, query.Descending)).WithDistinct()
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
	snapshot, owner, present := plan.DerivedSource()
	if !present || !snapshot.Equal(input) || !owner.Equal(scope) || plan.Table() != "" || len(plan.SourceFields()) != 0 || plan.Distinct() || len(plan.Orderings()) != 0 {
		t.Fatal("derived query flattened or fabricated its input")
	}
	if _, present := plan.Where(); present {
		t.Fatal("input filter moved after its slice")
	}
	if _, present := plan.Limit(); present {
		t.Fatal("input limit moved to the result")
	}
	if _, present := plan.Offset(); present {
		t.Fatal("input offset moved to the result")
	}
	snapshot, err = snapshot.WithLimit(99)
	if err != nil {
		t.Fatal(err)
	}
	preserved, _, _ := plan.DerivedSource()
	if preserved.Equal(snapshot) || !preserved.Equal(input) {
		t.Fatal("exposed input was mutable")
	}
	value := selectedScalar(t, scope, 0)
	condition, err := query.NewScalarCondition(value, query.LookupGreaterThan, query.Integer(3))
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := plan.WithConditions(condition)
	if err != nil {
		t.Fatal(err)
	}
	if err := filtered.ValidateDerived(); err != nil {
		t.Fatal(err)
	}
	if plan.Equal(filtered) || !plan.Equal(plan) {
		t.Fatal("stage refinements lost query identity")
	}
	if _, err := plan.WithConditions(query.NewCondition(id, query.LookupExact, query.Integer(3))); err == nil {
		t.Fatal("row field crossed a stage")
	}
	other, otherScope, err := query.NewDerivedPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Equal(other) || scope.Equal(otherScope) {
		t.Fatal("independent stage authority was conflated")
	}
	foreign, err := query.NewScalarCondition(selectedScalar(t, otherScope, 0), query.LookupExact, query.Integer(3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.WithConditions(foreign); err == nil {
		t.Fatal("foreign result predicate accepted")
	}
	if _, err := plan.WithResultShape(query.NewPlan("items", []query.FieldRef{id}).ResultShape()); err == nil {
		t.Fatal("derived query became a writable model")
	}
	if _, err := query.NewQueryUpdatePlan(plan, id, nil); err == nil {
		t.Fatal("empty update bypassed derived-source rejection")
	}
	lock, err := query.NewRowLock(query.LockForUpdate, query.LockWait, query.LockSelf())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.WithRowLock(lock); err == nil {
		t.Fatal("result reference became a row lock")
	}
	locked, err := input.WithRowLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := query.NewDerivedPlan(locked); err == nil {
		t.Fatal("input lock silently crossed a stage")
	}
}

func TestDerivedPlanBudgetsIncludeConstantStagesAndCompleteInput(t *testing.T) {
	input, _ := derivedInput(t)
	constant, err := query.ScalarResult(scalarLiteral(t, query.Integer(1)))
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewProjectionResult(constant)
	if err != nil {
		t.Fatal(err)
	}
	for depth := 2; depth <= query.MaximumResultStages; depth++ {
		plan, scope, err := query.NewDerivedPlan(input)
		if err != nil {
			t.Fatal(err)
		}
		if plan.ResultStageDepth() != depth || scope.Depth() != depth-1 {
			t.Fatal("constant projection reset physical stage depth")
		}
		input, err = plan.WithResultShape(shape)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := query.NewDerivedPlan(input); err == nil {
		t.Fatal("literal-only stages bypassed depth budget")
	}
	input, _ = derivedInput(t)
	columns := make([]query.ResultExpression, query.MaxProjectionExpressions)
	for index := range columns {
		columns[index], err = query.ScalarResult(scalarLiteral(t, query.Integer(int64(index))))
		if err != nil {
			t.Fatal(err)
		}
	}
	wide, err := query.NewProjectionResult(columns...)
	if err != nil {
		t.Fatal(err)
	}
	input, err = input.WithResultShape(wide)
	if err != nil {
		t.Fatal(err)
	}
	for depth := 2; depth <= query.MaximumResultStages; depth++ {
		var next query.Plan
		next, _, err = query.NewDerivedPlan(input)
		if err != nil {
			if depth <= 2 || depth >= query.MaximumResultStages {
				t.Fatal("wrong budget stopped the staged selection", depth, err)
			}
			return
		}
		input = next
	}
	t.Fatal("per-stage limits replaced the complete query budget")
}

func TestDerivedEmptyAnalysisPreservesAggregateRowsAndPageTotals(t *testing.T) {
	input, _ := derivedInput(t)
	empty, err := input.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := query.NewDerivedPlan(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.EmptyResult() {
		t.Fatal("zero-row input lost emptiness")
	}
	page, err := query.NewPagePlan(rows, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !page.EmptyResult() {
		t.Fatal("zero-row page lost emptiness")
	}
	count, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := rows.WithResultShape(count)
	if err != nil {
		t.Fatal(err)
	}
	if !aggregate.EmptyResult() {
		t.Fatal("aggregate lost empty source identity")
	}
	one, _, err := query.NewDerivedPlan(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if one.EmptyResult() {
		t.Fatal("COUNT=0 row was treated as an empty result")
	}
	for _, limit := range []int{0, 1} {
		page, err := query.NewPagePlan(one, limit, 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.EmptyResult() {
			t.Fatal("empty page erased total=1", limit)
		}
	}
}
