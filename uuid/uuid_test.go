package uuid_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/uuid"
)

func TestUUIDCanonicalValuesFromPinnedPublicReference(t *testing.T) {
	raw, err := os.ReadFile("../internal/uuidtest/testdata/django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django, DRF string
		Model       []struct {
			ToPython struct{ Value *struct{ Text, Hex string } } `json:"to_python"`
		}
	}
	if err := json.Unmarshal(raw, &reference); err != nil || reference.Django != "6.1" || reference.DRF != "3.18.0" || len(reference.Model) != 62 {
		t.Fatal("UUID reference is incomplete", err)
	}
	checked := 0
	for _, item := range reference.Model {
		if item.ToPython.Value == nil {
			continue
		}
		want := item.ToPython.Value
		for _, text := range []string{want.Text, want.Hex, strings.ToUpper(want.Text), strings.ToUpper(want.Hex)} {
			value, err := uuid.Parse(text)
			if err != nil || value.String() != want.Text || value.Hex() != want.Hex {
				t.Fatal("canonical UUID differs from the pinned public value", err)
			}
			bytes, err := hex.DecodeString(want.Hex)
			if err != nil {
				t.Fatal(err)
			}
			copied, err := uuid.FromBytes(bytes)
			if err != nil || copied != value {
				t.Fatal("UUID byte order differs", err)
			}
			bytes[0] ^= 0xff
			owned := copied.Bytes()
			owned[15] ^= 0xff
			if copied != value {
				t.Fatal("UUID retained mutable byte storage")
			}
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no reference UUID values checked")
	}
}

func TestUUIDRejectsMalformedModelSpellingsAndBytes(t *testing.T) {
	for _, text := range []string{"", "0", strings.Repeat("0", 31), strings.Repeat("0", 33),
		"g" + strings.Repeat("0", 31), strings.Repeat("０", 32), "{00000000-0000-0000-0000-000000000000}",
		"urn:uuid:00000000-0000-0000-0000-000000000000", "0000000000000-0000-0000-000000000000",
		"00000000-0000_0000-0000-000000000000", " 00000000-0000-0000-0000-000000000000", string(make([]byte, 32))} {
		if value, err := uuid.Parse(text); value != (uuid.UUID{}) || !errors.Is(err, uuid.ErrInvalid) {
			t.Fatal("invalid model spelling retained a value", err)
		}
	}
	for _, data := range [][]byte{nil, {}, make([]byte, 15), make([]byte, 17)} {
		if value, err := uuid.FromBytes(data); value != (uuid.UUID{}) || !errors.Is(err, uuid.ErrInvalid) {
			t.Fatal("invalid byte length accepted", err)
		}
	}
}

func TestUUIDAllBytePositionsAndUnsignedOrdering(t *testing.T) {
	for position := range 16 {
		var previous uuid.UUID
		for digit := range 256 {
			var value uuid.UUID
			value[position] = byte(digit)
			parsed, err := uuid.Parse(value.String())
			if err != nil || parsed != value {
				t.Fatal("UUID formatter lost a byte", err)
			}
			if digit > 0 && (value.Compare(previous) <= 0 || previous.Compare(value) >= 0) {
				t.Fatal("UUID ordering used signed bytes")
			}
			if value.Compare(value) != 0 {
				t.Fatal("UUID equality differs")
			}
			previous = value
		}
	}
	var low, high uuid.UUID
	for index := range low {
		low[index] = 0xff
	}
	low[0], high[0] = 0x7f, 0x80
	if low.Compare(high) >= 0 || low.Hex() >= high.Hex() {
		t.Fatal("UUID high-bit boundary lost unsigned order")
	}
}
