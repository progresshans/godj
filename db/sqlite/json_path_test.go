package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONPathFunctionPreservesTypePrecisionAndRejectsInvalidDocuments(t *testing.T) {
	for _, tc := range []struct {
		doc, path string
		want      driver.Value
	}{
		{`{"":1,"\u0000":2,"a":3,"a\u0000":4}`, `[""]`, `1`},
		{`{"":1,"\u0000":2,"a":3,"a\u0000":4}`, `["\u0000"]`, `2`},
		{`{"a\u0000":4,"a":3}`, `["a"]`, `3`},
		{`{"a\u0000":4,"a":3}`, `["a\u0000"]`, `4`},
		{`{"a":340282366920938463463374607431768211455}`, `["a"]`, `340282366920938463463374607431768211455`},
		{`{"a":1.2300e-400}`, `["a"]`, `1.2300e-400`},
		{`{"a":{"y":2,"x":1}}`, `["a"]`, `{"x":1,"y":2}`},
		{`{"a":null}`, `["a"]`, `null`}, {`{"a":"null"}`, `["a"]`, `"null"`},
		{`[false]`, `[0]`, `false`}, {`{"0":false}`, `["0"]`, `false`},
		{`{"0":false}`, `[0]`, nil}, {`[false]`, `["0"]`, nil}, {`false`, `[0]`, nil},
		{`null`, `[0]`, nil}, {`{"a":null}`, `["a",0]`, nil},
	} {
		got, err := sqliteJSONAt(nil, []driver.Value{tc.doc, tc.path})
		if err != nil || got != tc.want {
			t.Fatalf("document %s path %s: %v, %v want %v", tc.doc, tc.path, got, err, tc.want)
		}
	}
	if got, err := sqliteJSONAt(nil, []driver.Value{nil, `["a"]`}); err != nil || got != nil {
		t.Fatal("SQL NULL", err)
	}
	for _, args := range [][]driver.Value{nil, {`{}`, `[]`}, {`{}`, `[-1]`}, {`{}`, `[2147483648]`}, {`{}`, `[1.0]`}, {`{}`, `[null]`}, {`{}`, `{"a":0}`}, {`{}`, strings.Repeat(" ", 32769)}, {`{}`, `["\ud800"]`}, {`{"a":1,"a":2}`, `["a"]`}, {`"\ud800"`, `[0]`}, {`NaN`, `[0]`}, {[]byte(`{}`), `["a"]`}, {`{}`, []byte(`["a"]`)}, {`{}`, `["a"]`, nil}} {
		if got, err := sqliteJSONAt(nil, args); err == nil || got != nil {
			t.Fatal("invalid path/document accepted")
		}
	}
	long, _ := json.Marshal([]string{strings.Repeat("a", 4097)})
	if _, err := sqliteJSONAt(nil, []driver.Value{`{}`, string(long)}); err == nil {
		t.Fatal("path byte cap bypassed")
	}
}

func TestJSONFunctionsAvailableOnEveryPhysicalConnectionAndReopen(t *testing.T) {
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "path.sqlite3"))
	for attempt := 0; attempt < 2; attempt++ {
		backend, err := Open(t.Context(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		backend.database.SetMaxOpenConns(2)
		first, err := backend.database.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		second, err := backend.database.Conn(t.Context())
		if err != nil {
			_ = first.Close()
			t.Fatal(err)
		}
		for _, conn := range []*sql.Conn{first, second} {
			var got string
			err := conn.QueryRowContext(t.Context(), "SELECT godj_json_at(?, ?)", `{"\u0000":"nul","":"empty"}`, `[""]`).Scan(&got)
			if err != nil || got != `"empty"` {
				t.Fatal(fmt.Sprintf("physical connection attempt %d", attempt), got, err)
			}
			var comparison int64
			if err := conn.QueryRowContext(t.Context(), "SELECT godj_json_path_cmp(godj_json_at(?, ?), ?)", `{"x":9007199254740993.000001}`, `["x"]`, `9007199254740993`).Scan(&comparison); err != nil || comparison != 1 {
				t.Fatal("exact comparison on physical connection", comparison, err)
			}
			var presence int64
			if err := conn.QueryRowContext(t.Context(), "SELECT godj_json_has_keys(godj_json_at(?, ?), ?, ?)", `{"a":{"\u0000":1}}`, `["a"]`, `[""]`, int64(0)).Scan(&presence); err != nil || presence != 0 {
				t.Fatal("literal key presence on physical connection", presence, err)
			}
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		if err := second.Close(); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
