package queryplan

import (
	"slices"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// DerivedDialect leaves physical scalar values, aggregate codecs, ordering
// keys and pagination to the backend. SQL structure and reference ownership
// are shared. Aggregate returns how often its operand/filter arguments occur.
type DerivedDialect struct {
	Scalar     ScalarDialect
	Aggregate  func(query.ResultExpression, string, string) (string, int, error)
	OrderValue func(query.FieldKind, string) string
	Pagination func(*strings.Builder, *[]any, query.Plan) error
}

// SourceTable retains the physical root used by backend error classification.
// A derived plan has no model table of its own and must not fabricate one.
func SourceTable(plan query.Plan) string {
	for {
		if input, _, page := plan.PageInput(); page {
			plan = input
			continue
		}
		input, _, derived := plan.DerivedSource()
		if !derived {
			return plan.Table()
		}
		plan = input
	}
}

// A CTE is visible in its own body. Do not shadow an input table or an inner
// CTE, including SQLite's case-insensitive identifiers. Conservatively reserving
// occurrences in quoted text also works without parsing dialect-specific SQL.
func unusedSQLIdentifier(prefix, input string) string {
	used := make(map[string]bool)
	input = strings.ToLower(input)
	for offset := 0; offset < len(input); {
		index := strings.Index(input[offset:], prefix)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(prefix)
		if end < len(input) && input[end] == '_' {
			last := end + 1
			for last < len(input) && input[last] >= '0' && input[last] <= '9' {
				last++
			}
			if last > end+1 {
				end = last
			}
		}
		used[input[start:end]] = true
		offset = end
	}
	name := prefix
	for index := 0; used[name]; index++ {
		name = prefix + "_" + strconv.Itoa(index)
	}
	return name
}

// CompileDerived names the input's actual scalar cells using a CTE column
// list, which both backends support. The input precedes new expressions in SQL
// and argument order, including SQLite's positional question-mark parameters.
func CompileDerived(plan query.Plan, input string, inputArguments []any, dialect DerivedDialect) (string, []any, error) {
	if err := plan.ValidateDerived(); err != nil {
		return "", nil, err
	}
	_, scope, present := plan.DerivedSource()
	if !present || input == "" || len(input) > 4<<20 || dialect.Scalar.ParameterLimit <= 0 ||
		len(inputArguments) > dialect.Scalar.ParameterLimit || dialect.Scalar.Value == nil || dialect.Scalar.Placeholder == nil ||
		dialect.Aggregate == nil || dialect.Pagination == nil {
		return "", nil, invalidPlan("derived compiler has an invalid input or dialect")
	}
	alias := unusedSQLIdentifier("godj_results", input)
	arguments := slices.Clone(inputArguments)
	parameter := func(value query.Value) (string, error) {
		if len(arguments) >= dialect.Scalar.ParameterLimit {
			return "", invalidPlan("derived query exceeds its parameter budget")
		}
		argument, err := dialect.Scalar.Value(value)
		if err != nil {
			return "", err
		}
		arguments = append(arguments, argument)
		return dialect.Scalar.Placeholder(len(arguments)), nil
	}
	scalarDialect := dialect.Scalar
	scalarDialect.Field = func(query.FieldRef) (string, error) {
		return "", invalidPlan("derived scalar cannot resolve a model field")
	}
	scalarDialect.Selected = func(ref query.ResultRef) (string, error) {
		index, valid := ref.Index()
		if !valid || !ref.Scope().Equal(scope) {
			return "", invalidPlan("derived scalar belongs to a different input")
		}
		return `"` + alias + `"."c` + strconv.Itoa(index) + `"`, nil
	}
	var scalar func(query.ScalarExpression) (string, error)
	var predicate func(query.Expression) (string, error)
	scalar = func(value query.ScalarExpression) (string, error) {
		if err := value.ValidateResultSource(scope); err != nil {
			return "", err
		}
		return CompileScalar(value, value.ResultKind(), &arguments, scalarDialect)
	}
	predicate = func(value query.Expression) (string, error) {
		var statement strings.Builder
		if err := appendDerivedPredicate(&statement, value, scalar, parameter); err != nil {
			return "", err
		}
		return statement.String(), nil
	}
	scalarDialect.Predicate = func(value query.Expression, _ *[]any) (string, error) { return predicate(value) }
	resultValue := func(value query.ResultExpression) (string, error) {
		if !value.IsAggregate() {
			operand, present := value.Scalar()
			if !present {
				return "", invalidPlan("derived result requires a scalar value")
			}
			return scalar(operand)
		}
		start := len(arguments)
		operand, filter := "", ""
		if value.Kind() != query.ResultCountAll {
			selected, present := value.Scalar()
			if !present {
				return "", invalidPlan("derived aggregate requires a scalar operand")
			}
			var err error
			operand, err = scalar(selected)
			if err != nil {
				return "", err
			}
		}
		if where, present := value.Filter(); present {
			condition, err := predicate(where)
			if err != nil {
				return "", err
			}
			filter = " FILTER (WHERE " + condition + ")"
		}
		statement, copies, err := dialect.Aggregate(value, operand, filter)
		if err != nil {
			return "", err
		}
		if copies < 1 || copies > 2 {
			return "", invalidPlan("aggregate compiler returned an invalid argument traversal")
		}
		if copies == 2 {
			arguments = append(arguments, arguments[start:]...)
		}
		return statement, nil
	}
	shape, selected := plan.ResultShape(), plan.ResultShape().Expressions()
	if _, err := HiddenOrderings(plan, selected); err != nil {
		return "", nil, err
	}
	var statement strings.Builder
	statement.WriteString(`WITH "` + alias + `" (`)
	appendGroupColumns(&statement, scope.ColumnCount(), "")
	statement.WriteString(") AS (" + input + ") SELECT ")
	if plan.Distinct() {
		statement.WriteString("DISTINCT ")
	}
	for index, value := range selected {
		if index > 0 {
			statement.WriteString(", ")
		}
		rendered, err := resultValue(value)
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(rendered + ` AS "c` + strconv.Itoa(index) + `"`)
	}
	statement.WriteString(` FROM "` + alias + `"`)
	if where, present := plan.Where(); present {
		condition, err := predicate(where)
		if err != nil {
			return "", nil, err
		}
		statement.WriteString(" WHERE " + condition)
	}
	if keys := shape.GroupKeys(); len(keys) != 0 {
		statement.WriteString(" GROUP BY ")
		for index := range keys {
			if index > 0 {
				statement.WriteString(", ")
			}
			statement.WriteString(strconv.Itoa(index + 1))
		}
	}
	var output string
	if shape.Kind() == query.ResultGrouped {
		var err error
		output, err = FinishGroups(statement.String(), plan, parameter, func(sql *strings.Builder) error {
			return dialect.Pagination(sql, &arguments, plan)
		})
		if err != nil {
			return "", nil, err
		}
	} else {
		for index, order := range plan.Orderings() {
			if index == 0 {
				statement.WriteString(" ORDER BY ")
			} else {
				statement.WriteString(", ")
			}
			value := order.Expression()
			if selectedIndex := ExpressionIndex(selected, value); selectedIndex >= 0 && value.ResultValueKind() != query.FieldJSON {
				statement.WriteString(strconv.Itoa(selectedIndex + 1))
			} else {
				rendered, err := resultValue(value)
				if err != nil {
					return "", nil, err
				}
				if dialect.OrderValue != nil {
					rendered = dialect.OrderValue(value.ResultValueKind(), rendered)
				}
				statement.WriteString(rendered)
			}
			AppendOrderDirection(&statement, order.Direction(), order.Nulls())
		}
		if err := dialect.Pagination(&statement, &arguments, plan); err != nil {
			return "", nil, err
		}
		output = statement.String()
	}
	if len(arguments) > dialect.Scalar.ParameterLimit || len(output) > 4<<20 {
		return "", nil, invalidPlan("derived query exceeds its SQL or parameter budget")
	}
	return output, arguments, nil
}

func appendDerivedPredicate(statement *strings.Builder, expression query.Expression,
	render func(query.ScalarExpression) (string, error), parameter func(query.Value) (string, error)) error {
	statement.WriteByte('(')
	if condition, leaf := expression.Condition(); leaf {
		if err := AppendScalarCondition(statement, condition, render, parameter); err != nil {
			return err
		}
	} else {
		children := expression.Children()
		switch expression.Kind() {
		case query.ExpressionNot:
			if len(children) != 1 {
				return invalidPlan("derived NOT requires one predicate")
			}
			statement.WriteString("NOT ")
			if err := appendDerivedPredicate(statement, children[0], render, parameter); err != nil {
				return err
			}
		case query.ExpressionAnd, query.ExpressionOr:
			if len(children) < 2 {
				return invalidPlan("derived connector requires multiple predicates")
			}
			for index, child := range children {
				if index > 0 {
					if expression.Kind() == query.ExpressionAnd {
						statement.WriteString(" AND ")
					} else {
						statement.WriteString(" OR ")
					}
				}
				if err := appendDerivedPredicate(statement, child, render, parameter); err != nil {
					return err
				}
			}
		default:
			return invalidPlan("derived predicate is uninitialized")
		}
	}
	statement.WriteByte(')')
	return nil
}
