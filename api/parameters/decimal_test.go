package parameters_test

import (
	"encoding/json"
	"net/http"
	"net/url"
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
