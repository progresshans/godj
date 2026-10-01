package definition

import (
	"github.com/progresshans/godj/schema/ir"
	"testing"
)

func TestIndexAndUnicodeWireAdmissionPrecedesCloning(t *testing.T) {
	for _, field := range []ir.Field{{Kind: ir.FieldSlug, DBIndex: true}, {Kind: ir.FieldSlug, AllowUnicode: true}, {Kind: ir.FieldSlug, DBIndex: true, AllowUnicode: true}} {
		full, empty := encodingSizeScanner{}, encodingSizeScanner{}
		if err := full.scanField("field", field); err != nil {
			t.Fatal(err)
		}
		if err := empty.scanField("field", ir.Field{Kind: ir.FieldSlug}); err != nil {
			t.Fatal(err)
		}
		want := uint64(0)
		if field.DBIndex {
			want += uint64(len(`,"db_index":true`))
		}
		if field.AllowUnicode {
			want += uint64(len(`,"allow_unicode":true`))
		}
		if full.lowerBound-empty.lowerBound != want {
			t.Fatal("new flags bypass encoded byte accounting")
		}
		exact := encodingSizeScanner{lowerBound: MaxDocumentBytes - full.lowerBound}
		if err := exact.scanField("field", field); err != nil {
			t.Fatal("exact budget rejected", err)
		}
		over := encodingSizeScanner{lowerBound: MaxDocumentBytes - full.lowerBound + 1}
		if err := over.scanField("field", field); err == nil {
			t.Fatal("oversized wire reached cloning")
		}
	}
}
