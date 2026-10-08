package orm

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/decimalstorage"
	"github.com/progresshans/godj/query"
)

func TestDurationAverageScannerRoundsAtResultBoundaryAndKeepsModelRange(t *testing.T) {
	for _, test := range []struct {
		value float64
		want  duration.Duration
	}{
		{1.5, duration.FromMicroseconds(2)}, {2.5, duration.FromMicroseconds(2)},
		{-1.5, duration.FromMicroseconds(-2)}, {-2.5, duration.FromMicroseconds(-2)},
		{-0.5, duration.Duration{}}, {-0x1p63, duration.FromMicroseconds(math.MinInt64)},
		{0x1p63, duration.Duration{Days: 106751991, Microseconds: 14454775808}},
	} {
		var scanner aggregateDurationScanner
		if err := scanner.Scan(test.value); err != nil || !scanner.valid || scanner.value != test.want {
			t.Fatal("duration mean result", scanner, err)
		}
		if err := scanner.Scan(nil); err != nil || scanner.valid || scanner.value != (duration.Duration{}) {
			t.Fatal("duration NULL result reset")
		}
	}
	for _, invalid := range []any{math.Inf(1), math.NaN(), math.MaxFloat64, "1", true} {
		var scanner aggregateDurationScanner
		if err := scanner.Scan(float64(1)); err != nil {
			t.Fatal(err)
		}
		if err := scanner.Scan(invalid); err == nil || scanner.valid || scanner.value != (duration.Duration{}) {
			t.Fatal("invalid duration mean retained state")
		}
	}
	var source DurationScanner
	if err := source.Scan(float64(1.5)); err == nil {
		t.Fatal("aggregate mean widened the source-field scanner")
	}
}

func TestNumericAggregateScannerKeepsGlobalDecimalDomainAndResetsFailure(t *testing.T) {
	for _, text := range []string{"1000000000000.01", "0.000000000000000000000001", "-0", "-" + strings.Repeat("9", 1000)} {
		value, err := decimal.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		key, err := decimalstorage.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []any{value, key} {
			var scanner aggregateDecimalScanner
			if err := scanner.Scan(input); err != nil || !scanner.valid || !scanner.value.Equal(value) {
				t.Fatal("aggregate narrowed field-independent value", err)
			}
			if err := scanner.Scan(nil); err != nil || scanner.valid || scanner.value != (decimal.Decimal{}) {
				t.Fatal("NULL retained result")
			}
		}
	}
	valid, _ := decimal.Parse("1")
	for _, input := range []any{"1", float64(1), int64(1), []byte("1"), decimal.Decimal{Coefficient: strings.Repeat("9", 1001)}, decimal.Decimal{Coefficient: "1", Exponent: -1001}} {
		var scanner aggregateDecimalScanner
		if err := scanner.Scan(valid); err != nil {
			t.Fatal(err)
		}
		if err := scanner.Scan(input); err == nil || scanner.valid || scanner.value != (decimal.Decimal{}) {
			t.Fatal("failed aggregate scalar retained state", err)
		}
	}
}

func TestNumericAggregateFiveCellsKeepTypedResultsAndRejectBeforeBuilder(t *testing.T) {
	fields := newResultTestFields()
	buildCalls := 0
	aggregate := Aggregate5(CountRows[resultTestModel](), Sum(fields.ID), Avg(fields.ID), Min(fields.ID), Max(fields.ID),
		func(count int64, sum Optional[int64], avg Optional[float64], min, max Optional[int64]) [5]float64 {
			buildCalls++
			s, _ := sum.Get()
			a, _ := avg.Get()
			low, _ := min.Get()
			high, _ := max.Get()
			return [5]float64{float64(count), float64(s), a, float64(low), float64(high)}
		})
	backend := resultBackendForRows([][]any{{int64(2), int64(3), float64(1.5), int64(1), int64(2)}})
	result, err := AggregateInto(t.Context(), newResultTestQuerySet(backend), aggregate)
	if err != nil || result != ([5]float64{2, 3, 1.5, 1, 2}) || buildCalls != 1 {
		t.Fatal("typed five-cell decode", result, err)
	}
	for _, stage := range []string{"scan", "iteration", "close", "extra_row", "cancel"} {
		rows := &resultTestRows{values: [][]any{{int64(2), int64(3), float64(1.5), int64(1), int64(2)}}}
		failure := errors.New("numeric read failure")
		ctx, cancel := context.WithCancel(t.Context())
		switch stage {
		case "scan":
			rows.scanErr = failure
		case "iteration":
			rows.iterationErr = failure
		case "close":
			rows.closeErr = failure
		case "extra_row":
			rows.values = append(rows.values, rows.values[0])
		case "cancel":
			rows.onNext = func(int) { cancel() }
		}
		backend := &resultTestBackend{query: func(int, context.Context, query.Plan) (db.Rows, error) { return rows, nil }}
		result, err := AggregateInto(ctx, newResultTestQuerySet(backend), aggregate)
		cancel()
		if err == nil || result != ([5]float64{}) || buildCalls != 1 || rows.closeCalls != 1 {
			t.Fatal("failed aggregate published builder output", stage, result, err, buildCalls, rows.closeCalls)
		}
	}
	var missing SumField[resultTestModel, int64]
	invalid := Aggregate1(Sum(missing), func(value Optional[int64]) Optional[int64] { t.Fatal("nil field called builder"); return value })
	before := len(backend.plans)
	if _, err := AggregateInto(t.Context(), newResultTestQuerySet(backend), invalid); err == nil || len(backend.plans) != before {
		t.Fatal("nil numeric field reached I/O")
	}
}
