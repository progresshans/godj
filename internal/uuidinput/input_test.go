package uuidinput

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/uuidtest"
	"github.com/progresshans/godj/uuid"
)

func TestUUIDTextInputMatchesPinnedPublicModel(t *testing.T) {
	raw, err := os.ReadFile("../uuidtest/testdata/django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Model []struct {
			Type     string
			Input    json.RawMessage
			ToPython struct {
				Codes []string
				Value *struct{ Text string }
			} `json:"to_python"`
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range reference.Model {
		if row.Type != "str" {
			continue
		}
		count++
		var text string
		if err := json.Unmarshal(row.Input, &text); err != nil {
			t.Fatal(err)
		}
		value, err := Parse(text)
		if (err != nil) != (len(row.ToPython.Codes) > 0) {
			t.Fatalf("%q error=%v codes=%v", text, err, row.ToPython.Codes)
		}
		if err == nil && (row.ToPython.Value == nil || value.String() != row.ToPython.Value.Text) {
			t.Fatalf("%q changed canonical value", text)
		}
	}
	if count != 42 {
		t.Fatalf("UUID string coverage %d", count)
	}
}

func TestUUIDIntegerInputRetainsAll128BitsAndTokenKind(t *testing.T) {
	for _, test := range []struct{ raw, hex string }{
		{"0", strings.Repeat("0", 32)}, {"-0", strings.Repeat("0", 32)}, {"1", strings.Repeat("0", 31) + "1"},
		{"18446744073709551616", "00000000000000010000000000000000"},
		{"170141183460469231731687303715884105728", "80000000000000000000000000000000"},
		{"340282366920938463463374607431768211455", strings.Repeat("f", 32)},
	} {
		value, err := Integer(test.raw)
		if err != nil || value.Hex() != test.hex {
			t.Fatal("integer UUID rounded", test.raw, value, err)
		}
	}
	for _, raw := range []string{"", "-1", "340282366920938463463374607431768211456", strings.Repeat("9", 1024), "1.0", "1e0", "-0.0", "00", "01", "+1", " 1", "1 ", "1_0", "١", "true", "null"} {
		if value, err := Integer(raw); err == nil || value != (uuid.UUID{}) {
			t.Fatalf("non-integer/range token %q accepted", raw)
		}
	}
}

func TestUUIDHexInputRejectsMalformedAliasesAndInvalidEncoding(t *testing.T) {
	for _, raw := range []string{"\xff" + strings.Repeat("0", 31), strings.Repeat("0", 30) + "__", strings.Repeat("0", 29) + "__1", "0x__" + strings.Repeat("0", 28), "\x1c" + strings.Repeat("0", 31), strings.Repeat("0", 31) + "\x00", "＋" + strings.Repeat("0", 31)} {
		if value, err := Parse(raw); err == nil || value != (uuid.UUID{}) {
			t.Fatalf("invalid UUID alias %q accepted", raw)
		}
	}
	if got := TrimSpace("\x1c\u00a0  x \t\x1f"); got != "x" {
		t.Fatal("Form whitespace boundary changed", got)
	}
}

func TestUUIDUnicodeDigitsMatchPinnedPythonRepertoire(t *testing.T) {
	observed := uuidtest.Load(t).UnicodeDecimal
	digits := 0
	for _, interval := range observed.Ranges {
		if interval.Last != interval.First+9 || len(interval.Values) != 10 {
			t.Fatal("independent Unicode range changed")
		}
		for digit, expected := range interval.Values {
			value, err := Parse(strings.Repeat(string(interval.First+rune(digit)), 32))
			if err != nil || value.String() != expected {
				t.Fatalf("U+%04X differs from pinned Python UUID: %v", interval.First+rune(digit), err)
			}
			digits++
		}
	}
	if digits != 760 {
		t.Fatal("Unicode decimal repertoire changed", digits)
	}
	for _, character := range []rune{'²', '①', 'Ⅵ', '\u200b'} {
		if _, err := Parse(strings.Repeat(string(character), 32)); err == nil {
			t.Fatalf("nondecimal U+%04X became a UUID digit", character)
		}
	}
}
