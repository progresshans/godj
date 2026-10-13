package orm

import (
	"strings"

	"github.com/progresshans/godj/query"
)

// DynamicExpression is a bounded immutable unresolved input. Binding names to
// the Manager's metadata produces the same ScalarExpression used by typed
// assignments; it introduces no SQL strings or independent execution path.
type DynamicExpression struct {
	node *dynamicScalarNode
	err  error
}

type dynamicScalarNode struct {
	name         string
	column       *int
	literal      query.Value
	operator     query.ArithmeticOperator
	left, right  DynamicExpression
	negate       bool
	depth, nodes int
	branches     []DynamicWhenExpression
	result       query.FieldKind
}

func DynamicF(name string) DynamicExpression {
	return DynamicExpression{node: &dynamicScalarNode{name: name, depth: 1, nodes: 1}}
}
func DynamicValue(value any) DynamicExpression {
	if expression, ok := value.(DynamicExpression); ok {
		return expression
	}
	literal, err := scalarValue(value)
	if err != nil {
		return DynamicExpression{err: err}
	}
	return DynamicExpression{node: &dynamicScalarNode{literal: literal, depth: 1, nodes: 1}}
}
func (expression DynamicExpression) Add(value any) DynamicExpression {
	return expression.binary(query.ArithmeticAdd, DynamicValue(value))
}
func (expression DynamicExpression) Subtract(value any) DynamicExpression {
	return expression.binary(query.ArithmeticSubtract, DynamicValue(value))
}
func (expression DynamicExpression) Multiply(value any) DynamicExpression {
	return expression.binary(query.ArithmeticMultiply, DynamicValue(value))
}
func (expression DynamicExpression) Divide(value any) DynamicExpression {
	return expression.binary(query.ArithmeticDivide, DynamicValue(value))
}
func (expression DynamicExpression) Remainder(value any) DynamicExpression {
	return expression.binary(query.ArithmeticModulo, DynamicValue(value))
}
func (expression DynamicExpression) Negate() DynamicExpression {
	if err := expression.validate(); err != nil {
		return DynamicExpression{err: err}
	}
	if expression.node.depth >= query.MaximumScalarDepth || expression.node.nodes >= query.MaximumScalarNodes {
		return DynamicExpression{err: invalidWritePlan("dynamic scalar exceeds its resource budget")}
	}
	return DynamicExpression{node: &dynamicScalarNode{negate: true, left: expression, depth: expression.node.depth + 1, nodes: expression.node.nodes + 1}}
}
func (expression DynamicExpression) validate() error {
	if expression.err != nil {
		return expression.err
	}
	if expression.node == nil {
		return invalidWritePlan("dynamic scalar is empty")
	}
	return nil
}
func (expression DynamicExpression) binary(operator query.ArithmeticOperator, right DynamicExpression) DynamicExpression {
	if err := expression.validate(); err != nil {
		return DynamicExpression{err: err}
	}
	if err := right.validate(); err != nil {
		return DynamicExpression{err: err}
	}
	depth, nodes := max(expression.node.depth, right.node.depth)+1, expression.node.nodes+right.node.nodes+1
	if depth > query.MaximumScalarDepth || nodes > query.MaximumScalarNodes {
		return DynamicExpression{err: invalidWritePlan("dynamic scalar exceeds its resource budget")}
	}
	return DynamicExpression{node: &dynamicScalarNode{operator: operator, left: expression, right: right, depth: depth, nodes: nodes}}
}

// dynamicBinding keeps model-name and result-column authority separate while
// both binding paths traverse the same immutable expression grammar.
type dynamicBinding struct {
	model   *preparedModel
	scope   query.ResultScope
	related func(string) (query.ScalarExpression, error)
}

func (expression DynamicExpression) bind(source dynamicBinding) (query.ScalarExpression, error) {
	if err := expression.validate(); err != nil {
		return query.ScalarExpression{}, err
	}
	node := expression.node
	if len(node.branches) > 0 {
		fallback, err := node.left.bind(source)
		if err != nil {
			return query.ScalarExpression{}, err
		}
		branches := make([]query.ScalarWhen, len(node.branches))
		for index, branch := range node.branches {
			predicate, err := branch.predicate.bind(source)
			if err != nil {
				return query.ScalarExpression{}, err
			}
			value, err := branch.value.bind(source)
			if err != nil {
				return query.ScalarExpression{}, err
			}
			branches[index], err = query.WhenScalar(predicate, value)
			if err != nil {
				return query.ScalarExpression{}, err
			}
		}
		return query.CaseScalar(fallback, branches...)
	}
	if node.operator != "" || node.negate {
		left, err := node.left.bind(source)
		if err != nil {
			return query.ScalarExpression{}, err
		}
		if node.negate {
			return query.NegateScalar(left)
		}
		right, err := node.right.bind(source)
		if err != nil {
			return query.ScalarExpression{}, err
		}
		return query.Arithmetic(node.operator, left, right)
	}
	if node.literal.Kind() != "" {
		if node.result != "" && node.literal.IsNull() {
			return query.NullExpression(node.result)
		}
		return query.LiteralExpression(node.literal)
	}
	if node.column != nil {
		ref, err := source.scope.Column(*node.column)
		if err != nil {
			return query.ScalarExpression{}, err
		}
		return query.SelectedExpression(ref)
	}
	if source.model == nil || source.scope.Valid() {
		return query.ScalarExpression{}, invalidResultBuilder("model field names cannot cross a result stage")
	}
	index, found := source.model.byName[node.name]
	if !found {
		if source.related != nil && strings.Contains(node.name, "__") {
			return source.related(node.name)
		}
		return query.ScalarExpression{}, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: node.name, Detail: "expression field is not concrete source metadata"}
	}
	return query.FieldExpression(fieldReference(source.model.metadata.Fields[index]))
}

type DynamicUpdateInput struct {
	Field string
	Value any // A closed scalar literal or DynamicExpression, never a SQL fragment.
}

func prepareDynamicUpdate[M any](model *preparedModel, inputs []DynamicUpdateInput) ([]UpdateAssignment[M], error) {
	if len(inputs) > query.MaximumScalarNodes {
		return nil, invalidWritePlan("too many query update assignments")
	}
	assignments := make([]UpdateAssignment[M], len(inputs))
	for index, input := range inputs {
		fieldIndex, found := model.byName[input.Field]
		if !found {
			return nil, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: input.Field, Detail: "query update field is not concrete source metadata"}
		}
		expression, err := DynamicValue(input.Value).bind(dynamicBinding{model: model})
		if err != nil {
			return nil, err
		}
		assignment, err := query.NewScalarAssignment(fieldReference(model.metadata.Fields[fieldIndex]), expression)
		if err != nil {
			return nil, err
		}
		assignments[index].assignment = assignment
	}
	return assignments, nil
}
