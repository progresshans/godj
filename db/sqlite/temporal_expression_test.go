package sqlite

import (
	"database/sql/driver"
	"math"
	"testing"

	"github.com/progresshans/godj/duration"
)

func TestTemporalNativeValuesPreserveMicrosecondsAndBoundaryRounding(t *testing.T) {
	for _, test := range []struct {
		name, operator, leftKind, rightKind string
		left, right, want                   driver.Value
	}{
		{"large_difference", "-", "datetime", "datetime", "9999-12-31 23:59:59.999999", "0001-01-01 00:00:00.000000", int64(315537897599999999)},
		{"large_negative_difference", "-", "datetime", "datetime", "0001-01-01 00:00:00.000000", "9999-12-31 23:59:59.999999", int64(-315537897599999999)},
		{"reverse_parameters", "+", "duration", "datetime", int64(1), "2400-02-29 23:59:59.999999", "2400-03-01 00:00:00.000000"},
		{"negative_shift", "+", "datetime", "duration", "2000-03-01 00:00:00.000000", int64(-1), "2000-02-29 23:59:59.999999"},
		{"far_epoch_even_tie", "+", "datetime", "duration_mean", "9999-01-01 00:00:00.000000", float64(0.5), "9999-01-01 00:00:00.000000"},
		{"far_epoch_odd_tie", "+", "datetime", "duration_mean", "9999-01-01 00:00:00.000001", float64(0.5), "9999-01-01 00:00:00.000002"},
		{"negative_epoch_even_tie", "-", "datetime", "duration_mean", "0001-01-01 00:00:00.000002", float64(0.5), "0001-01-01 00:00:00.000002"},
		{"negative_epoch_odd_tie", "-", "datetime", "duration_mean", "0001-01-01 00:00:00.000001", float64(0.5), "0001-01-01 00:00:00.000000"},
		{"fraction_survives", "+", "duration_mean", "duration", float64(0.5), int64(1), float64(1.5)},
		{"integer_case_stays_exact", "+", "duration_mean", "duration", int64(9007199254740993), int64(2), int64(9007199254740995)},
		{"duration_subtract", "-", "duration", "duration", int64(-3), int64(2), int64(-5)},
		{"null_datetime", "+", "datetime", "duration", nil, int64(0), nil},
		{"null_duration", "+", "datetime", "duration", "2026-10-15 00:00:00.000000", nil, nil},
		{"present_zero", "+", "duration", "duration", int64(0), int64(0), int64(0)},
		{"ratio_fraction", "/", "duration", "duration", int64(2), int64(3), float64(2.0 / 3.0)},
		{"ratio_negative", "/", "duration", "duration", int64(3), int64(-2), float64(-1.5)},
		{"ratio_large_low_bits", "/", "duration", "duration", int64(9007199254740995), int64(9007199254740993), math.Nextafter(1, 2)},
		{"ratio_even_tie", "/", "duration", "duration", int64(9007199254740993), int64(9007199254740992), float64(1)},
		{"ratio_native_mean", "/", "duration_mean", "duration_mean", float64(1.5), float64(0.5), float64(3)},
		{"ratio_zero", "/", "duration", "duration", int64(0), int64(-2), float64(0)},
		{"ratio_null", "/", "duration", "duration", int64(1), nil, nil},
		{"ratio_null_zero", "/", "duration", "duration", nil, int64(0), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := sqliteTemporalBinary(nil, []driver.Value{test.operator, test.leftKind, test.rightKind, test.left, test.right})
			if err != nil || got != test.want {
				t.Fatalf("got %v (%T), want %v (%T): %v", got, got, test.want, test.want, err)
			}
		})
	}
	for _, test := range []struct{ input, want driver.Value }{{int64(3), int64(-3)}, {float64(0.5), float64(-0.5)}, {nil, nil}} {
		got, err := sqliteTemporalNegate(nil, []driver.Value{"duration_mean", test.input})
		if err != nil || got != test.want {
			t.Fatal("negation lost native precision or NULL", got, err)
		}
	}
}

func TestTemporalNativeValuesRejectCorruptionAndInvalidIntermediates(t *testing.T) {
	for _, raw := range []driver.Value{"2026-02-30 00:00:00.000000", "0000-01-01 00:00:00.000000", "2026-10-15 00:00:00", "2026-10-15T00:00:00.000000Z", []byte("2026-10-15 00:00:00.000000"), float64(1), int64(1)} {
		for _, operands := range [][]driver.Value{{"+", "datetime", "duration", raw, nil}, {"+", "duration", "datetime", nil, raw}} {
			if _, err := sqliteTemporalBinary(nil, operands); err == nil {
				t.Fatal("NULL hid an invalid instant")
			}
		}
	}
	for _, operands := range [][]driver.Value{
		{}, {"+", "duration", "duration", int64(1)},
		{"/", "datetime", "duration", "2000-01-01 00:00:00.000000", int64(1)},
		{"/", "duration", "duration", int64(1), int64(0)},
		{"/", "duration", "duration", nil, float64(1)},
		{"/", "duration_mean", "duration_mean", float64(5e19), math.SmallestNonzeroFloat64},
		{"-", "duration", "datetime", int64(1), nil},
		{"+", "datetime", "datetime", nil, nil},
		{"+", "duration", "duration", nil, float64(1)},
		{"+", "duration", "duration", []byte("1"), nil},
		{"+", "duration", "duration", int64(math.MaxInt64), int64(1)},
		{"-", "duration", "duration", int64(math.MinInt64), int64(1)},
		{"+", "datetime", "duration", "9999-12-31 23:59:59.999999", int64(1)},
		{"-", "datetime", "duration", "0001-01-01 00:00:00.000000", int64(1)},
		{"+", "datetime", "duration_mean", nil, math.Inf(1)},
		{"+", "duration_mean", "duration", math.NaN(), nil},
		{"+", "duration_mean", "duration_mean", float64(5e19), float64(5e19)},
	} {
		if _, err := sqliteTemporalBinary(nil, operands); err == nil {
			t.Fatal("invalid operand, signature or result was accepted", operands)
		}
	}
	if _, err := sqliteTemporalNegate(nil, []driver.Value{"duration", int64(math.MinInt64)}); err == nil {
		t.Fatal("Duration negation wrapped int64")
	}
	// Floating endpoints may be rounded outside the model. Compare exact
	// binary64 ratios to integer model bounds, never rounded float bounds.
	lower := float64(duration.MinDays) * float64(duration.MicrosecondsPerDay)
	upper := float64(int64(duration.MaxDays)+1) * float64(duration.MicrosecondsPerDay)
	for _, value := range []float64{math.Nextafter(lower, math.Inf(-1)), upper, math.Inf(1), math.NaN()} {
		if _, err := sqliteDurationMean(nil, []driver.Value{value}); err == nil {
			t.Fatal("Duration mean escaped its model domain", value)
		}
	}
	for _, value := range []float64{math.Nextafter(lower, math.Inf(1)), math.Nextafter(upper, math.Inf(-1)), -0.5, 0.5} {
		if got, err := sqliteDurationMean(nil, []driver.Value{value}); err != nil || got != value {
			t.Fatal("valid native mean was rounded or narrowed", got, err)
		}
	}
}
