package sqlite

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
)

func expressionDecimalKey(t *testing.T, text string) []byte {
	t.Helper()
	value, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	key, err := decimalstorage.Encode(value)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func assertExpressionDecimal(t *testing.T, raw driver.Value, text string) {
	t.Helper()
	key, ok := raw.([]byte)
	if !ok {
		t.Fatalf("expected an exact Decimal key, got %T", raw)
	}
	actual, err := decimalstorage.Decode(key)
	want, parseErr := decimal.Parse(text)
	if err != nil || parseErr != nil || !actual.Equal(want) {
		t.Fatalf("Decimal result %s, want %s: %v %v", actual.String(), text, err, parseErr)
	}
}

func TestDecimalExpressionsUseExactArithmeticAndCanonicalDivisionScale(t *testing.T) {
	for _, test := range []struct{ op, left, right, want string }{
		{"+", "0.1", "0.2", "0.3"},
		{"-", "1", "2.5", "-1.5"},
		{"*", "123456789012345678.12", "0.25", "30864197253086419.53"},
		{"/", "1", "3", "0." + strings.Repeat("3", 20)},
		{"/", "100", "3", "33." + strings.Repeat("3", 16)},
		{"/", "1e100", "3", strings.Repeat("3", 100)},
		{"/", "2e100", "3", strings.Repeat("6", 99) + "7"},
		{"/", "-2e100", "3", "-" + strings.Repeat("6", 99) + "7"},
		{"/", "1e-1000", "2", "1e-1000"},
		{"/", "-1e-1000", "2", "-1e-1000"},
		{"/", "1e-1000", "3", "0"},
		{"%", "-5.75", "2", "-1.75"},
		{"%", "5.75", "-2", "1.75"},
	} {
		t.Run(test.left+test.op+test.right, func(t *testing.T) {
			raw, err := sqliteDecimalBinary(nil, []driver.Value{test.op, expressionDecimalKey(t, test.left), expressionDecimalKey(t, test.right)})
			if err != nil {
				t.Fatal(err)
			}
			assertExpressionDecimal(t, raw, test.want)
		})
	}
	for _, text := range []string{"0", "1.25", "-1e1000"} {
		key := expressionDecimalKey(t, text)
		raw, err := sqliteDecimalNegate(nil, []driver.Value{key})
		if err != nil {
			t.Fatal(err)
		}
		clear(key)
		want := "-" + text
		if strings.HasPrefix(text, "-") {
			want = text[1:]
		}
		assertExpressionDecimal(t, raw, want)
	}
}

func TestDecimalExpressionsRejectPhysicalCorruptionAndIntermediateRange(t *testing.T) {
	one := expressionDecimalKey(t, "1")
	for _, operands := range [][]driver.Value{
		{}, {"+", one}, {"drop table", one, one}, {int64(1), one, one},
		{"+", nil, "1"}, {"+", []byte("1"), nil}, {"+", float64(1), one},
		{"*", expressionDecimalKey(t, "1e1000"), expressionDecimalKey(t, "10")},
		{"+", expressionDecimalKey(t, "1e1000"), one},
		{"*", expressionDecimalKey(t, "1e-1000"), expressionDecimalKey(t, "0.1")},
	} {
		if _, err := sqliteDecimalBinary(nil, operands); err == nil {
			t.Fatal("invalid physical operand, operator or intermediate range was accepted")
		}
	}
	for _, operator := range []string{"/", "%"} {
		raw, err := sqliteDecimalBinary(nil, []driver.Value{operator, one, expressionDecimalKey(t, "0")})
		if err != nil || raw != nil {
			t.Fatal("SQLite zero division/remainder must be SQL NULL", err)
		}
	}
	for _, operator := range []string{"+", "-", "*", "/", "%"} {
		raw, err := sqliteDecimalBinary(nil, []driver.Value{operator, nil, one})
		if err != nil || raw != nil {
			t.Fatal("Decimal NULL did not propagate", err)
		}
	}
}

func TestComputedDecimalAggregateOwnsVariableScaleAndFailureState(t *testing.T) {
	for _, average := range []bool{false, true} {
		value := &decimalAggregate{computed: true, average: average}
		if raw, err := value.WindowValue(nil); err != nil || raw != nil {
			t.Fatal("empty computed aggregate was not NULL", err)
		}
		for _, input := range []string{"1", "0.25", "0.005"} {
			key := expressionDecimalKey(t, input)
			if err := value.Step(nil, []driver.Value{key}); err != nil {
				t.Fatal(err)
			}
			clear(key)
		}
		want := "1.255"
		if average {
			want = "0.418" + strings.Repeat("3", 17)
		}
		raw, err := value.WindowValue(nil)
		if err != nil {
			t.Fatal(err)
		}
		assertExpressionDecimal(t, raw, want)
		if err := value.Step(nil, []driver.Value{"0.1"}); err == nil {
			t.Fatal("computed aggregate admitted a non-BLOB physical value")
		}
		if err := value.Step(nil, []driver.Value{nil}); err == nil {
			t.Fatal("computed aggregate forgot its failure")
		}
		if _, err := value.WindowValue(nil); err == nil {
			t.Fatal("computed aggregate published a failed result")
		}
		value.Final(nil)
		if value.sum.Bits() != nil || value.count != 0 {
			t.Fatal("computed aggregate retained mutable state")
		}
	}
	// Canonical values can have more fractional places than a field can
	// declare. The final AVG scale is capped at 1000, without negative powers
	// in the integer accumulator or a binary64 conversion.
	tiny := "0." + strings.Repeat("0", 999) + "1" + strings.Repeat("0", 998) + "1"
	average := &decimalAggregate{computed: true, average: true}
	for range 2 {
		if err := average.Step(nil, []driver.Value{expressionDecimalKey(t, tiny)}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := average.WindowValue(nil)
	if err != nil {
		t.Fatal(err)
	}
	assertExpressionDecimal(t, raw, "1e-1000")
}
