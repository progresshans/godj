package ir

// FieldChangeKind separates supported field deltas from their backend-specific
// storage effects. Complete field normalization remains the caller's responsibility.
type FieldChangeKind uint8

const (
	ChangeChoices FieldChangeKind = iota + 1
	ChangeDecimalPrecision
)

// ClassifyFieldChange requires a real change to exactly one supported facet.
// Identity, default, nullability, relation and every other facet stay identical.
// The arguments and their nested metadata remain owned by their callers.
func ClassifyFieldChange(before, after Field) (FieldChangeKind, error) {
	if err := ValidateChoices(before); err != nil {
		return 0, err
	}
	if err := ValidateChoices(after); err != nil {
		return 0, err
	}
	if before.Equal(after) {
		return 0, validation("field", "unchanged", "AlterField requires a changed field")
	}
	previous, next := before, after
	previous.Choices, next.Choices = nil, nil
	if previous.Equal(next) {
		return ChangeChoices, nil
	}
	if before.Kind == FieldDecimal && after.Kind == FieldDecimal &&
		before.Decimal != nil && after.Decimal != nil && before.Decimal.Valid() && after.Decimal.Valid() {
		previous, next = before, after
		previous.Decimal, next.Decimal = nil, nil
		if previous.Equal(next) {
			return ChangeDecimalPrecision, nil
		}
	}
	return 0, validation("field", "unsupported_change", "AlterField supports choices-only or Decimal precision-only changes")
}
