package schema

import "github.com/progresshans/godj/schema/ir"

// ChoiceScalar is the current stored-value family for declared choices.
// Defaults support additional scalar kinds independently of this boundary.
type ChoiceScalar interface{ string | int64 }

func Choice[T ChoiceScalar](value T, label string) ir.Choice {
	choice := ir.Choice{Label: label}
	switch value := any(value).(type) {
	case string:
		choice.Value = ir.Scalar{Kind: ir.ScalarString, String: value}
	case int64:
		choice.Value = ir.Scalar{Kind: ir.ScalarInteger, Integer: value}
	}
	return choice
}

// Choices records an owned, ordered selection. An explicitly empty selection
// is invalid; omit this option for an unrestricted field.
func Choices(values ...ir.Choice) FieldOption {
	detached := make([]ir.Choice, len(values))
	copy(detached, values)
	return func(field *Field) {
		field.Choices = make([]ir.Choice, len(detached))
		copy(field.Choices, detached)
	}
}
