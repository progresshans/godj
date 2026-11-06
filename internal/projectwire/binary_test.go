package projectwire

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/progresshans/godj/internal/projectspec"
	"github.com/progresshans/godj/schema/ir"
)

func TestBinaryWireInputPolicyAndPayloadAreClosedAndBudgeted(t *testing.T) {
	field := ir.Field{Name: "payload", GoName: "Payload", Column: "payload", Kind: ir.FieldBinary, NonEditable: true, MaxLength: 4, Default: &ir.Scalar{Kind: ir.ScalarBinary, Binary: "AP9hgA=="}}
	wire, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	scan := func(raw []byte) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		return parseField(decoder, &specBudget{})
	}
	if err := scan(wire); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{`"non_editable":true`, `"non_editable":null`}, {`"non_editable":true`, `"non_editable":"true"`}, {`"non_editable":true`, `"non_editable":true,"non_editable":false`},
		{`"binary":"AP9hgA=="`, `"binary":null`}, {`"binary":"AP9hgA=="`, `"binary":0`}, {`"binary":"AP9hgA=="`, `"binary":"AP9hgA==","binary":""`},
		{`"binary":"AP9hgA=="`, `"binary":"` + strings.Repeat("A", projectspec.MaxSchemaStringBytes+1) + `"`},
	} {
		bad := bytes.Replace(wire, []byte(pair[0]), []byte(pair[1]), 1)
		if bytes.Equal(bad, wire) {
			t.Fatal("binary control unchanged")
		}
		if err := scan(bad); err == nil {
			t.Fatal("malformed/budgeted binary wire accepted", pair[0])
		}
	}
}
