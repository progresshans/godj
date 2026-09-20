package decimalstorage_test

import (
	"bytes"
	"math/big"
	"math/rand"
	"sort"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
)

func TestOrderKeysPreserveNumericOrderAndRejectNoncanonicalStorage(t *testing.T) {
	values := []decimal.Decimal{{}, {Coefficient: "-0"}, {Coefficient: "1", Exponent: 1000}, {Coefficient: "-1", Exponent: -1000}}
	random := rand.New(rand.NewSource(91))
	for range 2000 {
		digits := make([]byte, 1+random.Intn(80))
		for i := range digits {
			digits[i] = '0' + byte(random.Intn(10))
		}
		text := string(digits)
		if random.Intn(2) == 0 {
			text = "-" + text
		}
		value, err := decimal.New(text, int32(random.Intn(181)-90))
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	keys := make([][]byte, len(values))
	rationals := make([]*big.Rat, len(values))
	for i, value := range values {
		key, err := decimalstorage.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decimalstorage.Decode(key)
		if err != nil || !decoded.Equal(value) {
			t.Fatal("storage changed exact value", err)
		}
		if value.Equal(decimal.Decimal{}) {
			if decoded != (decimal.Decimal{}) {
				t.Fatal("stored zero was not normalized")
			}
		} else if decoded != value {
			t.Fatal("nonzero identity changed")
		}
		before := decoded
		key[0] ^= 127
		if decoded != before {
			t.Fatal("decoded coefficient aliases physical bytes")
		}
		key[0] ^= 127
		keys[i] = key
		rational, ok := new(big.Rat).SetString(value.String())
		if !ok {
			t.Fatal("independent rational parse")
		}
		rationals[i] = rational
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i], keys[j]) < 0 })
	sort.Slice(rationals, func(i, j int) bool { return rationals[i].Cmp(rationals[j]) < 0 })
	for i, key := range keys {
		value, err := decimalstorage.Decode(key)
		if err != nil {
			t.Fatal(err)
		}
		rational, _ := new(big.Rat).SetString(value.String())
		if rational.Cmp(rationals[i]) != 0 {
			t.Fatal("numeric order differs from BLOB key order")
		}
	}
	for _, raw := range [][]byte{nil, {}, {1, 0}, {2}, {3}, {2, 128, 0, 0, 0, '0', 0}, {2, 128, 0, 0, 0, '1', '0', 0}, {2, 255, 255, 255, 255, '1', 0}, {2, 128, 0, 0, 0, '1', 1}} {
		if _, err := decimalstorage.Decode(raw); err == nil {
			t.Fatal("invalid physical key accepted")
		}
	}
	if _, err := decimalstorage.Encode(decimal.Decimal{Coefficient: "NaN"}); err == nil {
		t.Fatal("invalid Decimal became storage")
	}
}
