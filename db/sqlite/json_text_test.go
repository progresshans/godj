package sqlite

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
)

func TestJSONTextLookupPreservesLiteralStringsNumbersAndSQLiteCaseScope(t *testing.T) {
	for _, tc := range []struct {
		raw, needle string
		path        int64
		want        int64
	}{
		{`"Alpha"`, "alpha", 0, 1}, {`"Alpha"`, `"`, 0, 1}, {`"Alpha"`, `"`, 1, 0},
		{`"50%_Back\\slash"`, "%_", 1, 1}, {`"plain"`, "%", 1, 0}, {`"plain"`, "_", 1, 0},
		{`"50%_Back\\slash"`, "\\", 1, 1}, {`"한글"`, "한글", 1, 1}, {`"Æ"`, "æ", 1, 0},
		{`"before\u0000AFTER"`, "after", 1, 1}, {`"before\u0000after"`, "\x00after", 1, 1},
		{`"before\u0000after"`, "\x00", 0, 0}, {`null`, "NULL", 1, 1}, {`true`, "true", 1, 1},
		{`340282366920938463463374607431768211455`, "768211455", 1, 1},
		{`1e-8`, "1e-8", 1, 1}, {`1e-8`, "1.0", 1, 0}, {`{"a":"Alpha"}`, "alpha", 1, 1},
		{`["Alpha",false]`, "FALSE", 1, 1}, {`null`, "", 1, 1},
		{`1e` + strings.Repeat("9", 4000), "999999", 1, 1},
	} {
		got, err := sqliteJSONIContains(nil, []driver.Value{tc.raw, []byte(tc.needle), tc.path})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if got, err := sqliteJSONIContains(nil, []driver.Value{nil, []byte{}, int64(1)}); err != nil || got != nil {
		t.Fatal("SQL NULL became text", got, err)
	}
	for _, args := range [][]driver.Value{
		nil,
		{nil, []byte(strings.Repeat("a", jsonvalue.MaxStringBytes+1)), int64(0)},
		{strings.Repeat("a", jsonvalue.MaxDocumentBytes+1), []byte("a"), int64(0)},
		{nil, []byte{}, int64(2)}, {nil, "x", int64(0)}, {nil, []byte{0xff}, int64(0)},
		{[]byte(`null`), []byte{}, int64(1)}, {`NaN`, []byte{}, int64(1)},
		{`{"x":1,"x":2}`, []byte{}, int64(1)}, {`"\ud800"`, []byte{}, int64(1)},
		{`1e` + strings.Repeat("9", 4097), []byte{}, int64(1)},
	} {
		if got, err := sqliteJSONIContains(nil, args); err == nil || got != nil {
			t.Fatal("invalid JSON text input accepted", got, err)
		}
	}
}
