package parameters_test

import (
	"errors"
	"math"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/parameters"
)

func TestHeaderCanonicalRevisionAndFieldMultiplicity(t *testing.T) {
	header, err := parameters.NewHeader("If-Revision", parameters.Required(parameters.CanonicalInt64(1, math.MaxInt64-1)), 19, "Row condition.")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		fields http.Header
		value  int64
		code   string
	}{
		{"minimum", http.Header{"If-Revision": {"1"}}, 1, ""},
		{"case insensitive", http.Header{"iF-rEVISION": {"7"}}, 7, ""},
		{"exact large integer", http.Header{"If-Revision": {"9007199254740993"}}, 9007199254740993, ""},
		{"maximum", http.Header{"If-Revision": {"9223372036854775806"}}, math.MaxInt64 - 1, ""},
		{"unrelated transport field", http.Header{"If-Revision": {"1"}, "Authorization": {strings.Repeat("x", 256)}}, 1, ""},
		{"absent", nil, 0, "required"},
		{"nil values", http.Header{"If-Revision": nil}, 0, "invalid"},
		{"no values", http.Header{"If-Revision": {}}, 0, "invalid"},
		{"empty", http.Header{"If-Revision": {""}}, 0, "invalid"},
		{"repeated equal", http.Header{"If-Revision": {"1", "1"}}, 0, "invalid"},
		{"repeated unequal", http.Header{"If-Revision": {"1", "2"}}, 0, "invalid"},
		{"case aliases", http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, 0, "invalid"},
		{"case alias empty", http.Header{"If-Revision": {"1"}, "if-revision": nil}, 0, "invalid"},
		{"unicode alias", http.Header{"If-Reviſion": {"1"}}, 0, "required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := test.fields.Clone()
			value, diagnostics, err := header.Parse(test.fields)
			if err != nil || value != test.value || !reflect.DeepEqual(before, test.fields) {
				t.Fatal("header result or borrowed ownership", value, err)
			}
			if test.code == "" {
				if !diagnostics.Empty() {
					t.Fatal(diagnostics)
				}
			} else {
				checkViolation(t, diagnostics, "If-Revision", test.code)
			}
		})
	}
	for _, raw := range []string{"0", "-1", "+1", "01", "1.0", "1e0", " 1", "1 ", "1\t", "*", "W/\"1\"", "\"1\"", "1,1", "%31", "١", "9223372036854775807", "9223372036854775808", strings.Repeat("1", 20), "1\n", "1\r", "1\x00", "\xff"} {
		value, diagnostics, err := header.Parse(http.Header{"If-Revision": {raw}})
		if err != nil || value != 0 {
			t.Fatal("invalid revision published", err)
		}
		checkViolation(t, diagnostics, "If-Revision", "invalid")
	}
}

func TestHeaderTextPresenceDoesNotApplyQueryOrListDecoding(t *testing.T) {
	header, err := parameters.NewHeader("X-Text", parameters.Optional(parameters.String(4, true)), 8, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "+", "%41", "a,b", " a ", "\t", "éé", "😀"} {
		value, diagnostics, err := header.Parse(http.Header{"x-text": {raw}})
		if err != nil || !diagnostics.Empty() || value == nil || *value != raw {
			t.Fatal("header text normalized", raw, value, diagnostics, err)
		}
	}
	for _, raw := range []string{"ééa", "😀a", "\xff", "\x00", "\x7f", "a\nb", "a\rb", "\v"} {
		value, diagnostics, err := header.Parse(http.Header{"X-Text": {raw}})
		if err != nil || value != nil {
			t.Fatal("invalid header text published", err)
		}
		checkViolation(t, diagnostics, "X-Text", "invalid")
	}
	if value, diagnostics, err := header.Parse(nil); err != nil || value != nil || !diagnostics.Empty() {
		t.Fatal("absence became an empty value", err)
	}
	for _, fields := range []http.Header{{"X-Text": nil}, {"X-Text": {"", ""}}, {"X-Text": {}, "x-text": {}}} {
		value, diagnostics, err := header.Parse(fields)
		if err != nil || value != nil {
			t.Fatal(err)
		}
		checkViolation(t, diagnostics, "X-Text", "invalid")
	}
	fallback, err := parameters.NewHeader("X-Text", parameters.Default(parameters.String(4, false), "yes"), 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if value, diagnostics, err := fallback.Parse(nil); err != nil || !diagnostics.Empty() || value != "yes" {
		t.Fatal("missing default", err)
	}
	if value, diagnostics, err := fallback.Parse(http.Header{"X-Text": {""}}); err != nil || value != "" {
		t.Fatal(err)
	} else {
		checkViolation(t, diagnostics, "X-Text", "invalid")
	}
	bounded, err := parameters.NewHeader("X-Text", parameters.Required(parameters.String(8, true)), 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if value, diagnostics, err := bounded.Parse(http.Header{"X-Text": {"12345"}}); err != nil || value != "" {
		t.Fatal(err)
	} else {
		checkViolation(t, diagnostics, "X-Text", "invalid")
	}
	digits, err := parameters.NewHeader("X-Count", parameters.Default(parameters.DigitsInt64(0, 100), 20), 4, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		fields http.Header
		want   int64
	}{{nil, 20}, {http.Header{"X-Count": {"0001"}}, 1}, {http.Header{"X-Count": {"0"}}, 0}} {
		value, diagnostics, err := digits.Parse(test.fields)
		if err != nil || !diagnostics.Empty() || value != test.want {
			t.Fatal("digits/default policy", value, err)
		}
	}
}

func TestHeaderSnapshotsPresenceAndMetadataAcrossConcurrentRequests(t *testing.T) {
	header, err := parameters.NewHeader("X-Count", parameters.Optional(parameters.CanonicalInt64(0, 9)), 1, "Optional count.")
	if err != nil {
		t.Fatal(err)
	}
	metadata := header.Parameter()
	metadata.Name = "Changed"
	metadata.Required = true
	if header.Parameter().Name != "X-Count" || header.Parameter().Required || header.MaxBytes() != 1 {
		t.Fatal("metadata mutation reached declaration")
	}
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			first, diagnostics, err := header.Parse(http.Header{"x-count": {"0"}})
			if err != nil || !diagnostics.Empty() || first == nil || *first != 0 {
				t.Error("first ownership", err)
				return
			}
			*first = 8
			second, diagnostics, err := header.Parse(http.Header{"X-Count": {"0"}})
			if err != nil || !diagnostics.Empty() || second == nil || *second != 0 || second == first {
				t.Error("shared optional storage", err)
			}
		})
	}
	group.Wait()
}

func TestHeaderRejectsInvalidDeclarationsAndUnpreparedUse(t *testing.T) {
	input := parameters.Required(parameters.CanonicalInt64(1, 9))
	for _, name := range []string{"", "X Bad", "X\nBad", "한글", "AUTHORIZATION", "Cookie", "content-type", "ACCEPT", "Host", "Transfer-Encoding", "Trailer"} {
		if _, err := parameters.NewHeader(name, input, 19, ""); err == nil {
			t.Fatal("invalid/reserved field accepted", name)
		}
	}
	for _, limit := range []int{-1, 0, (1 << 20) + 1} {
		if _, err := parameters.NewHeader("X-Count", input, limit, ""); err == nil {
			t.Fatal("invalid header budget accepted", limit)
		}
	}
	for _, input := range []parameters.Input[int64]{
		{}, parameters.Required(parameters.Codec[int64]{}), parameters.Required(parameters.CanonicalInt64(2, 1)),
		parameters.Required(parameters.DigitsInt64(-1, 9)), parameters.Default(parameters.CanonicalInt64(1, 9), 10),
	} {
		if _, err := parameters.NewHeader("X-Count", input, 19, ""); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
			t.Fatal("invalid header input", err)
		}
	}
	if _, err := parameters.NewHeader("X-Count", parameters.Default(parameters.CanonicalInt64(1, 100), 20), 1, ""); err == nil {
		t.Fatal("unrepresentable default accepted")
	}
	for _, fallback := range []string{"longer", "\n", "\r", "\x00", "\x7f", "\xff"} {
		if _, err := parameters.NewHeader("X-Text", parameters.Default(parameters.String(8, true), fallback), 4, ""); err == nil {
			t.Fatal("invalid header default accepted")
		}
	}
	if _, err := parameters.NewHeader("X-Count", input, 19, "\x00"); err == nil {
		t.Fatal("invalid parameter description accepted")
	}
	var zero parameters.Header[int64]
	if value, diagnostics, err := zero.Parse(nil); value != 0 || !diagnostics.Empty() || !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatal("zero header did not fail internally", err)
	}
	if zero.MaxBytes() != 0 || zero.Parameter().Name != "" {
		t.Fatal("zero header published metadata")
	}
}

func TestHeaderSchemaUsesTheSamePresenceRangeDefaultAndSource(t *testing.T) {
	value := int64(7)
	input := parameters.Default(parameters.CanonicalInt64(1, math.MaxInt64-1), value)
	header, err := parameters.NewHeader("If-Revision", input, 19, "Row condition.")
	if err != nil {
		t.Fatal(err)
	}
	value = 8
	parameter := header.Parameter()
	if parameter.Name != "If-Revision" || parameter.In != "header" || parameter.Required || parameter.AllowEmptyValue {
		t.Fatal("header parameter policy", parameter)
	}
	object, ok := parameter.Schema.Value().AsObject()
	if !ok {
		t.Fatal("missing schema")
	}
	for key, want := range map[string]int64{"minimum": 1, "maximum": math.MaxInt64 - 1, "default": 7, "x-godj-max-bytes": 19} {
		field, exists := object.Get(key)
		got, ok := field.AsInteger()
		if !exists || !ok || got != want {
			t.Fatal("header bound or default changed", key, got)
		}
	}
	grammar, _ := object.Get("x-godj-header-integer")
	text, _ := grammar.AsString()
	if text != "canonical-decimal" {
		t.Fatal("header lexical policy missing")
	}
	if _, exists := object.Get("x-godj-query-integer"); exists {
		t.Fatal("header advertised URL conversion")
	}
	query, err := parameters.New(64, parameters.Field("revision", input, func(out *int64, value int64) { *out = value }, ""))
	if err != nil {
		t.Fatal(err)
	}
	queryObject, _ := query.Parameters()[0].Schema.Value().AsObject()
	if _, exists := queryObject.Get("x-godj-query-integer"); !exists {
		t.Fatal("query lexical policy lost")
	}
	if _, exists := queryObject.Get("x-godj-header-integer"); exists {
		t.Fatal("header metadata leaked to query")
	}
	if got, diagnostics, err := header.Parse(nil); err != nil || !diagnostics.Empty() || got != 7 {
		t.Fatal("default differs from schema", got, err)
	}
	textHeader, err := parameters.NewHeader("X-Text", parameters.Required(parameters.String(8, true)), 4, "")
	if err != nil {
		t.Fatal(err)
	}
	stringObject, _ := textHeader.Parameter().Schema.Value().AsObject()
	bytes, _ := stringObject.Get("x-godj-max-bytes")
	maximum, _ := bytes.AsInteger()
	if !textHeader.Parameter().Required || textHeader.Parameter().AllowEmptyValue || maximum != 4 {
		t.Fatal("header empty/byte policy drift")
	}
}
