package query_test

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func scalarLiteral(t *testing.T, value query.Value) query.ScalarExpression {
	t.Helper()
	result, err := query.LiteralExpression(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func scalarField(t *testing.T, field query.FieldRef) query.ScalarExpression {
	t.Helper()
	result, err := query.FieldExpression(field)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func scalarAssignment(t *testing.T, field query.FieldRef, value query.ScalarExpression) query.ScalarAssignment {
	t.Helper()
	result, err := query.NewScalarAssignment(field, value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScalarExpressionTypesOwnershipAndLimits(t *testing.T) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, true)
	field, value := scalarField(t, amount), scalarLiteral(t, query.Integer(2))
	expression, err := query.Arithmetic(query.ArithmeticAdd, field, value)
	if err != nil || expression.ResultKind() != query.FieldInteger || !expression.Nullable() || expression.Depth() != 2 || expression.NodeCount() != 3 {
		t.Fatal(expression, err)
	}
	op, left, right, valid := expression.Binary()
	if !valid || op != query.ArithmeticAdd || !left.Equal(field) || !right.Equal(value) {
		t.Fatal("expression lost its ordered operands")
	}
	other, err := query.Arithmetic(query.ArithmeticAdd, value, field)
	if err != nil || expression.Equal(other) {
		t.Fatal("operand order was erased", err)
	}
	for name, construct := range map[string]func() error{
		"empty":           func() error { return (query.ScalarExpression{}).Validate() },
		"invalid_literal": func() error { _, err := query.LiteralExpression(query.Value{}); return err },
		"invalid_field": func() error {
			_, err := query.FieldExpression(query.NewFieldRef("bad\x00field", "value", query.FieldInteger, false))
			return err
		},
		"operator": func() error {
			_, err := query.Arithmetic(query.ArithmeticOperator("+ 1; DROP TABLE items; --"), field, value)
			return err
		},
		"mixed_kind": func() error {
			_, err := query.Arithmetic(query.ArithmeticAdd, field, scalarLiteral(t, query.Float(2)))
			return err
		},
		"two_nulls": func() error {
			value := scalarLiteral(t, query.Null())
			_, err := query.Arithmetic(query.ArithmeticAdd, value, value)
			return err
		},
		"float_modulo": func() error {
			value := scalarLiteral(t, query.Float(2))
			_, err := query.Arithmetic(query.ArithmeticModulo, value, value)
			return err
		},
		"boolean": func() error {
			value := scalarLiteral(t, query.Boolean(true))
			_, err := query.Arithmetic(query.ArithmeticAdd, value, value)
			return err
		},
		"string":           func() error { _, err := query.NegateScalar(scalarLiteral(t, query.String("1"))); return err },
		"null_nonnullable": func() error { _, err := query.NewScalarAssignment(id, scalarLiteral(t, query.Null())); return err },
		"assignment_kind":  func() error { _, err := query.NewScalarAssignment(id, scalarLiteral(t, query.String("1"))); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := construct(); err == nil {
				t.Fatal("invalid scalar was accepted")
			}
		})
	}
	withNull, err := query.Arithmetic(query.ArithmeticAdd, field, scalarLiteral(t, query.Null()))
	if err != nil || withNull.ResultKind() != query.FieldInteger || !withNull.Nullable() {
		t.Fatal(withNull, err)
	}
	if _, err := query.NewScalarAssignment(id, field); err != nil {
		t.Fatal("nullable reference should be checked per row by native constraints", err)
	}
	bareNull := scalarLiteral(t, query.Null())
	for _, kind := range []query.FieldKind{query.FieldInteger, query.FieldFloat, query.FieldDecimal, query.FieldUUID, query.FieldBinary, query.FieldJSON, query.FieldString, query.FieldBoolean, query.FieldDateTime, query.FieldDate, query.FieldTime, query.FieldDuration} {
		t.Run("null_assignment_"+string(kind), func(t *testing.T) {
			target := query.NewFieldRef("value", "value", kind, true)
			if kind == query.FieldDecimal {
				target = query.NewDecimalFieldRef("value", "value", true, 9, 2)
			}
			typedNull, err := query.NullExpression(kind)
			if err != nil {
				t.Fatal(err)
			}
			assignment := scalarAssignment(t, target, bareNull)
			if !assignment.Equal(scalarAssignment(t, target, typedNull)) || bareNull.ResultKind() != "" || len(assignment.Expression().Fields()) != 0 {
				t.Fatal("NULL assignment lost its target type or mutated the shared literal")
			}
		})
	}
	integerNull, err := query.NullExpression(query.FieldInteger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewScalarAssignment(query.NewFieldRef("text", "text", query.FieldString, true), integerNull); err == nil {
		t.Fatal("an explicitly typed NULL changed kind during assignment")
	}
	if _, err := query.ScalarResult(bareNull); err == nil {
		t.Fatal("assignment supplied a type to an independent read of the same NULL")
	}
	deep := value
	for deep.Depth() < query.MaximumScalarDepth {
		deep, err = query.NegateScalar(deep)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := query.NegateScalar(deep); err == nil {
		t.Fatal("unbounded unary depth")
	}
	wide := value
	for wide.NodeCount()*2+1 <= query.MaximumScalarNodes {
		wide, err = query.Arithmetic(query.ArithmeticAdd, wide, wide)
		if err != nil {
			t.Fatal(err)
		}
	}
	if wide.NodeCount() != 1023 {
		t.Fatal("test did not reach the node boundary")
	}
	if _, err := query.Arithmetic(query.ArithmeticAdd, wide, wide); err == nil {
		t.Fatal("shared nodes bypassed the SQL expansion budget")
	}
	decimal := query.NewDecimalFieldRef("cost", "cost", false, 9, 2)
	narrow := query.NewDecimalFieldRef("small", "small", false, 8, 2)
	if _, err := query.NewScalarAssignment(narrow, scalarField(t, decimal)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("lossy Decimal copy was implicit", err)
	}
	if _, err := query.NewScalarAssignment(decimal, scalarField(t, narrow)); err != nil {
		t.Fatal("exact widening rejected", err)
	}
	if _, err := query.Arithmetic(query.ArithmeticAdd, scalarField(t, decimal), scalarField(t, decimal)); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("Decimal arithmetic used a floating coercion", err)
	}
}

func TestQueryUpdatePlanOwnsSelectionAndAssignments(t *testing.T) {
	id := query.NewFieldRef("id", "key_column", query.FieldInteger, false)
	amount := query.NewFieldRef("amount", "amount", query.FieldInteger, false)
	fields := []query.FieldRef{id, amount}
	source := query.NewPlan("items", fields).WithOrderings(query.NewOrdering(amount, query.Descending)).WithDistinct()
	assignments := []query.ScalarAssignment{scalarAssignment(t, amount, scalarField(t, id))}
	plan, err := query.NewQueryUpdatePlan(source, id, assignments)
	if err != nil {
		t.Fatal(err)
	}
	fields[0], assignments[0] = amount, scalarAssignment(t, id, scalarLiteral(t, query.Integer(41)))
	returned := plan.Assignments()
	returned[0] = assignments[0]
	if !plan.Assignments()[0].Field().Equal(amount) || len(plan.Selection().Orderings()) != 0 || plan.Selection().Distinct() || plan.NoOp() {
		t.Fatal("plan aliases input or retains read shape")
	}
	if len(source.Orderings()) != 1 || !source.Distinct() {
		t.Fatal("write changed source query")
	}
	if _, err := query.NewQueryUpdatePlan(source, id, assignments); err != nil {
		t.Fatal("explicit primary-key assignment rejected", err)
	}
	empty, err := query.NewQueryUpdatePlan(source, id, nil)
	if err != nil || !empty.NoOp() {
		t.Fatal(empty, err)
	}
	for name, assignments := range map[string][]query.ScalarAssignment{
		"zero":            {{}},
		"duplicate":       {plan.Assignments()[0], plan.Assignments()[0]},
		"foreign_target":  {scalarAssignment(t, query.NewFieldRef("foreign", "foreign", query.FieldInteger, false), scalarField(t, id))},
		"foreign_operand": {scalarAssignment(t, amount, scalarField(t, query.NewFieldRef("id", "key_column", query.FieldInteger, true)))},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := query.NewQueryUpdatePlan(source, id, assignments); err == nil {
				t.Fatal("invalid ownership accepted")
			}
		})
	}
	sliced, err := source.WithLimit(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewQueryUpdatePlan(sliced, id, nil); !errors.Is(err, &query.Error{Code: query.CodeUnsupported}) {
		t.Fatal("empty assignments bypassed slice rejection", err)
	}
	wide := scalarLiteral(t, query.Integer(1))
	for wide.NodeCount()*2+1 <= query.MaximumScalarNodes {
		wide, err = query.Arithmetic(query.ArithmeticAdd, wide, wide)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := query.NewQueryUpdatePlan(source, id, []query.ScalarAssignment{scalarAssignment(t, id, wide), scalarAssignment(t, amount, wide)}); err == nil {
		t.Fatal("assignment set exceeded total expression budget")
	}
}
