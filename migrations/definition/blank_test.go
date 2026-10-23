package definition_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestBlankDefinitionRoundTripDigestAndHistoricalScalarAndCollectionState(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "blankhistory", Models: []schema.Model{
		{Name: "label", GoName: "Label", Fields: []schema.Field{schema.CharField("name", "Name", 20)}},
		{Name: "owner", GoName: "Owner", Fields: []schema.Field{schema.EmailField("email", "Email", schema.Nullable())}, ManyToMany: []schema.ManyToManyField{schema.ManyToMany("labels", "Labels", schema.Target("blankhistory", "label"), schema.RelatedName("owners"))}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var owner, label ir.Model
	for _, model := range s.Models {
		if model.Name == "owner" {
			owner = model
		} else {
			label = model
		}
	}
	before, manyBefore := owner.Fields[1], owner.ManyToMany[0]
	after, manyAfter := before.Clone(), manyBefore.Clone()
	after.Blank, manyAfter.Blank = true, true
	initial := migrations.Migration{App: s.AppLabel, Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: s.AppLabel, Model: label}, migrations.CreateModel{AppLabel: s.AppLabel, Model: owner}}}
	changed := migrations.Migration{App: s.AppLabel, Name: "0002_blank", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		&migrations.AlterField{AppLabel: s.AppLabel, ModelName: owner.Name, Before: before, After: after},
		&migrations.AlterManyToMany{AppLabel: s.AppLabel, ModelName: owner.Name, Before: manyBefore, After: manyAfter},
	}}
	producer := definition.Producer{Name: "blank-reference", Version: "1"}
	sources := make([]definition.Source, 2)
	for i, migration := range []migrations.Migration{initial, changed} {
		wire, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources[i] = definition.Source{SourceID: migration.Name, Document: wire}
	}
	if bytes.Contains(sources[0].Document, []byte(`"blank"`)) {
		t.Fatal("default false rewrote old historical shape")
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []migrations.MigrationKey{initial.Key(), changed.Key()} {
		state, err := reconstructor.Reconstruct(migrations.AfterStateRequest(key))
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := state.Model(s.AppLabel, owner.Name)
		if actual.Fields[1].Blank != (i == 1) || actual.ManyToMany[0].Blank != (i == 1) || !actual.Fields[1].Nullable {
			t.Fatal("historical blank/null policy lost", actual)
		}
	}
	for i, migration := range loaded.Definitions() {
		wire, err := definition.Encode(producer, migration)
		if err != nil || !bytes.Equal(wire, sources[i].Document) {
			t.Fatal("round-trip changed the historical definition", err)
		}
	}
	var document map[string]any
	if err := json.Unmarshal(sources[0].Document, &document); err != nil {
		t.Fatal(err)
	}
	operations := document["migration"].(map[string]any)["operations"].([]any)
	model := operations[1].(map[string]any)["model"].(map[string]any)
	field := model["fields"].([]any)[1].(map[string]any)
	many := model["many_to_many"].([]any)[0].(map[string]any)
	base, _, err := definition.Load(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []map[string]any{field, many} {
		selected["blank"] = false
		wire, _ := json.Marshal(document)
		explicit, _, err := definition.Load(definition.Source{SourceID: "explicit", Document: wire})
		if err != nil || explicit.Digest() != base.Digest() {
			t.Fatal("explicit false changed semantic identity", err)
		}
		selected["blank"] = true
		wire, _ = json.Marshal(document)
		allowed, _, err := definition.Load(definition.Source{SourceID: "allowed", Document: wire})
		if err != nil || allowed.Digest() == base.Digest() {
			t.Fatal("blank omitted from semantic digest", err)
		}
		for _, invalid := range []any{nil, 1, "true", map[string]any{}} {
			selected["blank"] = invalid
			wire, _ = json.Marshal(document)
			if _, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: wire}); err == nil {
				t.Fatal("non-boolean blank admitted")
			}
		}
		delete(selected, "blank")
	}
}
