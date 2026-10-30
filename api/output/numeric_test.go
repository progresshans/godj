package output_test

import (
	"math"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/serializers"
)

func TestNumericOutputKeepsExactValuesNullAndFiniteJSONBoundary(t *testing.T) {
	type metrics struct {
		Cost    *decimal.Decimal
		Effort  *float64
		Elapsed *duration.Duration
	}
	shape := output.Object(
		output.Field("cost", output.Nullable(output.Decimal()), func(v metrics) *decimal.Decimal { return v.Cost }),
		output.Field("effort", output.Nullable(output.Float()), func(v metrics) *float64 { return v.Effort }),
		output.Field("elapsed", output.Nullable(output.Duration()), func(v metrics) *duration.Duration { return v.Elapsed }),
	)
	prepared := prepare(t, shape, serializers.Limits{})
	cost, err := decimal.Parse("10999999999999.89")
	if err != nil {
		t.Fatal(err)
	}
	effort := 1.5
	elapsed := duration.FromMicroseconds(-1)
	response, err := prepared.JSON(t.Context(), http.StatusOK, metrics{&cost, &effort, &elapsed})
	if err != nil {
		t.Fatal(err)
	}
	body, err := response.Body()
	if err != nil || string(body) != `{"cost":"10999999999999.89","effort":1.5,"elapsed":"-1 23:59:59.999999"}` {
		t.Fatal("numeric response", string(body), err)
	}
	null, err := prepared.JSON(t.Context(), http.StatusOK, metrics{})
	if err != nil {
		t.Fatal(err)
	}
	body, err = null.Body()
	if err != nil || string(body) != `{"cost":null,"effort":null,"elapsed":null}` {
		t.Fatal("numeric nullable response", string(body), err)
	}
	for _, invalid := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := prepared.JSON(t.Context(), http.StatusOK, metrics{&cost, &invalid, &elapsed}); err == nil {
			t.Fatal("non-finite numeric output published")
		}
	}
	invalidDecimal := decimal.Decimal{Coefficient: strings.Repeat("9", 1001)}
	invalidDuration := duration.Duration{Days: duration.MaxDays + 1}
	for _, value := range []metrics{{Cost: &invalidDecimal}, {Elapsed: &invalidDuration}} {
		if _, err := prepared.JSON(t.Context(), http.StatusOK, value); err == nil {
			t.Fatal("invalid numeric literal published")
		}
	}
}

func TestDecimalOutputGlobalBoundaryAndCanonicalSchema(t *testing.T) {
	prepared := prepare(t, output.Decimal(), serializers.Limits{})
	schema, ok := prepared.Schema().Value().AsObject()
	if !ok {
		t.Fatal("decimal schema")
	}
	patternValue, _ := schema.Get("pattern")
	patternText, _ := patternValue.AsString()
	pattern, err := regexp.Compile(patternText)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []decimal.Decimal{{}, {Coefficient: "-0"}, {Coefficient: "1", Exponent: 1000}, {Coefficient: strings.Repeat("9", 1000), Exponent: -1999}} {
		response, err := prepared.JSON(t.Context(), http.StatusOK, literal)
		if err != nil {
			t.Fatal(err)
		}
		body, err := response.Body()
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `"`+literal.String()+`"` || !pattern.MatchString(literal.String()) {
			t.Fatal("decimal global value narrowed or schema rejected it")
		}
	}
	for _, text := range []string{"-0.0", "01", "+1", "1.0", "1e2", "NaN", "1\n"} {
		if pattern.MatchString(text) {
			t.Fatal("noncanonical decimal schema", text)
		}
	}
	policyValue, _ := schema.Get("x-godj-decimal")
	policy, ok := policyValue.AsObject()
	if !ok {
		t.Fatal("decimal global policy")
	}
	for name, want := range map[string]int64{"maxSignificantDigits": 1000, "minimumAdjustedExponent": -1000, "maximumAdjustedExponent": 1000} {
		value, _ := policy.Get(name)
		number, ok := value.AsInteger()
		if !ok || number != want {
			t.Fatal("decimal global bound", name)
		}
	}
}
