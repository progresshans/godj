package serializers_test

import (
	"github.com/progresshans/godj/serializers"
	"strings"
	"testing"
	"time"
)

func TestDateTimeJSONUsesExplicitOffsetAndTypedCanonicalValues(t *testing.T) {
	defaultTime := time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.FixedZone("input", 9*3600))
	field, err := serializers.DateTimeField("at", serializers.WithNullable(), serializers.WithDefault(serializers.DateTime(defaultTime)))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []string{`{}`, `{"at":"2026-09-19T12:34:56.123456789+09:00"}`, `{"at":"2026-09-19T03:34:56.123456Z"}`} {
		result, err := spec.Bind(decodeObject(t, document), serializers.ModeFull)
		if err != nil || !result.Valid() {
			t.Fatal("valid datetime input rejected")
		}
		value, ok := result.Values().Get("at")
		instant, typed := value.AsDateTime()
		if !ok || !typed || instant != defaultTime.UTC().Truncate(time.Microsecond) {
			t.Fatal("typed datetime normalization failed")
		}
		object, err := serializers.NewObject(serializers.MemberOf("at", value))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := serializers.Encode(object.Value(), serializers.Limits{})
		if err != nil || string(encoded) != `{"at":"2026-09-19T03:34:56.123456Z"}` {
			t.Fatalf("datetime JSON = %s, %v", encoded, err)
		}
	}
	for _, document := range []string{`{"at":false}`, `{"at":0}`, `{"at":"2026-09-19 03:34:56"}`, `{"at":"2026-09-19T03:34:56"}`, `{"at":"0001-01-01T00:00:00+01:00"}`, `{"at":""}`, `{"at":"2026-02-30T00:00:00Z"}`} {
		result, err := spec.Bind(decodeObject(t, document), serializers.ModeFull)
		if err != nil || result.Valid() {
			t.Fatalf("invalid datetime JSON accepted: %s", document)
		}
	}
	partial, err := spec.Bind(decodeObject(t, `{}`), serializers.ModePartial)
	if err != nil || !partial.Valid() {
		t.Fatal(err)
	}
	if _, present := partial.Values().Get("at"); present {
		t.Fatal("PATCH inserted an omitted datetime default")
	}
	null, err := spec.Bind(decodeObject(t, `{"at":null}`), serializers.ModeFull)
	value, _ := null.Values().Get("at")
	if err != nil || !null.Valid() || !value.IsNull() {
		t.Fatal("null datetime default override was lost")
	}
	encoded, err := serializers.Encode(serializers.DateTime(time.Time{}), serializers.Limits{})
	if err != nil || !strings.Contains(string(encoded), "0001-01-01") {
		t.Fatal("zero time became null")
	}
	if _, err := serializers.Encode(serializers.DateTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)), serializers.Limits{}); err == nil {
		t.Fatal("out-of-range datetime encoded")
	}
}
