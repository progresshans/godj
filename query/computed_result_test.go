package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestComputedResultPreservesSourceDomainAndPredicateNullSemantics(t *testing.T) {
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	value, err := query.FieldExpression(field)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := query.LiteralExpression(query.Integer(1))
	scalar, err := query.Arithmetic(query.ArithmeticAdd, value, one)
	if err != nil {
		t.Fatal(err)
	}
	result, err := query.ScalarResult(scalar)
	if err != nil {
		t.Fatal(err)
	}
	if _, field := result.Field(); field || result.ResultValueKind() != query.FieldInteger || !result.ResultNullable() {
		t.Fatal("computed value fabricated a field or lost NULL")
	}
	avg, err := query.AvgResult(result)
	if err != nil {
		t.Fatal(err)
	}
	if avg.ResultValueKind() != query.FieldFloat || avg.OperandKind() != query.FieldInteger {
		t.Fatal("computed AVG confused operand/result domains")
	}
	shape, err := query.NewProjectionResult(result)
	if err != nil {
		t.Fatal(err)
	}
	base := query.NewPlan("items", []query.FieldRef{field})
	if _, err := base.WithResultShape(shape); err != nil {
		t.Fatal(err)
	}
	foreign := query.NewPlan("items", []query.FieldRef{query.NewFieldRef("amount", "amount", query.FieldInteger, false)})
	if _, err := foreign.WithResultShape(shape); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
		t.Fatal("source nullability forged", err)
	}
	if _, err := query.NewProjectionResult(result, result); err == nil {
		t.Fatal("duplicate computed cell accepted")
	}
	condition, err := query.NewScalarCondition(scalar, query.LookupExact, query.Integer(2))
	if err != nil {
		t.Fatal(err)
	}
	predicate, err := query.NewExpression(condition)
	if err != nil {
		t.Fatal(err)
	}
	negated, err := query.NotExpression(predicate)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := base.WithWhere(negated)
	if err != nil {
		t.Fatal(err)
	}
	if plan.EmptyResult() || !condition.OperandNullable() || condition.Field() != (query.FieldRef{}) {
		t.Fatal("computed predicate borrowed physical field identity")
	}
	if _, err := query.NewScalarCondition(scalar, query.LookupExact, query.String("2")); err == nil {
		t.Fatal("computed value kind coerced")
	}
	if _, err := query.SumResult(avg); err == nil {
		t.Fatal("nested aggregate accepted without a query stage")
	}
	bare, _ := query.LiteralExpression(query.Null())
	if _, err := query.ScalarResult(bare); err == nil {
		t.Fatal("untyped selected NULL accepted")
	}
	typed, _ := query.NullExpression(query.FieldInteger)
	if _, err := query.ScalarResult(typed); err != nil {
		t.Fatal(err)
	}
}

func TestConditionalScalarOwnsBranchesAndCombinedResourceBudget(t *testing.T) {
	field := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	fallback, _ := query.FieldExpression(field)
	one, _ := query.LiteralExpression(query.Integer(1))
	predicate, _ := query.NewExpression(query.NewCondition(field, query.LookupGreaterThan, query.Integer(0)))
	branch, _ := query.WhenScalar(predicate, one)
	inputs := []query.ScalarWhen{branch}
	scalar, err := query.CaseScalar(fallback, inputs...)
	if err != nil {
		t.Fatal(err)
	}
	inputs[0] = query.ScalarWhen{}
	_, branches, present := scalar.Case()
	if !present || len(branches) != 1 || !branches[0].Predicate().Equal(predicate) {
		t.Fatal("conditional aliases input storage")
	}
	branches[0] = query.ScalarWhen{}
	if err := scalar.ValidateSource([]query.FieldRef{field}); err != nil {
		t.Fatal(err)
	}
	if err := scalar.ValidateSource(nil); err == nil {
		t.Fatal("conditional field authority disappeared")
	}
	copy, err := query.CaseScalar(fallback)
	if err != nil || !copy.Equal(fallback) {
		t.Fatal("empty CASE did not retain its fallback", err)
	}
	for scalar.Depth() < query.MaximumScalarDepth-1 {
		condition, err := query.NewScalarCondition(scalar, query.LookupExact, query.Integer(1))
		if err != nil {
			t.Fatal(err)
		}
		predicate, err := query.NewExpression(condition)
		if err != nil {
			t.Fatal(err)
		}
		branch, err := query.WhenScalar(predicate, one)
		if err != nil {
			t.Fatal(err)
		}
		next, err := query.CaseScalar(one, branch)
		if err != nil {
			if scalar.Depth()+2 <= query.MaximumScalarDepth && scalar.NodeCount()+3 <= query.MaximumScalarNodes {
				t.Fatal("conditional rejected below resource bound", err)
			}
			break
		}
		scalar = next
	}
	condition, err := query.NewScalarCondition(scalar, query.LookupExact, query.Integer(1))
	if err != nil {
		t.Fatal(err)
	}
	predicate, err = query.NewExpression(condition)
	if err == nil {
		branch, err = query.WhenScalar(predicate, one)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := query.CaseScalar(one, branch); err == nil {
			t.Fatal("mixed Boolean/scalar depth budget was reset at a boundary")
		}
	}
}
