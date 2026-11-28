package migrations_test

import (
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestManyToManyStateAndCreateHistoryOwnMetadata(t *testing.T) {
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "app", Models: []ir.Model{{Name: "node", GoName: "Node", ManyToMany: []ir.ManyToManyField{{Name: "peers", GoName: "Peers", Target: ir.ModelIdentity{AppLabel: "app", ModelName: "node"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := migrations.NewProjectState(schema)
	if err != nil {
		t.Fatal(err)
	}
	returned, _ := state.Schema("app")
	returned.Models[0].ManyToMany[0].Name = "changed"
	fresh, _ := state.Schema("app")
	if fresh.Models[0].ManyToMany[0].Name != "peers" {
		t.Fatal("state aliases metadata")
	}
	changed, err := migrations.NewProjectState(returned)
	if err != nil {
		t.Fatal(err)
	}
	if state.Equal(changed) {
		t.Fatal("state equality omitted ManyToMany")
	}
	migration := migrations.Migration{App: "app", Name: "0001_node", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "app", Model: schema.Models[0]}}}
	document, err := definition.Encode(definition.Producer{Name: "test", Version: "1"}, migration)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "create", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	reconstructor, err := loaded.Reconstructor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil || !actual.Equal(state) {
		t.Fatal("CreateModel lost columnless declaration", err)
	}
}
