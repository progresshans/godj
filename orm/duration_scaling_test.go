package orm

import (
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

type durationScalingModel struct{}

func TestDurationScalingBuildersKeepFactorAndResultTypes(t *testing.T) {
	period := Value[durationScalingModel](duration.FromMicroseconds(9))
	for _, test := range []struct {
		name     string
		value    DurationExpression[durationScalingModel]
		factor   query.FieldKind
		operator query.ArithmeticOperator
	}{
		{"integer", ScaleDuration(period, int64(3)), query.FieldInteger, query.ArithmeticMultiply},
		{"float", ScaleDuration(period, 0.5), query.FieldFloat, query.ArithmeticMultiply},
		{"decimal", ScaleDuration(period, decimal.Decimal{Coefficient: "25", Exponent: -1}), query.FieldDecimal, query.ArithmeticMultiply},
		{"integer_expression", ScaleDurationExpressions(period, Value[durationScalingModel](int64(3))), query.FieldInteger, query.ArithmeticMultiply},
		{"integer_divisor", DivideDuration(period, int64(3)), query.FieldInteger, query.ArithmeticDivide},
		{"float_divisor", DivideDurationExpressions(period, Value[durationScalingModel](0.5)), query.FieldFloat, query.ArithmeticDivide},
		{"decimal_divisor", DivideDuration(period, decimal.Decimal{Coefficient: "25", Exponent: -1}), query.FieldDecimal, query.ArithmeticDivide},
	} {
		t.Run(test.name, func(t *testing.T) {
			expression, err := operandExpression[durationScalingModel, duration.Duration](test.value)
			if err != nil || expression.ResultKind() != query.FieldDuration {
				t.Fatal("typed scaling result differs", err)
			}
			operator, left, right, valid := expression.Binary()
			if !valid || operator != test.operator || left.ResultKind() != query.FieldDuration || right.ResultKind() != test.factor {
				t.Fatal("typed scaling converted its operands")
			}
		})
	}
	ratio := Duration(period).RatioExpression(period)
	expression, err := operandExpression[durationScalingModel, float64](ratio)
	if err != nil || expression.ResultKind() != query.FieldFloat {
		t.Fatal("typed ratio retained a period result", err)
	}
	var absent ScalarExpression[durationScalingModel, duration.Duration]
	if _, err := operandExpression[durationScalingModel, duration.Duration](ScaleDuration(absent, int64(2))); err == nil {
		t.Fatal("invalid period builder reached scaling")
	}
}
