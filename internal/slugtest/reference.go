// Package slugtest loads independently observed synthetic slug fixtures.
package slugtest

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"testing"
)

//go:embed testdata/slug-*.json
var fixtures embed.FS

type Input struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}
type Cleaned struct {
	Value *string  `json:"value"`
	Codes []string `json:"codes"`
}
type Form struct {
	Maximum  int                `json:"max_length"`
	Required bool               `json:"required"`
	Unicode  bool               `json:"allow_unicode"`
	Strip    bool               `json:"strip"`
	Widget   string             `json:"widget"`
	Cases    map[string]Cleaned `json:"cases"`
}
type ModelForm struct {
	Valid     bool     `json:"valid"`
	Candidate *string  `json:"candidate"`
	Cleaned   *string  `json:"cleaned"`
	Errors    []string `json:"errors"`
}
type Snapshot struct {
	Rows [][]*string `json:"rows"`
}
type Reference struct {
	Django      string                        `json:"django"`
	DRF         string                        `json:"drf"`
	Python      string                        `json:"python"`
	Backend     string                        `json:"backend"`
	InputHash   string                        `json:"input_sha256"`
	Validators  map[string]map[string]bool    `json:"validators"`
	Forms       map[string]Form               `json:"forms"`
	Serializers map[string]map[string]Cleaned `json:"serializers"`
	Defaults    map[string]*string            `json:"serializer_defaults"`
	Changed     []struct {
		Initial   *string `json:"initial"`
		Submitted string  `json:"submitted"`
		Changed   bool    `json:"changed"`
	} `json:"changed"`
	ModelForms   map[string]map[string]ModelForm `json:"model_forms"`
	ModelChoices map[string]ModelForm            `json:"model_choices"`
	Storage      struct {
		Before      Snapshot            `json:"before"`
		Reverse     Snapshot            `json:"reverse"`
		Unvalidated string              `json:"unvalidated_save"`
		Queries     map[string][]string `json:"queries"`
		FormSaved   string              `json:"form_saved"`
	} `json:"storage"`
}

func Load(t testing.TB, backend string) (Reference, []Input) {
	t.Helper()
	inputs, err := fixtures.ReadFile("testdata/slug-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	document, err := fixtures.ReadFile("testdata/slug-django61-" + backend + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var result Reference
	var cases []Input
	if err := json.Unmarshal(document, &result); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(inputs, &cases); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(inputs)
	if backend == "postgres" {
		backend = "postgresql"
	}
	if result.Django != "6.1" || result.DRF != "3.18.0" || result.Python != "3.14.3" || result.Backend != backend || result.InputHash != hex.EncodeToString(hash[:]) || len(cases) != 73 || len(result.Forms) != 9 || len(result.Serializers) != 8 || len(result.Validators) != 2 || len(result.Defaults) != 4 || len(result.Changed) != 6 || len(result.ModelForms) != 2 || len(result.ModelChoices) != 3 {
		t.Fatal("slug reference authority or complete input roster differs")
	}
	for _, observed := range result.Forms {
		if len(observed.Cases) != len(cases) {
			t.Fatal("incomplete native form corpus")
		}
	}
	for _, observed := range result.Serializers {
		if len(observed) != len(cases) {
			t.Fatal("incomplete native serializer corpus")
		}
	}
	for _, observed := range result.Validators {
		if len(observed) != len(cases) {
			t.Fatal("incomplete native validator corpus")
		}
	}
	return result, cases
}
