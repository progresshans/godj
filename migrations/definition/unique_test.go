package definition_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestUniqueHistoricalOperationsOwnRoundTripAndDigest(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "unique", Models: []schema.Model{{Name: "entry", GoName: "Entry", Fields: []schema.Field{
		schema.CharField("reference", "Reference", 24, schema.Unique()), schema.TextField("label", "Label", schema.Nullable()),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before, after := model.Fields[2].Clone(), model.Fields[2].Clone()
	after.Unique = true
	added := model.Fields[1].Clone()
	added.Name, added.GoName, added.Column, added.Nullable = "external", "External", "external", true
	initial := migrations.Migration{App: "unique", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "unique", Model: model}}}
	add := migrations.Migration{App: "unique", Name: "0002_external", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{migrations.AddField{AppLabel: "unique", ModelName: "entry", Field: added}}}
	alter := migrations.Migration{App: "unique", Name: "0003_label", Dependencies: []migrations.MigrationKey{add.Key()}, Operations: []migrations.Operation{migrations.AlterField{AppLabel: "unique", ModelName: "entry", Before: before, After: after}}}
	producer := definition.Producer{Name: "unique-test", Version: "1"}
	var sources []definition.Source
	for _, migration := range []migrations.Migration{initial, add, alter} {
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
	for index, operation := range loaded.Definitions() {
		wire, err := definition.Encode(producer, operation)
		if err != nil || !bytes.Equal(wire, sources[index].Document) {
			t.Fatal("unique historical operation changed on round trip", err)
		}
	}
	definitions := loaded.Definitions()
	create := definitions[0].Operations[0].(migrations.CreateModel)
	addition := definitions[1].Operations[0].(migrations.AddField)
	change := definitions[2].Operations[0].(migrations.AlterField)
	if !create.Model.Fields[1].Unique || !addition.Field.Equal(added) || !change.Before.Equal(before) || !change.After.Equal(after) {
		t.Fatal("unique was lost in a historical operation")
	}
	create.Model.Fields[1].Unique = false
	if !loaded.Definitions()[0].Operations[0].(migrations.CreateModel).Model.Fields[1].Unique {
		t.Fatal("caller changed loaded unique metadata")
	}
	initial.Operations[0] = create
	plainWire, err := definition.Encode(producer, initial)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := definition.Load(definition.Source{SourceID: "plain", Document: plainWire})
	if err != nil {
		t.Fatal(err)
	}
	unique, _, err := definition.Load(sources[0])
	if err != nil || plain.Digest() == unique.Digest() {
		t.Fatal("unique did not change historical digest", err)
	}
	explicitFalse := bytes.ReplaceAll(plainWire, []byte(`"primary_key":false`), []byte(`"primary_key":false,"unique":false`))
	if bytes.Equal(explicitFalse, plainWire) {
		t.Fatal("explicit-false control did not change wire")
	}
	falseSet, _, err := definition.Load(definition.Source{SourceID: "false", Document: explicitFalse})
	if err != nil || falseSet.Digest() != plain.Digest() {
		t.Fatal("omitted and false unique differ semantically", err)
	}
	again, err := definition.Encode(producer, falseSet.Definitions()[0])
	if err != nil || !bytes.Equal(plainWire, again) {
		t.Fatal("false unique is not canonically omitted", err)
	}
	for _, value := range []string{`null`, `0`, `"true"`, `[]`, `{}`, `true,"unique":false`} {
		bad := bytes.Replace(sources[0].Document, []byte(`"unique":true`), []byte(`"unique":`+value), 1)
		if bytes.Equal(bad, sources[0].Document) {
			t.Fatal("negative control did not change unique")
		}
		if _, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad}); err == nil {
			t.Fatalf("invalid unique accepted: %s", value)
		}
	}
	badPK := bytes.Replace(sources[0].Document, []byte(`"primary_key":true`), []byte(`"primary_key":true,"unique":true`), 1)
	if _, _, err := definition.Load(definition.Source{SourceID: "redundant-pk", Document: badPK}); err == nil {
		t.Fatal("noncanonical primary-key uniqueness accepted")
	}
}
