package orm

import "github.com/progresshans/godj/query"

// DynamicScalar is bound to one model's immutable metadata. It shares the
// typed AST and execution path; its decoded value retains the runtime kind.
type DynamicScalar[M any] struct {
	expression query.ResultExpression
	err        error
	marker     [0]func(M)
}

func BindExpression[M any](source QuerySet[M], input DynamicExpression) (DynamicScalar[M], error) {
	if source.configurationErr != nil {
		return DynamicScalar[M]{}, source.configurationErr
	}
	if source.prepared == nil {
		return DynamicScalar[M]{}, invalidResultBuilder("expression binding requires model metadata")
	}
	value, err := input.bind(source.prepared)
	if err != nil {
		return DynamicScalar[M]{}, err
	}
	expression, err := query.ScalarResult(value)
	if err != nil {
		return DynamicScalar[M]{}, err
	}
	if err := value.ValidateSource(source.plan.SourceFields()); err != nil {
		return DynamicScalar[M]{}, err
	}
	return DynamicScalar[M]{expression: expression}, nil
}

func (value DynamicScalar[M]) scalarResultField(M, query.Value) (query.ResultExpression, func() scalarCell[query.Value], error) {
	if value.err != nil {
		return query.ResultExpression{}, nil, value.err
	}
	factory, err := dynamicComputedCell(value.expression)
	return value.expression, factory, err
}

func dynamicComputedCell(expression query.ResultExpression) (func() scalarCell[query.Value], error) {
	factory, err := dynamicGroupDecoder([]query.ResultExpression{expression})
	if err != nil {
		return nil, err
	}
	return func() scalarCell[query.Value] {
		decoder := factory()
		return scalarCell[query.Value]{destination: decoder.destinations[0], value: func() query.Value { return decoder.decode()[0] }}
	}, nil
}

func (value DynamicScalar[M]) comparison(lookup query.Lookup, literal any) Predicate[M] {
	if value.err != nil {
		return Predicate[M]{err: value.err}
	}
	right, err := scalarValue(literal)
	if err != nil {
		return Predicate[M]{err: err}
	}
	scalar, present := value.expression.Scalar()
	if !present {
		return Predicate[M]{err: invalidResultBuilder("dynamic scalar is not bound")}
	}
	condition, err := query.NewScalarCondition(scalar, lookup, right)
	return predicateFromCondition[M](condition, err)
}
func (value DynamicScalar[M]) Exact(literal any) Predicate[M] {
	return value.comparison(query.LookupExact, literal)
}
func (value DynamicScalar[M]) GreaterThan(literal any) Predicate[M] {
	return value.comparison(query.LookupGreaterThan, literal)
}
func (value DynamicScalar[M]) GreaterThanOrEqual(literal any) Predicate[M] {
	return value.comparison(query.LookupGreaterThanOrEqual, literal)
}
func (value DynamicScalar[M]) LessThan(literal any) Predicate[M] {
	return value.comparison(query.LookupLessThan, literal)
}
func (value DynamicScalar[M]) LessThanOrEqual(literal any) Predicate[M] {
	return value.comparison(query.LookupLessThanOrEqual, literal)
}
func (value DynamicScalar[M]) IsNull(isNull bool) Predicate[M] {
	return value.comparison(query.LookupIsNull, isNull)
}
func (value DynamicScalar[M]) Asc() Ordering[M] {
	return resultOrdering[M](value.expression, value.err, query.Ascending)
}
func (value DynamicScalar[M]) Desc() Ordering[M] {
	return resultOrdering[M](value.expression, value.err, query.Descending)
}

func (value DynamicScalar[M]) aggregate(kind query.ResultExpressionKind) AggregateExpression[M, query.Value] {
	if value.err != nil {
		return AggregateExpression[M, query.Value]{err: value.err}
	}
	expression, err := query.AggregateResult(kind, value.expression)
	if err != nil {
		return AggregateExpression[M, query.Value]{err: err}
	}
	factory, err := dynamicComputedCell(expression)
	return AggregateExpression[M, query.Value]{expression: expression, newCell: factory, err: err}
}
func (value DynamicScalar[M]) Sum() AggregateExpression[M, query.Value] {
	return value.aggregate(query.ResultSum)
}
func (value DynamicScalar[M]) Avg() AggregateExpression[M, query.Value] {
	return value.aggregate(query.ResultAvg)
}
func (value DynamicScalar[M]) Min() AggregateExpression[M, query.Value] {
	return value.aggregate(query.ResultMin)
}
func (value DynamicScalar[M]) Max() AggregateExpression[M, query.Value] {
	return value.aggregate(query.ResultMax)
}

// ProjectDynamic owns an ordered set of metadata-bound expressions. The same
// projection can be used by SelectInto or as computed keys in GroupBy.
func ProjectDynamic[M any](source QuerySet[M], inputs ...DynamicExpression) (Projection[M, []query.Value], error) {
	if len(inputs) == 0 || len(inputs) > query.MaxProjectionExpressions {
		return Projection[M, []query.Value]{}, invalidResultBuilder("dynamic projection exceeds its expression bound")
	}
	expressions := make([]query.ResultExpression, len(inputs))
	for index, input := range inputs {
		value, err := BindExpression(source, input)
		if err != nil {
			return Projection[M, []query.Value]{}, err
		}
		expressions[index] = value.expression
	}
	if _, err := query.NewProjectionResult(expressions...); err != nil {
		return Projection[M, []query.Value]{}, err
	}
	factory, err := dynamicGroupDecoder(expressions)
	if err != nil {
		return Projection[M, []query.Value]{}, err
	}
	return Projection[M, []query.Value]{expressions: expressions, newDecoder: factory}, nil
}
