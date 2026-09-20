package orm

import (
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type jsonField[M any] struct{ field[M] }
type JSONField[M any] struct{ jsonField[M] }
type NullableJSONField[M any] struct{ jsonField[M] }

func NewJSONField[M any](metadata ir.Field) JSONField[M] {
	return JSONField[M]{jsonField[M]{newField[M](metadata, query.FieldJSON, ir.FieldJSON, false)}}
}
func NewNullableJSONField[M any](metadata ir.Field) NullableJSONField[M] {
	return NullableJSONField[M]{jsonField[M]{newField[M](metadata, query.FieldJSON, ir.FieldJSON, true)}}
}

func (f jsonField[M]) Exact(value jsonvalue.Value) Predicate[M] {
	return f.predicate(query.LookupExact, query.JSON(value))
}

// Contains and ContainedBy use the backend's JSON containment capability.
// PostgreSQL implements these predicates; SQLite rejects them explicitly.
func (f jsonField[M]) Contains(value jsonvalue.Value) Predicate[M] {
	return f.predicate(query.LookupContains, query.JSON(value))
}
func (f jsonField[M]) ContainedBy(value jsonvalue.Value) Predicate[M] {
	return f.predicate(query.LookupContainedBy, query.JSON(value))
}
func (f jsonField[M]) IsNull(value bool) Predicate[M] {
	return f.predicate(query.LookupIsNull, query.Boolean(value))
}
func (f jsonField[M]) writableField(M) (query.FieldRef, error) { return f.reference, f.err }
func (f jsonField[M]) referenceField(M, jsonvalue.Value) (query.FieldRef, error) {
	return f.reference, f.err
}
func (f jsonField[M]) ExactField(right FieldReference[M, jsonvalue.Value]) Predicate[M] {
	return f.fieldPredicate(query.LookupExact, right.reference, right.err)
}

// JSONScanner accepts SQLite TEXT or a native backend JSON value. SQL NULL,
// BLOB and invalid JSON fail explicitly; nullable scans preserve JSON null.
type JSONScanner struct{ JSON jsonvalue.Value }

func (scanner *JSONScanner) Scan(raw any) error {
	if scanner == nil {
		return jsonvalue.ErrInvalid
	}
	value, err := scanJSON(raw)
	scanner.JSON = value
	return err
}

type NullableJSONScanner struct {
	JSON  jsonvalue.Value
	Valid bool
}

func (scanner *NullableJSONScanner) Scan(raw any) error {
	if scanner == nil {
		return jsonvalue.ErrInvalid
	}
	scanner.JSON, scanner.Valid = jsonvalue.Value{}, false
	if raw == nil {
		return nil
	}
	value, err := scanJSON(raw)
	if err != nil {
		return err
	}
	scanner.JSON, scanner.Valid = value, true
	return nil
}
func scanJSON(raw any) (jsonvalue.Value, error) {
	switch value := raw.(type) {
	case jsonvalue.Value:
		return value.Canonical()
	case string:
		return jsonvalue.Parse([]byte(value))
	default:
		return jsonvalue.Value{}, jsonvalue.ErrInvalid
	}
}

func (f JSONField[M]) scalarResultField(M, jsonvalue.Value) (query.FieldRef, func() scalarCell[jsonvalue.Value], error) {
	return f.reference, func() scalarCell[jsonvalue.Value] {
		var value JSONScanner
		return scalarCell[jsonvalue.Value]{destination: &value, value: func() jsonvalue.Value { return value.JSON }}
	}, f.err
}
func (f NullableJSONField[M]) scalarResultField(M, *jsonvalue.Value) (query.FieldRef, func() scalarCell[*jsonvalue.Value], error) {
	return f.reference, func() scalarCell[*jsonvalue.Value] {
		var value NullableJSONScanner
		return scalarCell[*jsonvalue.Value]{destination: &value, value: func() *jsonvalue.Value {
			if !value.Valid {
				return nil
			}
			copy := value.JSON
			return &copy
		}}
	}, f.err
}

func (f jsonField[M]) In(values ...jsonvalue.Value) Predicate[M] {
	return membershipPredicate(f.field, values, query.JSON)
}
