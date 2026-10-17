package codegen

import "github.com/progresshans/godj/schema/ir"

func validateManyToManyProject(schemas []ir.Schema) error {
	for _, schema := range schemas {
		for _, model := range schema.Models {
			if len(model.ManyToMany) != 0 {
				_, err := ir.ResolveManyToMany(schemas...)
				return err
			}
		}
	}
	return nil
}
