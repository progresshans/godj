package definition

import (
	"bytes"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestRenameModelDefinitionBindsBothCompleteDeclarations(t *testing.T) {
	old := ir.Model{Name: "entry", GoName: "Entry", DBTable: "blog_entry", Fields: []ir.Field{{Name: "id", GoName: "ID", Column: "id", Kind: ir.FieldAuto, PrimaryKey: true}}}
	next := old.Clone()
	next.Name, next.GoName, next.DBTable = "publication", "Publication", "blog_publication"
	op := migrations.RenameModel{AppLabel: "blog", Before: old, After: next}
	m := migrations.Migration{App: "blog", Name: "0002_rename", Operations: []migrations.Operation{&op}}
	encode := func() []byte {
		t.Helper()
		doc, err := Encode(Producer{Name: "test", Version: "1"}, m)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	load := func(doc []byte) migrations.LoadedDefinitionSet {
		t.Helper()
		loaded, _, err := Load(Source{SourceID: "rename", Document: doc})
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}
	document := encode()
	set := load(document)
	const binding = "sha256:792f97ad6cba42e1dcba6ca52d895ff96844e2af6f565ff189b9de5d21660193"
	if set.Sources()[0].HistoryDigest != binding {
		t.Fatal("independent canonical history vector", set.Sources()[0].HistoryDigest)
	}
	copy := set.Definitions()[0].Operations[0].(migrations.RenameModel)
	copy.After.Fields[0].Column = "tampered"
	original := set.Definitions()[0].Operations[0].(migrations.RenameModel)
	if !original.Before.Equal(old) || !original.After.Equal(next) {
		t.Fatal("loaded definitions leaked mutable fields")
	}
	op.After.DBTable = old.DBTable
	if load(encode()).Sources()[0].HistoryDigest == binding {
		t.Fatal("fixed physical name did not change the binding")
	}
	for _, bad := range [][]byte{
		bytes.Replace(document, []byte(`"after":`), []byte(`"old_after":`), 1),
		bytes.Replace(document, []byte(`"go_name":"Publication"`), []byte(`"go_name":"invalid"`), 1),
		bytes.Replace(document, []byte(`"name":"publication"`), []byte(`"name":"entry"`), 1),
		bytes.Replace(document, []byte(`"kind":"rename_model"`), []byte(`"kind":"rename_model","model_name":"entry"`), 1),
	} {
		if _, _, err := Load(Source{SourceID: "bad", Document: bad}); err == nil {
			t.Fatal("invalid model rename accepted", string(bad))
		}
	}
	op.After.Fields = append(op.After.Fields, ir.Field{Name: "added", GoName: "Added", Column: "added", Kind: ir.FieldText})
	if _, err := Encode(Producer{Name: "test", Version: "1"}, m); err == nil {
		t.Fatal("encoded a hidden field addition")
	}
	op.After = next.Clone()
	op.Before.DBTable = strings.Repeat("x", MaxDocumentBytes+1)
	if _, err := Encode(Producer{Name: "test", Version: "1"}, m); err == nil {
		t.Fatal("unbounded rename input")
	}
}
