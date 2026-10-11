package queryplan

import (
	"strings"

	"github.com/progresshans/godj/query"
)

// ScalarDialect keeps physical values and numeric execution in the backend.
// The shared traversal owns the immutable grammar and parameter order for
// reads and writes alike. Field must resolve the original source metadata.
type ScalarDialect struct {
	ParameterLimit int
	Field          func(query.FieldRef) (string, error)
	Value          func(query.Value) (any, error)
	Placeholder    func(int) string
	Literal        func(query.Value, query.FieldKind, string) (string, error)
	Expression     func(query.ScalarExpression, string) (string, error)
	Predicate      func(query.Expression, *[]any) (string, error)
}

func CompileScalar(expression query.ScalarExpression, expected query.FieldKind, arguments *[]any, dialect ScalarDialect) (string, error) {
	if err := expression.Validate(); err != nil {
		return "", err
	}
	if arguments == nil || dialect.ParameterLimit <= 0 || len(*arguments) > dialect.ParameterLimit || dialect.Field == nil || dialect.Value == nil || dialect.Placeholder == nil {
		return "", invalidPlan("scalar compiler has an invalid dialect or parameter budget")
	}
	var render func(query.ScalarExpression, query.FieldKind) (string, error)
	render = func(expression query.ScalarExpression, expected query.FieldKind) (string, error) {
		var statement string
		var err error
		if value, ok := expression.Literal(); ok {
			if len(*arguments) >= dialect.ParameterLimit {
				return "", invalidPlan("scalar expression exceeds its parameter budget")
			}
			argument, err := dialect.Value(value)
			if err != nil {
				return "", err
			}
			*arguments = append(*arguments, argument)
			statement = dialect.Placeholder(len(*arguments))
			if dialect.Literal != nil {
				statement, err = dialect.Literal(value, expected, statement)
				if err != nil {
					return "", err
				}
			}
		} else if field, ok := expression.Field(); ok {
			statement, err = dialect.Field(field)
			if err != nil {
				return "", err
			}
		} else if operator, left, right, ok := expression.Binary(); ok {
			leftSQL, err := render(left, expression.ResultKind())
			if err != nil {
				return "", err
			}
			rightSQL, err := render(right, expression.ResultKind())
			if err != nil {
				return "", err
			}
			statement = "(" + leftSQL + " " + string(operator) + " " + rightSQL + ")"
		} else if value, ok := expression.Negated(); ok {
			inner, err := render(value, expression.ResultKind())
			if err != nil {
				return "", err
			}
			statement = "(-" + inner + ")"
		} else if fallback, branches, ok := expression.Case(); ok {
			if dialect.Predicate == nil {
				return "", invalidPlan("scalar compiler does not provide conditional predicates")
			}
			var selected strings.Builder
			selected.WriteString("CASE")
			for _, branch := range branches {
				condition, err := dialect.Predicate(branch.Predicate(), arguments)
				if err != nil {
					return "", err
				}
				value, err := render(branch.Value(), expression.ResultKind())
				if err != nil {
					return "", err
				}
				selected.WriteString(" WHEN " + condition + " THEN " + value)
			}
			otherwise, err := render(fallback, expression.ResultKind())
			if err != nil {
				return "", err
			}
			selected.WriteString(" ELSE " + otherwise + " END")
			statement = "(" + selected.String() + ")"
		} else {
			return "", invalidPlan("scalar expression has an unknown node")
		}
		if dialect.Expression != nil {
			return dialect.Expression(expression, statement)
		}
		return statement, nil
	}
	statement, err := render(expression, expected)
	if err == nil && (len(*arguments) > dialect.ParameterLimit || len(statement) > 4<<20) {
		err = invalidPlan("scalar expression exceeds its SQL or parameter budget")
	}
	return statement, err
}
