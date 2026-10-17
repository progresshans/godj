package forms_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/forms"
)

func TestInitialValuesAgainstPinnedDjango(t *testing.T) {
	data, err := os.ReadFile("testdata/initial-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django     string
		Python     string
		QueryCount int `json:"query_count"`
		Cases      []struct {
			Name, Kind, Initial string
			UnboundValue        string `json:"unbound_value"`
			Submitted, Cleaned  *string
			Valid, Changed      bool
			Codes               []string
		}
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.QueryCount != 0 || len(reference.Cases) != 12 {
		t.Fatal("unexpected native initial reference")
	}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			var field forms.Field
			var err error
			switch observed.Kind {
			case "char":
				field, err = forms.CharField("value", forms.WithMaxLength(4))
			case "email":
				field, err = forms.EmailField("value", forms.WithMaxLength(10))
			case "decimal":
				field, err = forms.DecimalField("value", 5, 2)
			default:
				t.Fatal("unexpected native field kind")
			}
			if err != nil {
				t.Fatal(err)
			}
			spec, err := forms.NewSpec([]forms.Field{field})
			if err != nil {
				t.Fatal(err)
			}
			parse := func(text string) forms.Value {
				if observed.Kind != "decimal" {
					return forms.String(text)
				}
				number, err := decimal.Parse(text)
				if err != nil {
					t.Fatal(err)
				}
				return forms.Decimal(number)
			}
			initial := map[string]forms.Value{"value": parse(observed.Initial)}
			unbound, err := spec.Unbound(initial)
			if err != nil || unbound.Bound() || unbound.Valid() || !unbound.Errors().Empty() {
				t.Fatal("initial was submitted to input validation", err)
			}
			value, present := unbound.Initial().Get("value")
			if !present || !value.Equal(parse(observed.UnboundValue)) {
				t.Fatal("unbound initial differs from native value")
			}
			submitted := map[string][]string{}
			if observed.Submitted != nil {
				submitted["value"] = []string{*observed.Submitted}
			}
			bound, err := spec.Bind(t.Context(), forms.NewData(submitted), initial)
			if err != nil {
				t.Fatal("existing initial prevented binding a new submission", err)
			}
			initial["value"] = parse("0")
			value, present = bound.Initial().Get("value")
			if !present || !value.Equal(parse(observed.Initial)) {
				t.Fatal("bound initial did not retain a detached snapshot")
			}
			codes := []string{}
			for _, failure := range bound.Errors().All() {
				codes = append(codes, string(failure.Code()))
			}
			if !bound.Bound() || bound.Valid() != observed.Valid || (len(bound.Changed()) != 0) != observed.Changed || !slices.Equal(codes, observed.Codes) {
				t.Fatalf("native input result differs: valid=%v changed=%v codes=%v", bound.Valid(), bound.Changed(), codes)
			}
			cleaned, present := bound.Cleaned().Get("value")
			if present != (observed.Cleaned != nil) || present && !cleaned.Equal(parse(*observed.Cleaned)) {
				t.Fatal("native cleaned result differs")
			}
			raw, present := bound.Submitted().Get("value")
			if present != (observed.Submitted != nil) || present && !slices.Equal(raw, []string{*observed.Submitted}) {
				t.Fatal("original submission was replaced with the initial value")
			}
		})
	}
}

func TestInitialValuesRetainRepresentationAndDefaultGuards(t *testing.T) {
	text, err := forms.CharField("text", forms.WithMaxLength(4))
	if err != nil {
		t.Fatal(err)
	}
	number, err := forms.DecimalField("number", 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := forms.NewSpec([]forms.Field{text, number})
	if err != nil {
		t.Fatal(err)
	}
	for _, initial := range []map[string]forms.Value{
		{"unknown": forms.String("x")},
		{"text": forms.Integer(1)},
		{"text": forms.String("x\x00y")},
		{"text": forms.String(string([]byte{255}))},
		{"number": forms.String("1.23")},
		{"number": forms.Decimal(decimal.Decimal{Coefficient: "NaN"})},
	} {
		if _, err := spec.Unbound(initial); err == nil {
			t.Fatal("malformed initial representation accepted")
		}
		if _, err := spec.Bind(t.Context(), forms.NewData(nil), initial); err == nil {
			t.Fatal("malformed initial representation bound")
		}
	}
	if _, err := forms.CharField("text", forms.WithMaxLength(4), forms.WithDefault(forms.String("longer"))); err == nil {
		t.Fatal("invalid declared default accepted")
	}
	if _, err := forms.DecimalField("number", 5, 2, forms.WithDefault(forms.Decimal(decimal.Decimal{Coefficient: "1234", Exponent: -3}))); err == nil {
		t.Fatal("invalid declared decimal default accepted")
	}
}
