package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestRelatedScalarSourceAndCardinality(t *testing.T) {
	root := ir.ModelIdentity{AppLabel: "app", ModelName: "entry"}
	target := ir.ModelIdentity{AppLabel: "app", ModelName: "datum"}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	fk := query.NewFieldRef("datum", "datum_id", query.FieldInteger, true)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	path, err := query.NewForwardRelationPath(root, "app_entry", "datum", "datum_id", target, "app_datum", "id", true, amount, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	value, err := query.RelatedExpression(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := query.NewPlan("app_entry", []query.FieldRef{id, fk, amount})
	if err := plan.ValidateScalar(value); err != nil {
		t.Fatal(err)
	}
	if !value.Nullable() || !value.HasRelations() || len(value.Fields()) != 0 || value.ResultKind() != query.FieldInteger {
		t.Fatal("related value lost optional ancestry or fabricated a root field")
	}
	paths := value.RelationPaths()
	paths[0] = query.RelationPath{}
	if actual, _ := value.RelationPath(); !actual.Equal(path) {
		t.Fatal("relation path storage escaped")
	}
	if err := value.ValidateSource(plan.SourceFields()); err == nil {
		t.Fatal("field list authorized a relation")
	}
	for _, invalid := range []query.Plan{
		query.NewPlan("other_table", plan.SourceFields()),
		query.NewPlan("app_entry", []query.FieldRef{id, amount}),
		query.NewPlan("app_entry", []query.FieldRef{id, amount, query.NewFieldRef("datum", "datum_id", query.FieldInteger, false)}),
	} {
		if err := invalid.ValidateScalar(value); err == nil {
			t.Fatal("related source authority bypassed")
		}
	}
	for _, cardinality := range []ir.RelationCardinality{ir.RelationOneToOne, ir.RelationOneToMany} {
		reverse, err := query.NewReverseRelationPath(target, "app_datum", "entry", "entry_id", root, "app_entry", "id", "datum", false, amount, cardinality)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := query.RelatedExpression(reverse)
		if cardinality == ir.RelationOneToMany {
			if err == nil {
				t.Fatal("collection became a scalar value")
			}
			continue
		}
		if err != nil || !selected.Nullable() {
			t.Fatal("missing reverse OneToOne lost NULL", err)
		}
		if err := plan.ValidateScalar(selected); err != nil {
			t.Fatal(err)
		}
		if _, err := query.RelatedFieldResult(reverse); err != nil {
			t.Fatal("direct and computed single-valued selections diverged", err)
		}
		if err := query.NewPlan("wrong", plan.SourceFields()).ValidateScalar(selected); err == nil {
			t.Fatal("reverse source table bypassed")
		}
	}
	rootValue, _ := query.FieldExpression(amount)
	if rootValue.Equal(value) {
		t.Fatal("same-name root and target leaves collapsed")
	}
}

func TestScalarComparisonValidatesBothOperandsAndScopes(t *testing.T) {
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	left, _ := query.FieldExpression(amount)
	foreign, _ := query.FieldExpression(query.NewFieldRef("amount", "amount", query.FieldInteger, true))
	plan := query.NewPlan("items", []query.FieldRef{amount})
	condition, err := query.NewScalarComparison(left, query.LookupLessThan, foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.WithConditions(condition); err == nil {
		t.Fatal("RHS source metadata was unchecked")
	}
	one, _ := query.LiteralExpression(query.Integer(1))
	first, _ := query.NewScalarComparison(left, query.LookupExact, one)
	second, _ := query.NewScalarComparison(left, query.LookupExact, left)
	if first.Equal(second) {
		t.Fatal("condition equality ignored RHS value")
	}
	if actual, ok := first.RHSScalar(); !ok || !actual.Equal(one) {
		t.Fatal("RHS accessor lost operand")
	}
	for _, kind := range []query.FieldKind{query.FieldBoolean, query.FieldJSON, query.FieldString} {
		right, _ := query.NullExpression(kind)
		if _, err := query.NewScalarComparison(left, query.LookupExact, right); err == nil {
			t.Fatal("comparison coerced result kind", kind)
		}
		if kind != query.FieldString {
			if _, err := query.NewScalarComparison(right, query.LookupLessThan, right); err == nil {
				t.Fatal("unordered comparison accepted", kind)
			}
		}
	}
	if _, err := query.NewScalarComparison(left, query.LookupIsNull, one); err == nil {
		t.Fatal("isnull accepted a scalar RHS")
	}
	selection, _ := query.ScalarResult(left)
	shape, _ := query.NewProjectionResult(selection)
	scope, _ := query.NewResultScope(shape)
	other, _ := query.NewResultScope(shape)
	ref, _ := scope.Column(0)
	otherRef, _ := other.Column(0)
	selected, _ := query.SelectedExpression(ref)
	otherSelected, _ := query.SelectedExpression(otherRef)
	condition, _ = query.NewScalarComparison(one, query.LookupExact, selected)
	if _, err := plan.WithConditions(condition); err == nil {
		t.Fatal("selected RHS crossed into a model source")
	}
	predicate, _ := query.NewExpression(condition)
	branch, _ := query.WhenScalar(predicate, one)
	caseValue, _ := query.CaseScalar(one, branch)
	if _, err := query.NewScalarAssignment(amount, caseValue); err == nil {
		t.Fatal("selected RHS hidden in CASE reached UPDATE")
	}
	if err := caseValue.ValidateResultSource(scope); err != nil {
		t.Fatal(err)
	}
	if err := caseValue.ValidateResultSource(other); err == nil {
		t.Fatal("CASE RHS crossed a result scope")
	}
	condition, _ = query.NewScalarComparison(selected, query.LookupExact, otherSelected)
	predicate, _ = query.NewExpression(condition)
	branch, _ = query.WhenScalar(predicate, one)
	caseValue, _ = query.CaseScalar(one, branch)
	if err := caseValue.ValidateResultSource(scope); err == nil {
		t.Fatal("RHS from an equal-shaped scope was accepted")
	}
}

func TestRelatedScalarCaseCannotHideAssignmentOrStageCrossing(t *testing.T) {
	root, target := ir.ModelIdentity{AppLabel: "app", ModelName: "entry"}, ir.ModelIdentity{AppLabel: "app", ModelName: "datum"}
	fk := query.NewFieldRef("datum", "datum_id", query.FieldInteger, true)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	path, _ := query.NewForwardRelationPath(root, "entries", "datum", "datum_id", target, "data", "id", true, amount, ir.RelationManyToOne)
	related, _ := query.RelatedExpression(path)
	one, _ := query.LiteralExpression(query.Integer(1))
	condition, _ := query.NewScalarComparison(one, query.LookupLessThan, related)
	predicate, err := query.NewExpression(condition)
	if err != nil || !predicate.HasRelations() || len(predicate.SourceFields()) != 0 {
		t.Fatal("RHS relation provenance lost", err)
	}
	direct, _ := query.NewExpression(query.NewRelatedCondition(path, query.LookupExact, query.Integer(1)))
	for _, predicate := range []query.Expression{predicate, direct} {
		branch, err := query.WhenScalar(predicate, one)
		if err != nil {
			t.Fatal(err)
		}
		value, err := query.CaseScalar(one, branch)
		if err != nil {
			t.Fatal(err)
		}
		value, err = query.NegateScalar(value)
		if err != nil {
			t.Fatal(err)
		}
		value, err = query.Arithmetic(query.ArithmeticAdd, one, value)
		if err != nil {
			t.Fatal(err)
		}
		if !value.HasRelations() || len(value.RelationPaths()) != 1 {
			t.Fatal("composed CASE lost hidden relation")
		}
		if _, err := query.NewScalarAssignment(amount, value); err == nil {
			t.Fatal("hidden relation assigned")
		}
		if err := query.NewPlan("entries", []query.FieldRef{fk, amount}).ValidateScalar(value); err != nil {
			t.Fatal(err)
		}
		if err := query.NewPlan("entries", []query.FieldRef{amount}).ValidateScalar(value); err == nil {
			t.Fatal("hidden relation lost source authority")
		}
		result, _ := query.ScalarResult(one)
		shape, _ := query.NewProjectionResult(result)
		scope, _ := query.NewResultScope(shape)
		if err := value.ValidateResultSource(scope); err == nil {
			t.Fatal("hidden original relation crossed result stage")
		}
	}
}

func TestScalarComparisonCountsBothResourceBudgets(t *testing.T) {
	one, _ := query.LiteralExpression(query.Integer(1))
	wide := one
	for wide.NodeCount()*2+1 < query.MaximumScalarNodes {
		var err error
		wide, err = query.Arithmetic(query.ArithmeticAdd, wide, wide)
		if err != nil {
			t.Fatal(err)
		}
	}
	condition, err := query.NewScalarComparison(wide, query.LookupExact, wide)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewExpression(condition); err == nil {
		t.Fatal("RHS nodes bypassed combined predicate budget")
	}
	deep := one
	for deep.Depth() < query.MaximumScalarDepth {
		var err error
		deep, err = query.NegateScalar(deep)
		if err != nil {
			t.Fatal(err)
		}
	}
	condition, err = query.NewScalarComparison(one, query.LookupExact, deep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewExpression(condition); err == nil {
		t.Fatal("RHS depth bypassed predicate budget")
	}
}
