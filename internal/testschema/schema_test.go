package testschema

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/schema/ir"
)

func TestFixturesKeepNestedMutationsIndependent(t *testing.T) {
	for _, test := range []struct {
		name  string
		build func() (ir.Schema, ir.Schema)
	}{{"relation", Relation}, {"query relation", QueryRelation}, {"project bundle", Bundle}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authors, blog := test.build()
			before, err := json.Marshal([]ir.Schema{authors, blog})
			if err != nil {
				t.Fatal(err)
			}
			authors.Models[0].Fields[0].MaxLength++
			for index := range blog.Models[0].Fields {
				field := &blog.Models[0].Fields[index]
				if field.Relation != nil {
					field.Relation.Target.AppLabel = "mutated"
				}
			}
			freshAuthors, freshBlog := test.build()
			after, err := json.Marshal([]ir.Schema{freshAuthors, freshBlog})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("fixture mutation leaked into a later caller")
			}
		})
	}
}
