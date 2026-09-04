package codegen_test

import "github.com/progresshans/godj/schema/ir"

func relationQueryGenerationSchemas() (ir.Schema, ir.Schema) {
	authors, blog := relationGenerationSchemas()
	blog.Models[0].Fields = append(
		blog.Models[0].Fields[:1],
		append(
			[]ir.Field{{Name: "title", GoName: "Title", Kind: ir.FieldChar, MaxLength: 200}},
			blog.Models[0].Fields[1:]...,
		)...,
	)
	return authors, blog
}
