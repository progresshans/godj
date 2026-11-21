package decimalinput_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/decimalinput"
)

func TestDecimalInputHugeExponentsRemainBoundedAndPrecisionIsNotCanonicalized(t *testing.T) {
	for _, text := range []string{"1e999999", "1e-999999", "0e999999", "0e-999999"} {
		parsed, err := decimalinput.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if code := parsed.Precision(12, 2, false); code != "max_digits" {
			t.Fatalf("huge exponent %s returned %s", text, code)
		}
		if parsed.TextLength() > 20 {
			t.Fatal("scientific text expanded the exponent")
		}
	}
	parsed, err := decimalinput.Parse("0E+20")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Precision(12, 2, true) != "" || parsed.Precision(12, 2, false) != "max_digits" {
		t.Fatal("Form and serializer zero exponent profiles were conflated")
	}
	parsed, err = decimalinput.Parse("1.200")
	if err != nil {
		t.Fatal(err)
	}
	value, err := parsed.Value()
	if err != nil || value.String() != "1.2" || parsed.Precision(12, 2, true) != "max_decimal_places" {
		t.Fatal("canonicalization hid submitted scale")
	}
	for _, raw := range []string{"1e9223372036854775808", "1e-9223372036854775808", "1.0e-1999999999999999997", "11e999999999999999999", strings.Repeat("_", 4097), "1\xff"} {
		if _, err := decimalinput.Parse(raw); err == nil {
			t.Fatal("overflow, resource or invalid UTF-8 input accepted")
		}
	}
	for _, raw := range []string{"0.0e1000000000000000000", "10e-1999999999999999997"} {
		if _, err := decimalinput.Parse(raw); err != nil {
			t.Fatal("finite constructor boundary rejected", err)
		}
	}
	for _, text := range []string{"-0", "-0.0", "-0e0"} {
		parsed, err := decimalinput.JSONNumber(text)
		if err != nil {
			t.Fatal(err)
		}
		value, err := parsed.Value()
		if err != nil || (value.Coefficient == "-0") != (text != "-0") {
			t.Fatal("JSON integer/decimal zero profile changed")
		}
	}
}
