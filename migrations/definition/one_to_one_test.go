package definition_test

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestOneToOneHistoricalWireIsDistinctAndCanonical(t *testing.T) {
	model, err := schema.Build(schema.Definition{AppLabel: "wire", Models: []schema.Model{
		{Name: "ticket", GoName: "Ticket"},
		{Name: "report", GoName: "Report", Fields: []schema.Field{
			schema.OneToOne("ticket", "TicketID", schema.Target("wire", "ticket"), schema.ReverseRelation{}, schema.Protect),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations.Migration{App: "wire", Name: "0001_initial", Operations: []migrations.Operation{
		migrations.CreateModel{AppLabel: "wire", Model: model.Models[0]}, migrations.CreateModel{AppLabel: "wire", Model: model.Models[1]},
	}}
	producer := definition.Producer{Name: "one-to-one-test", Version: "1"}
	wire, err := definition.Encode(producer, initial)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "original", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Definitions()[0].Operations[1].(migrations.CreateModel).Model.Fields[1]
	if got.Relation.Cardinality != ir.RelationOneToOne || !got.Unique || got.Relation.Reverse.Name != "report" {
		t.Fatal("wire lost one-to-one meaning")
	}
	again, err := definition.Encode(producer, loaded.Definitions()[0])
	if err != nil || !bytes.Equal(again, wire) {
		t.Fatal("wire did not round-trip canonically", err)
	}
	uniqueFK := bytes.Replace(wire, []byte(`"cardinality":"one_to_one"`), []byte(`"cardinality":"many_to_one"`), 1)
	ordinary, _, err := definition.Load(definition.Source{SourceID: "fk", Document: uniqueFK})
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Digest() == loaded.Digest() {
		t.Fatal("historical digest erased cardinality")
	}
	for _, bad := range [][]byte{
		bytes.Replace(wire, []byte(`"unique":true`), []byte(`"unique":false`), 1),
		bytes.Replace(wire, []byte(`"cardinality":"one_to_one"`), []byte(`"cardinality":"one_to_many"`), 1),
		regexp.MustCompile(`"reverse":\{[^}]*\}`).ReplaceAll(wire, []byte(`"reverse":{}`)),
	} {
		if bytes.Equal(bad, wire) {
			t.Fatal("negative control did not change wire")
		}
		if _, _, err := definition.Load(definition.Source{SourceID: "invalid", Document: bad}); err == nil {
			t.Fatal("noncanonical one-to-one wire accepted")
		}
	}
}
