package godj

import (
	"testing"

	"github.com/progresshans/godj/conformance/internal/protocol"
)

func testObjectField(t *testing.T, value protocol.Value, name string) protocol.Value {
	t.Helper()
	if value.Type != protocol.ValueObject {
		t.Fatalf("value = %#v, want object", value)
	}
	for _, field := range value.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	t.Fatalf("object has no field %q: %#v", name, value)
	return protocol.Value{}
}
