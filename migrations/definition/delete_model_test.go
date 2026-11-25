package definition

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestDeleteModelWireBindsTheHistoricalShapeAndPosition(t *testing.T) {
	op := migrations.DeleteModel{AppLabel: "blog", BeforeModel: "keeper", Model: ir.Model{Name: "post", GoName: "Post", DBTable: "blog_post",
		Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}}}
	producer := Producer{Name: "fixture", Version: "1"}
	change := migrations.Migration{App: "blog", Name: "0002_drop", Operations: []migrations.Operation{&op}}
	document, err := Encode(producer, change)
	if err != nil {
		t.Fatal(err)
	}
	one, _, err := Load(Source{SourceID: "drop", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if got := one.Definitions()[0].Operations[0]; !reflect.DeepEqual(got, op) {
		t.Fatal("wrong deletion round trip", got)
	}
	for _, edit := range [][]byte{
		bytes.Replace(document, []byte(`"before_model":"keeper"`), []byte(`"before_model":"post"`), 1),
		bytes.Replace(document, []byte(`"before_model":"keeper"`), []byte(`"before_model":null`), 1),
		bytes.Replace(document, []byte(`"delete_model"`), []byte(`"create_model"`), 1),
	} {
		if _, _, err := Load(Source{SourceID: "invalid", Document: edit}); err == nil {
			t.Fatal("accepted malformed deletion", string(edit))
		}
	}
	original := one.Sources()[0].HistoryDigest
	for _, edit := range [][]byte{
		bytes.Replace(document, []byte(`"before_model":"keeper"`), []byte(`"before_model":"last"`), 1),
		bytes.Replace(document, []byte(`"db_table":"blog_post"`), []byte(`"db_table":"retired"`), 1),
	} {
		set, _, err := Load(Source{SourceID: "drop", Document: edit})
		if err != nil || set.Sources()[0].HistoryDigest == original {
			t.Fatal("deletion does not bind the actual shape and position", err)
		}
	}
}
