package jsonvalue_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/jsonvalue"
)

func TestJSONValuesPreserveRootKindsAndExactNumberTokens(t *testing.T) {
	for _, test := range []struct {
		input, canonical string
		decoded          any
	}{
		{" null ", "null", nil},
		{" true ", "true", true}, {"false", "false", false},
		{"0", "0", json.Number("0")}, {"-0", "-0", json.Number("-0")},
		{"1.0", "1.0", json.Number("1.0")}, {"1e400", "1e400", json.Number("1e400")},
		{"340282366920938463463374607431768211455", "340282366920938463463374607431768211455", json.Number("340282366920938463463374607431768211455")},
		{"0.123456789012345678901234567890", "0.123456789012345678901234567890", json.Number("0.123456789012345678901234567890")},
		{`"{\"a\":1}"`, `"{\"a\":1}"`, `{"a":1}`},
		{`"\ud83d\ude00"`, `"😀"`, "😀"},
		{`"\u0000"`, `"\u0000"`, "\x00"},
		{`[]`, `[]`, []any{}}, {`{}`, `{}`, map[string]any{}},
		{` {"b": [true, null], "a": 1} `, `{"a":1,"b":[true,null]}`, map[string]any{"a": json.Number("1"), "b": []any{true, nil}}},
	} {
		t.Run(test.input, func(t *testing.T) {
			value, err := jsonvalue.Parse([]byte(test.input))
			if err != nil || value.Text != test.canonical {
				t.Fatalf("canonical=%q err=%v", value.Text, err)
			}
			decoded, err := value.Decode()
			if err != nil || !reflect.DeepEqual(decoded, test.decoded) {
				t.Fatalf("decoded=%#v want %#v: %v", decoded, test.decoded, err)
			}
			again, err := value.Canonical()
			if err != nil || again != value {
				t.Fatal("canonical encoding is not idempotent", err)
			}
		})
	}
	if (jsonvalue.Value{}).Valid() || !jsonvalue.Null().Valid() {
		t.Fatal("uninitialized JSON became JSON null")
	}
}

func TestJSONSnapshotsDoNotShareInputBytesOrDecodedContainers(t *testing.T) {
	input := []byte(`{"items":[{"value":340282366920938463463374607431768211455}]}`)
	value, err := jsonvalue.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	want := value.Text
	for index := range input {
		input[index] = 'x'
	}
	bytes := value.Bytes()
	bytes[0] = 'x'
	decoded, err := value.Decode()
	if err != nil {
		t.Fatal(err)
	}
	object := decoded.(map[string]any)
	object["items"].([]any)[0].(map[string]any)["value"] = false
	object["changed"] = true
	again, err := value.Decode()
	if err != nil || value.Text != want || reflect.DeepEqual(decoded, again) {
		t.Fatal("JSON value retained caller-owned mutable state", err)
	}
	if number := again.(map[string]any)["items"].([]any)[0].(map[string]any)["value"]; number != json.Number("340282366920938463463374607431768211455") {
		t.Fatal("nested exact integer was changed", number)
	}
}

func TestJSONRejectsAmbiguousLossyAndIncompleteInput(t *testing.T) {
	for _, input := range []string{"", " ", "NaN", "Infinity", "-Infinity", "01", "+1", ".1", "1.", "1e", "{} []", "[1,]", `{"a":1,"\u0061":2}`, `{"nested":{"a":1,"a":2}}`, `"\ud800"`, `"\udfff"`, "\"\xff\""} {
		value, err := jsonvalue.Parse([]byte(input))
		if !errors.Is(err, jsonvalue.ErrInvalid) || value != (jsonvalue.Value{}) {
			t.Fatalf("invalid input %q published %#v: %v", input, value, err)
		}
	}
}

func TestJSONBoundsInputCanonicalEncodingAndTraversal(t *testing.T) {
	for _, input := range []string{
		strings.Repeat(" ", jsonvalue.MaxDocumentBytes) + "0",
		strings.Repeat("[", jsonvalue.MaxDepth+1) + "0" + strings.Repeat("]", jsonvalue.MaxDepth+1),
		"[" + strings.Repeat("0,", jsonvalue.MaxArrayItems) + "0]",
		strings.Repeat("9", jsonvalue.MaxNumberBytes+1),
		`"` + strings.Repeat("<", jsonvalue.MaxDocumentBytes/6+1) + `"`,
	} {
		value, err := jsonvalue.Parse([]byte(input))
		if !errors.Is(err, jsonvalue.ErrLimit) || value != (jsonvalue.Value{}) {
			t.Fatal("resource overflow published a JSON value", err)
		}
	}
	for _, input := range []string{strings.Repeat("9", jsonvalue.MaxNumberBytes), strings.Repeat("[", jsonvalue.MaxDepth) + "0" + strings.Repeat("]", jsonvalue.MaxDepth)} {
		if _, err := jsonvalue.Parse([]byte(input)); err != nil {
			t.Fatal("exact limit rejected", err)
		}
	}
}
