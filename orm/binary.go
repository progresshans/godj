package orm

import (
	"github.com/progresshans/godj/binaryvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type binaryField[M any] struct{ field[M] }
type BinaryField[M any] struct{ binaryField[M] }
type NullableBinaryField[M any] struct{ binaryField[M] }

func NewBinaryField[M any](metadata ir.Field) BinaryField[M] {
	return BinaryField[M]{binaryField[M]{newField[M](metadata, query.FieldBinary, ir.FieldBinary, false)}}
}
func NewNullableBinaryField[M any](metadata ir.Field) NullableBinaryField[M] {
	return NullableBinaryField[M]{binaryField[M]{newField[M](metadata, query.FieldBinary, ir.FieldBinary, true)}}
}

func (f binaryField[M]) Exact(value binaryvalue.Value) Predicate[M] {
	return f.predicate(query.LookupExact, query.Binary(value))
}
func (f binaryField[M]) GreaterThan(value binaryvalue.Value) Predicate[M] {
	return f.predicate(query.LookupGreaterThan, query.Binary(value))
}
func (f binaryField[M]) GreaterThanOrEqual(value binaryvalue.Value) Predicate[M] {
	return f.predicate(query.LookupGreaterThanOrEqual, query.Binary(value))
}
func (f binaryField[M]) LessThan(value binaryvalue.Value) Predicate[M] {
	return f.predicate(query.LookupLessThan, query.Binary(value))
}
func (f binaryField[M]) LessThanOrEqual(value binaryvalue.Value) Predicate[M] {
	return f.predicate(query.LookupLessThanOrEqual, query.Binary(value))
}
func (f binaryField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f binaryField[M]) Asc() Ordering[M]                        { return f.ordering(query.Ascending) }
func (f binaryField[M]) Desc() Ordering[M]                       { return f.ordering(query.Descending) }
func (f binaryField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f binaryField[M]) referenceField(M, binaryvalue.Value) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f binaryField[M]) ExactField(right FieldReference[M, binaryvalue.Value]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}
func (f binaryField[M]) GreaterThanField(right FieldReference[M, binaryvalue.Value]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThan, right.reference, right.err)
}
func (f binaryField[M]) GreaterThanOrEqualField(right FieldReference[M, binaryvalue.Value]) Predicate[M] {
	return f.fieldPredicate(query.LookupGreaterThanOrEqual, right.reference, right.err)
}
func (f binaryField[M]) LessThanField(right FieldReference[M, binaryvalue.Value]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThan, right.reference, right.err)
}
func (f binaryField[M]) LessThanOrEqualField(right FieldReference[M, binaryvalue.Value]) Predicate[M] {
	return f.fieldPredicate(query.LookupLessThanOrEqual, right.reference, right.err)
}

// BinaryScanner accepts a database BLOB/bytea or an owned binary value. Text
// is rejected, even if it happens to contain base64 or valid UTF-8.
type BinaryScanner struct{ Binary binaryvalue.Value }

func (scanner *BinaryScanner) Scan(raw any) error {
	if scanner == nil {
		return binaryvalue.ErrInvalid
	}
	value, err := scanBinary(raw)
	scanner.Binary = value
	return err
}

type NullableBinaryScanner struct {
	Binary binaryvalue.Value
	Valid  bool
}

func (scanner *NullableBinaryScanner) Scan(raw any) error {
	if scanner == nil {
		return binaryvalue.ErrInvalid
	}
	scanner.Binary, scanner.Valid = binaryvalue.Value{}, false
	if raw == nil {
		return nil
	}
	value, err := scanBinary(raw)
	if err != nil {
		return err
	}
	scanner.Binary, scanner.Valid = value, true
	return nil
}
func scanBinary(raw any) (binaryvalue.Value, error) {
	switch value := raw.(type) {
	case binaryvalue.Value:
		return value.Canonical()
	case []byte:
		return binaryvalue.FromBytes(value)
	}
	return binaryvalue.Value{}, binaryvalue.ErrInvalid
}

func (f BinaryField[M]) scalarResultField(M, binaryvalue.Value) (query.ResultExpression, func() scalarCell[binaryvalue.Value], error) {
	return query.FieldResult(f.reference), func() scalarCell[binaryvalue.Value] {
		var value BinaryScanner
		return scalarCell[binaryvalue.Value]{destination: &value, value: func() binaryvalue.Value { return value.Binary }}
	}, f.err
}
func (f NullableBinaryField[M]) scalarResultField(M, *binaryvalue.Value) (query.ResultExpression, func() scalarCell[*binaryvalue.Value], error) {
	return query.FieldResult(f.reference), nullableBinaryResultCell, f.err
}

func nullableBinaryResultCell() scalarCell[*binaryvalue.Value] {
	var value NullableBinaryScanner
	return scalarCell[*binaryvalue.Value]{destination: &value, value: func() *binaryvalue.Value {
		if !value.Valid {
			return nil
		}
		copy := value.Binary
		return &copy
	}}
}
func (f binaryField[M]) scalarOrderedField(M, binaryvalue.Value) (query.FieldRef, func() scalarCell[Optional[binaryvalue.Value]], error) {
	return f.reference, func() scalarCell[Optional[binaryvalue.Value]] {
		var value NullableBinaryScanner
		return scalarCell[Optional[binaryvalue.Value]]{destination: &value, value: func() Optional[binaryvalue.Value] {
			return Optional[binaryvalue.Value]{value: value.Binary, valid: value.Valid}
		}}
	}, f.err
}

func (f binaryField[M]) In(values ...binaryvalue.Value) Predicate[M] {
	return membershipPredicate(f.field, values, query.Binary)
}
