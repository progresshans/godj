package queryplan

import (
	"strings"

	"github.com/progresshans/godj/query"
)

// ScalarDialect keeps physical values and numeric execution in the backend.
// The shared traversal owns the immutable grammar and parameter order for
// reads and writes alike. Field must resolve the original source metadata.
type ScalarParameterBinder func(query.Value) (string, error)
type FunctionCompiler func(query.ScalarExpression, []string, ScalarParameterBinder) (string, error)

type ScalarDialect struct {
	ParameterLimit int
	Field          func(query.FieldRef) (string, error)
	Related        func(query.RelationPath) (string, error)
	Selected       func(query.ResultRef) (string, error)
	Value          func(query.Value) (any, error)
	Placeholder    func(int) string
	Literal        func(query.Value, query.FieldKind, string) (string, error)
	Binary         func(query.ScalarExpression, string, string) (string, error)
	Negate         func(query.ScalarExpression, string) (string, error)
	Function       FunctionCompiler
	CaseArm        func(query.ScalarExpression, query.ScalarExpression, string) (string, error)
	EmptyValue     func(query.ResultExpression, string) (string, error)
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
	bindParameter := func(value query.Value) (string, error) {
		if len(*arguments) >= dialect.ParameterLimit {
			return "", invalidPlan("scalar expression exceeds its parameter budget")
		}
		argument, err := dialect.Value(value)
		if err != nil {
			return "", err
		}
		*arguments = append(*arguments, argument)
		return dialect.Placeholder(len(*arguments)), nil
	}
	var render func(query.ScalarExpression, query.FieldKind) (string, error)
	render = func(expression query.ScalarExpression, expected query.FieldKind) (string, error) {
		// A parent's output domain is only a fallback for an untyped NULL.
		// DateTime - DateTime, for example, returns Duration without changing
		// either operand's explicit NULL/literal type to INTERVAL.
		if expression.ResultKind() != "" {
			expected = expression.ResultKind()
		}
		var statement string
		var err error
		if value, ok := expression.Literal(); ok {
			statement, err = bindParameter(value)
			if err != nil {
				return "", err
			}
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
		} else if path, ok := expression.RelationPath(); ok {
			if dialect.Related == nil {
				return "", invalidPlan("scalar compiler does not provide related values")
			}
			statement, err = dialect.Related(path)
			if err != nil {
				return "", err
			}
		} else if ref, ok := expression.ResultReference(); ok {
			if dialect.Selected == nil {
				return "", invalidPlan("scalar compiler does not provide selected result columns")
			}
			statement, err = dialect.Selected(ref)
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
			if dialect.Binary != nil {
				statement, err = dialect.Binary(expression, leftSQL, rightSQL)
				if err != nil {
					return "", err
				}
			}
		} else if value, ok := expression.Negated(); ok {
			inner, err := render(value, expression.ResultKind())
			if err != nil {
				return "", err
			}
			statement = "(-" + inner + ")"
			if dialect.Negate != nil {
				statement, err = dialect.Negate(expression, inner)
				if err != nil {
					return "", err
				}
			}
		} else if _, inputs, ok := expression.Function(); ok {
			if dialect.Function == nil {
				return "", invalidPlan("scalar compiler does not provide functions")
			}
			operands := make([]string, len(inputs))
			for index, input := range inputs {
				operands[index], err = render(input, input.ResultKind())
				if err != nil {
					return "", err
				}
			}
			statement, err = dialect.Function(expression, operands, bindParameter)
			if err != nil {
				return "", err
			}
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
				if dialect.CaseArm != nil {
					value, err = dialect.CaseArm(expression, branch.Value(), value)
					if err != nil {
						return "", err
					}
				}
				selected.WriteString(" WHEN " + condition + " THEN " + value)
				if selected.Len() > 4<<20 {
					return "", invalidPlan("scalar expression exceeds its SQL budget")
				}
			}
			otherwise, err := render(fallback, expression.ResultKind())
			if err != nil {
				return "", err
			}
			if dialect.CaseArm != nil {
				otherwise, err = dialect.CaseArm(expression, fallback, otherwise)
				if err != nil {
					return "", err
				}
			}
			selected.WriteString(" ELSE " + otherwise + " END")
			statement = "(" + selected.String() + ")"
		} else {
			return "", invalidPlan("scalar expression has an unknown node")
		}
		if dialect.Expression != nil {
			statement, err = dialect.Expression(expression, statement)
			if err != nil {
				return "", err
			}
		}
		return ScalarSQL(statement)
	}
	statement, err := render(expression, expected)
	if err == nil && (len(*arguments) > dialect.ParameterLimit || len(statement) > 4<<20) {
		err = invalidPlan("scalar expression exceeds its SQL or parameter budget")
	}
	return statement, err
}

// ScalarSQL bounds backend wrappers before repeated operands are copied into
// a larger statement. Intermediate scalar nodes share the final SQL budget.
func ScalarSQL(parts ...string) (string, error) {
	size := 0
	for _, part := range parts {
		if len(part) > (4<<20)-size {
			return "", invalidPlan("scalar expression exceeds its SQL budget")
		}
		size += len(part)
	}
	return strings.Join(parts, ""), nil
}
