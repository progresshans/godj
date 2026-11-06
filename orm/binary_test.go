package orm_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
)

func TestBinaryScannerOwnershipAndForeignTypes(t *testing.T) {
	data := []byte{0, 255, 128}
	scanner := orm.BinaryScanner{}
	if err := scanner.Scan(data); err != nil {
		t.Fatal(err)
	}
	data[0] = 3
	if scanner.Binary.Data != "\x00\xff\x80" {
		t.Fatal("driver buffer retained")
	}
	for _, input := range []any{nil, "", "AP8=", int64(3), true, binaryvalue.Value{Data: strings.Repeat("x", binaryvalue.MaxBytes+1)}} {
		scanner.Binary = binaryvalue.Value{Data: "old"}
		if err := scanner.Scan(input); err == nil || scanner.Binary != (binaryvalue.Value{}) {
			t.Fatal("foreign scan accepted or retained prior value", err)
		}
	}
	nullable := orm.NullableBinaryScanner{Binary: binaryvalue.Value{Data: "old"}, Valid: true}
	if err := nullable.Scan(nil); err != nil || nullable.Valid || nullable.Binary != (binaryvalue.Value{}) {
		t.Fatal("NULL did not reset scanner", err)
	}
	if err := nullable.Scan([]byte{}); err != nil || !nullable.Valid || nullable.Binary != (binaryvalue.Value{}) {
		t.Fatal("empty bytes collapsed into NULL", err)
	}
	if err := nullable.Scan("text"); err == nil || nullable.Valid || nullable.Binary != (binaryvalue.Value{}) {
		t.Fatal("text was silently converted", err)
	}
}

func TestBinaryQueryValueKeepsTypeOwnershipAndBounds(t *testing.T) {
	data := []byte{0, 255, 128}
	owned, err := binaryvalue.FromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	value := query.Binary(owned)
	data[0] = 3
	parameter, err := value.DatabaseValue()
	bytes, ok := parameter.([]byte)
	if err != nil || !ok || string(bytes) != "\x00\xff\x80" {
		t.Fatal("binary parameter is not byte storage", err)
	}
	bytes[0] = 7
	again, ok := value.Binary()
	if !ok || again != owned || value.Equal(query.String(owned.Data)) || value.Equal(query.Null()) {
		t.Fatal("binary query value lost type or ownership")
	}
	parameter, err = query.Binary(binaryvalue.Value{}).DatabaseValue()
	if bytes, ok := parameter.([]byte); err != nil || !ok || bytes == nil || len(bytes) != 0 {
		t.Fatal("empty binary parameter became NULL", err)
	}
	if _, err := query.Binary(binaryvalue.Value{Data: strings.Repeat("x", binaryvalue.MaxBytes+1)}).DatabaseValue(); err == nil {
		t.Fatal("oversized query value accepted")
	}
	var nilScanner *orm.BinaryScanner
	if !errors.Is(nilScanner.Scan(nil), binaryvalue.ErrInvalid) {
		t.Fatal("nil scanner did not fail")
	}
	left := query.NewFieldRef("payload", "payload", query.FieldBinary, true)
	right := query.NewFieldRef("copy", "copy", query.FieldBinary, false)
	if _, err := query.NewFieldCondition(left, query.LookupExact, right); err != nil {
		t.Fatal(err)
	}
	if _, err := query.NewInCondition(left, []query.Value{value, query.Binary(binaryvalue.Value{}), query.Null()}); err != nil {
		t.Fatal(err)
	}
}
