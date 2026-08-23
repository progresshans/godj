package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestFieldComparisonConditionIsImmutableAndSourceBound(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	minimum := query.NewFieldRef("minimum", "minimum", query.FieldInteger, true)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)

	for _, test := range []struct {
		name   string
		left   query.FieldRef
		lookup query.Lookup
		right  query.FieldRef
	}{
		{name: "integer exact", left: id, lookup: query.LookupExact, right: minimum},
		{name: "integer greater than", left: id, lookup: query.LookupGreaterThan, right: minimum},
		{name: "integer greater than or equal", left: id, lookup: query.LookupGreaterThanOrEqual, right: id},
		{name: "integer less than", left: id, lookup: query.LookupLessThan, right: minimum},
		{name: "integer less than or equal", left: minimum, lookup: query.LookupLessThanOrEqual, right: id},
		{name: "string exact nullable RHS", left: title, lookup: query.LookupExact, right: summary},
		{name: "string ordered nullable LHS", left: summary, lookup: query.LookupGreaterThanOrEqual, right: title},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			condition, err := query.NewFieldCondition(test.left, test.lookup, test.right)
			if err != nil {
				t.Fatalf("NewFieldCondition() error = %v", err)
			}
			if got, ok := condition.RHSField(); !ok || !got.Equal(test.right) {
				t.Fatalf("RHSField() = (%#v, %v), want %#v/true", got, ok, test.right)
			}
			if condition.Value() != (query.Value{}) {
				t.Fatalf("field condition Value() = %#v, want zero", condition.Value())
			}
			if values, ok := condition.Values(); ok || values != nil {
				t.Fatalf("field condition Values() = (%#v, %v), want nil/false", values, ok)
			}

			expression, err := query.NewExpression(condition)
			if err != nil {
				t.Fatalf("NewExpression() error = %v", err)
			}
			plan, err := query.NewPlan("article", []query.FieldRef{id, minimum, title, summary}).WithWhere(expression)
			if err != nil {
				t.Fatalf("WithWhere() error = %v", err)
			}
			if !plan.Equal(query.NewPlan("article", []query.FieldRef{id, minimum, title, summary}).WithConditions(condition)) {
				t.Fatal("validated and unchecked plans with the same field condition differ")
			}
			returned := plan.Conditions()
			if len(returned) != 1 || !returned[0].Equal(condition) {
				t.Fatalf("Conditions() = %#v, want detached field condition", returned)
			}
		})
	}

	condition, err := query.NewFieldCondition(title, query.LookupExact, summary)
	if err != nil {
		t.Fatalf("NewFieldCondition() error = %v", err)
	}
	expression, err := query.NewExpression(condition)
	if err != nil {
		t.Fatalf("NewExpression() error = %v", err)
	}
	if _, err := query.NewPlan("article", []query.FieldRef{id, title}).WithWhere(expression); !invalidComparisonPlan(err) {
		t.Fatalf("WithWhere(missing RHS source) error = %v, want invalid_plan", err)
	}
}

func TestComparisonLookupAndFieldRHSValidation(t *testing.T) {
	t.Parallel()

	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)

	for _, condition := range []query.Condition{
		query.NewCondition(id, query.LookupGreaterThan, query.Integer(1)),
		query.NewCondition(id, query.LookupGreaterThanOrEqual, query.Integer(1)),
		query.NewCondition(id, query.LookupLessThan, query.Integer(1)),
		query.NewCondition(id, query.LookupLessThanOrEqual, query.Integer(1)),
		query.NewCondition(title, query.LookupGreaterThan, query.String("a")),
		query.NewCondition(title, query.LookupLessThanOrEqual, query.String("z")),
	} {
		if _, err := query.NewExpression(condition); err != nil {
			t.Fatalf("valid ordered condition %#v error = %v", condition, err)
		}
	}

	for _, condition := range []query.Condition{
		query.NewCondition(id, query.LookupGreaterThan, query.String("1")),
		query.NewCondition(title, query.LookupLessThan, query.Integer(1)),
		query.NewCondition(published, query.LookupGreaterThan, query.Boolean(false)),
		query.NewCondition(published, query.LookupLessThanOrEqual, query.Boolean(true)),
	} {
		if _, err := query.NewExpression(condition); !invalidComparisonPlan(err) {
			t.Fatalf("invalid ordered condition %#v error = %v, want invalid_plan", condition, err)
		}
	}

	for _, test := range []struct {
		name   string
		left   query.FieldRef
		lookup query.Lookup
		right  query.FieldRef
	}{
		{name: "zero RHS", left: title, lookup: query.LookupExact},
		{name: "kind mismatch", left: title, lookup: query.LookupExact, right: id},
		{name: "Boolean exact excluded", left: published, lookup: query.LookupExact, right: published},
		{name: "icontains", left: title, lookup: query.LookupIContains, right: title},
		{name: "isnull", left: title, lookup: query.LookupIsNull, right: title},
		{name: "IN", left: title, lookup: query.LookupIn, right: title},
		{name: "unknown", left: title, lookup: query.Lookup("starts"), right: title},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			condition, err := query.NewFieldCondition(test.left, test.lookup, test.right)
			if !invalidComparisonPlan(err) {
				t.Fatalf("NewFieldCondition() error = %v, want invalid_plan", err)
			}
			if condition != (query.Condition{}) {
				t.Fatalf("failed condition = %#v, want zero", condition)
			}
		})
	}
}

func invalidComparisonPlan(err error) bool {
	return errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan})
}
