package postgres

import (
	"strconv"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/query"
)

func temporalResultSQL(kind query.FieldKind, statement string) (string, error) {
	var valid, sqlType, diagnostic string
	var err error
	switch kind {
	case query.FieldDateTime:
		valid, err = queryplan.ScalarSQL("isfinite(", statement, ") AND ", statement,
			" >= TIMESTAMPTZ '0001-01-01 00:00:00+00' AND ", statement, " < TIMESTAMPTZ '10000-01-01 00:00:00+00'")
		sqlType, diagnostic = "timestamp with time zone", "godj_invalid_datetime_value"
	case query.FieldDuration:
		valid, err = queryplan.ScalarSQL("isfinite(", statement, ") AND EXTRACT(YEAR FROM ", statement,
			") = 0 AND EXTRACT(MONTH FROM ", statement, ") = 0 AND ", statement,
			" >= INTERVAL '", strconv.Itoa(int(duration.MinDays)), " days' AND ", statement,
			" < INTERVAL '", strconv.Itoa(int(duration.MaxDays)+1), " days'")
		sqlType, diagnostic = "interval", "godj_invalid_duration_operand"
	case query.FieldDate:
		valid, err = queryplan.ScalarSQL("isfinite(", statement, ") AND ", statement,
			" >= DATE '0001-01-01' AND ", statement, " < DATE '10000-01-01'")
		sqlType, diagnostic = "date", "godj_invalid_date_value"
	case query.FieldTime:
		valid, err = queryplan.ScalarSQL(statement, " >= TIME '00:00:00' AND ", statement, " < TIME '24:00:00'")
		sqlType, diagnostic = "time without time zone", "godj_invalid_time_value"
	default:
		return "", invalidPlan("temporal value guard requires a calendar, clock, instant or duration")
	}
	if err != nil {
		return "", err
	}
	return guardedScalarSQL(statement, valid, sqlType, diagnostic)
}

func temporalScalarResultSQL(kind query.FieldKind, statement string) (string, error) {
	checked, err := temporalResultSQL(kind, `"godj_temporal_result"."value"`)
	if err != nil {
		return "", err
	}
	return queryplan.ScalarSQL("(SELECT ", checked, " FROM (SELECT ", statement,
		` AS "value" OFFSET 0) AS "godj_temporal_result")`)
}

func compilePostgresTemporalBinary(expression query.ScalarExpression, left, right string) (string, error) {
	operator, leftOperand, rightOperand, valid := expression.Binary()
	if !valid {
		return "", invalidPlan("temporal compiler requires a binary node")
	}
	const l, r = `"godj_temporal_operands"."left"`, `"godj_temporal_operands"."right"`
	operation := "(" + l + " " + string(operator) + " " + r + ")"
	if expression.ResultKind() == query.FieldDateTime {
		instant, elapsed := l, r
		if leftOperand.ResultKind() == query.FieldDuration && rightOperand.ResultKind() == query.FieldDateTime {
			instant, elapsed = r, l
		}
		// Model Duration is elapsed time. Native interval day arithmetic on a
		// timestamptz follows the session timezone's DST calendar; do it on a
		// UTC timestamp and convert back so checked-out sessions cannot alter it.
		operation = "((" + instant + " AT TIME ZONE 'UTC') " + string(operator) + " " + elapsed + ") AT TIME ZONE 'UTC'"
	}
	sqlType := "interval"
	if expression.ResultKind() == query.FieldDateTime {
		sqlType = "timestamp with time zone"
	}
	// Unlike native strict arithmetic, the NULL arm consumes both checked
	// operands. Text is used only to force evaluation across their different
	// physical types; NULLIF yields NULL, never an encoded temporal value.
	consume := "(COALESCE(" + l + "::text, '') || COALESCE(" + r + "::text, ''))"
	return queryplan.ScalarSQL("(SELECT CASE WHEN ", l, " IS NULL OR ", r,
		" IS NULL THEN NULLIF(", consume, ", ", consume, ")::", sqlType,
		" ELSE (", operation, ") END FROM (SELECT ", left, ` AS "left", `, right,
		` AS "right" OFFSET 0) AS "godj_temporal_operands")`)
}
