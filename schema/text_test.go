package schema_test

import (
	"strings"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestTextFieldPreservesNullableAndLongStringDefaults(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("line\n日本語 \"quoted\" ", 1000)} {
		for _, nullable := range []bool{false, true} {
			options := []schema.FieldOption{schema.Default(value)}
			if nullable {
				options = append(options, schema.Nullable())
			}
			input := schema.Definition{AppLabel: "notes", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{schema.TextField("body", "Body", options...)}}}}
			result, err := schema.Build(input)
			if err != nil {
				t.Fatal(err)
			}
			field := result.Models[0].Fields[1]
			if field.Kind != ir.FieldText || field.MaxLength != 0 || field.Nullable != nullable || field.Default == nil || field.Default.String != value {
				t.Fatal("Text metadata lost a value or imposed a length")
			}
			input.Models[0].Fields[0].Default.String = "mutated"
			if field.Default.String != value {
				t.Fatal("normalized Text default aliases caller state")
			}
		}
	}
	for _, field := range []schema.Field{
		schema.TextField("body", "Body", schema.Default(false)),
		schema.TextField("body", "Body", schema.Default(int64(1))),
		{Name: "body", GoName: "Body", Kind: ir.FieldText, MaxLength: 100},
		{Name: "body", GoName: "Body", Kind: ir.FieldText, Relation: &ir.ForeignKeyRelation{}},
	} {
		if _, err := schema.Build(schema.Definition{AppLabel: "notes", Models: []schema.Model{{Name: "note", GoName: "Note", Fields: []schema.Field{field}}}}); err == nil {
			t.Fatal("invalid Text shape accepted")
		}
	}
}
