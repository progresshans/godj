package decimal_test

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"github.com/progresshans/godj/decimal"
)

func parsed(t testing.TB, text string) decimal.Decimal {
	t.Helper()
	value, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCanonicalDecimalPrecisionAndScale(t *testing.T) {
	for _, test := range []struct {
		input, canonical, fixed string
		scale                   int
	}{
		{"+0001.5000", "1.5", "1.50", 2}, {"-.5", "-0.5", "-0.500", 3}, {"1e3", "1000", "1000", 0},
		{"-0.0000", "-0", "-0.00", 2}, {"0e999", "0", "0.0000", 4},
		{"123456789012345678.123456789012", "123456789012345678.123456789012", "123456789012345678.123456789012", 12},
	} {
		value := parsed(t, test.input)
		if value.String() != test.canonical {
			t.Fatalf("canonical %q: %s", test.input, value)
		}
		fixed, err := value.Fixed(test.scale)
		if err != nil || fixed != test.fixed {
			t.Fatalf("fixed %q: %q %v", test.input, fixed, err)
		}
		wire, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var restored decimal.Decimal
		if err = json.Unmarshal(wire, &restored); err != nil || restored != value {
			t.Fatal("model JSON changed canonical identity", err)
		}
	}
	if !parsed(t, "-0").Equal(decimal.Decimal{}) || parsed(t, "-0") == (decimal.Decimal{}) {
		t.Fatal("zero identity and equality were conflated")
	}
	for _, test := range []struct {
		input          string
		digits, places int
		fits           bool
	}{
		{"999.99", 5, 2, true}, {"-999.99", 5, 2, true}, {"1000", 5, 2, false}, {"1.235", 5, 2, false}, {"1.2300", 5, 2, true},
		{"0", 4, 4, true}, {"0.0001", 4, 4, true}, {"0.00001", 4, 4, false}, {"1", 4, 4, false}, {"9999", 4, 0, true},
		{"1", 0, 0, false}, {"1", 4, -1, false}, {"1", 4, 5, false}, {"1", 1001, 0, false},
	} {
		if parsed(t, test.input).Fits(test.digits, test.places) != test.fits {
			t.Fatalf("precision %q (%d,%d)", test.input, test.digits, test.places)
		}
	}
	if _, err := parsed(t, "1.235").Fixed(2); !errors.Is(err, decimal.ErrScale) {
		t.Fatal("excess scale was rounded", err)
	}
}

func TestDecimalInvalidLiteralsAndFailurePreserveDestination(t *testing.T) {
	for _, raw := range []string{"", " ", "1 ", "NaN", "sNaN", "Infinity", "0x1p0", "1_0", "١٢.٥", ".", "+", "--1", "1e", "1e+", "1e1x", "1\x00"} {
		if _, err := decimal.Parse(raw); err == nil {
			t.Fatalf("invalid model grammar %q", raw)
		}
	}
	for _, value := range []decimal.Decimal{{Coefficient: "-"}, {Coefficient: "+1"}, {Coefficient: "1.5"}, {Coefficient: "1", Exponent: math.MaxInt32}, {Coefficient: "1", Exponent: math.MinInt32}, {Coefficient: strings.Repeat("1", 1001)}, {Exponent: 1}} {
		if value.Valid() {
			t.Fatal("invalid literal accepted")
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("invalid value serialized")
		}
	}
	value := parsed(t, "1.5")
	before := value
	for _, raw := range []string{`null`, `1.5`, `true`, `{}`, `"bad"`, `"NaN"`} {
		if err := json.Unmarshal([]byte(raw), &value); err == nil || value != before {
			t.Fatal("invalid JSON changed caller value")
		}
	}
	if _, err := decimal.Parse(strings.Repeat("0", decimal.MaxInputBytes+1)); !errors.Is(err, decimal.ErrRange) {
		t.Fatal("text budget ignored")
	}
	for _, raw := range []string{"1e1001", "1e-1001", strings.Repeat("1", 1001)} {
		if _, err := decimal.Parse(raw); !errors.Is(err, decimal.ErrRange) {
			t.Fatalf("range %s: %v", raw[:min(len(raw), 10)], err)
		}
	}
	for _, raw := range []string{"1e1000", "1e-1000", "0." + strings.Repeat("0", 999) + strings.Repeat("1", 1000), strings.Repeat("9", 1000)} {
		value := parsed(t, raw)
		if !parsed(t, value.String()).Equal(value) {
			t.Fatal("range edge changed value")
		}
	}
}

func TestDecimalCompareAgainstIndependentRationals(t *testing.T) {
	random := rand.New(rand.NewSource(91))
	for range 3000 {
		makeValue := func() decimal.Decimal {
			digits := make([]byte, 1+random.Intn(80))
			for i := range digits {
				digits[i] = '0' + byte(random.Intn(10))
			}
			coefficient := string(digits)
			if random.Intn(2) == 0 {
				coefficient = "-" + coefficient
			}
			value, err := decimal.New(coefficient, int32(random.Intn(181)-90))
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
		left, right := makeValue(), makeValue()
		l, lok := new(big.Rat).SetString(left.String())
		r, rok := new(big.Rat).SetString(right.String())
		if !lok || !rok {
			t.Fatal("independent rational parse")
		}
		comparison, err := left.Compare(right)
		if err != nil || comparison != l.Cmp(r) {
			t.Fatal("numeric comparison differs", err)
		}
	}
}
