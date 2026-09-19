package definition_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestTextDefaultHistoricalRoundTripAndInvalidLength(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("日本語\nlong text ", 500)} {
		model, err := schema.Build(schema.Definition{AppLabel: "notes", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{
			schema.TextField("body", "Body", schema.Default(value)),
			schema.TextField("abstract", "Abstract", schema.Nullable(), schema.Default(value)),
		}}}})
		if err != nil {
			t.Fatal(err)
		}
		producer := definition.Producer{Name: "text-test", Version: "1"}
		encoded, err := definition.Encode(producer, migrations.Migration{App: "notes", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "notes", Model: model.Models[0]}}})
		if err != nil {
			t.Fatal(err)
		}
		loaded, _, err := definition.Load(definition.Source{SourceID: "text-test", Document: encoded})
		if err != nil {
			t.Fatal(err)
		}
		again, err := definition.Encode(producer, loaded.Definitions()[0])
		if err != nil || !bytes.Equal(encoded, again) {
			t.Fatalf("Text default was lost or truncated: %v", err)
		}
		malformed := bytes.ReplaceAll(encoded, []byte(`"max_length":0`), []byte(`"max_length":10`))
		if _, _, err := definition.Load(definition.Source{SourceID: "invalid-text-length", Document: malformed}); err == nil {
			t.Fatal("historical Text accepted a storage length constraint")
		}
	}
}
