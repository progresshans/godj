package query_test

import (
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

func TestDurationScalingRetainsFactorDomainsAndDimensionlessRatios(t *testing.T) {
	period := scalarLiteral(t, query.Duration(duration.FromMicroseconds(9)))
	factors := map[string]query.ScalarExpression{
		"integer": scalarLiteral(t, query.Integer(3)),
		"float":   scalarLiteral(t, query.Float(0.5)),
		"decimal": scalarLiteral(t, query.Decimal(decimal.Decimal{Coefficient: "25", Exponent: -1})),
	}
	for name, factor := range factors {
		for _, operation := range []struct {
			name     string
			operator query.ArithmeticOperator
		}{{"multiply", query.ArithmeticMultiply}, {"divide", query.ArithmeticDivide}} {
			t.Run(name+"/"+operation.name, func(t *testing.T) {
				value, err := query.Arithmetic(operation.operator, period, factor)
				if err != nil || value.ResultKind() != query.FieldDuration || value.Validate() != nil || !value.RequiresDurationFraction() || !value.RequiresDurationRounding() {
					t.Fatal("period scaling lost its result domain", err)
				}
				actual, left, right, valid := value.Binary()
				if !valid || actual != operation.operator || !left.Equal(period) || !right.Equal(factor) {
					t.Fatal("period scaling coerced or reordered an operand")
				}
			})
		}
		value, err := query.Arithmetic(query.ArithmeticMultiply, factor, period)
		if err != nil || value.ResultKind() != query.FieldDuration {
			t.Fatal("reverse scaling lost its period domain", name, err)
		}
		_, left, right, _ := value.Binary()
		if !left.Equal(factor) || !right.Equal(period) {
			t.Fatal("reverse scaling changed the operand domains or order")
		}
	}
	ratio, err := query.Arithmetic(query.ArithmeticDivide, period, period)
	if err != nil || ratio.ResultKind() != query.FieldFloat || ratio.RequiresDurationRounding() {
		t.Fatal("period ratio retained a time unit or decoder", err)
	}
}

func TestDurationScalingFractionProvenanceAcrossResultStages(t *testing.T) {
	period := scalarLiteral(t, query.Duration(duration.FromMicroseconds(1)))
	fraction, err := query.Arithmetic(query.ArithmeticDivide, period, scalarLiteral(t, query.Integer(3)))
	if err != nil {
		t.Fatal(err)
	}
	negated, err := query.NegateScalar(fraction)
	if err != nil {
		t.Fatal(err)
	}
	field := query.NewFieldRef("id", "id", query.FieldInteger, false)
	predicate, err := query.NewExpression(query.NewCondition(field, query.LookupExact, query.Integer(1)))
	if err != nil {
		t.Fatal(err)
	}
	branch, err := query.WhenScalar(predicate, negated)
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := query.CaseScalar(period, branch)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []query.ResultExpressionKind{query.ResultSum, query.ResultAvg, query.ResultMin, query.ResultMax} {
		aggregate, err := query.ScalarAggregateResult(kind, conditional)
		if err != nil || !aggregate.RequiresDurationFraction() {
			t.Fatal("aggregate lost exact fractional provenance", kind, err)
		}
		shape, err := query.NewAggregateResult(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		scope, err := query.NewResultScope(shape)
		if err != nil {
			t.Fatal(err)
		}
		ref, err := scope.Column(0)
		if err != nil {
			t.Fatal(err)
		}
		selected, err := query.SelectedExpression(ref)
		if err != nil || !selected.RequiresDurationFraction() {
			t.Fatal("result stage rounded a fraction", err)
		}
		composed, err := query.Arithmetic(query.ArithmeticAdd, selected, period)
		if err != nil || !composed.RequiresDurationFraction() {
			t.Fatal("later arithmetic dropped a denominator", err)
		}
		ratio, err := query.Arithmetic(query.ArithmeticDivide, composed, period)
		if err != nil || ratio.RequiresDurationFraction() || ratio.RequiresDurationRounding() {
			t.Fatal("Float ratio retained a Duration scanner", err)
		}
	}
	if period.RequiresDurationFraction() || period.RequiresDurationRounding() {
		t.Fatal("a shared ordinary Duration acquired fractional provenance")
	}
}

func TestDurationScalingRequiresUnambiguousKinds(t *testing.T) {
	period := temporalNull(t, query.FieldDuration)
	for _, kind := range []query.FieldKind{query.FieldInteger, query.FieldFloat, query.FieldDecimal} {
		factor := temporalNull(t, kind)
		for _, operator := range []query.ArithmeticOperator{query.ArithmeticMultiply, query.ArithmeticDivide} {
			value, err := query.Arithmetic(operator, period, factor)
			if err != nil || value.ResultKind() != query.FieldDuration || !value.Nullable() {
				t.Fatal("typed NULL changed the scaling signature", kind, err)
			}
		}
	}
	for _, factor := range []query.ScalarExpression{
		scalarLiteral(t, query.Null()), temporalNull(t, query.FieldBoolean),
		temporalNull(t, query.FieldDate), temporalNull(t, query.FieldTime), temporalNull(t, query.FieldDateTime),
	} {
		for _, operator := range []query.ArithmeticOperator{query.ArithmeticMultiply, query.ArithmeticDivide} {
			if _, err := query.Arithmetic(operator, period, factor); err == nil {
				t.Fatal("unsupported factor was coerced", factor.ResultKind(), operator)
			}
		}
	}
	if _, err := query.Arithmetic(query.ArithmeticMultiply, period, period); err == nil {
		t.Fatal("a product of two periods inferred a supported unit")
	}
	if _, err := query.Arithmetic(query.ArithmeticDivide, scalarLiteral(t, query.Integer(1)), period); err == nil {
		t.Fatal("inverse period inferred a supported unit")
	}
}
