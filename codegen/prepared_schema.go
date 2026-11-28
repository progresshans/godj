package codegen

import "github.com/progresshans/godj/schema/ir"

// preparedSchema belongs to one generation. Renderers share its owned schema
// and canonical digest read-only; no caller input or global cache is retained.
type preparedSchema struct {
	schema  ir.Schema
	storage ir.Schema
	hash    string
}

func prepareSchema(input ir.Schema) (preparedSchema, error) {
	schema, hash, err := ir.NormalizeAndHash(input)
	if err != nil {
		return preparedSchema{}, err
	}
	storage := schema
	for _, model := range schema.Models {
		for _, field := range model.ManyToMany {
			if field.Through == nil {
				storage, err = ir.StorageSchema(schema)
				if err != nil {
					return preparedSchema{}, err
				}
				return preparedSchema{schema: schema, storage: storage, hash: hash}, nil
			}
		}
	}
	return preparedSchema{schema: schema, storage: storage, hash: hash}, nil
}
