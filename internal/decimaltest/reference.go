// Package decimaltest reads independent pinned Django/DRF decimal observations.
package decimaltest

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/decimal"
)

//go:embed testdata/django61.json
var raw []byte

type Number struct {
	Text     string
	Sign     int
	Digits   []int
	Exponent json.RawMessage
}

func (number Number) Decimal(t testing.TB) decimal.Decimal {
	t.Helper()
	value, err := decimal.Parse(number.Text)
	if err != nil {
		t.Fatal("invalid finite reference decimal", err)
	}
	return value
}

type Result struct {
	Valid     bool
	Validated map[string]*Number
	Errors    map[string][]string
	Rendered  string
}
type Observation struct {
	Serializer string
	Partial    bool
	Input      map[string]struct {
		Type  string
		Value json.RawMessage
	}
	Result
}
type Reference struct {
	Django, DRF, Python string
	Form                []struct {
		Required, Valid bool
		Input           map[string]string
		Cleaned         map[string]*Number
		Errors          map[string][]string
		Changed         map[string]struct {
			Value     *bool
			Exception string
		}
		Widget      string
		WidgetAttrs map[string]string `json:"widget_attrs"`
	}
	Serializer []Observation
	Precision  []struct {
		Input                   string
		MaxDigits               *int `json:"max_digits"`
		DecimalPlaces           *int `json:"decimal_places"`
		Form, Model, Serializer struct {
			Value  *Number
			Codes  []string
			Output string
		}
	}
	JSONDecimalNumbers []struct {
		Raw string
		Result
	} `json:"json_decimal_numbers"`
	JSONPrecisionNumbers []struct {
		Raw     string
		Lexical Result `json:"lexical_decimal"`
	} `json:"json_precision_numbers"`
}

func Load(t testing.TB) Reference {
	t.Helper()
	var reference Reference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || reference.Python != "3.14.3" || len(reference.Form) != 136 || len(reference.Serializer) != 360 || len(reference.Precision) != 90 || len(reference.JSONDecimalNumbers) != 19 || len(reference.JSONPrecisionNumbers) != 4 {
		t.Fatal("decimal reference version or roster incomplete")
	}
	return reference
}
