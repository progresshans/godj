package queryplan

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// CompileEmptyInput applies the same statically empty contract as EmptyRows
// when the result is an input inside a larger statement. Callers first compile
// the complete original plan for metadata, capability and resource validation.
// This prevents a folded-empty producer's unused arithmetic from being
// evaluated by a native planner, without losing its aggregate row cardinality.
func CompileEmptyInput(shape query.ResultShape, dialect ScalarDialect) (string, []any, error) {
	single := shape.Kind() == query.ResultAggregate
	if !single && shape.Kind() != query.ResultProjection && (shape.Kind() != query.ResultGrouped || shape.GroupMode() != query.GroupRows) {
		return "", nil, invalidPlan("empty input requires explicit scalar rows")
	}
	expressions := shape.Expressions()
	if len(expressions) == 0 {
		return "", nil, invalidPlan("empty input has no scalar columns")
	}
	dialect.Field = func(query.FieldRef) (string, error) { return "", invalidPlan("empty input cannot resolve a field") }
	var statement strings.Builder
	var arguments []any
	statement.WriteString("SELECT ")
	for index, expression := range expressions {
		if index > 0 {
			statement.WriteString(", ")
		}
		value, err := query.NullExpression(expression.ResultValueKind())
		if err != nil {
			return "", nil, err
		}
		if single && (expression.Kind() == query.ResultCountAll || expression.Kind() == query.ResultCount) {
			value, err = query.LiteralExpression(query.Integer(0))
			if err != nil {
				return "", nil, err
			}
		}
		rendered, err := CompileScalar(value, value.ResultKind(), &arguments, dialect)
		if err != nil {
			return "", nil, err
		}
		if dialect.EmptyValue != nil {
			rendered, err = dialect.EmptyValue(expression, rendered)
			if err != nil {
				return "", nil, err
			}
		}
		statement.WriteString(rendered + ` AS "c` + strconv.Itoa(index) + `"`)
	}
	if !single {
		statement.WriteString(" WHERE 0 = 1")
	}
	return statement.String(), arguments, nil
}
