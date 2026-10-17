package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

// A NUMERIC pair is private execution state. It may exceed the Duration domain
// while accumulating or computing a dimensionless ratio; only actual Duration
// nodes go through the range-checked ordered key encoder.
func postgresReducedFractionSQL(pair string) (string, error) {
	return queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::numeric[]
 ELSE ARRAY[pg_catalog.div(value[1]*pg_catalog.sign(value[2]),divisor),
            pg_catalog.div(pg_catalog.abs(value[2]),divisor)] END
 FROM (SELECT `, pair, ` AS value OFFSET 0) AS godj_fraction_reduce_input
 CROSS JOIN LATERAL (SELECT pg_catalog.gcd(value[1],value[2]) AS divisor OFFSET 0) AS godj_fraction_reduce_gcd)`)
}

func postgresIntervalFractionSQL(statement string) (string, error) {
	checked, err := temporalScalarResultSQL(query.FieldDuration, statement)
	if err != nil {
		return "", err
	}
	return queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::numeric[]
 ELSE ARRAY[EXTRACT(EPOCH FROM value)*1000000,1::numeric] END
 FROM (SELECT `, checked, ` AS value OFFSET 0) AS godj_interval_fraction)`)
}

func postgresDurationOperandSQL(expression query.ScalarExpression, statement string) (string, error) {
	if expression.RequiresDurationFraction() {
		return postgresDurationFractionPairSQL(statement)
	}
	return postgresIntervalFractionSQL(statement)
}

func postgresNumberFractionSQL(kind query.FieldKind, statement string) (string, error) {
	switch kind {
	case query.FieldInteger:
		return queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::numeric[]
 ELSE ARRAY[value::numeric,1::numeric] END
 FROM (SELECT `, statement, ` AS value OFFSET 0) AS godj_integer_fraction)`)
	case query.FieldDecimal:
		return queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::numeric[]
 ELSE ARRAY[pg_catalog.trunc(value*denominator,0),denominator] END
 FROM (SELECT `, statement, ` AS value OFFSET 0) AS godj_decimal_fraction
 CROSS JOIN LATERAL (SELECT ('1e'||pg_catalog.scale(value)::text)::numeric AS denominator OFFSET 0) AS scale)`)
	case query.FieldFloat:
		// float8send is the actual IEEE binary64 in network byte order. A
		// decimal cast first would silently replace e.g. binary64 0.1 by 1/10.
		return queryplan.ScalarSQL(`(SELECT CASE WHEN input.value IS NULL THEN NULL::numeric[]
 WHEN bits.exponent < 2047 THEN ARRAY[
  CASE WHEN pg_catalog.get_byte(raw.value,0) >= 128 THEN -1::numeric ELSE 1::numeric END *
   significand.value * pg_catalog.power(2::numeric,GREATEST(binary_power.value,0)),
   pg_catalog.power(2::numeric,GREATEST(-binary_power.value,0))]
 ELSE ARRAY[('godj_invalid_float_operand'||pg_catalog.left(input.value::text,0))::numeric,1::numeric] END
 FROM (SELECT `, statement, ` AS value OFFSET 0) AS input
 CROSS JOIN LATERAL (SELECT pg_catalog.float8send(input.value) AS value OFFSET 0) AS raw
 CROSS JOIN LATERAL (
  SELECT (pg_catalog.get_byte(raw.value,0)&127)*16+(pg_catalog.get_byte(raw.value,1)>>4) AS exponent,
   (pg_catalog.get_byte(raw.value,1)&15)::numeric*281474976710656 +
   pg_catalog.get_byte(raw.value,2)::numeric*1099511627776 +
   pg_catalog.get_byte(raw.value,3)::numeric*4294967296 +
   pg_catalog.get_byte(raw.value,4)::numeric*16777216 +
   pg_catalog.get_byte(raw.value,5)::numeric*65536 +
   pg_catalog.get_byte(raw.value,6)::numeric*256 + pg_catalog.get_byte(raw.value,7)::numeric AS fraction OFFSET 0
 ) AS bits
 CROSS JOIN LATERAL (SELECT bits.fraction + CASE WHEN bits.exponent = 0 THEN 0::numeric ELSE 4503599627370496::numeric END AS value OFFSET 0) AS significand
 CROSS JOIN LATERAL (SELECT CASE WHEN bits.exponent = 0 THEN -1074 ELSE bits.exponent-1075 END AS value OFFSET 0) AS binary_power)`)
	default:
		return "", invalidPlan("duration factor requires an integer, Float or Decimal")
	}
}

// Integer quotient and remainder implement half-even without NUMERIC division
// scale, floating-point epochs, or premature rounding of either operand.
func postgresRoundFractionSQL(pair string) (string, error) {
	return queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::numeric
 ELSE whole + CASE WHEN 2*pg_catalog.abs(remainder) > value[2] OR
  (2*pg_catalog.abs(remainder) = value[2] AND pg_catalog.mod(pg_catalog.abs(whole),2) = 1)
 THEN pg_catalog.sign(value[1]) ELSE 0::numeric END END
 FROM (SELECT `, pair, ` AS value OFFSET 0) AS godj_fraction_round_input
 CROSS JOIN LATERAL (SELECT pg_catalog.div(value[1],value[2]) AS whole,
  pg_catalog.mod(value[1],value[2]) AS remainder OFFSET 0) AS godj_fraction_round_parts)`)
}

// Split at a day boundary before INTERVAL's parser: full model microseconds
// exceed int64, whereas both the day count and subday microseconds fit.
func postgresMicrosecondsIntervalSQL(statement string) (string, error) {
	return queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::interval
 ELSE (pg_catalog.trunc(days,0)::text||' days '||
       pg_catalog.trunc(value-days*86400000000,0)::text||' microseconds')::interval END
 FROM (SELECT `, statement, ` AS value OFFSET 0) AS godj_interval_integer
 CROSS JOIN LATERAL (SELECT pg_catalog.div(value,86400000000) -
  CASE WHEN value < 0 AND pg_catalog.mod(value,86400000000) <> 0 THEN 1 ELSE 0 END AS days OFFSET 0) AS godj_interval_days)`)
}

func postgresStoreFractionSQL(statement string) (string, error) {
	pair, err := postgresDurationFractionPairSQL(statement)
	if err != nil {
		return "", err
	}
	whole, err := postgresRoundFractionSQL(pair)
	if err != nil {
		return "", err
	}
	return postgresMicrosecondsIntervalSQL(whole)
}

// Compute the rounded binary64 significand with exact cross multiplication.
// The bounded binary search avoids logarithms and works for fractions much
// smaller than any fixed decimal scale. The final text is the exact decimal
// value of a representable binary64, so its cast cannot round a second time.
func postgresFractionFloatSQL(pair string) (string, error) {
	return queryplan.ScalarSQL(`(WITH RECURSIVE
 godj_fraction_float_input AS MATERIALIZED (SELECT `, pair, ` AS value),
 godj_fraction_float_parts AS MATERIALIZED (
  SELECT value,pg_catalog.abs(value[1]) AS n,value[2] AS d FROM godj_fraction_float_input
 ),
 godj_fraction_float_exponent(low,high) AS (
  SELECT -1075,1024
  UNION ALL
  SELECT CASE WHEN enough.value THEN midpoint.value ELSE walk.low END,
         CASE WHEN enough.value THEN walk.high ELSE midpoint.value END
  FROM godj_fraction_float_exponent AS walk CROSS JOIN godj_fraction_float_parts AS input
  CROSS JOIN LATERAL (SELECT walk.low+(walk.high-walk.low)/2 AS value OFFSET 0) AS midpoint
  CROSS JOIN LATERAL (SELECT CASE WHEN midpoint.value >= 0
   THEN input.n >= input.d*pg_catalog.power(2::numeric,midpoint.value)
   ELSE input.n*pg_catalog.power(2::numeric,-midpoint.value) >= input.d END AS value OFFSET 0) AS enough
  WHERE walk.high-walk.low > 1
 ),
 godj_fraction_float_scaled AS MATERIALIZED (
  SELECT input.*,GREATEST(exponent.low,-1022)-52 AS binary_power,
   input.n*pg_catalog.power(2::numeric,GREATEST(52-GREATEST(exponent.low,-1022),0)) AS numerator,
   input.d*pg_catalog.power(2::numeric,GREATEST(GREATEST(exponent.low,-1022)-52,0)) AS denominator
  FROM godj_fraction_float_parts AS input
  CROSS JOIN (SELECT low FROM godj_fraction_float_exponent WHERE high-low = 1) AS exponent
 ),
 godj_fraction_float_rounded AS MATERIALIZED (
  SELECT input.*,quotient.value + CASE WHEN remainder.value*2 > input.denominator OR
   (remainder.value*2 = input.denominator AND pg_catalog.mod(quotient.value,2) = 1)
   THEN 1 ELSE 0 END AS significand
  FROM godj_fraction_float_scaled AS input
  CROSS JOIN LATERAL (SELECT pg_catalog.div(input.numerator,input.denominator) AS value OFFSET 0) AS quotient
  CROSS JOIN LATERAL (SELECT pg_catalog.mod(input.numerator,input.denominator) AS value OFFSET 0) AS remainder
 )
 SELECT CASE WHEN value IS NULL THEN NULL::double precision
 WHEN significand = 0 THEN CASE WHEN value[1] < 0 THEN '-0'::double precision ELSE 0::double precision END
 ELSE (CASE WHEN value[1] < 0 THEN '-' ELSE '' END ||
  pg_catalog.trunc(significand*pg_catalog.power(CASE WHEN binary_power < 0 THEN 5::numeric ELSE 2::numeric END,
   pg_catalog.abs(binary_power)),0)::text ||
  CASE WHEN binary_power < 0 THEN 'e'||binary_power::text ELSE '' END)::double precision END
 FROM godj_fraction_float_rounded)`)
}

func compilePostgresFractionBinary(expression query.ScalarExpression, left, right string) (string, error) {
	operator, leftOperand, rightOperand, ok := expression.Binary()
	if !ok {
		return "", invalidPlan("duration fraction compiler requires a binary node")
	}
	if expression.ResultKind() == query.FieldDateTime {
		return compilePostgresFractionShift(operator, leftOperand, rightOperand, left, right)
	}
	operand := func(value query.ScalarExpression, statement string) (string, error) {
		if value.ResultKind() == query.FieldDuration {
			return postgresDurationOperandSQL(value, statement)
		}
		return postgresNumberFractionSQL(value.ResultKind(), statement)
	}
	l, err := operand(leftOperand, left)
	if err != nil {
		return "", err
	}
	r, err := operand(rightOperand, right)
	if err != nil {
		return "", err
	}
	var operation string
	switch operator {
	case query.ArithmeticAdd, query.ArithmeticSubtract:
		operation = "ARRAY[l[1]*r[2]" + string(operator) + "r[1]*l[2],l[2]*r[2]]"
	case query.ArithmeticMultiply:
		operation = "ARRAY[l[1]*r[1],l[2]*r[2]]"
	case query.ArithmeticDivide:
		// Preserve the native zero-divisor error, after both operands have
		// been checked and the ordinary SQL NULL case has been handled.
		operation = "CASE WHEN r[1] = 0 THEN ARRAY[1::numeric/r[1],1::numeric] ELSE ARRAY[l[1]*r[2],l[2]*r[1]] END"
	default:
		return "", invalidPlan("duration fraction has an unsupported arithmetic operator")
	}
	pair, err := queryplan.ScalarSQL(`(SELECT CASE WHEN l IS NULL OR r IS NULL THEN
 NULLIF(COALESCE(l::text,'')||COALESCE(r::text,''),COALESCE(l::text,'')||COALESCE(r::text,''))::numeric[]
 ELSE `, operation, ` END FROM (SELECT `, l, ` AS l, `, r, ` AS r OFFSET 0) AS godj_fraction_operands)`)
	if err != nil {
		return "", err
	}
	pair, err = postgresReducedFractionSQL(pair)
	if err != nil {
		return "", err
	}
	if expression.ResultKind() == query.FieldFloat {
		return postgresFractionFloatSQL(pair)
	}
	return postgresDurationFractionKeySQL(pair)
}

func compilePostgresFractionShift(operator query.ArithmeticOperator, leftOperand, rightOperand query.ScalarExpression, left, right string) (string, error) {
	instant, period, periodOperand := left, right, rightOperand
	if leftOperand.ResultKind() == query.FieldDuration {
		instant, period, periodOperand = right, left, leftOperand
	}
	pair, err := postgresDurationOperandSQL(periodOperand, period)
	if err != nil {
		return "", err
	}
	sum, err := queryplan.ScalarSQL(`(SELECT CASE WHEN instant IS NULL OR period IS NULL THEN
 NULLIF(COALESCE(instant::text,'')||COALESCE(period::text,''),COALESCE(instant::text,'')||COALESCE(period::text,''))::numeric[]
 ELSE ARRAY[(EXTRACT(EPOCH FROM instant)*1000000)*period[2] `, string(operator), ` period[1],period[2]] END
 FROM (SELECT `, instant, ` AS instant, `, pair, ` AS period OFFSET 0) AS godj_fraction_shift)`)
	if err != nil {
		return "", err
	}
	whole, err := postgresRoundFractionSQL(sum)
	if err != nil {
		return "", err
	}
	interval, err := postgresMicrosecondsIntervalSQL(whole)
	if err != nil {
		return "", err
	}
	return queryplan.ScalarSQL("((TIMESTAMP '1970-01-01 00:00:00' + ", interval, ") AT TIME ZONE 'UTC')")
}

func compilePostgresNegate(expression query.ScalarExpression, statement string) (string, error) {
	if !expression.RequiresDurationFraction() {
		return queryplan.ScalarSQL("(-", statement, ")")
	}
	pair, err := postgresDurationFractionPairSQL(statement)
	if err != nil {
		return "", err
	}
	pair, err = queryplan.ScalarSQL(`(SELECT CASE WHEN value IS NULL THEN NULL::numeric[] ELSE ARRAY[-value[1],value[2]] END
 FROM (SELECT `, pair, ` AS value OFFSET 0) AS godj_fraction_negate)`)
	if err != nil {
		return "", err
	}
	return postgresDurationFractionKeySQL(pair)
}

func compilePostgresCaseArm(parent, child query.ScalarExpression, statement string) (string, error) {
	if !parent.RequiresDurationFraction() || child.RequiresDurationFraction() {
		return statement, nil
	}
	pair, err := postgresIntervalFractionSQL(statement)
	if err != nil {
		return "", err
	}
	return postgresDurationFractionKeySQL(pair)
}
