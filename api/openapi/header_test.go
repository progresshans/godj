package openapi_test

import (
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/progresshans/godj/api/openapi"
)

func TestHeaderSchemasRetainScalarAndHTTPTextBoundaries(t *testing.T) {
	for _, test := range []struct {
		minimum, maximum int64
		grammar          openapi.IntegerTextGrammar
		bytes            int
		fallback         *int64
	}{
		{2, 1, openapi.CanonicalDecimal, 19, nil}, {0, 1, "invalid", 19, nil}, {-1, 1, openapi.UnsignedDigits, 19, nil},
		{1, 9, openapi.CanonicalDecimal, 19, new(int64(0))}, {1, 100, openapi.CanonicalDecimal, 1, new(int64(20))},
		{1, math.MaxInt64, openapi.CanonicalDecimal, 0, nil}, {1, 9, openapi.CanonicalDecimal, 1 << 21, nil},
	} {
		if _, err := openapi.HeaderInteger(test.minimum, test.maximum, test.grammar, test.bytes, test.fallback); err == nil {
			t.Fatal("invalid header integer accepted")
		}
	}
	for _, test := range []struct {
		bytes    int
		empty    bool
		fallback *string
	}{
		{0, true, nil}, {1 << 21, true, nil}, {4, true, new("ééa")}, {4, false, new("")}, {4, true, new("\xff")},
		{4, true, new("\x00")}, {4, true, new("\n")}, {4, true, new("\r")}, {4, true, new("\x7f")},
	} {
		if _, err := openapi.HeaderString(test.bytes, test.empty, test.fallback); err == nil {
			t.Fatal("invalid header text/default accepted")
		}
	}
	schema, err := openapi.HeaderString(4, true, new("\t"))
	if err != nil {
		t.Fatal(err)
	}
	object, _ := schema.Value().AsObject()
	value, _ := object.Get("pattern")
	pattern, _ := value.AsString()
	const guard = "(?![\\s\\S])"
	if !strings.HasSuffix(pattern, guard) {
		t.Fatal("header schema can match end before newline")
	}
	compiled, err := regexp.Compile(strings.TrimSuffix(pattern, guard))
	if err != nil {
		t.Fatal(err)
	}
	for value := 0; value < 128; value++ {
		accepted := value == '\t' || value >= 0x20 && value != 0x7f
		if compiled.MatchString(string(rune(value))) != accepted {
			t.Fatal("header schema control boundary", value)
		}
	}
	for _, raw := range []string{"", "é", "😀", "a,b", "+", "%41", " \t "} {
		if !compiled.MatchString(raw) {
			t.Fatal("header schema normalized or rejected text", raw)
		}
	}
	for _, name := range []string{"aUthoRizatioN", "COOKIE", "Content-Type", "Accept", "HOST", "transfer-encoding", "TRAILER"} {
		if err := openapi.ValidateParameter(openapi.Parameter{Name: name, In: "header", Schema: schema}); err == nil {
			t.Fatal("transport header declaration accepted", name)
		}
	}
}
