package postgres

import (
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
)

// These inline expressions share durationcalc's ordered continued-fraction key.
// NUMERIC owns integer coefficients and convergents; no period, denominator or
// coefficient crosses Float or bigint. int8send only encodes a byte length.
// Inputs are bound once, so nesting expressions does not duplicate their SQL or
// parameters. No connection-local or persistent database helper is installed.

func postgresInvertBytes(statement string) string {
	return "pg_catalog.decode(pg_catalog.translate(pg_catalog.encode(" + statement +
		", 'hex'), '0123456789abcdef', 'fedcba9876543210'), 'hex')"
}

// postgresDurationFractionKeySQL accepts a NUMERIC[numerator, denominator].
// The pair is internal query state, with integer components and nonzero divisor.
// Normalize the sign and validate the unrounded model range before encoding.
func postgresDurationFractionKeySQL(pair string) (string, error) {
	const prefix = `(WITH RECURSIVE
godj_fraction_encode_input AS MATERIALIZED (SELECT `
	const suffix = ` AS value),
godj_fraction_encode_parts AS MATERIALIZED (
 SELECT value, value[1] * pg_catalog.sign(value[2]) AS n,
        pg_catalog.abs(value[2]) AS d
 FROM godj_fraction_encode_input
),
godj_fraction_encode_checked AS MATERIALIZED (
 SELECT CASE WHEN value IS NULL THEN NULL::numeric[]
 WHEN pg_catalog.array_ndims(value) = 1 AND pg_catalog.array_lower(value, 1) = 1
  AND pg_catalog.cardinality(value) = 2
  AND n IS NOT NULL AND d IS NOT NULL AND d > 0
  AND pg_catalog.abs(n) < 'Infinity'::numeric AND d < 'Infinity'::numeric
  AND n = pg_catalog.trunc(n) AND d = pg_catalog.trunc(d)
  AND n >= -999999999::numeric * 86400000000 * d
  AND n <= 86399999999999999999::numeric * d
 THEN ARRAY[n,d]
 ELSE ARRAY[EXTRACT(EPOCH FROM
   ('godj_invalid_duration_operand' || pg_catalog.left(value::text,0))::interval),1::numeric]
 END AS value
 FROM godj_fraction_encode_parts
),
godj_fraction_encode_walk(n,d,odd,data) AS (
 SELECT pg_catalog.abs(value[1]),value[2],false,''::bytea
 FROM godj_fraction_encode_checked
 UNION ALL
 SELECT walk.d,operation.remainder,NOT walk.odd,
  walk.data || CASE WHEN walk.odd THEN {{invert_segment}} ELSE segment.value END ||
  CASE WHEN operation.remainder = 0
   THEN pg_catalog.decode(CASE WHEN walk.odd THEN 'ff' ELSE '00' END,'hex')
   ELSE ''::bytea END
 FROM godj_fraction_encode_walk AS walk
 CROSS JOIN LATERAL (
  SELECT CASE WHEN walk.d <> 0 THEN pg_catalog.div(walk.n,walk.d) ELSE 0::numeric END AS quotient,
         CASE WHEN walk.d <> 0 THEN pg_catalog.mod(walk.n,walk.d) ELSE 0::numeric END AS remainder OFFSET 0
 ) AS operation
 CROSS JOIN LATERAL (
  SELECT CASE WHEN operation.quotient = 0 THEN ''
   ELSE pg_catalog.trunc(operation.quotient,0)::text END AS value OFFSET 0
 ) AS digits
 CROSS JOIN LATERAL (
  SELECT pg_catalog.decode('80','hex') ||
   pg_catalog.int8send(pg_catalog.octet_length(digits.value)::bigint) ||
   pg_catalog.convert_to(digits.value,'UTF8') AS value OFFSET 0
 ) AS segment
 WHERE walk.d <> 0
)
SELECT CASE WHEN checked.value IS NULL THEN NULL::bytea
 WHEN checked.value[1] = 0 THEN pg_catalog.decode('4744520101','hex')
 ELSE pg_catalog.decode(CASE WHEN checked.value[1] < 0 THEN '4744520100' ELSE '4744520102' END,'hex') ||
  CASE WHEN checked.value[1] < 0 THEN {{invert_payload}} ELSE finished.data END
 END
FROM godj_fraction_encode_checked AS checked
LEFT JOIN LATERAL (
 SELECT data FROM godj_fraction_encode_walk WHERE d = 0
) AS finished ON true)`
	end := strings.NewReplacer(
		"{{invert_segment}}", postgresInvertBytes("segment.value"),
		"{{invert_payload}}", postgresInvertBytes("finished.data"),
	).Replace(suffix)
	return queryplan.ScalarSQL(prefix, pair, end)
}

// postgresDurationFractionPairSQL checks and decodes the query key to a reduced
// NUMERIC pair. The length prefix is uint64; its high four bytes must be zero
// because a PostgreSQL bytea cannot contain a 2^32-byte coefficient. Low bytes
// are accumulated in bigint, checked against the actual input length, and only
// then used as substring/get_byte indexes. Bad lengths never reach those calls.
func postgresDurationFractionPairSQL(key string) (string, error) {
	const prefix = `(WITH RECURSIVE
godj_fraction_decode_input AS MATERIALIZED (SELECT `
	const suffix = ` AS value),
godj_fraction_decode_header AS MATERIALIZED (
 SELECT value,
  CASE WHEN pg_catalog.octet_length(value) >= 5 THEN pg_catalog.get_byte(value,4) ELSE -1 END AS sign,
  pg_catalog.substring(value,1,4) = pg_catalog.decode('47445201','hex') AS valid
 FROM godj_fraction_decode_input
),
godj_fraction_decode_data AS MATERIALIZED (
 SELECT value,sign,valid,
  CASE WHEN sign = 0 THEN {{invert_payload}}
   ELSE pg_catalog.substring(value,6) END AS data
 FROM godj_fraction_decode_header
),
godj_fraction_decode_walk(step,pos,odd,p0,p1,q0,q1,done,valid) AS (
 SELECT 0,0,false,0::numeric,1::numeric,1::numeric,0::numeric,false,
  COALESCE(valid AND sign IN (0,2) AND pg_catalog.octet_length(data) > 0,false)
 FROM godj_fraction_decode_data
 UNION ALL
 SELECT walk.step+1,following.pos,NOT walk.odd,
  walk.p1,digits.value*walk.p1+walk.p0,
  walk.q1,digits.value*walk.q1+walk.q0,
  following.byte = CASE WHEN walk.odd THEN 255 ELSE 0 END,
  COALESCE(segment_length.valid AND digits.value IS NOT NULL AND
   CASE WHEN following.byte = CASE WHEN walk.odd THEN 255 ELSE 0 END
    THEN following.pos = pg_catalog.octet_length(input.data)-1
     AND (walk.step = 0 OR digits.value > 1)
     AND digits.value*walk.p1+walk.p0 > 0
    ELSE true END,false)
 FROM godj_fraction_decode_walk AS walk
 CROSS JOIN godj_fraction_decode_data AS input
 CROSS JOIN LATERAL (
  SELECT CASE WHEN walk.odd THEN 255 ELSE 0 END AS mask OFFSET 0
 ) AS direction
 CROSS JOIN LATERAL (
  SELECT CASE WHEN pg_catalog.octet_length(input.data)-walk.pos >= 10 THEN
   (pg_catalog.get_byte(input.data,walk.pos) # direction.mask) = 128 AND
   (pg_catalog.get_byte(input.data,walk.pos+1) # direction.mask) = 0 AND
   (pg_catalog.get_byte(input.data,walk.pos+2) # direction.mask) = 0 AND
   (pg_catalog.get_byte(input.data,walk.pos+3) # direction.mask) = 0 AND
   (pg_catalog.get_byte(input.data,walk.pos+4) # direction.mask) = 0
   ELSE false END AS valid,
  CASE WHEN pg_catalog.octet_length(input.data)-walk.pos >= 10 THEN
   ((pg_catalog.get_byte(input.data,walk.pos+5) # direction.mask)::bigint << 24) +
   ((pg_catalog.get_byte(input.data,walk.pos+6) # direction.mask)::bigint << 16) +
   ((pg_catalog.get_byte(input.data,walk.pos+7) # direction.mask)::bigint << 8) +
   (pg_catalog.get_byte(input.data,walk.pos+8) # direction.mask)::bigint
   ELSE -1::bigint END AS size OFFSET 0
 ) AS header
 CROSS JOIN LATERAL (
  SELECT header.valid AND header.size >= 0 AND
   header.size <= pg_catalog.octet_length(input.data)-walk.pos-10 AS valid OFFSET 0
 ) AS segment_length
 CROSS JOIN LATERAL (
  SELECT CASE WHEN segment_length.valid THEN
   pg_catalog.substring(input.data,walk.pos+10,header.size::integer)
   ELSE NULL::bytea END AS value OFFSET 0
 ) AS magnitude
 CROSS JOIN LATERAL (
  SELECT CASE WHEN walk.odd THEN {{invert_magnitude}} ELSE magnitude.value END AS value OFFSET 0
 ) AS magnitude_digits
 CROSS JOIN LATERAL (
  SELECT CASE WHEN segment_length.valid AND header.size = 0 AND walk.step = 0 THEN 0::numeric
   WHEN pg_catalog.encode(magnitude_digits.value,'hex') ~ '^3[1-9](3[0-9])*$'
   THEN pg_catalog.convert_from(magnitude_digits.value,'UTF8')::numeric
   ELSE NULL::numeric END AS value OFFSET 0
 ) AS digits
 CROSS JOIN LATERAL (
  SELECT CASE WHEN segment_length.valid THEN (walk.pos+9+header.size)::integer ELSE walk.pos END AS pos,
   CASE WHEN segment_length.valid THEN pg_catalog.get_byte(input.data,(walk.pos+9+header.size)::integer)
   ELSE -1 END AS byte OFFSET 0
 ) AS following
 WHERE walk.valid AND NOT walk.done
)
SELECT CASE WHEN input.value IS NULL THEN NULL::numeric[]
 WHEN input.valid AND input.sign = 1 AND pg_catalog.octet_length(input.value) = 5
 THEN ARRAY[0::numeric,1::numeric]
 WHEN parsed.valid AND parsed.done AND parsed.q1 > 0
  AND (CASE WHEN input.sign = 0 THEN -parsed.p1 ELSE parsed.p1 END) >= -999999999::numeric * 86400000000 * parsed.q1
  AND (CASE WHEN input.sign = 0 THEN -parsed.p1 ELSE parsed.p1 END) <= 86399999999999999999::numeric * parsed.q1
 THEN ARRAY[CASE WHEN input.sign = 0 THEN -parsed.p1 ELSE parsed.p1 END,parsed.q1]
 ELSE ARRAY[EXTRACT(EPOCH FROM
   ('godj_invalid_duration_operand' || pg_catalog.left(input.value::text,0))::interval),1::numeric]
 END
FROM godj_fraction_decode_data AS input
CROSS JOIN LATERAL (
 SELECT * FROM godj_fraction_decode_walk ORDER BY step DESC LIMIT 1
) AS parsed)`
	end := strings.NewReplacer(
		"{{invert_payload}}", postgresInvertBytes("pg_catalog.substring(value,6)"),
		"{{invert_magnitude}}", postgresInvertBytes("magnitude.value"),
	).Replace(suffix)
	return queryplan.ScalarSQL(prefix, key, end)
}
