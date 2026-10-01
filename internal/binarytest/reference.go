// Package binarytest loads independent Django/DRF BinaryField observations.
package binarytest

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

//go:embed testdata/*.json
var fixtures embed.FS

type Value struct {
	Kind, Base64 string
	Value        json.RawMessage
}

func (value Value) Binary(t testing.TB) *binaryvalue.Value {
	t.Helper()
	if value.Kind == "null" {
		return nil
	}
	if value.Kind != "binary" {
		t.Fatal("reference is not binary", value.Kind)
	}
	decoded, err := binaryvalue.Parse(value.Base64)
	if err != nil || decoded.Base64() != value.Base64 {
		t.Fatal("reference is not canonical base64", err)
	}
	return &decoded
}

type Outcome struct {
	Value     Value
	Codes     []string
	Exception string
}
type Case struct {
	ModelClean Outcome `json:"model_clean"`
	ModelForm  struct {
		Valid        bool
		Errors       map[string][]string
		Candidate    Value
		InternalNote string `json:"internal_note"`
		Cleaned      Value
	} `json:"model_form"`
	Serializer struct {
		Valid     bool
		Values    Value
		Errors    map[string][]string
		Exception string
	}
}
type Profile struct {
	Editable           bool
	MaxLength          *int `json:"max_length"`
	Default            Value
	FormFields         []string `json:"form_fields"`
	SerializerReadOnly bool     `json:"serializer_read_only"`
	SerializerRequired bool     `json:"serializer_required"`
	Cases              map[string]Case
}
type Reference struct {
	Django, DRF, Python, Backend string
	InputSHA256                  string            `json:"input_sha256"`
	SourceSHA256                 map[string]string `json:"source_sha256"`
	Profiles                     map[string]Profile
	Storage                      struct {
		Rows      [][]json.RawMessage
		Queries   map[string][]string
		Min, Max  Outcome
		Lifecycle []struct {
			Stage    string
			SQLCount int `json:"sql_count"`
			Rows     [][]json.RawMessage
		}
		UniqueRejected    bool `json:"unique_rejected"`
		RollbackPreserved bool `json:"rollback_preserved"`
		Deferred          struct {
			SQLCount      int  `json:"sql_count"`
			RowsUnchanged bool `json:"rows_unchanged"`
			Value         Value
		}
	}
}
type Input struct {
	Name    string
	Present bool
	Value   json.RawMessage
}

func Load(t testing.TB, backend string) Reference {
	t.Helper()
	digests := map[string]string{
		"sqlite":   "28f0bb41ba3f8031ebcc04dd64a7b214e035183be48eab0414b59fa2d67dcdd2",
		"postgres": "76cf9722a9aac71d33a764a4c4e32411af220184f090ed779cb609bc69b71ae0",
	}
	raw, err := fixtures.ReadFile("testdata/django61-" + backend + ".json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if digests[backend] == "" || hex.EncodeToString(sum[:]) != digests[backend] {
		t.Fatal("independent binary observation changed")
	}
	var result Reference
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	wantBackend := backend
	if backend == "postgres" {
		wantBackend = "postgresql"
	}
	if result.Django != "6.1" || result.DRF != "3.18.0" || result.Python != "3.14.3" || result.Backend != wantBackend || len(result.Profiles) != 7 || len(result.SourceSHA256) != 6 || len(result.Storage.Rows) != 7 || len(result.Storage.Queries) != 10 || len(result.Storage.Lifecycle) != 6 || !result.Storage.UniqueRejected || !result.Storage.RollbackPreserved || !result.Storage.Deferred.RowsUnchanged || result.Storage.Deferred.SQLCount != 0 {
		t.Fatal("binary reference profile or inventory changed")
	}
	for _, profile := range result.Profiles {
		if len(profile.Cases) != 39 {
			t.Fatal("binary input cases missing")
		}
	}
	return result
}

func Inputs(t testing.TB) []Input {
	t.Helper()
	raw, err := fixtures.ReadFile("testdata/inputs.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "d89cb51b632f3d9d63ecc883b61b9eb70d1e35e3e28c73cc117621daf8f44f2e" {
		t.Fatal("binary input corpus changed")
	}
	var result []Input
	if err := json.Unmarshal(raw, &result); err != nil || len(result) != 39 {
		t.Fatal("binary input inventory", err)
	}
	return result
}

// Model is the Go declaration of the independently observed input profiles.
// The observation supplies outcomes; this function never manufactures them.
func Model(t testing.TB, name string) ir.Model {
	t.Helper()
	options := []schema.FieldOption{schema.Editable(name != "hidden")}
	if name != "required" {
		options = append(options, schema.MaxLength(4))
	}
	if name == "blank" || name == "nullable" || name == "hidden" || name == "default" {
		options = append(options, schema.Blank())
	}
	if name == "nullable" || name == "nullable_required" || name == "hidden" {
		options = append(options, schema.Nullable())
	}
	if name == "default" {
		options = append(options, schema.Default(binaryvalue.Value{Data: "def"}))
	}
	definition, err := schema.Build(schema.Definition{AppLabel: "binary_reference", Models: []schema.Model{{Name: "packet", GoName: "Packet", Fields: []schema.Field{
		schema.CharField("title", "Title", 32), schema.BinaryField("payload", "Payload", options...), schema.CharField("internal_note", "InternalNote", 32, schema.Editable(false), schema.Default("server")),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	return definition.Models[0]
}
