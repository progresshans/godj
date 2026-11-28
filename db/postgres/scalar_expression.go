package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func compileReadScalar(expression query.ScalarExpression, source queryplan.ScalarSource, arguments *[]any) (string, error) {
	if err := source.Validate(expression); err != nil {
		return "", err
	}
	return queryplan.CompileScalar(expression, expression.ResultKind(), arguments, queryplan.ScalarDialect{
		ParameterLimit: postgresBulkParameters, Value: postgresValue, Placeholder: placeholder,
		Literal: readScalarLiteral, Binary: compilePostgresBinary, Negate: compilePostgresNegate, Function: compilePostgresFunction, CaseArm: compilePostgresCaseArm, Expression: wrapReadScalar,
		Annotation: source.Annotation, Selected: source.Selected, Reference: source.Reference,
		Predicate: func(value query.Expression, arguments *[]any) (string, error) {
			nested := source
			nested.Exists = nil
			return compileScalarPredicate(value, nested, arguments)
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
	if field.Kind() == query.FieldDecimal {
		return numericAggregateOperand(field, column)
	}
	if field.Kind() == query.FieldDateTime || field.Kind() == query.FieldDuration || field.Kind() == query.FieldDate || field.Kind() == query.FieldTime {
		return temporalResultSQL(field.Kind(), column)
	}
	return column, nil
}

func compileAssignedScalar(field query.FieldRef, expression query.ScalarExpression, statement string) (string, error) {
	if expression.RequiresDurationFraction() {
		var err error
		statement, err = postgresStoreFractionSQL(statement)
		if err != nil {
			return "", err
		}
	}
	if field.Kind() == query.FieldDateTime || field.Kind() == query.FieldDuration || field.Kind() == query.FieldDate || field.Kind() == query.FieldTime {
		return temporalScalarResultSQL(field.Kind(), statement)
	}
	return readScalarField(field, statement)
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
