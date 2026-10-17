package definition_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestImageDefinitionReferencesRoundTripAndRequireHistoricalOwners(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "photos", Models: []schema.Model{{Name: "photo", GoName: "Photo", Fields: []schema.Field{
		schema.CharField("title", "Title", 40),
		schema.ImageField("photo", "Photo", schema.Nullable(), schema.ImageDimensions("width", "height")),
		schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before := model.Clone()
	before.Fields = before.Fields[:2]
	initial := migrations.Migration{App: s.AppLabel, Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: s.AppLabel, Model: before}}}
	add := migrations.Migration{App: s.AppLabel, Name: "0002_image", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AddField{AppLabel: s.AppLabel, ModelName: model.Name, Field: model.Fields[3]},
		migrations.AddField{AppLabel: s.AppLabel, ModelName: model.Name, Field: model.Fields[4]},
		migrations.AddField{AppLabel: s.AppLabel, ModelName: model.Name, Field: model.Fields[2], BeforeField: "width"},
	}}
	after := model.Fields[2]
	after.WidthField, after.HeightField = after.HeightField, after.WidthField
	alter := migrations.Migration{App: s.AppLabel, Name: "0003_dimensions", Dependencies: []migrations.MigrationKey{add.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: s.AppLabel, ModelName: model.Name, Before: model.Fields[2], After: after}}}
	producer := definition.Producer{Name: "image-reference", Version: "1"}
	sources := []definition.Source{}
	for _, migration := range []migrations.Migration{initial, add, alter} {
		wire, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	if bytes.Contains(sources[0].Document, []byte(`width_field`)) {
		t.Fatal("ordinary fields changed wire shape")
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	for i, migration := range loaded.Definitions() {
		wire, err := definition.Encode(producer, migration)
		if err != nil || !bytes.Equal(wire, sources[i].Document) {
			t.Fatal("image wire not canonical", err)
		}
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(migrations.AfterStateRequest(add.Key()))
	if err != nil {
		t.Fatal(err)
	}
	actual, ok := state.Model(s.AppLabel, model.Name)
	if !ok || !actual.Equal(model) {
		t.Fatal("added image state lost references or field order")
	}
	final, err := reconstructor.Reconstruct(migrations.AfterStateRequest(alter.Key()))
	if err != nil {
		t.Fatal(err)
	}
	actual, _ = final.Model(s.AppLabel, model.Name)
	if !actual.Fields[2].Equal(after) || final.Equal(state) {
		t.Fatal("dimension metadata absent from history")
	}
	var document map[string]any
	if err := json.Unmarshal(sources[1].Document, &document); err != nil {
		t.Fatal(err)
	}
	operations := document["migration"].(map[string]any)["operations"].([]any)
	field := operations[2].(map[string]any)["field"].(map[string]any)
	for _, invalid := range []any{nil, 1, true, "", "bad.name", []any{"width"}} {
		field["width_field"] = invalid
		wire, _ := json.Marshal(document)
		if _, _, err := definition.Load(sources[0], definition.Source{SourceID: "invalid", Document: wire}); err == nil {
			t.Fatal("malformed image reference admitted", invalid)
		}
	}
	field["width_field"] = "width"
	field["kind"] = "file"
	wire, _ := json.Marshal(document)
	if _, _, err := definition.Load(sources[0], definition.Source{SourceID: "wrong-kind", Document: wire}); err == nil {
		t.Fatal("non-image dimension metadata dropped")
	}
	field["kind"] = "image"
	for _, target := range []string{"missing", "photo", "title", "height"} {
		field["width_field"] = target
		wire, _ := json.Marshal(document)
		bad, _, err := definition.Load(sources[0], definition.Source{SourceID: "bad-owner", Document: wire})
		if err == nil {
			_, err = bad.Reconstructor(t.Context())
		}
		if err == nil {
			t.Fatal("invalid historical dimension target admitted", target)
		}
	}
	// A structurally valid field still cannot be replayed before its dimensions.
	reordered := add
	unanchored := add.Operations[2].(migrations.AddField)
	unanchored.BeforeField = ""
	reordered.Operations = []migrations.Operation{unanchored, add.Operations[0], add.Operations[1]}
	wire, err = definition.Encode(producer, reordered)
	if err != nil {
		t.Fatal(err)
	}
	bad, _, err := definition.Load(sources[0], definition.Source{SourceID: "wrong-order", Document: wire})
	if err == nil {
		_, err = bad.Reconstructor(t.Context())
	}
	if err == nil {
		t.Fatal("image became visible before dimensions")
	}
	// Swapping only dimension references produces different semantic identities.
	swap := add
	swap.Operations = append([]migrations.Operation(nil), add.Operations...)
	operation := swap.Operations[2].(migrations.AddField)
	operation.Field = after
	swap.Operations[2] = operation
	wire, err = definition.Encode(producer, swap)
	if err != nil {
		t.Fatal(err)
	}
	swapped, _, err := definition.Load(sources[0], definition.Source{SourceID: "swapped", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	original, _, err := definition.Load(sources[:2]...)
	if err != nil || swapped.Digest() == original.Digest() {
		t.Fatal("dimension references absent from semantic digest", err)
	}
}
