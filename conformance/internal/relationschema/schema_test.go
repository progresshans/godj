package relationschema

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestDeclarationsDoNotShareMutableState(t *testing.T) {
	for name, build := range map[string]func() (ir.Schema, error){"authors": AuthorsSchema, "blog": BlogSchema} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			first, err := build()
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			first.AppLabel = "changed"
			for i := range first.Models {
				first.Models[i].Name = "changed"
				for j := range first.Models[i].Fields {
					field := &first.Models[i].Fields[j]
					field.Name = "changed"
					if field.Relation != nil {
						field.Relation.Target.AppLabel = "changed"
					}
				}
			}
			second, err := build()
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("mutating one fixture changed a later declaration")
			}
		})
	}
}
