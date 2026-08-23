package orm

import "github.com/progresshans/godj/query"

// ReferenceField is the sealed, model- and value-specific capability accepted
// by F. StringField and NullableStringField intentionally share string as their
// comparison value type even though model reads use string and *string.
type ReferenceField[M, V any] interface {
	referenceField(M, V) (query.FieldRef, error)
}

// FieldReference is one immutable same-model field operand. Its private state
// prevents callers from forging a typed reference without going through F.
type FieldReference[M, V any] struct {
	reference query.FieldRef
	err       error
	marker    [0]func(M, V)
}

// F converts one generated typed scalar field into a field-reference operand.
// It performs no database I/O and preserves constructor errors for terminal
// preflight.
func F[M, V any](field ReferenceField[M, V]) FieldReference[M, V] {
	if interfaceIsNil(field) {
		return FieldReference[M, V]{err: &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeInvalidPlan,
			Detail:   "field reference source is nil",
		}}
	}
	var model M
	var value V
	reference, err := field.referenceField(model, value)
	return FieldReference[M, V]{reference: reference, err: err}
}

func (f IntegerField[M]) referenceField(M, int64) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f StringField[M]) referenceField(M, string) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f NullableStringField[M]) referenceField(M, string) (query.FieldRef, error) {
	return f.reference, f.err
}

func (f IntegerField[M]) ExactField(right FieldReference[M, int64]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupExact, right.reference, right.err)
}

func (f IntegerField[M]) GreaterThanField(right FieldReference[M, int64]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}

func (f IntegerField[M]) GreaterThanOrEqualField(right FieldReference[M, int64]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}

func (f IntegerField[M]) LessThanField(right FieldReference[M, int64]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}

func (f IntegerField[M]) LessThanOrEqualField(right FieldReference[M, int64]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

func (f StringField[M]) ExactField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupExact, right.reference, right.err)
}

func (f StringField[M]) GreaterThanField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}

func (f StringField[M]) GreaterThanOrEqualField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}

func (f StringField[M]) LessThanField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}

func (f StringField[M]) LessThanOrEqualField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

func (f NullableStringField[M]) ExactField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupExact, right.reference, right.err)
}

func (f NullableStringField[M]) GreaterThanField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}

func (f NullableStringField[M]) GreaterThanOrEqualField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}

func (f NullableStringField[M]) LessThanField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}

func (f NullableStringField[M]) LessThanOrEqualField(right FieldReference[M, string]) Predicate[M] {
	return f.field.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}
