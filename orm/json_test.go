package orm_test

import (
	"testing"

	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/orm"
)

func TestJSONScannersPreserveNullPrecisionAndOwnedTrees(t *testing.T) {
	for _, raw := range []string{`null`, `false`, `0`, `1.0`, `1e400`, `"null"`, `[]`, `{"b":[340282366920938463463374607431768211455,null],"a":true}`} {
		want, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []any{raw, jsonvalue.Value{Text: raw}} {
			var required orm.JSONScanner
			var optional orm.NullableJSONScanner
			if err := required.Scan(input); err != nil || required.JSON != want {
				t.Fatal("JSON scan lost exact value", err)
			}
			if err := optional.Scan(input); err != nil || !optional.Valid || optional.JSON != want {
				t.Fatal("JSON null became SQL NULL", err)
			}
			if err := optional.Scan(nil); err != nil || optional.Valid || optional.JSON != (jsonvalue.Value{}) {
				t.Fatal("SQL NULL retained JSON", err)
			}
		}
	}
	for _, input := range []any{nil, "", `{"a":1,"a":2}`, `"\ud800"`, `NaN`, `0 true`, []byte("null"), 0, false, map[string]any{"a": 1}, jsonvalue.Value{}} {
		required := orm.JSONScanner{JSON: jsonvalue.Null()}
		optional := orm.NullableJSONScanner{JSON: jsonvalue.Null(), Valid: true}
		if err := required.Scan(input); err == nil || required.JSON != (jsonvalue.Value{}) {
			t.Fatal("invalid required JSON retained prior value")
		}
		err := optional.Scan(input)
		if (err == nil) != (input == nil) || optional.Valid || optional.JSON != (jsonvalue.Value{}) {
			t.Fatal("invalid optional JSON retained prior value", err)
		}
	}
	var missingRequired *orm.JSONScanner
	var missingOptional *orm.NullableJSONScanner
	if missingRequired.Scan("null") == nil || missingOptional.Scan(nil) == nil {
		t.Fatal("nil JSON scanner accepted input")
	}
}
