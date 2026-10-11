package sqlite

import (
	"database/sql/driver"
	"math/big"
	"strings"
	"testing"
)

func TestJSONPathComparisonPreservesExactNumbersAndSQLiteClasses(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		want        int64
	}{
		{`1`, `1.000e0`, 0}, {`-0.00e10`, `0`, 0}, {`0`, `1e-400`, -1},
		{`9007199254740993.000001`, `9007199254740993`, 1},
		{`340282366920938463463374607431768211455`, `340282366920938463463374607431768211454`, 1},
		{`-9007199254740993.000001`, `-9007199254740993`, -1},
		{`1e400`, `9e399`, 1}, {`-1e-400`, `-9e-401`, -1},
		{`1e` + strings.Repeat("9", 4000), `1.00e` + strings.Repeat("9", 4000), 0},
		{`1e-` + strings.Repeat("9", 4000), `0`, 1},
		{`1`, `"0"`, -1}, {`"0"`, `1`, 1}, {`true`, `true`, 1},
		{`false`, `"false"`, 0}, {`null`, `null`, 0}, {`"null"`, `null`, 0},
		{`[]`, `"[]"`, 0}, {`{"a":0}`, `"z"`, 1},
		{`"a\u0000z"`, `"a\u0000y"`, 1}, {`"한글"`, `"z"`, 1},
	} {
		got, err := sqliteJSONPathCompare(nil, []driver.Value{tc.left, tc.right})
		if err != nil || got != tc.want {
			t.Fatalf("compare %s / %s: %v, %v; want %d", tc.left, tc.right, got, err, tc.want)
		}
	}
	// The oracle uses rational arithmetic, independently of coefficient/exponent
	// ordering. Keep this finite matrix small enough to expand its exponents.
	values := []string{"-321.025", "-1e12", "-1e-12", "-1.01", "-1", "-0", "0.000e3", "0", "1e-12", "0.001", "1", "1.0", "1.01", "10", "12e-1", "99999", "1e12"}
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
			got, err := sqliteJSONPathCompare(nil, []driver.Value{left, right})
			if err != nil || got != int64(l.Cmp(r)) {
				t.Fatal("numeric comparison differs from independent rational", left, right, got, err)
			}
		}
	}
	if got, err := sqliteJSONPathCompare(nil, []driver.Value{nil, `null`}); err != nil || got != nil {
		t.Fatal("SQL NULL became a JSON value", got, err)
	}
	for _, args := range [][]driver.Value{
		nil, {`0`}, {`0`, `0`, nil}, {[]byte(`0`), `0`}, {`0`, []byte(`0`)},
		{`NaN`, `0`}, {`0`, `NaN`}, {`"\ud800"`, `0`}, {`0`, `"\ud800"`},
		{`{"a":0,"a":1}`, `0`}, {`0`, `[]`}, {nil, `{}`},
		{strings.Repeat("0", 4097), `0`}, {`0`, strings.Repeat("9", 4097)},
	} {
		if got, err := sqliteJSONPathCompare(nil, args); err == nil || got != nil {
			t.Fatal("invalid JSON comparison accepted", got, err)
		}
	}
}
