package serializers

import "github.com/progresshans/godj/schema/ir"

func modelCollections(model ir.Model, stored map[string]ir.Field) (map[string]ir.ManyToManyField, error) {
	result := make(map[string]ir.ManyToManyField, len(model.ManyToMany))
	for _, field := range model.ManyToMany {
		_, duplicate := result[field.Name]
		_, scalar := stored[field.Name]
		if duplicate || scalar {
			return nil, invalidConfig("model."+field.Name, "duplicate model field")
		}
		if field.Target.AppLabel == "" || field.Target.ModelName == "" || (field.Symmetry != ir.ManyToManyDirected && field.Symmetry != ir.ManyToManySymmetrical) {
			return nil, invalidConfig("model."+field.Name, "invalid collection metadata")
		}
		result[field.Name] = field.Clone()
	}
	return result, nil
}
