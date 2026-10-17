package durationcalc_test

import (
	"bytes"
	"errors"
	"math"
	"math/big"
	"sort"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/internal/durationcalc"
)

func divide(t *testing.T, value durationcalc.Value, divisor int64) durationcalc.Value {
	t.Helper()
	result, err := value.Divide(durationcalc.IntegerFactor(divisor))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScalingPreservesFractionsAcrossOperationsAndOwnedKeys(t *testing.T) {
	for _, denominator := range []int64{2, 3, 7, 9007199254740993, math.MaxInt64} {
		original := durationcalc.FromMicroseconds(1)
		fraction := divide(t, original, denominator)
		key := fraction.Key()
		selected, err := durationcalc.FromKey(key)
		if err != nil || selected.Compare(fraction) != 0 {
			t.Fatal("selected result lost its exact value", denominator, err)
		}
		for i := range key {
			key[i] = 0 // No Value may retain the driver's reusable buffer.
		}
		result, err := selected.Scale(durationcalc.IntegerFactor(denominator))
		if err != nil || result.Compare(original) != 0 {
			t.Fatal("intermediate result was rounded", denominator, err)
		}
		if fraction.Compare(divide(t, original, denominator)) != 0 || original.Compare(durationcalc.FromMicroseconds(1)) != 0 {
			t.Fatal("calculation mutated a shared input")
		}
	}
	large := durationcalc.FromMicroseconds(9007199254740995)
	result, err := large.Scale(durationcalc.IntegerFactor(1))
	if err != nil || result.Compare(large) != 0 {
		t.Fatal("integer input lost its low bits", err)
	}
}

func TestScalingPreservesDecimalAndBinary64FactorDomains(t *testing.T) {
	value := durationcalc.FromMicroseconds(5)
	exact, err := durationcalc.DecimalFactor(decimal.Decimal{Coefficient: "1", Exponent: -1})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := durationcalc.FloatFactor(0.1)
	if err != nil {
		t.Fatal(err)
	}
	decimalValue, err := value.Scale(exact)
	if err != nil {
		t.Fatal(err)
	}
	floatValue, err := value.Scale(binary)
	if err != nil {
		t.Fatal(err)
	}
	decimalResult, err := decimalValue.Duration()
	if err != nil || decimalResult != duration.FromMicroseconds(0) {
		t.Fatal("exact Decimal half tie did not round to even", decimalResult, err)
	}
	floatResult, err := floatValue.Duration()
	if err != nil || floatResult != duration.FromMicroseconds(1) || floatValue.Compare(decimalValue) <= 0 {
		t.Fatal("binary64 input was replaced by a decimal approximation", floatResult, err)
	}
	for _, exponent := range []int32{-1000, 1000} {
		factor, err := durationcalc.DecimalFactor(decimal.Decimal{Coefficient: "1", Exponent: exponent})
		if err != nil {
			t.Fatal(err)
		}
		one := durationcalc.FromMicroseconds(1)
		var tiny, restored durationcalc.Value
		if exponent < 0 {
			tiny, err = one.Scale(factor)
			if err == nil {
				restored, err = tiny.Divide(factor)
			}
		} else {
			tiny, err = one.Divide(factor)
			if err == nil {
				restored, err = tiny.Scale(factor)
			}
		}
		if err != nil || tiny.Compare(durationcalc.Value{}) <= 0 || restored.Compare(one) != 0 {
			t.Fatal("a valid factor was narrowed or a small intermediate underflowed", exponent, err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := durationcalc.FloatFactor(value); !errors.Is(err, durationcalc.ErrInvalid) {
			t.Fatal("nonfinite factor was accepted", err)
		}
		if _, err := durationcalc.FromNativeMean(value); !errors.Is(err, durationcalc.ErrInvalid) {
			t.Fatal("nonfinite mean was accepted", err)
		}
	}
	if _, err := durationcalc.DecimalFactor(decimal.Decimal{Coefficient: "10", Exponent: -2}); !errors.Is(err, durationcalc.ErrInvalid) {
		t.Fatal("a noncanonical physical Decimal bypassed validation", err)
	}
}

func TestMaterializationRoundsTheCombinedResultOnce(t *testing.T) {
	for _, test := range []struct {
		numerator, want int64
	}{{1, 0}, {3, 2}, {5, 2}, {-1, 0}, {-3, -2}, {-5, -2}} {
		value := divide(t, durationcalc.FromMicroseconds(test.numerator), 2)
		got, err := value.Duration()
		if err != nil || got != duration.FromMicroseconds(test.want) {
			t.Fatal("final half-even conversion changed", test.numerator, got, err)
		}
	}
	half := divide(t, durationcalc.FromMicroseconds(1), 2)
	for _, origin := range []int64{1, 9007199254740993, -9007199254740993} {
		want := origin + 1
		if got := half.RoundOffset(origin); !got.IsInt64() || got.Int64() != want {
			t.Fatal("DateTime rounded its period before the odd origin", origin, got, want)
		}
	}
	mean, err := durationcalc.FromNativeMean(0.5)
	if err != nil || mean.Compare(half) != 0 {
		t.Fatal("native mean lost fractional microseconds", err)
	}
}

func TestEachDurationExpressionChecksItsFullModelDomain(t *testing.T) {
	upper := duration.Duration{Days: duration.MaxDays, Microseconds: duration.MicrosecondsPerDay - 1}
	maximum, err := durationcalc.FromDuration(upper)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := maximum.Duration(); err != nil || result != upper {
		t.Fatal("the full model value was reduced to int64", result, err)
	}
	if _, err := maximum.Add(durationcalc.FromMicroseconds(1)); !errors.Is(err, duration.ErrRange) {
		t.Fatal("an out-of-range intermediate was accepted", err)
	}
	if _, err := maximum.Scale(durationcalc.IntegerFactor(2)); !errors.Is(err, duration.ErrRange) {
		t.Fatal("scaling escaped the intermediate domain", err)
	}
	if _, err := maximum.Negate(); !errors.Is(err, duration.ErrRange) {
		t.Fatal("the asymmetric negative model bound was ignored", err)
	}
	if _, err := (durationcalc.Value{}).Divide(durationcalc.Factor{}); !errors.Is(err, durationcalc.ErrZeroDivisor) {
		t.Fatal("zero divided by zero became a value", err)
	}
	if _, err := maximum.Ratio(durationcalc.Value{}); !errors.Is(err, durationcalc.ErrZeroDivisor) {
		t.Fatal("a zero period divisor became a value", err)
	}
	one := durationcalc.FromMicroseconds(1)
	if result, err := durationcalc.FromMicroseconds(9007199254740995).Ratio(durationcalc.FromMicroseconds(9007199254740993)); err != nil || result != math.Nextafter(1, 2) {
		t.Fatal("ratio converted an operand before division", result, err)
	}
	smallest, err := durationcalc.FloatFactor(math.SmallestNonzeroFloat64)
	if err != nil {
		t.Fatal(err)
	}
	tiny, err := one.Scale(smallest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := one.Ratio(tiny); !errors.Is(err, durationcalc.ErrFloatRange) {
		t.Fatal("a ratio published Infinity", err)
	}
}

func TestAggregateKeepsExactContributionsUntilItsResult(t *testing.T) {
	var sum durationcalc.Aggregate
	if _, present, err := sum.Result(false); err != nil || present {
		t.Fatal("empty aggregate became zero", present, err)
	}
	third := divide(t, durationcalc.FromMicroseconds(1), 3)
	for range 3 {
		if err := sum.Add(third); err != nil {
			t.Fatal(err)
		}
	}
	value, present, err := sum.Result(false)
	if err != nil || !present || value.Compare(durationcalc.FromMicroseconds(1)) != 0 {
		t.Fatal("sum rounded each contribution", present, err)
	}
	mean, present, err := sum.Result(true)
	if err != nil || !present || mean.Compare(third) != 0 {
		t.Fatal("mean lost the denominator", present, err)
	}
	maximum, err := durationcalc.FromDuration(duration.Duration{Days: duration.MaxDays})
	if err != nil {
		t.Fatal(err)
	}
	minimum, err := maximum.Negate()
	if err != nil {
		t.Fatal(err)
	}
	var large durationcalc.Aggregate
	for range 2 {
		if err := large.Add(maximum); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := large.Result(false); !errors.Is(err, duration.ErrRange) {
		t.Fatal("completed SUM escaped its model domain", err)
	}
	average, present, err := large.Result(true)
	if err != nil || !present || average.Compare(maximum) != 0 {
		t.Fatal("AVG checked its accumulator as a completed Duration", err)
	}
	if err := large.Add(minimum); err != nil {
		t.Fatal(err)
	}
	result, present, err := large.Result(false)
	if err != nil || !present || result.Compare(maximum) != 0 {
		t.Fatal("SUM lost exact cancellation", err)
	}
}

func TestCanonicalKeysPreserveExactNumericOrdering(t *testing.T) {
	type sample struct {
		value durationcalc.Value
		ratio *big.Rat
		key   []byte
	}
	var samples []sample
	for numerator := int64(-25); numerator <= 25; numerator++ {
		for denominator := int64(1); denominator <= 31; denominator++ {
			value := divide(t, durationcalc.FromMicroseconds(numerator), denominator)
			key := value.Key()
			decoded, err := durationcalc.FromKey(key)
			if err != nil || decoded.Compare(value) != 0 || !bytes.Equal(decoded.Key(), key) {
				t.Fatal("fraction did not have one owned canonical key", numerator, denominator, err)
			}
			samples = append(samples, sample{value, new(big.Rat).SetFrac64(numerator, denominator), key})
		}
	}
	for _, numerator := range []int64{math.MinInt64, -9007199254740993, 9007199254740993, math.MaxInt64} {
		for _, denominator := range []int64{1, 2971215073, 4807526976, math.MaxInt64} {
			value := divide(t, durationcalc.FromMicroseconds(numerator), denominator)
			samples = append(samples, sample{value, new(big.Rat).SetFrac64(numerator, denominator), value.Key()})
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].ratio.Cmp(samples[j].ratio) < 0 })
	for index := 1; index < len(samples); index++ {
		previous, current := samples[index-1], samples[index]
		want := previous.ratio.Cmp(current.ratio)
		if got := bytes.Compare(previous.key, current.key); got != want || previous.value.Compare(current.value) != want {
			t.Fatal("byte order changed an exact rational comparison", previous.ratio, current.ratio, got, want)
		}
	}
}

func TestCanonicalKeyRejectsMalformedAndAliasedEncodings(t *testing.T) {
	valid := divide(t, durationcalc.FromMicroseconds(3), 7).Key()
	for length := range len(valid) {
		if _, err := durationcalc.FromKey(valid[:length]); !errors.Is(err, durationcalc.ErrKey) {
			t.Fatal("truncated key was accepted", length, err)
		}
	}
	for _, key := range [][]byte{
		nil, {}, []byte("3/7"), append(bytes.Clone(valid), 0),
		{'G', 'D', 'R', 1, 3}, {'G', 'D', 'R', 2, 1}, {'G', 'D', 'R', 1, 1, 0},
		{'G', 'D', 'R', 1, 2, 0x80, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	} {
		before := bytes.Clone(key)
		if _, err := durationcalc.FromKey(key); !errors.Is(err, durationcalc.ErrKey) || !bytes.Equal(key, before) {
			t.Fatal("invalid key was accepted or mutated", key, err)
		}
	}
	// [1; 1] aliases [2], while a positive sign with coefficient zero aliases
	// the unique zero key. Neither alternate representation may reach DISTINCT.
	for _, key := range [][]byte{
		{'G', 'D', 'R', 1, 2, 0x80, 0, 0, 0, 0, 0, 0, 0, 1, '1', 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe, 0xce, 0xff},
		{'G', 'D', 'R', 1, 2, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		if _, err := durationcalc.FromKey(key); !errors.Is(err, durationcalc.ErrKey) {
			t.Fatal("an alternate numeric key was accepted", key, err)
		}
	}
}
