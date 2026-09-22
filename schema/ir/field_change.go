package ir

// FieldChangeKind separates supported field deltas from their backend-specific
// storage effects. Complete field normalization remains the caller's responsibility.
type FieldChangeKind uint8

const (
	ChangeChoices FieldChangeKind = iota + 1
	ChangeDecimalPrecision
	ChangeUnique
	ChangeRelation
)

// ClassifyFieldChange requires a real change to exactly one supported facet.
// Relation changes may change cardinality, reverse namespace, delete policy and
// the associated uniqueness together, while preserving the FK target.
// Identity, default, nullability and every other facet stay identical.
// The arguments and their nested metadata remain owned by their callers.
func ClassifyFieldChange(before, after Field) (FieldChangeKind, error) {
	for _, field := range []Field{before, after} {
		if field.Relation != nil && (!field.Relation.OnDelete.Valid() || field.Relation.OnDelete == DeleteSetNull && !field.Nullable) {
			return 0, validation("field.relation.on_delete", "invalid_relation", "delete policy must be recognized and SET_NULL requires a nullable field")
		}
		if field.Relation != nil && field.Relation.Cardinality == RelationOneToOne && !field.Unique {
			return 0, validation("field.unique", "invalid_relation", "normalized one-to-one fields require column uniqueness")
		}
	}
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
	previous, next = before, after
	previous.Unique, next.Unique = false, false
	if !before.PrimaryKey && previous.Equal(next) {
		return ChangeUnique, nil
	}
	if before.Kind == FieldForeignKey && after.Kind == FieldForeignKey &&
		before.Relation != nil && after.Relation != nil &&
		before.Relation.Cardinality.SingleValued() && after.Relation.Cardinality.SingleValued() &&
		(before.Relation.Cardinality != RelationOneToOne || before.Unique) &&
		(after.Relation.Cardinality != RelationOneToOne || after.Unique) {
		previous, next = before.Clone(), after.Clone()
		previous.Unique, next.Unique = false, false
		previous.Relation.Cardinality, next.Relation.Cardinality = RelationManyToOne, RelationManyToOne
		previous.Relation.Reverse, next.Relation.Reverse = ReverseRelation{}, ReverseRelation{}
		previous.Relation.OnDelete, next.Relation.OnDelete = DeleteProtect, DeleteProtect
		if previous.Equal(next) {
			return ChangeRelation, nil
		}
	}
	if before.Kind == FieldDecimal && after.Kind == FieldDecimal &&
		before.Decimal != nil && after.Decimal != nil && before.Decimal.Valid() && after.Decimal.Valid() {
		previous, next = before, after
		previous.Decimal, next.Decimal = nil, nil
		if previous.Equal(next) {
			return ChangeDecimalPrecision, nil
		}
	}
	return 0, validation("field", "unsupported_change", "AlterField supports choices, uniqueness, relation cardinality/reverse namespace/delete policy or Decimal precision changes")
}
