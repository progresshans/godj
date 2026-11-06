package definition_test

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
)

func TestBinaryHistoryRetainsEmptyDefaultAndInputPolicy(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "binaryref", Models: []schema.Model{{Name: "packet", GoName: "Packet", Fields: []schema.Field{
		schema.BinaryField("payload", "Payload", schema.Default(binaryvalue.Value{}), schema.MaxLength(4)),
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := s.Models[0]
	before, after := model.Fields[1].Clone(), model.Fields[1].Clone()
	after.NonEditable = false
	length := after.Clone()
	length.MaxLength = 8
	initial := migrations.Migration{App: s.AppLabel, Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: s.AppLabel, Model: model}}}
	change := migrations.Migration{App: s.AppLabel, Name: "0002_policy", Dependencies: []migrations.MigrationKey{initial.Key()}, Operations: []migrations.Operation{
		migrations.AlterField{AppLabel: s.AppLabel, ModelName: model.Name, Before: before, After: after},
		migrations.AlterField{AppLabel: s.AppLabel, ModelName: model.Name, Before: after, After: length},
	}}
	producer := definition.Producer{Name: "binary-history", Version: "1"}
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
			t.Fatal("binary history changed on round trip", err)
		}
	}
	if !bytes.Contains(sources[0].Document, []byte(`"binary":""`)) {
		t.Fatal("present empty binary default was omitted")
	}
	for _, pair := range [][2]string{
		{`"non_editable":true`, `"non_editable":null`}, {`"non_editable":true`, `"non_editable":"true"`},
		{`"non_editable":true`, `"non_editable":true,"non_editable":false`},
		{`"binary":""`, `"binary":null`}, {`"binary":""`, `"binary":"Zh=="`}, {`"binary":""`, `"binary":"Zg"`},
		{`"binary":""`, `"binary":"","string":""`}, {`"binary":""`, `"binary":"","binary":""`},
	} {
		bad := bytes.Replace(sources[0].Document, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(bad, sources[0].Document) {
			t.Fatal("control did not alter wire")
		}
		if _, _, err := definition.Load(definition.Source{SourceID: "bad", Document: bad}); err == nil {
			t.Fatal("malformed binary history accepted", pair[1])
		}
	}
	base, _, err := definition.Load(sources[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{`"non_editable":true`, `"non_editable":false`}, {`"binary":""`, `"binary":"Zg=="`}} {
		wire := bytes.Replace(sources[0].Document, []byte(pair[0]), []byte(pair[1]), 1)
		changed, _, err := definition.Load(definition.Source{SourceID: "changed", Document: wire})
		if err != nil || changed.Digest() == base.Digest() {
			t.Fatal("binary policy/value omitted from history identity", err)
		}
	}
}
