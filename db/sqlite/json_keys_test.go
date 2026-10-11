package sqlite

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
)

func TestJSONKeysFunctionNullsLiteralKeysAndBoundedDocuments(t *testing.T) {
	for _, tc := range []struct {
		doc  driver.Value
		keys string
		mode int64
		want driver.Value
	}{
		{nil, `["a"]`, 0, int64(0)}, {nil, `[]`, 0, nil}, {nil, `[]`, 1, nil},
		{`null`, `[]`, 1, int64(1)}, {`null`, `[]`, 0, int64(0)}, {`null`, `["a"]`, 1, int64(0)},
		{`"a"`, `["a"]`, 0, int64(0)}, {`["a"]`, `["a"]`, 0, int64(0)},
		{`{"a":null}`, `["a"]`, 0, int64(1)}, {`{"a":null}`, `["a","b"]`, 1, int64(0)},
		{`{"a":false}`, `["b","a"]`, 0, int64(1)}, {`{"a":1}`, `["a","a"]`, 1, int64(1)},
		{`{"\u0000":1}`, `[""]`, 0, int64(0)}, {`{"":1}`, `["\u0000"]`, 0, int64(0)},
		{`{"a\u0000":1}`, `["a"]`, 0, int64(0)}, {`{"a":1}`, `["a\u0000"]`, 0, int64(0)},
		{`{"":1,"\u0000":2}`, `["","\u0000"]`, 1, int64(1)},
		{`{"0":1,"a\"\\😀":1}`, `["0","a\"\\😀"]`, 1, int64(1)},
	} {
		got, err := sqliteJSONHasKeys(nil, []driver.Value{tc.doc, tc.keys, tc.mode})
		if err != nil || got != tc.want {
			t.Fatalf("%#v: got %v, %v want %v", tc, got, err, tc.want)
		}
	}
	for _, args := range [][]driver.Value{
		nil, {`{}`, `[]`}, {`{}`, `[]`, int64(0), nil}, {`{}`, `[]`, nil}, {`{}`, `[]`, int64(-1)}, {`{}`, `[]`, int64(2)},
		{nil, `[null]`, int64(0)}, {`{}`, `null`, int64(0)}, {`{}`, `[1]`, int64(0)}, {`{}`, `["\ud800"]`, int64(0)},
		{`{}`, strings.Repeat(" ", 32769), int64(0)}, {[]byte(`{}`), `[]`, int64(0)}, {`{}`, []byte(`[]`), int64(0)},
		{`{"a":1,"a":2}`, `["a"]`, int64(0)}, {`NaN`, `[]`, int64(1)}, {`"\ud800"`, `[]`, int64(0)},
		{strings.Repeat(" ", jsonvalue.MaxDocumentBytes+1), `[]`, int64(0)},
	} {
		if got, err := sqliteJSONHasKeys(nil, args); err == nil || got != nil {
			t.Fatal("invalid arguments/documents accepted")
		}
	}
	for _, keys := range [][]string{{strings.Repeat("a", query.MaxJSONKeyBytes+1)}, make([]string, query.MaxJSONKeys+1)} {
		encoded, _ := json.Marshal(keys)
		if _, err := sqliteJSONHasKeys(nil, []driver.Value{`{}`, string(encoded), int64(1)}); err == nil {
			t.Fatal("key budget bypassed")
		}
	}
}
