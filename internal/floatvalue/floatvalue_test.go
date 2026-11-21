package floatvalue

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"testing"
)

func TestIndependentStringConversion(t *testing.T) {
	data, err := os.ReadFile("../floattest/testdata/django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var observation struct {
		Model []struct {
			Type     string
			Input    json.RawMessage
			ToPython struct {
				Value struct{ Bits string }
				Codes []string
			} `json:"to_python"`
		}
	}
	if err = json.Unmarshal(data, &observation); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range observation.Model {
		if entry.Type != "str" {
			continue
		}
		count++
		var raw string
		if err = json.Unmarshal(entry.Input, &raw); err != nil {
			t.Fatal(err)
		}
		actual, err := ParseInput(raw)
		if len(entry.ToPython.Codes) > 0 {
			if err == nil {
				t.Errorf("accepted invalid input %q", raw)
			}
			continue
		}
		expected, parseErr := strconv.ParseUint(entry.ToPython.Value.Bits, 16, 64)
		if err != nil || parseErr != nil || math.Float64bits(actual) != expected {
			t.Errorf("input %q: bits=%016x error=%v want %s", raw, math.Float64bits(actual), err, entry.ToPython.Value.Bits)
		}
	}
	if count != 67 {
		t.Fatalf("string corpus count=%d", count)
	}
}
func TestCanonicalBitsRoundtrip(t *testing.T) {
	bits := uint64(0x6a09e667f3bcc909)
	for i := 0; i < 10000; i++ {
		bits ^= bits << 13
		bits ^= bits >> 7
		bits ^= bits << 17
		value := math.Float64frombits(bits)
		actual, err := FromBits(Bits(value))
		if err != nil || math.Float64bits(actual) != CanonicalBits(value) {
			t.Fatalf("bits %016x: %v", bits, err)
		}
	}
	for _, text := range []string{"0", "00000000000000000", "7FF0000000000000", "7ff0000000000001", "fff8000000000000", "+000000000000001", "000000000000000g"} {
		if _, err := FromBits(text); err == nil {
			t.Errorf("accepted noncanonical bits %q", text)
		}
	}
}
func TestJSONNumberRoundtrip(t *testing.T) {
	for _, value := range []float64{0, math.Copysign(0, -1), 0.1, 1, 1e15, math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64} {
		text, err := JSON(value)
		if err != nil {
			t.Fatal(err)
		}
		var actual float64
		if err = json.Unmarshal([]byte(text), &actual); err != nil || math.Float64bits(actual) != math.Float64bits(value) {
			t.Fatalf("%s: %016x %v", text, math.Float64bits(actual), err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
		if _, err := JSON(value); err == nil {
			t.Error("accepted nonfinite JSON")
		}
	}
}

func TestIndependentJSONNumberConversion(t *testing.T) {
	data, err := os.ReadFile("../floattest/testdata/django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var observation struct {
		Numbers []struct {
			Raw             string
			Valid           bool
			Validated       struct{ Effort struct{ Bits string } }
			Errors          map[string][]string
			RenderException string `json:"render_exception"`
		} `json:"json_numbers"`
	}
	if err = json.Unmarshal(data, &observation); err != nil {
		t.Fatal(err)
	}
	if len(observation.Numbers) != 16 {
		t.Fatal("numeric observation missing")
	}
	for _, entry := range observation.Numbers {
		value, err := JSONNumber(entry.Raw)
		if !entry.Valid {
			if len(entry.Errors["effort"]) != 1 || entry.Errors["effort"][0] != "overflow" || !errors.Is(err, ErrOverflow) {
				t.Errorf("integer overflow: %v, %v", entry.Errors, err)
			}
			continue
		}
		if entry.RenderException != "" {
			if entry.RenderException != "ValueError" || !errors.Is(err, ErrInvalid) {
				t.Errorf("nonfinite admission: %s %v", entry.Raw, err)
			}
			continue
		}
		bits, parseErr := strconv.ParseUint(entry.Validated.Effort.Bits, 16, 64)
		if parseErr != nil || err != nil || math.Float64bits(value) != bits {
			t.Errorf("number %s: %016x %v want %s", entry.Raw, math.Float64bits(value), err, entry.Validated.Effort.Bits)
		}
	}
}
