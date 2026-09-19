package codegen

import "github.com/progresshans/godj/schema/ir"

// preparedSchema belongs to one generation. Renderers share its owned schema
// and canonical digest read-only; no caller input or global cache is retained.
type preparedSchema struct {
	schema ir.Schema
	hash   string
}

func prepareSchema(input ir.Schema) (preparedSchema, error) {
	schema, hash, err := ir.NormalizeAndHash(input)
	if err != nil {
		return preparedSchema{}, err
	}
	return preparedSchema{schema: schema, hash: hash}, nil
}
