package query_test

import (
	"testing"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestNumericAggregateResultsPreserveSourceAndWidenIntegerAverage(t *testing.T) {
	fields := []query.FieldRef{
		query.NewFieldRef("integer", "integer", query.FieldInteger, false),
		query.NewFieldRef("float", "float", query.FieldFloat, true),
		query.NewDecimalFieldRef("decimal", "decimal", true, 14, 2),
		query.NewFieldRef("duration", "duration", query.FieldDuration, false),
	}
	for _, field := range fields {
		for _, makeAggregate := range []func(query.ResultExpression) (query.ResultExpression, error){query.SumResult, query.AvgResult} {
			operand := query.FieldResult(field)
			value, err := makeAggregate(operand)
			if err != nil {
				t.Fatal(err)
			}
			resultKind := field.Kind()
			if value.Kind() == query.ResultAvg && field.Kind() == query.FieldInteger {
				resultKind = query.FieldFloat
			}
			if !value.IsAggregate() || !value.ResultNullable() || value.ResultValueKind() != resultKind {
				t.Fatal("numeric result type", value.Kind(), field.Kind())
			}
			if retained, ok := value.Field(); !ok || !retained.Equal(field) || operand.Kind() != query.ResultField {
				t.Fatal("rewrote source field")
			}
			distinct, err := value.WithDistinct()
			if err != nil || !distinct.Distinct() || value.Distinct() || distinct.Equal(value) {
				t.Fatal("distinct identity", err)
			}
			shape, err := query.NewAggregateResult(value, distinct)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := query.NewPlan("values", fields).WithResultShape(shape); err != nil {
				t.Fatal(err)
			}
			if _, err := makeAggregate(value); err == nil {
				t.Fatal("nested aggregate accepted")
			}
		}
	}
	average, err := query.AvgResult(query.FieldResult(fields[0]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewGroupExpression(average, query.LookupGreaterThan, query.Float(1.5)); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewGroupExpression(average, query.LookupGreaterThan, query.Integer(1)); err == nil {
		t.Fatal("AVG(integer) HAVING used operand kind")
	}
	if query.CountAllResult().ResultNullable() || query.CountAllResult().ResultValueKind() != query.FieldInteger {
		t.Fatal("count result type")
	}
	if _, err := query.NewPlan("values", []query.FieldRef{query.NewFieldRef("integer", "integer", query.FieldFloat, false)}).WithResultShape(mustNumericShape(t, average)); err == nil {
		t.Fatal("result type replaced source authority")
	}
}

func mustNumericShape(t *testing.T, expression query.ResultExpression) query.ResultShape {
	t.Helper()
	shape, err := query.NewAggregateResult(expression)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

func TestNumericAggregatesRejectNonnumericOperandsAndRetainForwardAuthority(t *testing.T) {
	for _, kind := range []query.FieldKind{query.FieldBoolean, query.FieldString, query.FieldDate, query.FieldTime, query.FieldDateTime, query.FieldUUID, query.FieldBinary, query.FieldJSON} {
		operand := query.FieldResult(query.NewFieldRef("value", "value", kind, true))
		if _, err := query.SumResult(operand); err == nil {
			t.Fatal("SUM accepted", kind)
		}
		if _, err := query.AvgResult(operand); err == nil {
			t.Fatal("AVG accepted", kind)
		}
	}
	for _, operand := range []query.ResultExpression{{}, query.CountAllResult(), query.FieldResult(query.NewFieldRef("cost", "cost", query.FieldDecimal, true))} {
		if _, err := query.SumResult(operand); err == nil {
			t.Fatal("invalid numeric operand")
		}
	}
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	fk := query.NewFieldRef("parent", "parent_id", query.FieldInteger, true)
	cost := query.NewDecimalFieldRef("cost", "cost", false, 7, 2)
	path, err := query.NewForwardRelationPath(ir.ModelIdentity{AppLabel: "a", ModelName: "child"}, "children", "parent", "parent_id", ir.ModelIdentity{AppLabel: "a", ModelName: "parent"}, "parents", "id", true, cost, ir.RelationManyToOne)
	if err != nil {
		t.Fatal(err)
	}
	operand, err := query.RelatedFieldResult(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := query.AvgResult(operand)
	if err != nil {
		t.Fatal(err)
	}
	retained, related := value.RelationPath()
	if !related || !retained.Equal(path) || !value.ResultNullable() || value.ResultValueKind() != query.FieldDecimal {
		t.Fatal("forward aggregate lost route")
	}
	if _, err := query.NewPlan("children", []query.FieldRef{id, fk}).WithResultShape(mustNumericShape(t, value)); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewPlan("children", []query.FieldRef{id}).WithResultShape(mustNumericShape(t, value)); err == nil {
		t.Fatal("forward aggregate bypassed root metadata")
	}
}
