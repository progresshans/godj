package definition

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestAddFieldPositionRoundTripDigestAndStrictWire(t *testing.T) {
	model := ir.Model{Name: "node", GoName: "Node", DBTable: "graph_node", Fields: []ir.Field{
		{Name: "label", GoName: "Label", Column: "label", Kind: ir.FieldText},
		{Name: "key", GoName: "Key", Column: "key", Kind: ir.FieldAuto, PrimaryKey: true},
	}}
	add := migrations.AddField{AppLabel: "graph", ModelName: "node", BeforeField: "label", Field: ir.Field{Name: "rank", GoName: "Rank", Column: "rank", Kind: ir.FieldInteger, Nullable: true}}
	migration := migrations.Migration{App: "graph", Name: "0001_position", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "graph", Model: model}, &add}}
	producer := Producer{Name: "position-test", Version: "1"}
	document, err := Encode(producer, migration)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(Source{SourceID: "position", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	r, err := loaded.Reconstructor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := r.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := state.Model("graph", "node")
	want := []ir.Field{add.Field, model.Fields[0], model.Fields[1]}
	if !reflect.DeepEqual(actual.Fields, want) {
		t.Fatal("wire roundtrip lost logical order")
	}
	encoded, err := Encode(producer, loaded.Definitions()[0])
	if err != nil || !bytes.Equal(document, encoded) {
		t.Fatalf("noncanonical roundtrip: %v", err)
	}
	add.BeforeField = "key"
	changed, err := Encode(producer, migration)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := Load(Source{SourceID: "changed", Document: changed})
	if err != nil || other.Digest() == loaded.Digest() {
		t.Fatalf("digest omitted insertion position: %v", err)
	}
	for _, replacement := range []string{`null`, `1`, `{}`, `"rank"`, `"bad anchor"`} {
		bad := bytes.Replace(document, []byte(`"before_field":"label"`), []byte(`"before_field":`+replacement), 1)
		if _, _, err := Load(Source{SourceID: "bad", Document: bad}); err == nil {
			t.Fatalf("accepted before_field %s", replacement)
		}
	}
	add.BeforeField = "rank"
	if value, err := Encode(producer, migration); err == nil || value != nil {
		t.Fatal("encoded self insertion anchor")
	}
	add.BeforeField = strings.Repeat("x", MaxDocumentBytes)
	if value, err := Encode(producer, migration); err == nil || value != nil || !strings.Contains(err.Error(), "document_bytes") {
		t.Fatalf("anchor did not enter pre-clone resource admission: %v", err)
	}
}
