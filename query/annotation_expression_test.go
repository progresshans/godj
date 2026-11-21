package query_test

import (
	"testing"

	"github.com/progresshans/godj/internal/querytest"
	"github.com/progresshans/godj/query"
)

func annotationScalar(t testing.TB, ref query.AnnotationRef) query.ScalarExpression {
	t.Helper()
	value, err := query.AnnotationExpression(ref)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func annotationResult(t testing.TB, value query.ScalarExpression) query.ResultExpression {
	t.Helper()
	result, err := query.ScalarResult(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestAnnotationExpressionsBindDependenciesWithoutNameOrFieldAuthority(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	first, original, err := base.WithAnnotation("first", query.FieldResult(field), false)
	if err != nil {
		t.Fatal(err)
	}
	value := annotationScalar(t, original)
	if err := first.ValidateScalar(value); err != nil {
		t.Fatal(err)
	}
	if err := base.ValidateScalar(value); err == nil {
		t.Fatal("an unbound reference acquired source authority")
	}
	if err := value.ValidateSource(base.SourceFields()); err == nil {
		t.Fatal("field metadata alone authorized a named value")
	}
	if _, err := query.NewScalarAssignment(field, value); err == nil {
		t.Fatal("a named read value acquired assignment authority")
	}
	negative, err := query.NegateScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	dependent, _, err := first.WithAnnotation("second", annotationResult(t, negative), true)
	if err != nil {
		t.Fatal(err)
	}
	rebound, replacement, err := dependent.WithAnnotation("first", query.CountAllResult(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := rebound.ValidateScalar(negative); err != nil {
		t.Fatal("rebind revoked an earlier dependency", err)
	}
	refs := negative.AnnotationReferences()
	if len(refs) != 1 || !refs[0].Equal(original) || refs[0].Equal(replacement) || !negative.HasAnnotations() || negative.ContainsAggregate() {
		t.Fatal("dependency was rebound by name or changed its aggregate domain")
	}
	refs[0] = replacement
	if !negative.AnnotationReferences()[0].Equal(original) {
		t.Fatal("caller changed a dependency container")
	}
	_, foreign, err := base.WithAnnotation("first", query.FieldResult(field), false)
	if err != nil {
		t.Fatal(err)
	}
	foreignValue := annotationScalar(t, foreign)
	if foreignValue.Equal(value) {
		t.Fatal("equal names and producers erased branch identity")
	}
	condition, err := query.NewScalarComparison(value, query.LookupExact, foreignValue)
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
	conditional, err := query.CaseScalar(value, branch)
	if err != nil {
		t.Fatal(err)
	}
	if err := rebound.ValidateScalar(conditional); err == nil {
		t.Fatal("CASE RHS admitted a foreign reference")
	}
	if _, _, err := rebound.WithAnnotation("foreign", annotationResult(t, conditional), true); err == nil {
		t.Fatal("a definition laundered a foreign CASE reference")
	}
	if _, err := query.AnnotationExpression(query.AnnotationRef{}); err == nil {
		t.Fatal("zero annotation reference was accepted")
	}
}

func TestAnnotationExpressionsKeepAggregateAndDurationProvenance(t *testing.T) {
	t.Parallel()
	duration := query.NewFieldRef("elapsed", "elapsed", query.FieldDuration, false)
	base := query.NewPlan("measures", []query.FieldRef{duration})
	mean, err := query.AvgResult(query.FieldResult(duration))
	if err != nil {
		t.Fatal(err)
	}
	withMean, meanRef, err := base.WithAnnotation("mean", mean, true)
	if err != nil {
		t.Fatal(err)
	}
	value := annotationScalar(t, meanRef)
	if !value.ContainsAggregate() || !value.Nullable() || !value.RequiresDurationRounding() || value.RequiresDurationFraction() {
		t.Fatal("native Duration average lost its domain")
	}
	if err := withMean.ValidateAggregateScalar(value); err == nil {
		t.Fatal("aggregate reference became a nested aggregate operand")
	}
	if _, err := query.ScalarAggregateResult(query.ResultAvg, value); err == nil {
		t.Fatal("nested aggregate was accepted without a result stage")
	}
	withCount, countRef, err := withMean.WithAnnotation("count", query.CountAllResult(), true)
	if err != nil {
		t.Fatal(err)
	}
	count := annotationScalar(t, countRef)
	countResult := annotationResult(t, count)
	if count.Nullable() || countResult.ResultNullable() || !countResult.IsAggregate() || countResult.IsAggregateCall() {
		t.Fatal("COUNT reference confused aggregate presence, call or NULL")
	}
	if _, err := query.NewGroupedResult([]query.ResultExpression{countResult}, []query.ResultExpression{mean}); err == nil {
		t.Fatal("aggregate reference became a same-stage group key")
	}
	condition, err := query.NewScalarCondition(count, query.LookupGreaterThan, query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := query.NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	if !predicate.ContainsAggregate() || !predicate.HasAnnotations() {
		t.Fatal("predicate lost its aggregate dependency")
	}
	if _, err := mean.WithFilter(predicate); err == nil {
		t.Fatal("aggregate FILTER accepted another same-stage aggregate")
	}
	branch, err := query.WhenScalar(predicate, count)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := query.CaseScalar(count, branch)
	if err != nil || !conditional.ContainsAggregate() || conditional.Nullable() {
		t.Fatal("CASE lost aggregate or COUNT nullability", err)
	}
	if err := withCount.ValidateScalar(conditional); err != nil {
		t.Fatal(err)
	}
	direct, err := query.FieldExpression(duration)
	if err != nil {
		t.Fatal(err)
	}
	two, err := query.LiteralExpression(query.Integer(2))
	if err != nil {
		t.Fatal(err)
	}
	fraction, err := query.Arithmetic(query.ArithmeticDivide, direct, two)
	if err != nil {
		t.Fatal(err)
	}
	_, fractionRef, err := base.WithAnnotation("fraction", annotationResult(t, fraction), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := annotationScalar(t, fractionRef); !got.RequiresDurationFraction() || !got.RequiresDurationRounding() || got.ContainsAggregate() {
		t.Fatal("Duration fraction became an ordinary field or native average")
	}
}

func TestAnnotationExpressionsRequireNewOwnershipAfterResultStage(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	annotated, ref, err := base.WithAnnotation("count", query.CountAllResult(), true)
	if err != nil {
		t.Fatal(err)
	}
	value := annotationScalar(t, ref)
	shape, err := query.NewProjectionResult(annotationResult(t, value))
	if err != nil {
		t.Fatal(err)
	}
	projected, err := annotated.WithResultShape(shape)
	if err != nil {
		t.Fatal(err)
	}
	derived, scope, err := query.NewDerivedPlan(projected)
	if err != nil {
		t.Fatal(err)
	}
	if err := derived.ValidateScalar(value); err == nil {
		t.Fatal("annotation reference crossed a result boundary")
	}
	column, err := scope.Column(0)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := query.SelectedExpression(column)
	if err != nil || selected.ContainsAggregate() {
		t.Fatal("selected aggregate remained an aggregate in its next stage", err)
	}
	later, laterRef, err := derived.WithAnnotation("count", annotationResult(t, selected), true)
	if err != nil {
		t.Fatal(err)
	}
	newValue := annotationScalar(t, laterRef)
	if err := newValue.ValidateResultSource(scope); err == nil {
		t.Fatal("a result scope alone authorized an annotation")
	}
	if err := later.ValidateScalar(newValue); err != nil {
		t.Fatal(err)
	}
	if err := later.ValidateAggregateScalar(newValue); err != nil {
		t.Fatal("explicit result stage did not permit re-aggregation", err)
	}
	querytest.Conditions(t, later, mustAnnotationCondition(t, newValue))
}

func mustAnnotationCondition(t testing.TB, value query.ScalarExpression) query.Condition {
	t.Helper()
	condition, err := query.NewScalarCondition(value, query.LookupGreaterThan, query.Integer(0))
	if err != nil {
		t.Fatal(err)
	}
	return condition
}

func TestAnnotationExpressionsBoundExpandedDependencyTrees(t *testing.T) {
	t.Parallel()
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	base := query.NewPlan("measures", []query.FieldRef{field})
	plan, ref, err := base.WithAnnotation("value", query.FieldResult(field), false)
	if err != nil {
		t.Fatal(err)
	}
	value := annotationScalar(t, ref)
	for index := 0; ; index++ {
		if index > 10 {
			t.Fatal("shared definitions bypassed the expanded node limit")
		}
		doubled, err := query.Arithmetic(query.ArithmeticAdd, value, value)
		if err != nil {
			break
		}
		plan, ref, err = plan.WithAnnotation("value", annotationResult(t, doubled), false)
		if err != nil {
			t.Fatal(err)
		}
		next, err := query.AnnotationExpression(ref)
		if err != nil {
			break
		}
		if next.NodeCount() <= 2*value.NodeCount() || next.Depth() <= value.Depth() {
			t.Fatal("reference hid the cost of its expanded producer")
		}
		value = next
	}
	plan, ref, err = base.WithAnnotation("value", query.FieldResult(field), false)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; ; index++ {
		value, err := query.AnnotationExpression(ref)
		if err != nil {
			break
		}
		if index >= query.MaximumScalarDepth {
			t.Fatal("linear definitions bypassed the expanded depth limit")
		}
		plan, ref, err = plan.WithAnnotation("value", annotationResult(t, value), false)
		if err != nil {
			t.Fatal(err)
		}
	}
}
