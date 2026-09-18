package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
)

func TestMembershipEmptyAnalysisPreservesNullableNegationContext(t *testing.T) {
	optional := query.NewFieldRef("score", "score", query.FieldInteger, true)
	required := query.NewFieldRef("id", "id", query.FieldInteger, false)
	must := func(expression query.Expression, err error) query.Expression {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return expression
	}
	in := func(field query.FieldRef, values ...query.Value) query.Expression {
		t.Helper()
		condition, err := query.NewInCondition(field, values)
		if err != nil {
			t.Fatal(err)
		}
		return must(query.NewExpression(condition))
	}
	not := func(value query.Expression) query.Expression { return must(query.NotExpression(value)) }
	and := func(left, right query.Expression) query.Expression { return must(query.AndExpressions(left, right)) }
	or := func(left, right query.Expression) query.Expression { return must(query.OrExpressions(left, right)) }
	empty, nullOnly := in(optional), in(optional, query.Null())
	value := in(optional, query.Integer(1))
	for _, test := range []struct {
		name       string
		expression query.Expression
		empty      bool
	}{
		{"empty", empty, true},
		{"NULL only", nullOnly, true},
		{"value", value, false},
		{"empty NOT", not(empty), false},
		{"NULL only NOT", not(nullOnly), false},
		{"nonnullable NULL NOT", not(in(required, query.Null())), false},
		{"empty double NOT", not(not(empty)), true},
		{"NULL double NOT", not(not(nullOnly)), true},
		{"false AND unknown", and(empty, value), true},
		{"unknown AND false", and(value, empty), true},
		{"false OR unknown", or(empty, value), false},
		{"both false OR", or(empty, nullOnly), true},
		{"negated nullable context", not(and(nullOnly, not(empty))), false},
		{"negated tautology", not(or(nullOnly, not(empty))), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := query.NewPlan("entries", []query.FieldRef{required, optional}).WithWhere(test.expression)
			if err != nil {
				t.Fatal(err)
			}
			if plan.EmptyResult() != test.empty {
				t.Fatal("empty analysis changed NULL or Boolean semantics")
			}
		})
	}
}

func TestMembershipListsKeepEmptyAndNullIdentityWithoutCallerAliases(t *testing.T) {
	field := query.NewFieldRef("score", "score", query.FieldInteger, true)
	input := []query.Value{query.Null(), query.Integer(1), query.Integer(1)}
	condition, err := query.NewInCondition(field, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = query.Integer(2)
	values, ok := condition.Values()
	if !ok || len(values) != 3 || !values[0].IsNull() {
		t.Fatal("caller changed membership NULL identity")
	}
	values[0] = query.Integer(3)
	again, _ := condition.Values()
	if !again[0].IsNull() {
		t.Fatal("accessor returned aliased membership storage")
	}
	withoutNull, err := query.NewInCondition(field, []query.Value{query.Integer(1), query.Integer(1)})
	if err != nil || condition.Equal(withoutNull) {
		t.Fatal("membership dropped negation-relevant NULL identity")
	}
	for _, input := range [][]query.Value{nil, {}, {query.Null()}} {
		condition, err := query.NewInCondition(field, input)
		if err != nil {
			t.Fatal("valid empty or NULL list rejected:", err)
		}
		if values, ok := condition.Values(); !ok || len(values) != len(input) {
			t.Fatal("list framing was lost")
		}
	}
	invalid := query.NewFieldRef("bad", "bad", query.FieldKind("unknown"), false)
	if _, err := query.NewInCondition(invalid, nil); err == nil {
		t.Fatal("empty list concealed invalid field metadata")
	}
}
