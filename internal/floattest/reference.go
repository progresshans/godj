// Package floattest loads independent, bit-preserving Django/DRF observations.
package floattest

import (
	_ "embed"
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

//go:embed testdata/django61.json
var raw []byte

type Number struct {
	Bits string
	Repr string
}

func (number Number) Float(t testing.TB) float64 {
	t.Helper()
	bits, err := strconv.ParseUint(number.Bits, 16, 64)
	if err != nil || len(number.Bits) != 16 {
		t.Fatal("invalid reference float bits")
	}
	return math.Float64frombits(bits)
}

type Observation struct {
	Serializer     string
	Partial, Valid bool
	Input          map[string]struct {
		Type  string
		Value json.RawMessage
	}
	Validated       map[string]*Number
	Errors          map[string][]string
	RenderException string `json:"render_exception"`
}
type Reference struct {
	Django, DRF, Python string
	Form                []struct {
		Required    bool
		Input       map[string]string
		Valid       bool
		Cleaned     map[string]*Number
		Errors      map[string][]string
		Changed     map[string]bool
		Widget      string
		WidgetAttrs map[string]string `json:"widget_attrs"`
	}
	Serializer  []Observation
	JSONNumbers []struct {
		Raw             string
		Valid           bool
		Validated       map[string]*Number
		Errors          map[string][]string
		RenderException string `json:"render_exception"`
	} `json:"json_numbers"`
	Database json.RawMessage
}

func Load(t testing.TB) Reference {
	t.Helper()
	var result Reference
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Django != "6.1" || result.DRF != "3.18.0" || result.Python != "3.14.3" || len(result.Form) != 136 || len(result.Serializer) != 360 || len(result.JSONNumbers) != 16 {
		t.Fatal("float reference version or roster incomplete")
	}
	return result
}
