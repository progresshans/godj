// Package passwordreference exposes the unchanged independent Django corpus
// to model-independent and typed-account consumers. It runs no product policy.
package passwordreference

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/progresshans/godj/internal/unicode16"
)

//go:embed password-inputs.json password-django61.json
var documents embed.FS

type Diagnostic struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params"`
}

type Case struct {
	Name, Password    string
	Profile           map[string]string
	Minimum           int
	MaximumSimilarity float64 `json:"maximum_similarity"`
	Attributes        []string
	Dictionary        []string
}

type Corpus struct {
	Cases            []Case
	MatrixValues     []string  `json:"matrix_values"`
	MatrixThresholds []float64 `json:"matrix_thresholds"`
}

type Observation struct {
	Name   string
	Errors map[string][]Diagnostic
}

type Reference struct {
	Django, Python, Unicode string
	InputSHA256             string `json:"input_sha256"`
	SourceSHA256            string `json:"source_sha256"`
	DictionarySHA256        string `json:"dictionary_sha256"`
	DictionaryWords         int    `json:"dictionary_words"`
	DictionaryChecks        int    `json:"dictionary_checks"`
	DictionaryObserved      string `json:"dictionary_sha256_observed"`
	MatrixCases             int    `json:"matrix_cases"`
	MatrixSHA256            string `json:"matrix_sha256"`
	Cases                   []Observation
}

type Attribute struct{ Field, Label, Value string }

// ProfileAttributes adapts the independent producer's declared Django User
// fields. Labels are source metadata, not observed diagnostics or user input.
func (input Case) ProfileAttributes() []Attribute {
	names := input.Attributes
	if names == nil {
		names = []string{"username", "first_name", "last_name", "email"}
	}
	labels := map[string]string{"username": "username", "first_name": "first name", "last_name": "last name", "email": "email address"}
	values := make([]Attribute, len(names))
	for i, name := range names {
		values[i] = Attribute{name, labels[name], input.Profile[name]}
	}
	return values
}

// Load returns fresh caller-owned input and observation values. The capture
// pins the producer/runtime and exact input bytes independently of Go behavior.
func Load() (Corpus, Reference, error) {
	var corpus Corpus
	var reference Reference
	input, err := documents.ReadFile("password-inputs.json")
	if err != nil {
		return corpus, reference, err
	}
	observed, err := documents.ReadFile("password-django61.json")
	if err != nil {
		return corpus, reference, err
	}
	if err := json.Unmarshal(input, &corpus); err != nil {
		return Corpus{}, Reference{}, err
	}
	if err := json.Unmarshal(observed, &reference); err != nil {
		return Corpus{}, Reference{}, err
	}
	digest := sha256.Sum256(input)
	invalid := errors.New("password reference authority, input or coverage mismatch")
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Unicode != unicode16.Version || reference.InputSHA256 != hex.EncodeToString(digest[:]) || reference.SourceSHA256 != "eccaf8bc084009e006abc45eade4c08fe753420dc0015a8138b1b95b37282a21" || len(corpus.Cases) != 156 || len(corpus.Cases) != len(reference.Cases) {
		return Corpus{}, Reference{}, invalid
	}
	names := map[string]bool{}
	for i, input := range corpus.Cases {
		if input.Name == "" || names[input.Name] || input.Name != reference.Cases[i].Name || len(reference.Cases[i].Errors) != 5 {
			return Corpus{}, Reference{}, invalid
		}
		names[input.Name] = true
		for _, name := range []string{"similarity", "minimum", "common", "numeric", "combined"} {
			if _, present := reference.Cases[i].Errors[name]; !present {
				return Corpus{}, Reference{}, invalid
			}
		}
		for _, attribute := range input.ProfileAttributes() {
			if attribute.Label == "" {
				return Corpus{}, Reference{}, invalid
			}
		}
	}
	return corpus, reference, nil
}
