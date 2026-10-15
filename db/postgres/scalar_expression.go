package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileReadScalar(expression query.ScalarExpression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	if err := source.Plan.ValidateScalar(expression); err != nil {
		return "", err
	}
	return queryplan.CompileScalar(expression, expression.ResultKind(), arguments, queryplan.ScalarDialect{
		ParameterLimit: postgresBulkParameters, Value: postgresValue, Placeholder: placeholder,
		Literal: readScalarLiteral, Binary: compilePostgresBinary, Expression: wrapReadScalar,
		Predicate: func(value query.Expression, arguments *[]any) (string, error) {
			return compileScalarPredicate(value, source, arguments)
		},
		Field: func(field query.FieldRef) (string, error) {
			column, err := source.ResolveField(field, quoteIdentifier, quoteQualified)
			if err != nil {
				return "", err
			}
			return readScalarField(field, column)
		},
		Related: func(path query.RelationPath) (string, error) {
			column, err := source.ResolveRelated(path, quoteQualified)
			if err != nil {
				return "", err
			}
			return readScalarField(path.Terminal(), column)
		},
	})
}

func readScalarField(field query.FieldRef, column string) (string, error) {
	if field.Kind() == query.FieldDecimal || field.Kind() == query.FieldDuration {
		return numericAggregateOperand(field, column)
	}
	return column, nil
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
