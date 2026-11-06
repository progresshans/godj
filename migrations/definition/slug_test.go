package definition_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestSlugHistoryRetainsIndexUnicodeAndStrictWire(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "slugref", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.SlugField("address", "Address", schema.AllowUnicode(true), schema.Nullable()),
		schema.IntegerField("rank", "Rank", schema.DBIndex(true)),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before, after := model.Fields[1].Clone(), model.Fields[1].Clone()
	after.DBIndex, after.AllowUnicode = false, false
	added := before.Clone()
	added.Name, added.GoName, added.Column = "other", "Other", "other"
	initial := migrations.Migration{App: s.AppLabel, Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: s.AppLabel, Model: model}}}
	change := migrations.Migration{App: s.AppLabel, Name: "0002_change", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AlterField{AppLabel: s.AppLabel, ModelName: model.Name, Before: before, After: after},
		migrations.AddField{AppLabel: s.AppLabel, ModelName: model.Name, Field: added},
	}}
	producer := definition.Producer{Name: "slug-history", Version: "1"}
	var sources []definition.Source
	for _, migration := range []migrations.Migration{initial, change} {
		wire, err := definition.Encode(producer, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	for i, migration := range loaded.Definitions() {
		wire, err := definition.Encode(producer, migration)
		if err != nil || !bytes.Equal(wire, sources[i].Document) {
			t.Fatal("slug/index history changed on round trip", err)
		}
	}
	definitions := loaded.Definitions()
	create := definitions[0].Operations[0].(migrations.CreateModel)
	alter := definitions[1].Operations[0].(migrations.AlterField)
	add := definitions[1].Operations[1].(migrations.AddField)
	if !create.Model.Fields[1].Equal(before) || !alter.Before.Equal(before) || !alter.After.Equal(after) || !add.Field.Equal(added) {
		t.Fatal("history lost index or Unicode policy")
	}
	create.Model.Fields[1].AllowUnicode = false
	if !loaded.Definitions()[0].Operations[0].(migrations.CreateModel).Model.Fields[1].AllowUnicode {
		t.Fatal("caller mutated loaded slug policy")
	}
	base, _, err := definition.Load(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"db_index", "allow_unicode"} {
		t.Run(flag, func(t *testing.T) {
			original := []byte(`"` + flag + `":true`)
			for _, value := range []string{`null`, `0`, `"true"`, `[]`, `{}`, `true,"` + flag + `":false`} {
				bad := bytes.Replace(sources[0].Document, original, []byte(`"`+flag+`":`+value), 1)
				if bytes.Equal(bad, sources[0].Document) {
					t.Fatal("negative control did not change wire")
				}
				if _, _, err := definition.Load(definition.Source{SourceID: "bad", Document: bad}); err == nil {
					t.Fatalf("invalid %s accepted: %s", flag, value)
				}
			}
			changedWire := bytes.Replace(sources[0].Document, original, []byte(`"`+flag+`":false`), 1)
			changed, _, err := definition.Load(definition.Source{SourceID: "false", Document: changedWire})
			if err != nil || changed.Digest() == base.Digest() {
				t.Fatal("policy change lost from history digest", err)
			}
			canonical, err := definition.Encode(producer, changed.Definitions()[0])
			if err != nil || bytes.Contains(canonical, []byte(`"`+flag+`":false`)) {
				t.Fatal("false flag is not canonically omitted", err)
			}
			omitted, _, err := definition.Load(definition.Source{SourceID: "omitted", Document: canonical})
			if err != nil || omitted.Digest() != changed.Digest() {
				t.Fatal("omitted and explicit false differ", err)
			}
		})
	}
	wrongKind := bytes.Replace(sources[0].Document, []byte(`"kind":"slug"`), []byte(`"kind":"char"`), 1)
	if bytes.Equal(wrongKind, sources[0].Document) {
		t.Fatal("kind control did not change wire")
	}
	if _, _, err := definition.Load(definition.Source{SourceID: "wrong-kind", Document: wrongKind}); err == nil {
		t.Fatal("Unicode input policy admitted on another field kind")
	}
}
