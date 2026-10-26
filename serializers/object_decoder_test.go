package serializers_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/serializers"
)

func envelopeDecoder(t *testing.T) serializers.ObjectDecoder {
	t.Helper()
	field, err := serializers.JSONField("payload")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := serializers.NewObjectDecoder(spec)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := serializers.ComposeObjectDecoder(serializers.ObjectDecoderField{Name: "left", Decoder: leaf}, serializers.ObjectDecoderField{Name: "right", Decoder: leaf})
	if err != nil {
		t.Fatal(err)
	}
	return decoder
}

func TestObjectDecoderKeepsJSONGrammarAtDeclaredPaths(t *testing.T) {
	decoder := envelopeDecoder(t)
	raw := `{"left":{"payload":{"":9223372036854775808,"x/y":[{"~":true}]}},"right":{"payload":{"":null}}}`
	value, err := decoder.DecodeObject([]byte(raw), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	left, _ := value.Get("left")
	object, _ := left.AsObject()
	payload, _ := object.Get("payload")
	document, ok := payload.AsJSON()
	if !ok || !strings.Contains(document.Text, "9223372036854775808") || !strings.Contains(document.Text, `"":`) {
		t.Fatal("nested JSON was coerced or lost arbitrary names")
	}
	for name, raw := range map[string]string{
		"unknown_path":             `{"unknown":{"payload":{"":1}}}`,
		"same_depth_wrong_member":  `{"left":{"other":{"":1}}}`,
		"array_in_object_position": `{"left":[{"payload":{"":1}}]}`,
		"duplicate_envelope":       `{"left":{},"left":{}}`,
		"duplicate_leaf":           `{"left":{"payload":1,"payload":2}}`,
		"duplicate_payload":        `{"left":{"payload":{"":1,"":2}}}`,
		"nul_payload":              `{"left":{"payload":{"x":"\u0000"}}}`,
		"surrogate":                `{"left":{"payload":"\ud800"}}`,
		"trailing":                 `{} {}`,
		"wrong_root":               `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decoder.DecodeObject([]byte(raw), serializers.Limits{}); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
	list, err := decoder.DecodeList([]byte("["+raw+","+raw+"]"), serializers.Limits{})
	if err != nil || len(list) != 2 {
		t.Fatal("root array lost the nested grammar", err)
	}
	for _, raw := range []string{`{}`, `[1]`, `[{"left":[{"payload":{"":1}}]}]`, `[{"other":{"payload":{"":1}}}]`} {
		if _, err := decoder.DecodeList([]byte(raw), serializers.Limits{}); err == nil {
			t.Fatal("invalid list declaration accepted")
		}
	}
}

func TestObjectDecoderSharesAggregateLimitsAcrossEnvelopesAndRows(t *testing.T) {
	decoder := envelopeDecoder(t)
	// 1 root + 2 child objects + 2 payload arrays + 4 array values = 9.
	raw := []byte(`{"left":{"payload":[1,2]},"right":{"payload":[3,4]}}`)
	if _, err := decoder.DecodeObject(raw, serializers.Limits{MaxValues: 9, MaxDepth: 4, MaxDocumentBytes: len(raw)}); err != nil {
		t.Fatal("exact aggregate boundary", err)
	}
	for name, limits := range map[string]serializers.Limits{
		"values": {MaxValues: 8}, "depth": {MaxDepth: 3}, "bytes": {MaxDocumentBytes: len(raw) - 1}, "members": {MaxObjectMembers: 1}, "array": {MaxArrayItems: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decoder.DecodeObject(raw, limits); !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
				t.Fatal("aggregate budget escaped", err)
			}
		})
	}
	list := []byte("[" + string(raw) + "," + string(raw) + "]")
	if _, err := decoder.DecodeList(list, serializers.Limits{MaxValues: 19, MaxDepth: 5}); err != nil {
		t.Fatal("array boundary", err)
	}
	if _, err := decoder.DecodeList(list, serializers.Limits{MaxValues: 18}); !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatal("each row received a new value budget", err)
	}
}

func TestObjectDecoderOwnsAndBoundsConstruction(t *testing.T) {
	field, err := serializers.StringField("value")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := serializers.NewObjectDecoder(spec)
	if err != nil {
		t.Fatal(err)
	}
	fields := []serializers.ObjectDecoderField{{Name: "child", Decoder: leaf}}
	owned, err := serializers.ComposeObjectDecoder(fields...)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = serializers.ObjectDecoderField{}
	if _, err := owned.DecodeObject([]byte(`{"child":{"value":"text"}}`), serializers.Limits{}); err != nil {
		t.Fatal("retained mutable declaration", err)
	}
	for name, fields := range map[string][]serializers.ObjectDecoderField{
		"empty": nil, "zero_child": {{Name: "child"}}, "invalid_name": {{Name: "private/name", Decoder: leaf}}, "duplicate": {{Name: "child", Decoder: leaf}, {Name: "child", Decoder: leaf}},
	} {
		t.Run(name, func(t *testing.T) {
			value, err := serializers.ComposeObjectDecoder(fields...)
			if err == nil || value.Valid() || strings.Contains(err.Error(), "private") {
				t.Fatal("invalid declaration published or leaked", err)
			}
		})
	}
	if _, err := serializers.NewObjectDecoder(serializers.Spec{}); err == nil {
		t.Fatal("zero spec")
	}
	if _, err := (serializers.ObjectDecoder{}).DecodeObject([]byte(`{}`), serializers.Limits{}); err == nil {
		t.Fatal("zero decoder")
	}
	if _, err := (serializers.ObjectDecoder{}).DecodeList([]byte(`[]`), serializers.Limits{}); err == nil {
		t.Fatal("zero list decoder")
	}
	deep := leaf
	for range 63 {
		deep, err = serializers.ComposeObjectDecoder(serializers.ObjectDecoderField{Name: "child", Decoder: deep})
		if err != nil {
			t.Fatal("supported declaration depth", err)
		}
	}
	if _, err := serializers.ComposeObjectDecoder(serializers.ObjectDecoderField{Name: "child", Decoder: deep}); err == nil {
		t.Fatal("unbounded declaration depth")
	}
	wide := make([]serializers.ObjectDecoderField, 4095)
	for index := range wide {
		wide[index] = serializers.ObjectDecoderField{Name: fmt.Sprintf("field%d", index), Decoder: leaf}
	}
	maximum, err := serializers.ComposeObjectDecoder(wide...)
	if err != nil {
		t.Fatal("4096-node declaration", err)
	}
	if _, err := serializers.ComposeObjectDecoder(serializers.ObjectDecoderField{Name: "child", Decoder: maximum}); err == nil {
		t.Fatal("node bound ignored shared child")
	}
	if _, err := serializers.ComposeObjectDecoder(append(wide, serializers.ObjectDecoderField{Name: "overflow", Decoder: leaf})...); err == nil {
		t.Fatal("width bound ignored")
	}
}
