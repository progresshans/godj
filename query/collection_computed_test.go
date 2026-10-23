package query

import (
	"errors"
	"testing"
)

func TestCollectionComputedValuesRequireAggregateContext(t *testing.T) {
	path := collectionRouteFixture(t, NewFieldRef("amount", "amount", FieldInteger, true))
	id := path.PrimaryKeys()[0]
	plan := NewPlan("owners", []FieldRef{id})
	value, err := RelatedExpression(path)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := LiteralExpression(Integer(1))
	computed, err := Arithmetic(ArithmeticAdd, value, one)
	if err != nil {
		t.Fatal(err)
	}
	condition, err := NewScalarComparison(one, LookupLessThan, computed)
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := WhenScalar(predicate, computed)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := CaseScalar(one, branch)
	if err != nil {
		t.Fatal(err)
	}
	for _, operand := range []ScalarExpression{value, computed, conditional} {
		if err := plan.ValidateAggregateScalar(operand); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []ResultExpressionKind{ResultCount, ResultMin, ResultMax, ResultSum, ResultAvg} {
			aggregate, err := ScalarAggregateResult(kind, operand)
			if err != nil {
				t.Fatal(err)
			}
			filtered, err := aggregate.WithFilter(predicate)
			if err != nil {
				t.Fatal(err)
			}
			shape, err := NewGroupedResult([]ResultExpression{FieldResult(id)}, []ResultExpression{filtered})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := plan.WithResultShape(shape); err != nil {
				t.Fatal(err)
			}
			if _, present := aggregate.Filter(); present {
				t.Fatal("aggregate filter mutated its input")
			}
			if retained, present := filtered.Scalar(); !present || !retained.Equal(operand) {
				t.Fatal("aggregate lost operand identity")
			}
			if _, err := AggregateResult(ResultCount, aggregate); err == nil {
				t.Fatal("nested aggregate bypassed a result stage")
			}
		}
		if _, err := ScalarResult(operand); !errors.Is(err, &Error{Code: CodeUnsupported}) {
			t.Fatal("collection value escaped into projection or a group key", err)
		}
		if err := plan.ValidateScalar(operand); !errors.Is(err, &Error{Code: CodeUnsupported}) {
			t.Fatal("collection value escaped into row execution", err)
		}
		if _, err := NewScalarAssignment(id, operand); err == nil {
			t.Fatal("collection value escaped into an assignment")
		}
		for _, source := range []Plan{
			NewPlan("other", []FieldRef{id}),
			NewPlan("owners", []FieldRef{NewFieldRef("other", "id", FieldInteger, false)}),
		} {
			if err := source.ValidateAggregateScalar(operand); err == nil {
				t.Fatal("aggregate fabricated root authority")
			}
		}
		selected, _ := ScalarResult(one)
		shape, _ := NewProjectionResult(selected)
		scope, _ := NewResultScope(shape)
		if err := operand.ValidateResultSource(scope); err == nil {
			t.Fatal("original collection crossed a result stage")
		}
	}
	if err := plan.ValidateAggregatePredicate(predicate); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.WithWhere(predicate); err == nil {
		t.Fatal("computed collection predicate escaped into row WHERE")
	}
	if err := plan.ValidatePredicate(predicate); err == nil {
		t.Fatal("compiler row predicate validation accepted a collection")
	}
}

func TestCollectionComputedAggregateCannotBorrowOccurrenceOrMetadata(t *testing.T) {
	for _, kind := range []ResultExpressionKind{ResultCountAll, ResultCount, ResultSum} {
		if _, err := ScalarAggregateResult(kind, ScalarExpression{}); err == nil {
			t.Fatal("empty scalar became a completed aggregate", kind)
		}
	}
	path := collectionRouteFixture(t, NewDecimalFieldRef("cost", "cost", true, 14, 2))
	id := path.PrimaryKeys()[0]
	plan := NewPlan("owners", []FieldRef{id})
	bound, err := path.WithCollectionFilterScope(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RelatedExpression(bound); err == nil {
		t.Fatal("value constructor accepted another filter's occurrence")
	}
	one, _ := LiteralExpression(Integer(1))
	condition, err := NewExpression(NewRelatedCondition(bound, LookupIsNull, Boolean(false)))
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := WhenScalar(condition, one)
	value, err := CaseScalar(one, branch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ScalarAggregateResult(ResultSum, value); err == nil {
		t.Fatal("CASE predicate borrowed another filter occurrence")
	}
	if err := plan.ValidateAggregateScalar(value); err == nil {
		t.Fatal("compiler accepted CASE's borrowed occurrence")
	}
	if err := plan.ValidateAggregatePredicate(condition); err == nil {
		t.Fatal("aggregate FILTER borrowed another occurrence")
	}
	invalid := path
	invalid.terminal = NewFieldRef("cost", "cost", FieldDecimal, true)
	if _, err := RelatedExpression(invalid); err == nil {
		t.Fatal("collection expression invented missing precision")
	}
	foreign, _ := FieldExpression(NewFieldRef("id", "id", FieldInteger, true))
	foreignCondition, _ := NewScalarComparison(one, LookupLessThan, foreign)
	foreignPredicate, _ := NewExpression(foreignCondition)
	foreignBranch, _ := WhenScalar(foreignPredicate, one)
	foreignCase, _ := CaseScalar(one, foreignBranch)
	if err := plan.ValidateAggregateScalar(foreignCase); err == nil {
		t.Fatal("CASE RHS fabricated nullable source metadata")
	}
	if err := plan.ValidateAggregatePredicate(foreignPredicate); err == nil {
		t.Fatal("FILTER RHS fabricated nullable source metadata")
	}
}
