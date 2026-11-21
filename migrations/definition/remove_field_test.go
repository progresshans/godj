package definition

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestRemoveFieldDefinitionRetainsHistoryAndBounds(t *testing.T) {
	schema, err := ir.Normalize(ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "removal", Models: []ir.Model{{Name: "item", GoName: "Item", Fields: []ir.Field{
		{Name: "legacy", GoName: "Legacy", Kind: ir.FieldChar, MaxLength: 30, Nullable: true, Default: &ir.Scalar{Kind: ir.ScalarString, String: "restored"}},
		{Name: "tail", GoName: "Tail", Kind: ir.FieldText, Nullable: true},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	model := schema.Models[0]
	remove := migrations.RemoveField{AppLabel: "removal", ModelName: "item", Field: model.Fields[1].Clone(), BeforeField: "tail"}
	migration := migrations.Migration{App: "removal", Name: "0001_lifecycle", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "removal", Model: model}, &remove}}
	producer := Producer{Name: "removal-test", Version: "1"}
	wire, err := Encode(producer, migration)
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(Source{SourceID: "removal", Document: wire})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := Encode(producer, loaded.Definitions()[0])
	if err != nil || !bytes.Equal(copy, wire) {
		t.Fatal("field removal wire is not deterministic", err)
	}
	reconstructor, err := loaded.Reconstructor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatal(err)
	}
	actual, _ := state.Model("removal", "item")
	if len(actual.Fields) != 2 || actual.Fields[1].Name != "tail" {
		t.Fatal("decoded removal did not remove its exact field")
	}
	remove.Field.Default.String = "caller mutation"
	owned := loaded.Definitions()[0].Operations[1].(migrations.RemoveField)
	if owned.Field.Default.String != "restored" {
		t.Fatal("loaded removal aliases caller metadata")
	}
	owned.Field.Default.String = "accessor mutation"
	if loaded.Definitions()[0].Operations[1].(migrations.RemoveField).Field.Default.String != "restored" {
		t.Fatal("removal accessor exposes owned metadata")
	}
	remove.Field = model.Fields[1].Clone()
	add := migrations.AddField{AppLabel: remove.AppLabel, ModelName: remove.ModelName, Field: remove.Field.Clone(), BeforeField: remove.BeforeField}
	left, err := appendCanonicalOperation(nil, remove)
	if err != nil {
		t.Fatal(err)
	}
	right, err := appendCanonicalOperation(nil, add)
	if err != nil || bytes.Equal(left, right) {
		t.Fatal("opposite field operations have the same semantic identity", err)
	}
	for _, replacement := range []string{`null`, `1`, `"legacy"`, `"bad anchor"`} {
		bad := bytes.Replace(wire, []byte(`"before_field":"tail"`), []byte(`"before_field":`+replacement), 1)
		if _, _, err := Load(Source{SourceID: "bad", Document: bad}); err == nil {
			t.Fatal("invalid removal anchor accepted")
		}
	}
	for _, replacement := range []string{`"kind":"remove_field","extra":true`, `"kind":"remove_field","kind":"remove_field"`, `"kind":"unknown_remove_field"`} {
		bad := bytes.Replace(wire, []byte(`"kind":"remove_field"`), []byte(replacement), 1)
		if _, _, err := Load(Source{SourceID: "bad", Document: bad}); err == nil {
			t.Fatal("open or duplicate removal wire accepted")
		}
	}
	remove.BeforeField = strings.Repeat("x", MaxDocumentBytes)
	if encoded, err := Encode(producer, migration); err == nil || encoded != nil || !strings.Contains(err.Error(), "document_bytes") {
		t.Fatal("removal anchor escaped pre-clone resource bounds", err)
	}
	for _, operation := range []migrations.Operation{(*migrations.RemoveField)(nil), migrations.RemoveField{AppLabel: "removal", ModelName: "item", Field: model.Fields[0]}} {
		invalid := migration
		invalid.Operations = []migrations.Operation{operation}
		if encoded, err := Encode(producer, invalid); err == nil || encoded != nil {
			t.Fatal("invalid removal published a definition")
		}
	}
}
