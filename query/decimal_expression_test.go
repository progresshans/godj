package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
)

func TestDecimalExpressionsRetainSourceAndResultDomains(t *testing.T) {
	price := query.NewDecimalFieldRef("price", "price", true, 14, 2)
	fee := query.NewDecimalFieldRef("fee", "fee", false, 9, 4)
	left, right := scalarField(t, price), scalarField(t, fee)
	for _, operator := range []query.ArithmeticOperator{query.ArithmeticAdd, query.ArithmeticSubtract, query.ArithmeticMultiply, query.ArithmeticDivide, query.ArithmeticModulo} {
		t.Run(string(operator), func(t *testing.T) {
			value, err := query.Arithmetic(operator, left, right)
			if err != nil || value.ResultKind() != query.FieldDecimal || !value.Nullable() {
				t.Fatal(value, err)
			}
			if fields := value.Fields(); len(fields) != 2 || fields[0] != price || fields[1] != fee {
				t.Fatal("calculation replaced source precision with a result field")
			}
			if err := value.ValidateSource([]query.FieldRef{price, fee}); err != nil {
				t.Fatal(err)
			}
			if err := value.ValidateSource([]query.FieldRef{query.NewDecimalFieldRef("price", "price", true, 15, 2), fee}); err == nil {
				t.Fatal("arithmetic accepted conflicting source precision")
			}
			// The computed value may fit even when an input field's declared
			// scale is wider. Only the final value is assigned to this target.
			if _, err := query.NewScalarAssignment(price, value); err != nil {
				t.Fatal("computed precision was mistaken for a direct field copy", err)
			}
			selected, err := query.ScalarResult(value)
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []query.ResultExpressionKind{query.ResultCount, query.ResultMin, query.ResultMax, query.ResultSum, query.ResultAvg} {
				aggregate, err := query.AggregateResult(kind, selected)
				want := query.FieldDecimal
				if kind == query.ResultCount {
					want = query.FieldInteger
				}
				if err != nil || aggregate.ResultValueKind() != want {
					t.Fatal("aggregate changed the Decimal result domain", kind, aggregate, err)
				}
			}
		})
	}
	number, err := decimal.Parse("0.001")
	if err != nil {
		t.Fatal(err)
	}
	literal := scalarLiteral(t, query.Decimal(number))
	if _, err := query.NewScalarAssignment(price, literal); err == nil {
		t.Fatal("new arithmetic weakened direct literal assignment precision")
	}
	if _, err := query.NewScalarAssignment(price, right); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("new arithmetic weakened direct field copy precision", err)
	}
	for _, other := range []query.Value{query.Integer(1), query.Float(1), query.String("1")} {
		if _, err := query.Arithmetic(query.ArithmeticAdd, left, scalarLiteral(t, other)); err == nil {
			t.Fatal("Decimal arithmetic inserted an implicit conversion")
		}
	}
	null := scalarLiteral(t, query.Null())
	withNull, err := query.Arithmetic(query.ArithmeticAdd, right, null)
	if err != nil || withNull.ResultKind() != query.FieldDecimal || !withNull.Nullable() || null.ResultKind() != "" {
		t.Fatal("typed arithmetic mutated its shared NULL", err)
	}
	negative, err := query.NegateScalar(right)
	if err != nil || negative.ResultKind() != query.FieldDecimal || negative.Nullable() {
		t.Fatal("negation changed result kind or nullability", err)
	}
	projection, err := query.NewProjectionResult(query.FieldResult(price))
	if err != nil {
		t.Fatal(err)
	}
	scope, err := query.NewResultScope(projection)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := scope.Column(0)
	if err != nil {
		t.Fatal(err)
	}
	producer, valid := reference.Expression()
	if field, present := producer.Field(); !valid || !present || field != price {
		t.Fatal("result reference lost direct-source validation metadata")
	}
	selected, err := query.SelectedExpression(reference)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := query.Arithmetic(query.ArithmeticMultiply, selected, literal)
	if err != nil || derived.ValidateResultSource(scope) != nil {
		t.Fatal(derived, err)
	}
	if _, err := query.NewScalarAssignment(price, derived); err == nil {
		t.Fatal("selected Decimal bypassed assignment scope")
	}
	foreign, err := query.NewResultScope(projection)
	if err != nil || derived.ValidateResultSource(foreign) == nil || derived.ValidateSource([]query.FieldRef{price}) == nil {
		t.Fatal("equal shape or original model authorized a foreign result scope", err)
	}
}
