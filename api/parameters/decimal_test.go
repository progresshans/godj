package parameters_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/serializers"
)

func TestCanonicalDecimalParametersKeepExactTextPresenceAndSchema(t *testing.T) {
	type input struct {
		Factor  decimal.Decimal
		Minimum *decimal.Decimal
	}
	one, err := decimal.Parse("1")
	if err != nil {
		t.Fatal(err)
	}
	setters := 0
	q, err := parameters.New(128,
		parameters.Field("factor", parameters.Default(parameters.CanonicalDecimal(6, 4), one), func(value *input, factor decimal.Decimal) { setters++; value.Factor = factor }, "Exact factor."),
		parameters.Field("min", parameters.Optional(parameters.CanonicalDecimal(14, 2)), func(value *input, minimum *decimal.Decimal) { setters++; value.Minimum = minimum }, "Optional exact minimum."))
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"0", "-0", "1", "-1.25", "99.9999", "0.0001"} {
		parsed, diagnostics, err := q.Parse("factor=" + url.QueryEscape(raw) + "&min=999999999999.99")
		if err != nil || !diagnostics.Empty() || parsed.Factor.String() != raw || parsed.Minimum == nil || parsed.Minimum.String() != "999999999999.99" {
			t.Fatal("exact Decimal parameter", raw, parsed, diagnostics, err)
		}
	}
	parsed, diagnostics, err := q.Parse("")
	if err != nil || !diagnostics.Empty() || parsed.Factor != one || parsed.Minimum != nil {
		t.Fatal("Decimal omission", parsed, diagnostics, err)
	}
	for _, raw := range []string{"", "+1", "01", "1.0", "1.", ".5", "1e0", "-0.0", " 1", "1\n", "1\x00", "١", "NaN", "Infinity", "100", "0.00001", strings.Repeat("9", 4097)} {
		before := setters
		parsed, diagnostics, err := q.Parse("factor=" + url.QueryEscape(raw))
		if err != nil || diagnostics.Empty() || parsed != (input{}) || setters != before {
			t.Fatal("invalid Decimal reached setter", raw, parsed, diagnostics, err)
		}
	}
	metadata := q.Parameters()
	encoded, err := serializers.Encode(metadata[0].Schema.Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["type"] != "string" || schema["default"] != "1" || schema["maxLength"] != float64(8) || schema["x-godj-decimal"] == nil {
		t.Fatal("Decimal query schema lost exact domain", string(encoded))
	}
	for _, precision := range [][2]int{{0, 0}, {1001, 0}, {2, -1}, {2, 3}} {
		_, err := parameters.New(128, parameters.Field("x", parameters.Required(parameters.CanonicalDecimal(precision[0], precision[1])), func(*input, decimal.Decimal) {}, "Bad precision."))
		if err == nil {
			t.Fatal("invalid Decimal precision declared", precision)
		}
	}
	bad := decimal.Decimal{Coefficient: "bad"}
	if _, err := parameters.New(128, parameters.Field("x", parameters.Default(parameters.CanonicalDecimal(3, 2), bad), func(*input, decimal.Decimal) {}, "Invalid default.")); err == nil {
		t.Fatal("invalid Decimal default accepted")
	}
	// A complete schema-less string parser would miss header-specific bounds.
	header, err := parameters.NewHeader("X-Rate", parameters.Required(parameters.CanonicalDecimal(6, 4)), 8, "Exact rate.")
	if err != nil {
		t.Fatal(err)
	}
	value, diagnostics, err := header.Parse(http.Header{"X-Rate": []string{"-99.9999"}})
	if err != nil || !diagnostics.Empty() || value.String() != "-99.9999" {
		t.Fatal("exact Decimal header", value, diagnostics, err)
	}
}

func TestCanonicalPositiveDecimalQueryHeaderAndPublishedDomain(t *testing.T) {
	type input struct{ Value decimal.Decimal }
	for _, domain := range []struct {
		digits, places int
		valid, invalid []string
	}{
		{6, 4, []string{"1", "99.9999", "0.0001", "0.1"}, []string{"0", "-0", "-1", "-0.0001", "100", "0.00001", "01", "1.0", "1e0", "+1", "1\n"}},
		{4, 4, []string{"0.0001", "0.9999"}, []string{"0", "-0", "1", "0.00001", "0.1000"}},
		{2, 0, []string{"1", "99"}, []string{"0", "-0", "0.1", "100", "-1"}},
	} {
		setters := 0
		q, err := parameters.New(128, parameters.Field("x", parameters.Required(parameters.CanonicalPositiveDecimal(domain.digits, domain.places)), func(value *input, number decimal.Decimal) { setters++; value.Value = number }, "Positive exact value."))
		if err != nil {
			t.Fatal(err)
		}
		header, err := parameters.NewHeader("X-Rate", parameters.Required(parameters.CanonicalPositiveDecimal(domain.digits, domain.places)), 16, "Positive exact value.")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := serializers.Encode(q.Parameters()[0].Schema.Value(), serializers.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		var schema struct{ Type, Pattern string }
		if err := json.Unmarshal(encoded, &schema); err != nil || schema.Type != "string" || !strings.HasSuffix(schema.Pattern, `$(?![\s\S])`) {
			t.Fatal("positive grammar must consume the complete string", string(encoded), err)
		}
		// Go's $ already requires the absolute end. Strip only the redundant
		// ECMAScript final-newline guard to exercise the published grammar.
		pattern := regexp.MustCompile(strings.TrimSuffix(schema.Pattern, `(?![\s\S])`))
		for _, raw := range append(append([]string(nil), domain.valid...), domain.invalid...) {
			want := false
			for _, valid := range domain.valid {
				want = want || raw == valid
			}
			before := setters
			value, diagnostics, err := q.Parse("x=" + url.QueryEscape(raw))
			if err != nil || diagnostics.Empty() != want || pattern.MatchString(raw) != want || want && (value.Value.String() != raw || setters != before+1) || !want && (value != (input{}) || setters != before) {
				t.Fatal("positive query/parser/schema disagreement", raw, value, diagnostics, err)
			}
			parsed, diagnostics, err := header.Parse(http.Header{"X-Rate": []string{raw}})
			if err != nil || diagnostics.Empty() != want || want && parsed.String() != raw || !want && parsed != (decimal.Decimal{}) {
				t.Fatal("positive header domain differs", raw, parsed, diagnostics, err)
			}
		}
	}
	for _, raw := range []string{"0", "-0", "-1", "100"} {
		value, err := decimal.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		codec := parameters.CanonicalPositiveDecimal(6, 4)
		if _, err := parameters.New(128, parameters.Field("x", parameters.Default(codec, value), func(*input, decimal.Decimal) {}, "Invalid positive default.")); err == nil {
			t.Fatal("invalid positive query default accepted", raw)
		}
		if _, err := parameters.NewHeader("X-Rate", parameters.Default(codec, value), 8, "Invalid positive default."); err == nil {
			t.Fatal("invalid positive header default accepted", raw)
		}
	}
	one := decimal.Decimal{Coefficient: "1"}
	q, err := parameters.New(128, parameters.Field("x", parameters.Default(parameters.CanonicalPositiveDecimal(6, 4), one), func(value *input, number decimal.Decimal) { value.Value = number }, "Positive default."))
	if err != nil {
		t.Fatal(err)
	}
	value, diagnostics, err := q.Parse("")
	if err != nil || !diagnostics.Empty() || value.Value != one {
		t.Fatal("positive default lost", value, diagnostics, err)
	}
	for _, maximum := range []int{0, 2, 1<<20 + 1} {
		if _, err := parameters.NewHeader("X-Rate", parameters.Required(parameters.CanonicalPositiveDecimal(4, 4)), maximum, "Unusable positive header."); err == nil {
			t.Fatal("unusable positive header budget accepted", maximum)
		}
	}
}
