// Package uuidtest loads the independent pinned UUID observations.
package uuidtest

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/uuid"
)

//go:embed testdata/django61.json
var raw []byte

type Value struct{ Text, Hex, Int string }

func (value Value) UUID(t testing.TB) uuid.UUID {
	t.Helper()
	identifier, err := uuid.Parse(value.Text)
	if err != nil || identifier.String() != value.Text || identifier.Hex() != value.Hex {
		t.Fatal("inconsistent independent UUID value")
	}
	return identifier
}

type Result struct {
	Valid     bool
	Validated map[string]*Value
	Errors    map[string][]string
	Rendered  string
}
type Reference struct {
	Django, DRF, Python string
	Model               []json.RawMessage
	Form                []struct {
		Required    bool
		Input       map[string]string
		Valid       bool
		Cleaned     map[string]*Value
		Errors      map[string][]string
		Changed     map[string]bool
		Widget      string
		WidgetAttrs map[string]json.RawMessage `json:"widget_attrs"`
	}
	Serializer []struct {
		Serializer string
		Partial    bool
		Input      map[string]struct {
			Type  string
			Value json.RawMessage
		}
		Result
	}
	JSONNumbers []struct {
		Raw string
		Result
	} `json:"json_numbers"`
	UnicodeDecimal struct {
		Version string
		Ranges  []struct {
			First, Last rune
			Values      []string
		}
	} `json:"unicode_decimal"`
}

func Load(t testing.TB) Reference {
	t.Helper()
	var reference Reference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || reference.Python != "3.14.3" || len(reference.Model) != 62 || len(reference.Form) != 86 || len(reference.Serializer) != 252 || len(reference.JSONNumbers) != 13 || reference.UnicodeDecimal.Version != "16.0.0" || len(reference.UnicodeDecimal.Ranges) != 76 {
		t.Fatal("UUID reference version or roster changed")
	}
	return reference
}
