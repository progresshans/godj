package sqlite

import (
	"bytes"
	"database/sql/driver"
	"math/big"
	"strings"
	"testing"
)

func TestJSONOrderingKeysPreserveExactNumbersAndSQLiteClasses(t *testing.T) {
	key := func(raw string) []byte {
		t.Helper()
		value, err := sqliteJSONSortKey(nil, []driver.Value{raw})
		if err != nil {
			t.Fatal(err)
		}
		result, ok := value.([]byte)
		if !ok || len(result) == 0 {
			t.Fatal("sort key is not BLOB")
		}
		return result
	}
	values := []string{"-321.025", "-1e12", "-1e-12", "-1.201", "-1.2", "-1.01", "-1", "-0", "0.000e3", "0", "1e-12", "0.001", "1", "1.0", "1.01", "1.2", "1.201", "10", "12e-1", "99999", "1e12", "9007199254740993", "9007199254740993.000001", "340282366920938463463374607431768211455"}
	for _, left := range values {
		for _, right := range values {
			l, ok := new(big.Rat).SetString(left)
			if !ok {
				t.Fatal(left)
			}
			r, ok := new(big.Rat).SetString(right)
			if !ok {
				t.Fatal(right)
			}
			if got := bytes.Compare(key(left), key(right)); got != l.Cmp(r) {
				t.Fatal("sort key differs from independent rational arithmetic", left, right, got)
			}
		}
	}
	huge := strings.Repeat("9", 4000)
	sorted := []string{"-1e" + huge, "-1e400", "-1e-400", "-1e-" + huge, "0", "1e-" + huge, "1e-400", "1e400", "1e" + huge, `""`, `"0"`, `"[]"`, `false`, `null`, `true`, `{}`, `"한글"`}
	for i := 1; i < len(sorted); i++ {
		if bytes.Compare(key(sorted[i-1]), key(sorted[i])) >= 0 {
			t.Fatal("numeric/text key order", i)
		}
	}
	for _, pair := range [][2]string{{"-0", "0"}, {"1e" + huge, "1.00e" + huge}, {`null`, `"null"`}, {`false`, `"false"`}, {`[]`, `"[]"`}, {`{}`, `"{}"`}} {
		if !bytes.Equal(key(pair[0]), key(pair[1])) {
			t.Fatal("ordering equivalence changed", pair)
		}
	}
	if bytes.Compare(key(`"a\u0000a"`), key(`"a\u0000b"`)) >= 0 {
		t.Fatal("NUL text suffix ignored")
	}
	if len(key("1e"+huge)) > len(huge)+32 {
		t.Fatal("numeric sort key expanded exponent")
	}
	if got, err := sqliteJSONSortKey(nil, []driver.Value{nil}); got != nil || err != nil {
		t.Fatal("SQL NULL changed", got, err)
	}
	for _, args := range [][]driver.Value{nil, {nil, nil}, {[]byte(`0`)}, {int64(0)}, {`NaN`}, {`"\ud800"`}, {`{"a":0,"a":1}`}, {strings.Repeat("9", 4097)}} {
		if got, err := sqliteJSONSortKey(nil, args); err == nil || got != nil {
			t.Fatal("invalid JSON sort value accepted", got, err)
		}
	}
}
