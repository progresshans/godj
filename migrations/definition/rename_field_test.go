package definition

import (
	"bytes"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestRenameFieldDefinitionBindsOldAndNewIdentities(t *testing.T) {
	before := ir.Field{Name: "legacy", GoName: "Legacy", Column: "legacy", Kind: ir.FieldChar, MaxLength: 32}
	after := before
	after.Name, after.GoName, after.Column = "label", "Label", "label"
	op := migrations.RenameField{AppLabel: "issues", ModelName: "category", Before: before, After: after}
	m := migrations.Migration{App: "issues", Name: "0002_rename", Operations: []migrations.Operation{&op}}
	encode := func() []byte {
		t.Helper()
		b, err := Encode(Producer{Name: "issues", Version: "1"}, m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	read := func(b []byte) string {
		t.Helper()
		set, _, err := Load(Source{SourceID: "rename", Document: b})
		if err != nil {
			t.Fatal(err)
		}
		return set.Sources()[0].HistoryDigest
	}
	data := encode()
	if !bytes.Contains(data, []byte(`"kind":"rename_field"`)) {
		t.Fatal("missing authored rename", string(data))
	}
	digest := read(data)
	op.After.Column = before.Column
	if d := read(encode()); d == digest {
		t.Fatal("fixed physical column retained prior definition digest")
	}
	op.After.Column = after.Column
	op.Before.MaxLength++
	if _, err := Encode(Producer{Name: "issues", Version: "1"}, m); err == nil {
		t.Fatal("accepted unrelated field change")
	}
	for _, invalid := range [][]byte{
		bytes.Replace(data, []byte(`"rename_field"`), []byte(`"alter_field"`), 1),
		bytes.Replace(data, []byte(`"before":`), []byte(`"unknown":`), 1),
		bytes.Replace(data, []byte(`"model_name":"category"`), []byte(`"model_name":null`), 1),
	} {
		if _, _, err := Load(Source{SourceID: "invalid", Document: invalid}); err == nil {
			t.Fatal("accepted malformed rename", string(invalid))
		}
	}
}
