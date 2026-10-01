package postgres

import (
	"database/sql"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
)

func postgresValue(value query.Value) (any, error) {
	if document, ok := value.JSON(); ok {
		return postgresJSONValue(document)
	}
	if identifier, ok := value.UUID(); ok {
		return pgtype.UUID{Bytes: identifier.Bytes(), Valid: true}, nil
	}
	if number, ok := value.Decimal(); ok {
		coefficient := number.Coefficient
		if coefficient == "" {
			coefficient = "0"
		}
		integer, valid := new(big.Int).SetString(coefficient, 10)
		if !valid {
			return nil, decimal.ErrInvalid
		}
		return pgtype.Numeric{Int: integer, Exp: number.Exponent, Valid: true}, nil
	}
	if elapsed, ok := value.Duration(); ok {
		return pgtype.Interval{Days: elapsed.Days, Microseconds: elapsed.Microseconds, Valid: true}, nil
	}
	return value.DatabaseValue()
}

type scalarRows struct {
	*sql.Rows
	kinds []query.FieldKind
}

// database/sql exposes native INTERVAL, NUMERIC, UUID and JSONB as driver text. Convert
// only columns of those database types, including scalar results and joins.
func adaptScalarRows(rows *sql.Rows, plan query.Plan) (db.Rows, error) {
	needed := false
	visit := func(fields []query.FieldRef) {
		for _, field := range fields {
			needed = needed || field.Kind() == query.FieldDuration || field.Kind() == query.FieldDecimal || field.Kind() == query.FieldUUID || field.Kind() == query.FieldJSON
		}
	}
	visit(plan.SourceFields())
	for _, projection := range plan.RelationProjections() {
		visit(projection.TargetColumns())
	}
	// DTO expressions can select native values from a related model even
	// when none of the root model's fields require adaptation.
	for _, expression := range plan.ResultShape().Expressions() {
		if field, ok := expression.Field(); ok {
			visit([]query.FieldRef{field})
		}
	}
	if !needed {
		return rows, nil
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	kinds := make([]query.FieldKind, len(columns))
	found := false
	for index, column := range columns {
		switch column.DatabaseTypeName() {
		case "INTERVAL":
			kinds[index] = query.FieldDuration
		case "JSONB":
			kinds[index] = query.FieldJSON
		case "UUID":
			kinds[index] = query.FieldUUID
		case "NUMERIC":
			kinds[index] = query.FieldDecimal
		}
		found = found || kinds[index] != ""
	}
	if !found {
		return rows, nil
	}
	return &scalarRows{Rows: rows, kinds: kinds}, nil
}

func (rows *scalarRows) Scan(destinations ...any) error {
	if len(destinations) != len(rows.kinds) {
		return rows.Rows.Scan(destinations...)
	}
	adapted := append([]any(nil), destinations...)
	for index, kind := range rows.kinds {
		switch kind {
		case query.FieldDuration:
			adapted[index] = durationDestination{destination: destinations[index]}
		case query.FieldJSON:
			adapted[index] = jsonDestination{destination: destinations[index]}
		case query.FieldUUID:
			adapted[index] = uuidDestination{destination: destinations[index]}
		case query.FieldDecimal:
			adapted[index] = decimalDestination{destination: destinations[index]}
		}
	}
	return rows.Rows.Scan(adapted...)
}

type decimalDestination struct{ destination any }

func (target decimalDestination) Scan(raw any) error {
	fail := func(err error) error {
		if scanner, ok := target.destination.(sql.Scanner); ok {
			_ = scanner.Scan(nil)
		}
		if destination, ok := target.destination.(*any); ok && destination != nil {
			*destination = nil
		}
		return err
	}
	var canonical any
	if raw != nil {
		var text string
		switch value := raw.(type) {
		case string:
			text = value
		case []byte:
			text = string(value)
		default:
			return fail(decimal.ErrInvalid)
		}
		value, err := decimal.Parse(text)
		if err != nil {
			return fail(err)
		}
		canonical = value
	}
	switch destination := target.destination.(type) {
	case sql.Scanner:
		return destination.Scan(canonical)
	case *any:
		if destination == nil {
			return decimal.ErrInvalid
		}
		*destination = canonical
		return nil
	default:
		return decimal.ErrInvalid
	}
}
