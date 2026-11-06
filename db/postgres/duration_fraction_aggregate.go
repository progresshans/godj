package postgres

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

// ARRAY_AGG remains at the original aggregate query level, including when its
// scalar operand contains only constants. The following projection folds its
// complete contributing keys. Moving ARRAY_AGG inside this scalar subquery
// would aggregate one constant row and lose the source's row cardinality.
func postgresFractionAggregateSQL(keys string, average bool) (string, error) {
	pair, err := postgresDurationFractionPairSQL("input.values[walk.step+1]")
	if err != nil {
		return "", err
	}
	denominator := "d"
	if average {
		denominator = "d*count"
	}
	finished, err := queryplan.ScalarSQL(`(WITH RECURSIVE
 godj_fraction_aggregate_input AS MATERIALIZED (SELECT `, keys, ` AS values),
 godj_fraction_aggregate_walk(step,n,d,count) AS (
  SELECT 0,0::numeric,1::numeric,0::bigint
  UNION ALL
  SELECT walk.step+1,pg_catalog.div(combined.n,divisor.value),
   pg_catalog.div(combined.d,divisor.value),walk.count+CASE WHEN operand.value IS NULL THEN 0 ELSE 1 END
  FROM godj_fraction_aggregate_walk AS walk CROSS JOIN godj_fraction_aggregate_input AS input
  CROSS JOIN LATERAL (SELECT `, pair, ` AS value OFFSET 0) AS operand
  CROSS JOIN LATERAL (SELECT CASE WHEN operand.value IS NULL THEN walk.n
   ELSE walk.n*operand.value[2]+operand.value[1]*walk.d END AS n,
   CASE WHEN operand.value IS NULL THEN walk.d ELSE walk.d*operand.value[2] END AS d OFFSET 0) AS combined
  CROSS JOIN LATERAL (SELECT pg_catalog.gcd(combined.n,combined.d) AS value OFFSET 0) AS divisor
  WHERE walk.step < COALESCE(pg_catalog.cardinality(input.values),0)
 )
 SELECT CASE WHEN count = 0 THEN NULL::numeric[] ELSE ARRAY[n,`, denominator, `] END
 FROM godj_fraction_aggregate_walk ORDER BY step DESC LIMIT 1)`)
	if err != nil {
		return "", err
	}
	return postgresDurationFractionKeySQL(finished)
}

func finishPostgresAggregates(base string, expressions []query.ResultExpression) (string, error) {
	needed := false
	for _, expression := range expressions {
		if expression.RequiresDurationFraction() && (expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg) {
			needed = true
			break
		}
	}
	if !needed {
		return base, nil
	}
	columns, values := make([]string, len(expressions)), make([]string, len(expressions))
	for index, expression := range expressions {
		columns[index] = `"c` + strconv.Itoa(index) + `"`
		value := `"godj_fraction_aggregates".` + columns[index]
		if expression.RequiresDurationFraction() && (expression.Kind() == query.ResultSum || expression.Kind() == query.ResultAvg) {
			var err error
			value, err = postgresFractionAggregateSQL(value, expression.Kind() == query.ResultAvg)
			if err != nil {
				return "", err
			}
		}
		values[index] = value + " AS " + columns[index]
	}
	return queryplan.ScalarSQL("SELECT ", strings.Join(values, ", "), " FROM (", base,
		`) AS "godj_fraction_aggregates" (`, strings.Join(columns, ", "), ")")
}

func renderPostgresFractionAggregate(expression query.ResultExpression, operand, filter string) (string, error) {
	if expression.Distinct() {
		operand = "DISTINCT " + operand
	}
	switch expression.Kind() {
	case query.ResultCount:
		return queryplan.ScalarSQL("COUNT(", operand, ")", filter)
	case query.ResultSum, query.ResultAvg:
		return queryplan.ScalarSQL("pg_catalog.array_agg(", operand, ")", filter)
	case query.ResultMin, query.ResultMax:
		// MIN/MAX have no BYTEA overload. C-collated hex has exactly the same
		// byte order as the canonical fraction key, including prefix order.
		function := "MIN"
		if expression.Kind() == query.ResultMax {
			function = "MAX"
		}
		// DISTINCT is redundant for extrema, but retain the requested native
		// aggregate form by placing it outside encode's argument.
		distinct := ""
		if expression.Distinct() {
			distinct, operand = "DISTINCT ", strings.TrimPrefix(operand, "DISTINCT ")
		}
		return queryplan.ScalarSQL("pg_catalog.decode(", function, "(", distinct,
			"pg_catalog.encode(", operand, `,'hex') COLLATE "C")`, filter, ",'hex')")
	default:
		return "", invalidPlan("duration fraction has an unsupported aggregate")
	}
}
