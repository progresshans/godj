package orm

import (
	"github.com/progresshans/godj/calendar"
	"github.com/progresshans/godj/clock"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/uuid"
	"time"

	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func (f integerField[M]) In(values ...int64) Predicate[M] {
	return membershipPredicate(f.field, values, query.Integer)
}

func (f StringField[M]) In(values ...string) Predicate[M] {
	return membershipPredicate(f.field, values, query.String)
}

func (f NullableStringField[M]) In(values ...string) Predicate[M] {
	return membershipPredicate(f.field, values, query.String)
}

func (f BooleanField[M]) In(values ...bool) Predicate[M] {
	return membershipPredicate(f.field, values, query.Boolean)
}

func (f dateTimeField[M]) In(values ...time.Time) Predicate[M] {
	return membershipPredicate(f.field, values, query.DateTime)
}

func membershipPredicate[M, T any](field field[M], values []T, scalar func(T) query.Value) Predicate[M] {
	if field.err != nil {
		return Predicate[M]{err: field.err}
	}
	converted := make([]query.Value, len(values))
	for index, value := range values {
		converted[index] = scalar(value)
	}
	condition, err := query.NewInCondition(field.reference, converted)
	return predicateFromCondition[M](condition, err)
}

// Dynamic lists use the same concrete scalar types as individual lookups.
// A nil interface inside []any is explicit SQL NULL, while a typed nil slice
// is an empty list. Arbitrary iterators, pointer coercion and subqueries are
// not inferred from the caller's value.
func dynamicMembership(field ir.Field, raw any) ([]query.Value, error) {
	switch values := raw.(type) {
	case []int:
		if field.Kind != ir.FieldAuto && field.Kind != ir.FieldInteger {
			break
		}
		return dynamicMembershipValues(field, values)
	case []int64:
		if field.Kind != ir.FieldAuto && field.Kind != ir.FieldInteger {
			break
		}
		return dynamicMembershipValues(field, values)
	case []string:
		if field.Kind != ir.FieldChar && field.Kind != ir.FieldText {
			break
		}
		return dynamicMembershipValues(field, values)
	case []bool:
		if field.Kind != ir.FieldBoolean {
			break
		}
		return dynamicMembershipValues(field, values)
	case []jsonvalue.Value:
		if field.Kind != ir.FieldJSON {
			break
		}
		return dynamicMembershipValues(field, values)
	case []uuid.UUID:
		if field.Kind != ir.FieldUUID {
			break
		}
		return dynamicMembershipValues(field, values)
	case []decimal.Decimal:
		if field.Kind != ir.FieldDecimal {
			break
		}
		return dynamicMembershipValues(field, values)
	case []float64:
		if field.Kind != ir.FieldFloat {
			break
		}
		return dynamicMembershipValues(field, values)
	case []duration.Duration:
		if field.Kind != ir.FieldDuration {
			break
		}
		return dynamicMembershipValues(field, values)
	case []clock.Time:
		if field.Kind != ir.FieldTime {
			break
		}
		return dynamicMembershipValues(field, values)
	case []calendar.Date:
		if field.Kind != ir.FieldDate {
			break
		}
		return dynamicMembershipValues(field, values)
	case []time.Time:
		if field.Kind != ir.FieldDateTime {
			break
		}
		return dynamicMembershipValues(field, values)
	case []any:
		return dynamicMembershipValues(field, values)
	}
	return nil, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue,
		Field: field.Name, Lookup: string(query.LookupIn), Detail: "IN requires a scalar slice matching the field"}
}

func dynamicMembershipValues[T any](field ir.Field, values []T) ([]query.Value, error) {
	result := make([]query.Value, len(values))
	for index, value := range values {
		if any(value) == nil {
			result[index] = query.Null()
			continue
		}
		scalar, err := dynamicValue(field, query.LookupIn, value)
		if err != nil {
			return nil, err
		}
		result[index] = scalar
	}
	return result, nil
}
