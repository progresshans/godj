package query_test

import (
	"testing"
	"time"

	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

func temporalNull(t *testing.T, kind query.FieldKind) query.ScalarExpression {
	t.Helper()
	value, err := query.NullExpression(kind)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCalendarArithmeticRetainsExplicitDomainsAndPrecision(t *testing.T) {
	day := scalarLiteral(t, query.Date(calendar.Date{Year: 2000, Month: 2, Day: 29}))
	wall := scalarLiteral(t, query.Time(clock.Time{Hour: 23, Minute: 59, Second: 59, Microsecond: 999999}))
	instant := scalarLiteral(t, query.DateTime(time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)))
	period := scalarLiteral(t, query.Duration(duration.FromMicroseconds(1)))
	fraction, err := query.Arithmetic(query.ArithmeticDivide, period, scalarLiteral(t, query.Integer(2)))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		operator    query.ArithmeticOperator
		left, right query.ScalarExpression
		kind        query.FieldKind
	}{
		{"date_add", query.ArithmeticAdd, day, fraction, query.FieldDateTime},
		{"date_reverse", query.ArithmeticAdd, fraction, day, query.FieldDateTime},
		{"date_subtract", query.ArithmeticSubtract, day, period, query.FieldDateTime},
		{"date_difference", query.ArithmeticSubtract, day, day, query.FieldDuration},
		{"date_instant_difference", query.ArithmeticSubtract, day, instant, query.FieldDuration},
		{"instant_date_difference", query.ArithmeticSubtract, instant, day, query.FieldDuration},
		{"clock_add", query.ArithmeticAdd, wall, fraction, query.FieldTime},
		{"clock_reverse", query.ArithmeticAdd, fraction, wall, query.FieldTime},
		{"clock_subtract", query.ArithmeticSubtract, wall, period, query.FieldTime},
		{"clock_difference", query.ArithmeticSubtract, wall, wall, query.FieldDuration},
		{"missing_date", query.ArithmeticAdd, temporalNull(t, query.FieldDate), fraction, query.FieldDateTime},
		{"missing_clock", query.ArithmeticSubtract, wall, temporalNull(t, query.FieldTime), query.FieldDuration},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := query.Arithmetic(test.operator, test.left, test.right)
			if err != nil || value.ResultKind() != test.kind || value.Nullable() != (test.left.Nullable() || test.right.Nullable()) || value.RequiresDurationFraction() || value.RequiresDurationRounding() {
				t.Fatal("calendar result acquired the wrong domain or precision", value, err)
			}
			operator, left, right, ok := value.Binary()
			if !ok || operator != test.operator || !left.Equal(test.left) || !right.Equal(test.right) {
				t.Fatal("a temporal boundary changed its exact source operands")
			}
		})
	}
	for _, test := range []struct {
		operator    query.ArithmeticOperator
		left, right query.ScalarExpression
	}{
		{query.ArithmeticAdd, day, wall}, {query.ArithmeticAdd, wall, wall}, {query.ArithmeticAdd, day, day},
		{query.ArithmeticSubtract, period, day}, {query.ArithmeticSubtract, period, wall},
		{query.ArithmeticSubtract, wall, day}, {query.ArithmeticSubtract, instant, wall},
		{query.ArithmeticMultiply, day, period}, {query.ArithmeticDivide, wall, period},
	} {
		if _, err := query.Arithmetic(test.operator, test.left, test.right); err == nil {
			t.Fatal("unsupported calendar arithmetic inferred a date or unit", test)
		}
	}
	if !fraction.RequiresDurationFraction() || fraction.RequiresDurationRounding() {
		t.Fatal("reusing a fractional period mutated its provenance")
	}
}

func TestTemporalArithmeticPreservesOperandAndResultDomains(t *testing.T) {
	instant := scalarLiteral(t, query.DateTime(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)))
	period := scalarLiteral(t, query.Duration(duration.Duration{Microseconds: 1}))
	for _, test := range []struct {
		name        string
		operator    query.ArithmeticOperator
		left, right query.ScalarExpression
		result      query.FieldKind
	}{
		{"shift", query.ArithmeticAdd, instant, period, query.FieldDateTime},
		{"reverse_shift", query.ArithmeticAdd, period, instant, query.FieldDateTime},
		{"subtract_period", query.ArithmeticSubtract, instant, period, query.FieldDateTime},
		{"difference", query.ArithmeticSubtract, instant, instant, query.FieldDuration},
		{"add_periods", query.ArithmeticAdd, period, period, query.FieldDuration},
		{"subtract_periods", query.ArithmeticSubtract, period, period, query.FieldDuration},
		{"null_period", query.ArithmeticAdd, instant, temporalNull(t, query.FieldDuration), query.FieldDateTime},
		{"null_instant_difference", query.ArithmeticSubtract, instant, temporalNull(t, query.FieldDateTime), query.FieldDuration},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := query.Arithmetic(test.operator, test.left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			operator, left, right, valid := value.Binary()
			if !valid || operator != test.operator || !left.Equal(test.left) || !right.Equal(test.right) || value.ResultKind() != test.result {
				t.Fatal("arithmetic changed an operand's domain or order")
			}
			if value.Nullable() != (test.left.Nullable() || test.right.Nullable()) || value.RequiresDurationRounding() {
				t.Fatal("exact arithmetic lost its NULL or precision contract")
			}
		})
	}
	// Construction does not evaluate or clamp year-9999 + 1us. The backend
	// must report that row's range error when the expression actually runs.
	if value, err := query.NegateScalar(period); err != nil || value.ResultKind() != query.FieldDuration {
		t.Fatal("Duration negation was not retained", err)
	}
	for name, pair := range map[string][2]query.ScalarExpression{
		"two_instants":      {instant, instant},
		"integer_period":    {period, scalarLiteral(t, query.Integer(1))},
		"float_period":      {period, scalarLiteral(t, query.Float(1))},
		"untyped_null":      {instant, scalarLiteral(t, query.Null())},
		"untyped_null_left": {scalarLiteral(t, query.Null()), period},
	} {
		t.Run("reject_"+name, func(t *testing.T) {
			if _, err := query.Arithmetic(query.ArithmeticAdd, pair[0], pair[1]); err == nil {
				t.Fatal("unsupported temporal operands were coerced")
			}
		})
	}
	if _, err := query.Arithmetic(query.ArithmeticSubtract, period, instant); err == nil {
		t.Fatal("Duration - DateTime was accepted")
	}
	if _, err := query.Arithmetic(query.ArithmeticMultiply, period, period); err == nil {
		t.Fatal("Duration multiplication inferred an output unit")
	}
	if _, err := query.NegateScalar(instant); err == nil {
		t.Fatal("an instant was negated as a period")
	}
}

func TestArithmeticNullNormalizationOwnsItsOperandType(t *testing.T) {
	bare := scalarLiteral(t, query.Null())
	for _, value := range []query.Value{query.Integer(1), query.Float(1)} {
		literal := scalarLiteral(t, value)
		inferred, err := query.Arithmetic(query.ArithmeticAdd, bare, literal)
		if err != nil {
			t.Fatal(err)
		}
		explicit, err := query.Arithmetic(query.ArithmeticAdd, temporalNull(t, literal.ResultKind()), literal)
		if err != nil || !inferred.Equal(explicit) || bare.ResultKind() != "" {
			t.Fatal("NULL inference mutated its input or left an untyped operand", err)
		}
	}
}

func TestDurationMeanArithmeticAndAggregatesKeepNativePrecision(t *testing.T) {
	field := query.NewFieldRef("elapsed", "elapsed", query.FieldDuration, true)
	mean, err := query.AvgResult(query.FieldResult(field))
	if err != nil {
		t.Fatal(err)
	}
	shape, err := query.NewAggregateResult(mean)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := query.NewResultScope(shape)
	if err != nil {
		t.Fatal(err)
	}
	selected := selectedScalar(t, scope, 0)
	period := scalarLiteral(t, query.Duration(duration.Duration{Microseconds: 1}))
	computed, err := query.Arithmetic(query.ArithmeticAdd, selected, period)
	if err != nil || !computed.RequiresDurationRounding() || period.RequiresDurationRounding() {
		t.Fatal("arithmetic discarded AVG provenance or widened an exact period", err)
	}
	negated, err := query.NegateScalar(computed)
	if err != nil || !negated.RequiresDurationRounding() {
		t.Fatal("negation rounded the selected native mean", err)
	}
	for _, kind := range []query.ResultExpressionKind{query.ResultSum, query.ResultAvg, query.ResultMin, query.ResultMax} {
		aggregate, err := query.ScalarAggregateResult(kind, negated)
		if err != nil || aggregate.ResultValueKind() != query.FieldDuration || !aggregate.RequiresDurationRounding() {
			t.Fatal("a subsequent aggregate lost the native result domain", kind, err)
		}
	}
	instant := scalarLiteral(t, query.DateTime(time.Time{}))
	shifted, err := query.Arithmetic(query.ArithmeticAdd, instant, selected)
	if err != nil || shifted.ResultKind() != query.FieldDateTime || shifted.RequiresDurationRounding() {
		t.Fatal("DateTime result inherited a Duration decoder", err)
	}
	_, _, right, _ := shifted.Binary()
	if !right.RequiresDurationRounding() {
		t.Fatal("DateTime shift lost its operand's fractional precision")
	}
	result, err := query.Arithmetic(query.ArithmeticSubtract, shifted, instant)
	if err != nil || result.RequiresDurationRounding() {
		t.Fatal("difference of materialized DateTime values became a fractional Duration", err)
	}
}
