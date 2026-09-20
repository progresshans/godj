package orm

import (
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/uuid"
)

type uuidField[M any] struct{ field[M] }
type UUIDField[M any] struct{ uuidField[M] }
type NullableUUIDField[M any] struct{ uuidField[M] }

func NewUUIDField[M any](metadata ir.Field) UUIDField[M] {
	return UUIDField[M]{uuidField[M]{newField[M](metadata, query.FieldUUID, ir.FieldUUID, false)}}
}
func NewNullableUUIDField[M any](metadata ir.Field) NullableUUIDField[M] {
	return NullableUUIDField[M]{uuidField[M]{newField[M](metadata, query.FieldUUID, ir.FieldUUID, true)}}
}

func (f uuidField[M]) Exact(value uuid.UUID) Predicate[M] {
	return f.predicate(query.LookupExact, query.UUID(value))
}
func (f uuidField[M]) GreaterThan(value uuid.UUID) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.UUID(value))
}
func (f uuidField[M]) GreaterThanOrEqual(value uuid.UUID) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.UUID(value))
}
func (f uuidField[M]) LessThan(value uuid.UUID) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.UUID(value))
}
func (f uuidField[M]) LessThanOrEqual(value uuid.UUID) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.UUID(value))
}
func (f uuidField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f uuidField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f uuidField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f uuidField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f uuidField[M]) referenceField(M, uuid.UUID) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f uuidField[M]) ExactField(right FieldReference[M, uuid.UUID]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f uuidField[M]) GreaterThanField(right FieldReference[M, uuid.UUID]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f uuidField[M]) GreaterThanOrEqualField(right FieldReference[M, uuid.UUID]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f uuidField[M]) LessThanField(right FieldReference[M, uuid.UUID]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f uuidField[M]) LessThanOrEqualField(right FieldReference[M, uuid.UUID]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// UUIDScanner accepts canonical SQLite hex text or a native backend's
// exact UUID value. Other storage classes and noncanonical text are rejected.
type UUIDScanner struct{ UUID uuid.UUID }

func (scanner *UUIDScanner) Scan(raw any) error {
	if scanner == nil {
		return uuid.ErrInvalid
	}
	value, err := scanUUID(raw)
	scanner.UUID = value
	return err
}

type NullableUUIDScanner struct {
	UUID  uuid.UUID
	Valid bool
}

func (scanner *NullableUUIDScanner) Scan(raw any) error {
	if scanner == nil {
		return uuid.ErrInvalid
	}
	scanner.UUID, scanner.Valid = uuid.UUID{}, false
	if raw == nil {
		return nil
	}
	value, err := scanUUID(raw)
	if err != nil {
		return err
	}
	scanner.UUID, scanner.Valid = value, true
	return nil
}
func scanUUID(raw any) (uuid.UUID, error) {
	switch value := raw.(type) {
	case uuid.UUID:
		return value, nil
	case string:
		parsed, err := uuid.Parse(value)
		if err == nil && len(value) == 32 && parsed.Hex() == value {
			return parsed, nil
		}
	}
	return uuid.UUID{}, uuid.ErrInvalid
}

func (f UUIDField[M]) scalarResultField(M, uuid.UUID) (query.FieldRef, func() scalarCell[uuid.UUID], error) {
	return f.reference, func() scalarCell[uuid.UUID] {
		var value UUIDScanner
		return scalarCell[uuid.UUID]{destination: &value, value: func() uuid.UUID { return value.UUID }}
	}, f.err
}
func (f NullableUUIDField[M]) scalarResultField(M, *uuid.UUID) (query.FieldRef, func() scalarCell[*uuid.UUID], error) {
	return f.reference, func() scalarCell[*uuid.UUID] {
		var value NullableUUIDScanner
		return scalarCell[*uuid.UUID]{destination: &value, value: func() *uuid.UUID {
			if !value.Valid {
				return nil
			}
			copy := value.UUID
			return &copy
		}}
	}, f.err
}
func (f uuidField[M]) scalarOrderedField(M, uuid.UUID) (query.FieldRef, func() scalarCell[Optional[uuid.UUID]], error) {
	return f.reference, func() scalarCell[Optional[uuid.UUID]] {
		var value NullableUUIDScanner
		return scalarCell[Optional[uuid.UUID]]{destination: &value, value: func() Optional[uuid.UUID] { return Optional[uuid.UUID]{value: value.UUID, valid: value.Valid} }}
	}, f.err
}

func (f uuidField[M]) In(values ...uuid.UUID) Predicate[M] {
	return membershipPredicate(f.field, values, query.UUID)
}
