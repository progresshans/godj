package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func selectedScalar(t *testing.T, scope query.ResultScope, index int) query.ScalarExpression {
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

func aggregateResultScope(t *testing.T) query.ResultScope {
	t.Helper()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	avg, err := query.AvgResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewAggregateResult(query.CountAllResult(), avg)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := query.NewResultScope(shape)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func TestResultScopeOwnsSelectedColumns(t *testing.T) {
	scope := aggregateResultScope(t)
	count, avg := selectedScalar(t, scope, 0), selectedScalar(t, scope, 1)
	if !scope.Valid() || scope.ColumnCount() != 2 || scope.Depth() != 1 || !scope.Equal(scope) {
		t.Fatal("result scope lost its immutable shape")
	}
	if count.ResultKind() != query.FieldInteger || count.Nullable() || avg.ResultKind() != query.FieldFloat || !avg.Nullable() {
		t.Fatal("selected result reused aggregate operand metadata")
	}
	if _, field := count.Field(); field || len(count.Fields()) != 0 || count.Kind() != query.ScalarSelected {
		t.Fatal("selected count manufactured model metadata")
	}
	ref, present := avg.ResultReference()
	index, valid := ref.Index()
	if !present || !valid || index != 1 || !ref.Scope().Equal(scope) {
		t.Fatal("selected result lost its ordinal or owner")
	}
	columns := scope.Shape().Expressions()
	columns[0] = query.ResultExpression{}
	if !selectedScalar(t, scope, 0).Equal(count) {
		t.Fatal("scope exposed mutable selection storage")
	}
	other := aggregateResultScope(t)
	if !scope.Shape().Equal(other.Shape()) || scope.Equal(other) || count.Equal(selectedScalar(t, other, 0)) {
		t.Fatal("independent result scopes were rebound by structural similarity")
	}
	for _, index := range []int{-1, 2, 1 << 30} {
		if _, err := scope.Column(index); err == nil {
			t.Fatal("out-of-scope ordinal accepted", index)
		}
	}
	if _, err := (query.ResultScope{}).Column(0); err == nil {
		t.Fatal("zero result scope accepted")
	}
	if _, err := query.SelectedExpression(query.ResultRef{}); err == nil {
		t.Fatal("zero selected reference accepted")
	}
	if _, err := query.NewResultScope(query.NewPlan("items", nil).ResultShape()); err == nil {
		t.Fatal("implicit model rows accepted as a scalar result scope")
	}
}

func TestSelectedDurationMeanDefersRoundingAcrossStagesAndCase(t *testing.T) {
	field := query.NewFieldRef("elapsed", "elapsed", query.FieldDuration, true)
	mean, err := query.AvgResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewAggregateResult(mean)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := query.NewResultScope(shape)
	if err != nil {
		t.Fatal(err)
	}
	value := selectedScalar(t, scope, 0)
	if !value.RequiresDurationRounding() || !value.Nullable() || value.ResultKind() != query.FieldDuration {
		t.Fatal("duration average lost native result domain")
	}
	predicate, err := query.NewScalarCondition(value, query.LookupIsNull, query.Boolean(false))
	if err != nil {
		t.Fatal(err)
	}
	when, err := query.NewExpression(predicate)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := query.WhenScalar(when, value)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := query.NullExpression(query.FieldDuration)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := query.CaseScalar(fallback, branch)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := query.ScalarResult(conditional)
	if err != nil {
		t.Fatal(err)
	}
	nextShape, err := query.NewProjectionResult(selected)
	if err != nil {
		t.Fatal(err)
	}
	next, err := query.NewResultScope(nextShape)
	if err != nil {
		t.Fatal(err)
	}
	if !selectedScalar(t, next, 0).RequiresDurationRounding() || !selected.RequiresDurationRounding() {
		t.Fatal("CASE or subsequent selection rounded a native mean")
	}
	original, err := query.FieldExpression(field)
	if err != nil {
		t.Fatal(err)
	}
	if original.RequiresDurationRounding() || query.FieldResult(field).RequiresDurationRounding() {
		t.Fatal("duration result widened the model storage contract")
	}
}

func TestSelectedScalarEnforcesReadScope(t *testing.T) {
	scope := aggregateResultScope(t)
	count := selectedScalar(t, scope, 0)
	literal := scalarLiteral(t, query.Integer(2))
	value, err := query.Arithmetic(query.ArithmeticMultiply, count, literal)
	if err != nil {
		t.Fatal(err)
	}
	condition, err := query.NewScalarCondition(count, query.LookupGreaterThan, query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := query.NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := query.WhenScalar(predicate, value)
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := query.NullExpression(query.FieldInteger)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := query.CaseScalar(fallback, branch)
	if err != nil || !conditional.Nullable() {
		t.Fatal("result CASE lost NULL", err)
	}
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	row := query.NewPlan("items", []query.FieldRef{field})
	for _, expression := range []query.ScalarExpression{count, value, conditional} {
		if err := expression.ValidateResultSource(scope); err != nil {
			t.Fatal(err)
		}
		if err := expression.ValidateResultSource(aggregateResultScope(t)); err == nil {
			t.Fatal("foreign result scope accepted")
		}
		if err := expression.ValidateSource([]query.FieldRef{field}); err == nil {
			t.Fatal("selected result escaped into a row source")
		}
		if _, err := query.NewScalarAssignment(field, expression); err == nil {
			t.Fatal("selected result escaped into an assignment")
		}
		selected, err := query.ScalarResult(expression)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewProjectionResult(selected)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := row.WithResultShape(shape); err == nil {
			t.Fatal("result projection bound to a model table")
		}
	}
	if _, err := row.WithWhere(predicate); err == nil {
		t.Fatal("result predicate bound to a model table")
	}
	if err := scalarField(t, field).ValidateResultSource(scope); err == nil {
		t.Fatal("model field crossed a result boundary without selection")
	}
	if err := literal.ValidateResultSource(scope); err != nil {
		t.Fatal(err)
	}
	if err := literal.ValidateResultSource(query.ResultScope{}); err == nil {
		t.Fatal("constant disguised an uninitialized source")
	}
	// A reference used only in the CASE predicate still belongs to that scope.
	branch, err = query.WhenScalar(predicate, literal)
	if err != nil {
		t.Fatal(err)
	}
	onlyPredicate, err := query.CaseScalar(literal, branch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewScalarAssignment(field, onlyPredicate); err == nil {
		t.Fatal("CASE predicate hid a selected result in a write")
	}
	rowPredicate, err := query.NewExpression(query.NewCondition(field, query.LookupIsNull, query.Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	branch, err = query.WhenScalar(rowPredicate, count)
	if err != nil {
		t.Fatal(err)
	}
	mixed, err := query.CaseScalar(literal, branch)
	if err != nil {
		t.Fatal(err)
	}
	if err := mixed.ValidateResultSource(scope); err == nil {
		t.Fatal("CASE predicate hid a row field in a result stage")
	}
}

func TestResultScopeBoundsNestedStages(t *testing.T) {
	scope := aggregateResultScope(t)
	for depth := 2; depth <= query.MaximumResultStages+1; depth++ {
		value, err := query.ScalarResult(selectedScalar(t, scope, 0))
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewProjectionResult(value)
		if err != nil {
			t.Fatal(err)
		}
		next, err := query.NewResultScope(shape)
		if depth > query.MaximumResultStages {
			if err == nil {
				t.Fatal("result references bypassed the stage budget")
			}
			break
		}
		if err != nil || next.Depth() != depth {
			t.Fatal("stage depth was not retained", depth, next.Depth(), err)
		}
		if err := selectedScalar(t, scope, 0).ValidateResultSource(next); err == nil {
			t.Fatal("ancestor columns leaked across a completed result stage")
		}
		scope = next
	}
	value := selectedScalar(t, scope, 0)
	negated, err := query.NegateScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	condition, err := query.NewScalarCondition(value, query.LookupGreaterThan, query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := query.NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	constant := scalarLiteral(t, query.Integer(1))
	branch, err := query.WhenScalar(predicate, constant)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := query.CaseScalar(constant, branch)
	if err != nil {
		t.Fatal(err)
	}
	for _, scalar := range []query.ScalarExpression{negated, conditional} {
		selected, err := query.ScalarResult(scalar)
		if err != nil {
			t.Fatal(err)
		}
		shape, err := query.NewProjectionResult(selected)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := query.NewResultScope(shape); err == nil {
			t.Fatal("negation or CASE predicate concealed stage depth")
		}
	}
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	count, err := query.CountResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	count, err = count.WithFilter(predicate)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewAggregateResult(count)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewResultScope(shape); err == nil {
		t.Fatal("aggregate predicate concealed stage depth")
	}
}
