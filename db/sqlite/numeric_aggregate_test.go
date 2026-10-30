package sqlite

import (
	"database/sql/driver"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
)

func numericPacked(t *testing.T, text string, digits, places int64) []byte {
	t.Helper()
	value, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	key, err := decimalstorage.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := sqliteDecimalAggregateInput(nil, []driver.Value{key, digits, places})
	if err != nil {
		t.Fatal(err)
	}
	return packed.([]byte)
}

func TestDecimalAggregateOwnsStateAndRejectsPhysicalCorruption(t *testing.T) {
	packed := numericPacked(t, "0.10", 14, 2)
	value := &decimalAggregate{}
	if err := value.Step(nil, []driver.Value{packed}); err != nil {
		t.Fatal(err)
	}
	clear(packed)
	if err := value.Step(nil, []driver.Value{numericPacked(t, "0.20", 14, 2)}); err != nil {
		t.Fatal(err)
	}
	if err := value.Step(nil, []driver.Value{numericPacked(t, "-0.30", 14, 2)}); err != nil {
		t.Fatal(err)
	}
	raw, err := value.WindowValue(nil)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := decimalstorage.Decode(raw.([]byte))
	if err != nil || zero != (decimal.Decimal{}) {
		t.Fatal("lost exact cancellation or retained operand bytes", zero, err)
	}
	if err := value.Step(nil, []driver.Value{numericPacked(t, "1", 13, 2)}); err == nil {
		t.Fatal("changed precision in one invocation")
	}
	if err := value.Step(nil, []driver.Value{nil}); err == nil {
		t.Fatal("forgot prior failure")
	}
	if _, err := value.WindowValue(nil); err == nil {
		t.Fatal("published failed aggregate")
	}
	value.Final(nil)
	if value.count != 0 || value.sum.Sign() != 0 || value.sum.Bits() != nil {
		t.Fatal("final retained accumulator")
	}
	for _, raw := range []driver.Value{int64(1), float64(1), "1", []byte("1"), []byte{2}, make([]byte, decimal.MaxDigits+7)} {
		if _, err := sqliteDecimalAggregateInput(nil, []driver.Value{raw, int64(5), int64(2)}); err == nil {
			t.Fatalf("accepted physical input %T", raw)
		}
	}
	for _, spec := range [][2]int64{{0, 0}, {1001, 0}, {5, -1}, {5, 6}} {
		if _, err := sqliteDecimalAggregateInput(nil, []driver.Value{nil, spec[0], spec[1]}); err == nil {
			t.Fatal("NULL bypassed invalid field precision")
		}
	}
	key := numericPacked(t, "1000", 5, 0)
	binary.BigEndian.PutUint16(key[:2], 3)
	if err := (&decimalAggregate{}).Step(nil, []driver.Value{key}); err == nil {
		t.Fatal("forged packed precision admitted out-of-field value")
	}
	full := &decimalAggregate{count: math.MaxInt64, digits: 14, places: 2}
	if err := full.Step(nil, []driver.Value{numericPacked(t, "1", 14, 2)}); err == nil || full.count != math.MaxInt64 {
		t.Fatal("count overflow")
	}
	window := &decimalAggregate{}
	if err := window.WindowInverse(nil, nil); err == nil {
		t.Fatal("unsupported window silently accepted")
	}
	if _, err := window.WindowValue(nil); err == nil {
		t.Fatal("failed window returned a value")
	}
}

func TestDecimalAggregateAllowsWideIntermediateAverageAndRejectsWideResult(t *testing.T) {
	maximum := strings.Repeat("9", 1000)
	input := numericPacked(t, maximum, 1000, 0)
	sum, average := &decimalAggregate{}, &decimalAggregate{average: true}
	for _, accumulator := range []*decimalAggregate{sum, average} {
		for range 2 {
			if err := accumulator.Step(nil, []driver.Value{input}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := sum.WindowValue(nil); err == nil {
		t.Fatal("out-of-domain sum returned as Decimal")
	}
	raw, err := average.WindowValue(nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := decimalstorage.Decode(raw.([]byte))
	if err != nil || actual.String() != maximum {
		t.Fatal("average prematurely narrowed its intermediate sum", err)
	}
	for _, values := range []struct {
		input []string
		want  string
	}{
		{[]string{"0.6", "0.6"}, "1.2"},
		{[]string{"-0", "0"}, "0"},
	} {
		a := &decimalAggregate{}
		for _, text := range values.input {
			if err := a.Step(nil, []driver.Value{numericPacked(t, text, 1000, 1000)}); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := a.WindowValue(nil)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := decimalstorage.Decode(raw.([]byte))
		if err != nil || actual.String() != values.want {
			t.Fatal("result used source precision", actual, err)
		}
	}
}

func TestNumericAggregateFloatFailureBoundaries(t *testing.T) {
	for _, raw := range []driver.Value{"1", []byte("1"), math.NaN()} {
		if _, err := sqliteFloatOperand(nil, []driver.Value{raw}); err == nil {
			t.Fatal("invalid native float operand")
		}
	}
	if value, err := sqliteFloatAggregate(nil, []driver.Value{nil, int64(0)}); err != nil || value != nil {
		t.Fatal("empty float aggregate", value, err)
	}
	if _, err := sqliteFloatAggregate(nil, []driver.Value{nil, int64(1)}); err == nil {
		t.Fatal("NaN became missing")
	}
	if value, err := sqliteFloatAggregate(nil, []driver.Value{math.Inf(1), int64(2)}); err != nil || !math.IsInf(value.(float64), 1) {
		t.Fatal("native infinity narrowed", value, err)
	}
}
