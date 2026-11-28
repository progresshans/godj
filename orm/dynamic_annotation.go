package orm

import "github.com/progresshans/godj/query"

// NamedExpression is an unresolved immutable definition. An ordered batch
// binds each name against the preceding definitions in that same query.
type NamedExpression struct {
	name     string
	value    DynamicExpression
	kind     query.ResultExpressionKind
	countAll bool
	distinct bool
	filter   *DynamicPredicate
	err      error
}

func Named(name string, value DynamicExpression) NamedExpression {
	return NamedExpression{name: name, value: value}
}

func NamedCount(name string) NamedExpression {
	return NamedExpression{name: name, kind: query.ResultCount, countAll: true}
}

func (value NamedExpression) aggregate(kind query.ResultExpressionKind) NamedExpression {
	if value.kind != "" {
		value.err = invalidResultBuilder("a named aggregate cannot contain another aggregate")
	}
	value.kind = kind
	return value
}
func (value NamedExpression) Count() NamedExpression { return value.aggregate(query.ResultCount) }
func (value NamedExpression) Sum() NamedExpression   { return value.aggregate(query.ResultSum) }
func (value NamedExpression) Avg() NamedExpression   { return value.aggregate(query.ResultAvg) }
func (value NamedExpression) Min() NamedExpression   { return value.aggregate(query.ResultMin) }
func (value NamedExpression) Max() NamedExpression   { return value.aggregate(query.ResultMax) }
func (value NamedExpression) Distinct() NamedExpression {
	if value.kind == "" || value.countAll {
		value.err = invalidResultBuilder("DISTINCT requires a named aggregate operand")
	}
	value.distinct = true
	return value
}
func (value NamedExpression) Filter(predicate DynamicPredicate) NamedExpression {
	if value.kind == "" {
		value.err = invalidResultBuilder("aggregate FILTER requires a named aggregate")
	}
	if value.filter != nil {
		predicate = DynamicAnd(*value.filter, predicate)
	}
	value.filter = &predicate
	return value
}

func AnnotateDynamic[M, R any](source AnnotationSource[M, R], values ...NamedExpression) (NamedQuery[M, R], error) {
	return annotateDynamic(source, true, values)
}

func AliasDynamic[M, R any](source AnnotationSource[M, R], values ...NamedExpression) (NamedQuery[M, R], error) {
	return annotateDynamic(source, false, values)
}

func annotateDynamic[M, R any](source AnnotationSource[M, R], selected bool, values []NamedExpression) (NamedQuery[M, R], error) {
	var zero NamedQuery[M, R]
	input, err := namedInput(source)
	if err != nil {
		return zero, err
	}
	if len(values) == 0 || len(values) > query.MaximumAnnotationBindings {
		return zero, invalidResultBuilder("named batch must contain a bounded nonempty list of definitions")
	}
	for _, value := range values {
		binding, err := input.dynamicBinding()
		if err != nil {
			return zero, err
		}
		expression, err := value.bind(input.plan, binding)
		if err != nil {
			return zero, err
		}
		factory, err := dynamicComputedCell(expression)
		if err != nil {
			return zero, err
		}
		input, _, err = bindNamedValue(input, value.name, expression, factory, selected)
		if err != nil {
			return zero, err
		}
	}
	return newNamedQuery(input)
}

func (value NamedExpression) bind(plan query.Plan, binding dynamicBinding) (query.ResultExpression, error) {
	if value.err != nil {
		return query.ResultExpression{}, value.err
	}
	expression := query.CountAllResult()
	if !value.countAll {
		scalar, err := value.value.bind(binding)
		if err != nil {
			return query.ResultExpression{}, err
		}
		if value.kind == "" {
			if err := plan.ValidateScalar(scalar); err != nil {
				return query.ResultExpression{}, err
			}
			expression, err = query.ScalarResult(scalar)
		} else {
			if err := plan.ValidateAggregateScalar(scalar); err != nil {
				return query.ResultExpression{}, err
			}
			expression, err = query.ScalarAggregateResult(value.kind, scalar)
		}
		if err != nil {
			return query.ResultExpression{}, err
		}
	}
	if value.distinct {
		var err error
		expression, err = expression.WithDistinct()
		if err != nil {
			return query.ResultExpression{}, err
		}
	}
	if value.filter != nil {
		predicate, err := value.filter.bind(binding)
		if err != nil {
			return query.ResultExpression{}, err
		}
		if err := plan.ValidateAggregatePredicate(predicate); err != nil {
			return query.ResultExpression{}, err
		}
		expression, err = expression.WithFilter(predicate)
		if err != nil {
			return query.ResultExpression{}, err
		}
	}
	return expression, nil
}

func (source NamedQuery[M, R]) Expression(input DynamicExpression) (DynamicScalar[M], error) {
	var zero DynamicScalar[M]
	if err := source.ConfigurationError(); err != nil {
		return zero, err
	}
	binding, err := source.input.dynamicBinding()
	if err != nil {
		return zero, err
	}
	scalar, err := input.bind(binding)
	if err != nil {
		return zero, err
	}
	if err := source.input.plan.ValidateScalar(scalar); err != nil {
		return zero, err
	}
	expression, err := query.ScalarResult(scalar)
	return DynamicScalar[M]{expression: expression}, err
}

func (source NamedQuery[M, R]) AggregateExpression(kind query.ResultExpressionKind, input DynamicExpression) (AggregateExpression[M, query.Value], error) {
	var zero AggregateExpression[M, query.Value]
	if err := source.ConfigurationError(); err != nil {
		return zero, err
	}
	binding, err := source.input.dynamicBinding()
	if err != nil {
		return zero, err
	}
	if kind == "" {
		return zero, invalidResultBuilder("named aggregate requires an aggregate kind")
	}
	expression, err := (NamedExpression{value: input, kind: kind}).bind(source.input.plan, binding)
	if err != nil {
		return zero, err
	}
	factory, err := dynamicComputedCell(expression)
	return AggregateExpression[M, query.Value]{expression: expression, newCell: factory}, err
}

func (source NamedQuery[M, R]) Predicate(input DynamicPredicate) (Predicate[M], error) {
	var zero Predicate[M]
	if err := source.ConfigurationError(); err != nil {
		return zero, err
	}
	binding, err := source.input.dynamicBinding()
	if err != nil {
		return zero, err
	}
	expression, err := input.bind(binding)
	if err != nil {
		return zero, err
	}
	if _, err := source.input.plan.WithWhere(expression); err != nil {
		return zero, err
	}
	return Predicate[M]{expression: expression}, nil
}

func (source NamedQuery[M, R]) ProjectExpressions(inputs ...DynamicExpression) (Projection[M, []query.Value], error) {
	var zero Projection[M, []query.Value]
	if err := source.ConfigurationError(); err != nil {
		return zero, err
	}
	if len(inputs) == 0 || len(inputs) > query.MaxProjectionExpressions {
		return zero, invalidResultBuilder("named projection exceeds its selection budget")
	}
	expressions := make([]query.ResultExpression, len(inputs))
	for index, input := range inputs {
		value, err := source.Expression(input)
		if err != nil {
			return zero, err
		}
		expressions[index] = value.expression
	}
	shape, err := query.NewProjectionResult(expressions...)
	if err != nil {
		return zero, err
	}
	if _, err := source.input.plan.WithResultShape(shape); err != nil {
		return zero, err
	}
	factory, err := dynamicGroupDecoder(expressions)
	return Projection[M, []query.Value]{expressions: expressions, newDecoder: factory}, err
}
