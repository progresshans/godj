package openapi

import "github.com/progresshans/godj/serializers"

// Choice labels are presentation metadata. Only request schemas impose enum:
// model rows and omission defaults can contain values outside current choices.
func choiceAnnotations(field serializers.Field) ([]serializers.Member, error) {
	choices := field.Choices()
	if choices == nil {
		return nil, nil
	}
	descriptions := make([]serializers.Value, len(choices))
	for index, choice := range choices {
		item, err := serializers.NewObject(
			serializers.MemberOf("value", choice.Value),
			serializers.MemberOf("label", serializers.String(choice.Label)),
		)
		if err != nil {
			return nil, schemaConfigError("choices", "choice metadata is invalid")
		}
		descriptions[index] = item.Value()
	}
	labels, err := serializers.NewList(descriptions...)
	if err != nil {
		return nil, err
	}
	return []serializers.Member{serializers.MemberOf("x-godj-choices", labels)}, nil
}

func choiceInputSchema(field serializers.Field) (Schema, error) {
	choices := field.Choices()
	if choices == nil {
		return schemaFieldType(field)
	}
	values := make([]serializers.Value, 0, len(choices)+1)
	hasBlank := false
	for _, choice := range choices {
		values = append(values, choice.Value)
		if value, ok := choice.Value.AsString(); ok && value == "" {
			hasBlank = true
		}
	}
	if field.Kind() == serializers.FieldString && field.AllowEmpty() && !hasBlank {
		values = append(values, serializers.String(""))
	}
	enum, err := serializers.NewList(values...)
	if err != nil {
		return Schema{}, err
	}
	base := String()
	if field.Kind() == serializers.FieldInteger {
		base = Integer()
	}
	base, err = schemaAnnotate(base, serializers.MemberOf("enum", enum))
	if err != nil {
		return Schema{}, err
	}
	// Keep the enum within the non-null branch, which also lets independent
	// generators retain the enum type while representing null separately.
	if field.Nullable() {
		return Nullable(base)
	}
	return base, nil
}

func choiceDefaultName(field serializers.Field, value serializers.Value) string {
	choices := field.Choices()
	if choices == nil || value.IsNull() {
		return "default"
	}
	if text, ok := value.AsString(); ok {
		if text == "" && field.AllowEmpty() {
			return "default"
		}
		for _, choice := range choices {
			if candidate, ok := choice.Value.AsString(); ok && text == candidate {
				return "default"
			}
		}
	}
	if integer, ok := value.AsInteger(); ok {
		for _, choice := range choices {
			if candidate, ok := choice.Value.AsInteger(); ok && integer == candidate {
				return "default"
			}
		}
	}
	// Applying a server omission default is different from submitting it as
	// input. Do not instruct generated clients to send an out-of-enum default.
	return "x-godj-omission-default"
}
