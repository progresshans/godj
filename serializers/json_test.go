package serializers_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func TestDecodeEncodeObjectPreservesOrderAndClosedValues(t *testing.T) {
	document := []byte(`{"title":"Go","published":true,"summary":null,"items":[1,{"x":"y"}]}`)
	object, err := serializers.DecodeObject(document, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	members := object.Members()
	wantNames := []string{"title", "published", "summary", "items"}
	if len(members) != len(wantNames) {
		t.Fatalf("members = %d", len(members))
	}
	for index, want := range wantNames {
		if members[index].Name() != want {
			t.Fatalf("member[%d] = %q, want %q", index, members[index].Name(), want)
		}
	}
	if title, ok := members[0].Value().AsString(); !ok || title != "Go" {
		t.Fatalf("title = %q, %v", title, ok)
	}
	if published, ok := members[1].Value().AsBoolean(); !ok || !published {
		t.Fatalf("published = %v, %v", published, ok)
	}
	if !members[2].Value().IsNull() {
		t.Fatal("summary is not null")
	}
	items, ok := members[3].Value().AsList()
	if !ok || len(items) != 2 {
		t.Fatalf("items = %#v, %v", items, ok)
	}
	if integer, ok := items[0].AsInteger(); !ok || integer != 1 {
		t.Fatalf("items[0] = %d, %v", integer, ok)
	}

	encoded, err := serializers.EncodeObject(object, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(document) {
		t.Fatalf("encoded = %s", encoded)
	}

	// Returned composites are detached; replacing the caller's slice cannot
	// mutate the already-published object.
	items[0] = serializers.Integer(99)
	again, _ := object.Get("items")
	againItems, _ := again.AsList()
	if integer, _ := againItems[0].AsInteger(); integer != 1 {
		t.Fatalf("object mutated through detached list: %d", integer)
	}
}

func TestDecodeObjectRejectsMalformedAmbiguousAndUnsupportedInput(t *testing.T) {
	tests := []struct {
		name     string
		document []byte
		code     serializers.ErrorCode
	}{
		{name: "empty", document: nil, code: serializers.CodeInvalidDocument},
		{name: "top-level list", document: []byte(`[]`), code: serializers.CodeInvalidDocument},
		{name: "duplicate", document: []byte(`{"title":"a","title":"b"}`), code: serializers.CodeInvalidDocument},
		{name: "trailing value", document: []byte(`{} {}`), code: serializers.CodeInvalidDocument},
		{name: "trailing garbage", document: []byte(`{} x`), code: serializers.CodeInvalidDocument},
		{name: "float", document: []byte(`{"value":1.5}`), code: serializers.CodeInvalidDocument},
		{name: "exponent", document: []byte(`{"value":1e2}`), code: serializers.CodeInvalidDocument},
		{name: "negative zero", document: []byte(`{"value":-0}`), code: serializers.CodeInvalidDocument},
		{name: "overflow", document: []byte(`{"value":9223372036854775808}`), code: serializers.CodeInvalidDocument},
		{name: "nul", document: []byte(`{"value":"\u0000"}`), code: serializers.CodeInvalidDocument},
		{name: "lone high surrogate", document: []byte(`{"value":"\ud800"}`), code: serializers.CodeInvalidDocument},
		{name: "lone low surrogate", document: []byte(`{"value":"\udc00"}`), code: serializers.CodeInvalidDocument},
		{name: "high followed by non-low", document: []byte(`{"value":"\ud800\u0041"}`), code: serializers.CodeInvalidDocument},
		{name: "invalid utf8", document: []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, code: serializers.CodeInvalidDocument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := serializers.DecodeObject(test.document, serializers.Limits{})
			if !errors.Is(err, &serializers.Error{Code: test.code}) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDecodeObjectAcceptsPairedSurrogateAndEscapedLiteral(t *testing.T) {
	object, err := serializers.DecodeObject([]byte(`{"emoji":"\ud83d\ude00","literal":"\\ud800"}`), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	emoji, _ := object.Get("emoji")
	if value, ok := emoji.AsString(); !ok || value != "😀" {
		t.Fatalf("emoji = %q, %v", value, ok)
	}
	literal, _ := object.Get("literal")
	if value, ok := literal.AsString(); !ok || value != `\ud800` {
		t.Fatalf("literal = %q, %v", value, ok)
	}
}

func TestDecodeEncodeObjectEnforcesEveryResourceLimit(t *testing.T) {
	tests := []struct {
		name     string
		document string
		limits   serializers.Limits
	}{
		{name: "document", document: `{"a":1}`, limits: serializers.Limits{MaxDocumentBytes: 6}},
		{name: "depth", document: `{"a":{"b":1}}`, limits: serializers.Limits{MaxDepth: 2}},
		{name: "values", document: `{"a":1,"b":2}`, limits: serializers.Limits{MaxValues: 2}},
		{name: "members", document: `{"a":1,"b":2}`, limits: serializers.Limits{MaxObjectMembers: 1}},
		{name: "array", document: `{"a":[1,2]}`, limits: serializers.Limits{MaxArrayItems: 1}},
		{name: "string", document: `{"a":"ab"}`, limits: serializers.Limits{MaxStringBytes: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := serializers.DecodeObject([]byte(test.document), test.limits)
			if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
				t.Fatalf("decode error = %v", err)
			}
		})
	}

	object, err := serializers.NewObject(serializers.MemberOf("a", serializers.String("ab")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = serializers.EncodeObject(object, serializers.Limits{MaxDocumentBytes: 5})
	if !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatalf("encode error = %v", err)
	}
}

func TestObjectAndEncodeRejectInvalidPublishedValues(t *testing.T) {
	if _, err := serializers.NewObject(
		serializers.MemberOf("a", serializers.Integer(1)),
		serializers.MemberOf("a", serializers.Integer(2)),
	); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := serializers.NewObject(serializers.MemberOf("", serializers.Integer(1))); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
		t.Fatalf("empty name error = %v", err)
	}
	if _, err := serializers.Encode(serializers.Value{}, serializers.Limits{}); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
		t.Fatalf("zero value error = %v", err)
	}
	if _, err := serializers.NewList(serializers.String("bad\x00value")); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidValue}) {
		t.Fatalf("invalid nested string error = %v", err)
	}
	if _, err := serializers.DecodeObject([]byte(`{}`), serializers.Limits{MaxDepth: -1}); !errors.Is(err, &serializers.Error{Code: serializers.CodeInvalidConfig}) {
		t.Fatalf("invalid limits error = %v", err)
	}
}

func TestEncodeEscapesStringsWithoutReorderingObjects(t *testing.T) {
	object, err := serializers.NewObject(
		serializers.MemberOf("z", serializers.String("line\nquote\"")),
		serializers.MemberOf("a", serializers.Boolean(false)),
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := serializers.EncodeObject(object, serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"z":"line\nquote\"","a":false}`; got != want {
		t.Fatalf("encoded = %s, want %s", got, want)
	}
}

func FuzzDecodeObjectNeverPanics(f *testing.F) {
	for _, seed := range []string{`{}`, `{"a":1}`, `{"a":[null,true,"x"]}`, `{"a":1,"a":2}`, strings.Repeat("[", 80)} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, document []byte) {
		_, _ = serializers.DecodeObject(document, serializers.Limits{MaxDocumentBytes: 4096})
	})
}
