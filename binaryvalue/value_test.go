package binaryvalue_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
)

func TestBinaryOwnershipAndEmptyValue(t *testing.T) {
	input := []byte{0, 255, 'a', 128}
	value, err := binaryvalue.FromBytes(input)
	if err != nil || value.Base64() != "AP9hgA==" {
		t.Fatal(value, err)
	}
	copy := value
	input[0] = 8
	output := value.Bytes()
	output[1] = 2
	if value != copy || value.Base64() != "AP9hgA==" {
		t.Fatal("retained mutable byte storage")
	}
	empty, err := binaryvalue.FromBytes(nil)
	if err != nil || empty != (binaryvalue.Value{}) || empty.Bytes() == nil || !empty.Valid() {
		t.Fatal("empty bytes became missing or invalid", empty, err)
	}
	if empty.Compare(value) >= 0 || value.Compare(binaryvalue.Value{Data: "\x00"}) <= 0 || value.Compare(binaryvalue.Value{Data: "\x80"}) >= 0 {
		t.Fatal("byte ordering differs")
	}
}

func TestBinaryBase64SyntaxAndCanonicalization(t *testing.T) {
	for input, canonical := range map[string]string{"": "", "Zg==": "Zg==", "Zh==": "Zg==", "Zm8=": "Zm8=", "Zm9=": "Zm8=", "Zm9v": "Zm9v", "AAH/AA==": "AAH/AA=="} {
		value, err := binaryvalue.Parse(input)
		if err != nil || value.Base64() != canonical {
			t.Errorf("%q: %q, %v", input, value.Base64(), err)
		}
	}
	for _, input := range []string{"Zg", "Zm8", "Zm9v=", "Zm9v====", "Zg===", "====", "=Zg==", "Z=g=", "Zg==YQ==", "-_8=", " Zg==", "Zg==\n", "Zg\r\n==", "Z g==", "Zg\x00==", "읽기", "\u200bZg=="} {
		if _, err := binaryvalue.Parse(input); !errors.Is(err, binaryvalue.ErrInvalid) {
			t.Errorf("accepted %q: %v", input, err)
		}
	}
}

func TestBinaryLimitsAtDecodedAndEncodedBoundaries(t *testing.T) {
	value := binaryvalue.Value{Data: strings.Repeat("\xff", binaryvalue.MaxBytes)}
	if got, err := binaryvalue.Parse(value.Base64()); err != nil || got != value {
		t.Fatal("maximum value did not round trip", err)
	}
	over := binaryvalue.Value{Data: value.Data + "x"}
	if _, err := over.Canonical(); !errors.Is(err, binaryvalue.ErrLimit) || over.Valid() {
		t.Fatal("oversize literal accepted", err)
	}
	if _, err := binaryvalue.FromBytes(over.Bytes()); !errors.Is(err, binaryvalue.ErrLimit) {
		t.Fatal("oversize bytes accepted", err)
	}
	// The two decoded lengths fit in the same encoded-length bucket. The parser
	// must enforce the decoded limit after its preallocation bound too.
	if _, err := binaryvalue.Parse(over.Base64()); !errors.Is(err, binaryvalue.ErrLimit) {
		t.Fatal("oversize decoded payload accepted", err)
	}
	if _, err := binaryvalue.Parse(strings.Repeat("A", binaryvalue.MaxEncodedBytes+4)); !errors.Is(err, binaryvalue.ErrLimit) {
		t.Fatal("oversize encoded payload accepted", err)
	}
}

func FuzzBinaryRoundTrip(f *testing.F) {
	for _, data := range [][]byte{nil, {}, {0}, {0, 255, 128}, []byte("abc")} {
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		value, err := binaryvalue.FromBytes(data)
		if len(data) > binaryvalue.MaxBytes {
			if !errors.Is(err, binaryvalue.ErrLimit) {
				t.Fatal(err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := binaryvalue.Parse(value.Base64())
		if err != nil || got != value || string(got.Bytes()) != string(data) {
			t.Fatal("round trip", err)
		}
	})
}
