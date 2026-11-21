// Package urltest loads independently observed synthetic url fixtures.
package urltest

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"testing"
)

//go:embed testdata/url-*.json
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
	Scheme   string             `json:"assume_scheme"`
	Maximum  int                `json:"max_length"`
	Required bool               `json:"required"`
	Widget   string             `json:"widget"`
	Cases    map[string]Cleaned `json:"cases"`
}
type Reference struct {
	Validator map[string]bool `json:"validator"`
	Changed   []struct {
		Initial   *string `json:"initial"`
		Submitted string  `json:"submitted"`
		Changed   bool    `json:"changed"`
	} `json:"changed"`
	ModelForms   map[string]ModelForm          `json:"model_forms"`
	ModelChoices map[string]ModelForm          `json:"model_choices"`
	Defaults     map[string]*string            `json:"serializer_defaults"`
	Django       string                        `json:"django"`
	DRF          string                        `json:"drf"`
	Backend      string                        `json:"backend"`
	InputHash    string                        `json:"input_sha256"`
	Forms        map[string]Form               `json:"forms"`
	Serializers  map[string]map[string]Cleaned `json:"serializers"`
	Storage      struct {
		Before      [][]*string         `json:"before"`
		After       [][]*string         `json:"after"`
		Reverse     [][]*string         `json:"reverse"`
		Unvalidated string              `json:"unvalidated_save"`
		Queries     map[string][]string `json:"queries"`
	} `json:"storage"`
}

type ModelForm struct {
	Valid     bool     `json:"valid"`
	Candidate *string  `json:"candidate"`
	Cleaned   *string  `json:"cleaned"`
	Errors    []string `json:"errors"`
}

func Load(t testing.TB, backend string) (Reference, []Input) {
	t.Helper()
	inputs, err := fixtures.ReadFile("testdata/url-inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	document, err := fixtures.ReadFile("testdata/url-django61-" + backend + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var reference Reference
	var cases []Input
	if err := json.Unmarshal(document, &reference); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(inputs, &cases); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(inputs)
	wantBackend := backend
	if backend == "postgres" {
		wantBackend = "postgresql"
	}
	if reference.Django != "6.1" || reference.DRF != "3.18.0" || reference.Backend != wantBackend || reference.InputHash != hex.EncodeToString(hash[:]) || len(cases) != 82 || len(reference.Forms) != 7 || len(reference.Serializers) != 4 || len(reference.Defaults) != 4 || len(reference.Validator) != len(cases) || len(reference.Changed) != 6 || len(reference.ModelForms) != 5 || len(reference.ModelChoices) != 3 {
		t.Fatal("url reference authority or complete input roster differs")
	}
	return reference, cases
}
