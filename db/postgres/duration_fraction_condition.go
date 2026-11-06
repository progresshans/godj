package postgres

import (
	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/query"
)

func postgresIntervalKeySQL(statement string) (string, error) {
	pair, err := postgresIntervalFractionSQL(statement)
	if err != nil {
		return "", err
	}
	return postgresDurationFractionKeySQL(pair)
}

func postgresComparisonValue(condition query.Condition, leftSQL, rightSQL string) (string, string, error) {
	left, _ := condition.Scalar()
	right, scalarRight := condition.RHSScalar()
	if !left.RequiresDurationFraction() && !right.RequiresDurationFraction() {
		return leftSQL, rightSQL, nil
	}
	var err error
	if !left.RequiresDurationFraction() {
		leftSQL, err = postgresIntervalKeySQL(leftSQL)
		if err != nil {
			return "", "", err
		}
	}
	if !right.RequiresDurationFraction() {
		if !scalarRight {
			rightSQL, err = queryplan.ScalarSQL("CAST(", rightSQL, " AS interval)")
			if err != nil {
				return "", "", err
			}
		}
		rightSQL, err = postgresIntervalKeySQL(rightSQL)
	}
	return leftSQL, rightSQL, err
}

func postgresGroupComparisonLiteral(expression query.ResultExpression, statement string) (string, error) {
	if !expression.RequiresDurationFraction() {
		return statement, nil
	}
	statement, err := queryplan.ScalarSQL("CAST(", statement, " AS interval)")
	if err != nil {
		return "", err
	}
	return postgresIntervalKeySQL(statement)
}

func postgresEmptyResultValue(expression query.ResultExpression, statement string) (string, error) {
	if !expression.RequiresDurationFraction() {
		return statement, nil
	}
	// The common empty-source compiler supplies a typed INTERVAL NULL. Its
	// result column must retain the producer's BYTEA type for later stages.
	return postgresIntervalKeySQL(statement)
}
