package postgres

import (
	"strconv"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/query"
)

// Check the global result domain before a later expression or predicate can
// hide an invalid intermediate value. NUMERIC keeps its native display scale;
// significant digits and adjusted exponent describe the canonical Go value.
func decimalResultSQL(statement string) (string, error) {
	valid, err := queryplan.ScalarSQL(
		"abs(", statement, ") < '1e", strconv.Itoa(decimal.MaxAdjustedExponent+1), "'::numeric AND (",
		statement, " = 0 OR abs(", statement, ") >= '1e-", strconv.Itoa(decimal.MaxAdjustedExponent), "'::numeric) AND ",
		"length(ltrim(rtrim(replace(", statement, "::text, '.', ''), '0'), '-0')) <= ", strconv.Itoa(decimal.MaxDigits))
	if err != nil {
		return "", err
	}
	return guardedScalarSQL(statement, valid, "numeric", "godj_invalid_decimal_result")
}

func guardedScalarSQL(statement, valid, kind, diagnostic string) (string, error) {
	return queryplan.ScalarSQL("CASE WHEN ", statement, " IS NULL OR (", valid, ") THEN ", statement,
		" ELSE ('", diagnostic, "' || left(", statement, "::text, 0))::", kind, " END")
}

// Bind a scalar's computed value before checking its domain. Repeating the
// entire child SQL at every guard would grow exponentially with AST depth.
// OFFSET 0 preserves this evaluation boundary through PostgreSQL pull-up.
// Aggregates use decimalResultSQL directly: moving an aggregate over literal
// operands into a new SELECT would change which query level owns its rows.
func decimalScalarResultSQL(statement string) (string, error) {
	checked, err := decimalResultSQL(`"godj_decimal_result"."value"`)
	if err != nil {
		return "", err
	}
	return queryplan.ScalarSQL("(SELECT ", checked, " FROM (SELECT ", statement,
		` AS "value" OFFSET 0) AS "godj_decimal_result")`)
}

func compilePostgresBinary(expression query.ScalarExpression, left, right string) (string, error) {
	operator, leftOperand, rightOperand, valid := expression.Binary()
	if !valid {
		return "", invalidPlan("binary scalar compiler requires a binary node")
	}
	if expression.ResultKind() == query.FieldDateTime || expression.ResultKind() == query.FieldDuration ||
		leftOperand.ResultKind() == query.FieldDuration || rightOperand.ResultKind() == query.FieldDuration {
		return compilePostgresTemporalBinary(expression, left, right)
	}
	if expression.ResultKind() != query.FieldDecimal {
		return queryplan.ScalarSQL("(", left, " ", string(operator), " ", right, ")")
	}
	// Both operands are validated even when the other is NULL. A strict native
	// operator with a known NULL argument could otherwise be folded to NULL,
	// hiding a corrupt source or an invalid computed intermediate. NULLIF
	// consumes the remaining non-NULL operand without changing NULL semantics.
	const l, r = `"godj_decimal_operands"."left"`, `"godj_decimal_operands"."right"`
	return queryplan.ScalarSQL("(SELECT CASE WHEN ", l, " IS NULL OR ", r,
		" IS NULL THEN NULLIF(COALESCE(", l, ", ", r, "), COALESCE(", l, ", ", r,
		")) ELSE (", l, " ", string(operator), " ", r, ") END FROM (SELECT ",
		left, ` AS "left", `, right, ` AS "right" OFFSET 0) AS "godj_decimal_operands")`)
}

func wrapReadScalar(expression query.ScalarExpression, statement string) (string, error) {
	if expression.RequiresDurationFraction() {
		if _, selected := expression.ResultReference(); selected {
			pair, err := postgresDurationFractionPairSQL(statement)
			if err != nil {
				return "", err
			}
			return postgresDurationFractionKeySQL(pair)
		}
		return statement, nil // Arithmetic and chosen CASE arms produce checked keys.
	}
	if expression.ResultKind() == query.FieldDateTime || expression.ResultKind() == query.FieldDuration ||
		expression.ResultKind() == query.FieldDate || expression.ResultKind() == query.FieldTime {
		if _, selected := expression.ResultReference(); selected {
			return temporalResultSQL(expression.ResultKind(), statement)
		}
		if expression.Kind() == query.ScalarBinary || expression.Kind() == query.ScalarNegate {
			return temporalScalarResultSQL(expression.ResultKind(), statement)
		}
		return statement, nil // Fields, literals and chosen CASE branches own their checks.
	}
	if expression.ResultKind() != query.FieldDecimal {
		return statement, nil
	}
	if reference, selected := expression.ResultReference(); selected {
		producer, _ := reference.Expression()
		if producer.Kind() == query.ResultField {
			field, _ := producer.Field()
			return numericAggregateOperand(field, statement)
		}
		return decimalResultSQL(statement)
	}
	switch expression.Kind() {
	case query.ScalarBinary, query.ScalarNegate:
		return decimalScalarResultSQL(statement)
	default:
		return statement, nil // Source fields, literals and CASE branches are validated by their own renderers.
	}
}
