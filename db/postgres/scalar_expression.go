package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileReadScalar(expression query.ScalarExpression, alias string, arguments *[]any) (string, error) {
	return compileReadScalarFields(expression, func(field query.FieldRef) (string, error) {
		if alias == "" {
			return quoteIdentifier(field.Column())
		}
		return quoteQualified(alias, field.Column())
	}, arguments)
}

func compileReadScalarFields(expression query.ScalarExpression, resolve func(query.FieldRef) (string, error), arguments *[]any) (string, error) {
	return queryplan.CompileScalar(expression, expression.ResultKind(), arguments, queryplan.ScalarDialect{
		ParameterLimit: postgresBulkParameters, Value: postgresValue, Placeholder: placeholder,
		Literal: readScalarLiteral,
		Predicate: func(value query.Expression, arguments *[]any) (string, error) {
			return compileScalarPredicate(value, resolve, arguments)
		},
		Field: func(field query.FieldRef) (string, error) {
			column, err := resolve(field)
			if err != nil {
				return "", err
			}
			if field.Kind() == query.FieldDecimal || field.Kind() == query.FieldDuration {
				return numericAggregateOperand(field, column)
			}
			return column, nil
		},
	})
}

// Standalone parameters have no source column from which PostgreSQL can infer
// their SQL type. These fixed casts describe the value domain, never a field's
// precision, and cannot be supplied as user SQL.
func readScalarLiteral(value query.Value, expected query.FieldKind, parameter string) (string, error) {
	kind := query.FieldKind(value.Kind())
	if value.IsNull() {
		kind = expected
	}
	var sqlType string
	switch kind {
	case query.FieldInteger:
		sqlType = "bigint"
	case query.FieldFloat:
		sqlType = "double precision"
	case query.FieldDecimal:
		sqlType = "numeric"
	case query.FieldUUID:
		sqlType = "uuid"
	case query.FieldBinary:
		sqlType = "bytea"
	case query.FieldJSON:
		sqlType = "jsonb"
	case query.FieldString:
		sqlType = "text"
	case query.FieldBoolean:
		sqlType = "boolean"
	case query.FieldDateTime:
		sqlType = "timestamp with time zone"
	case query.FieldDate:
		sqlType = "date"
	case query.FieldTime:
		sqlType = "time without time zone"
	case query.FieldDuration:
		sqlType = "interval"
	default:
		return "", invalidPlan("scalar literal has no PostgreSQL result type")
	}
	return "CAST(" + parameter + " AS " + sqlType + ")", nil
}
