package orm

import "github.com/progresshans/godj/query"

// DynamicExpression is a bounded immutable unresolved input. Binding names to
// the Manager's metadata produces the same ScalarExpression used by typed
// assignments; it introduces no SQL strings or independent execution path.
type DynamicExpression struct {
	node *dynamicScalarNode
	err  error
}

type dynamicScalarNode struct {
	name         string
	literal      query.Value
	operator     query.ArithmeticOperator
	left, right  DynamicExpression
	negate       bool
	depth, nodes int
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
func (expression DynamicExpression) bind(model *preparedModel) (query.ScalarExpression, error) {
	if err := expression.validate(); err != nil {
		return query.ScalarExpression{}, err
	}
	node := expression.node
	if node.operator != "" || node.negate {
		left, err := node.left.bind(model)
		if err != nil {
			return query.ScalarExpression{}, err
		}
		if node.negate {
			return query.NegateScalar(left)
		}
		right, err := node.right.bind(model)
		if err != nil {
			return query.ScalarExpression{}, err
		}
		return query.Arithmetic(node.operator, left, right)
	}
	if node.literal.Kind() != "" {
		return query.LiteralExpression(node.literal)
	}
	index, found := model.byName[node.name]
	if !found {
		return query.ScalarExpression{}, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: node.name, Detail: "expression field is not concrete source metadata"}
	}
	return query.FieldExpression(fieldReference(model.metadata.Fields[index]))
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
		expression, err := DynamicValue(input.Value).bind(model)
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
