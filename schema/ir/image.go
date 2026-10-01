package ir

import (
	"fmt"
	"github.com/progresshans/godj/internal/identifiers"
)

func validateImageField(field Field, path string) error {
	for _, item := range []struct{ key, name string }{{"width_field", field.WidthField}, {"height_field", field.HeightField}} {
		if item.name == "" {
			continue
		}
		if field.Kind != FieldImage {
			return validation(path+"."+item.key, "unsupported", "image dimensions require ImageField")
		}
		if !identifiers.SQL(item.name) {
			return validation(path+"."+item.key, "invalid_identifier", item.name)
		}
	}
	return nil
}

// ImageDimensionOwners returns a newly owned dimension-field -> image-field map.
// A dimension must be an ordinary integer field in the same model. Sharing one
// target between dimensions/images is rejected rather than depending on field
// assignment order. The targets are managed model input, not editable fields.
func ImageDimensionOwners(model Model) (map[string]string, error) {
	return imageDimensionOwners(model, "model")
}
func imageDimensionOwners(model Model, path string) (map[string]string, error) {
	owners := map[string]string{}
	byName := make(map[string]Field, len(model.Fields))
	for _, field := range model.Fields {
		byName[field.Name] = field
	}
	for index, field := range model.Fields {
		fieldPath := fmt.Sprintf("%s.fields[%d]", path, index)
		if err := validateImageField(field, fieldPath); err != nil {
			return nil, err
		}
		for _, item := range []struct{ key, name string }{{"width_field", field.WidthField}, {"height_field", field.HeightField}} {
			if item.name == "" {
				continue
			}
			target, found := byName[item.name]
			if !found || target.Kind != FieldInteger || target.PrimaryKey {
				return nil, validation(fieldPath+"."+item.key, "invalid_dimension_field", "image dimension must refer to an ordinary integer field in the same model")
			}
			if _, exists := owners[item.name]; exists {
				return nil, validation(fieldPath+"."+item.key, "shared_dimension_field", "image dimensions require distinct owners")
			}
			owners[item.name] = field.Name
		}
	}
	return owners, nil
}
