package orm

import "github.com/progresshans/godj/query"

// ResultOf is a query phase marker, never a persisted model. A selected result
// therefore cannot satisfy the model-specific operand of a row assignment.
// Successive Derive calls produce successive phases as well as runtime scopes.
type ResultOf[M any] struct{ marker [0]func(M) }

// ResultInput is sealed to lazy explicit scalar results. Derive does not read
// their evaluation cache or run their DTO builder; it snapshots the SQL plan.
type ResultInput[M any] interface {
	resultInput(M) (resultOrigin, query.Plan, error)
}

type ResultSource[M any] struct {
	origin resultOrigin
	plan   query.Plan
	scope  query.ResultScope
}

func Derive[M any](input ResultInput[M]) (ResultSource[M], error) {
	if interfaceIsNil(input) {
		return ResultSource[M]{}, invalidResultBuilder("result input is nil")
	}
	origin, inputPlan, err := input.resultInput(*new(M))
	if err != nil {
		return ResultSource[M]{}, err
	}
	plan, scope, err := query.NewDerivedPlan(inputPlan)
	if err != nil {
		return ResultSource[M]{}, err
	}
	return ResultSource[M]{origin: origin, plan: plan, scope: scope}, nil
}
func (q ResultQuery[M, R]) resultInput(M) (resultOrigin, query.Plan, error) {
	return q.origin, q.plan, q.validatePlan()
}
func (q GroupedQuery[M, R]) resultInput(M) (resultOrigin, query.Plan, error) {
	if q.err != nil {
		return resultOrigin{}, query.Plan{}, q.err
	}
	if q.origin.validate == nil || q.evaluation == nil || q.newDecoder == nil || q.plan.ResultShape().GroupMode() != query.GroupRows {
		return resultOrigin{}, query.Plan{}, invalidResultBuilder("group query is uninitialized")
	}
	if err := q.plan.ValidateDerived(); err != nil {
		return resultOrigin{}, query.Plan{}, err
	}
	return q.origin, q.plan, q.plan.ValidateGrouping()
}
func (source ResultSource[M]) Plan() query.Plan { return source.plan }
func (source ResultSource[M]) validate() error {
	if source.origin.validate == nil || !source.scope.Valid() {
		return invalidResultBuilder("derived result source is uninitialized")
	}
	return source.plan.ValidateDerived()
}

func SelectResults[M, R any](source ResultSource[M], projection Projection[ResultOf[M], R]) (ResultQuery[ResultOf[M], R], error) {
	var zero ResultQuery[ResultOf[M], R]
	if err := firstError(source.validate(), projection.err); err != nil {
		return zero, err
	}
	if projection.newDecoder == nil {
		return zero, invalidResultBuilder("derived projection decoder is nil")
	}
	shape, err := query.NewProjectionResult(projection.expressions...)
	if err != nil {
		return zero, err
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	return newResultQuery[ResultOf[M]](source.origin, plan, projection.newDecoder), nil
}

// AggregateResults aggregates the complete input captured by Derive, including
// its prior grouping, filters, DISTINCT, ordering and slice. One executes this
// row; another Derive can use its aggregate outputs in a later statement stage.
func AggregateResults[M, R any](source ResultSource[M], aggregate Aggregate[ResultOf[M], R]) (ResultQuery[ResultOf[M], R], error) {
	var zero ResultQuery[ResultOf[M], R]
	if err := firstError(source.validate(), aggregate.err); err != nil {
		return zero, err
	}
	if aggregate.newDecoder == nil {
		return zero, invalidResultBuilder("derived aggregate decoder is nil")
	}
	shape, err := query.NewAggregateResult(aggregate.expressions...)
	if err != nil {
		return zero, err
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	return newResultQuery[ResultOf[M]](source.origin, plan, aggregate.newDecoder), nil
}

func GroupResults[M, K, A, R any](source ResultSource[M], keys Projection[ResultOf[M], K], aggregate Aggregate[ResultOf[M], A], build func(K, A) R) (GroupedQuery[ResultOf[M], R], error) {
	if err := source.validate(); err != nil {
		return GroupedQuery[ResultOf[M], R]{}, err
	}
	return newGroupedQuery(source.origin, source.plan, keys, aggregate, build)
}

// ResultColumn retains both the selected SQL domain and its original Go cell
// decoder. The private scope distinguishes separately derived equal queries.
type ResultColumn[M, V any] struct {
	value   ScalarExpression[M, V]
	newCell func() scalarCell[V]
}

func SelectedField[M, V any](source ResultSource[M], field ScalarField[M, V]) ResultColumn[ResultOf[M], V] {
	expression, factory, err := scalarResult(field)
	return selectedColumn(source, expression, factory, err)
}
func SelectedAggregate[M, V any](source ResultSource[M], aggregate AggregateExpression[M, V]) ResultColumn[ResultOf[M], V] {
	return selectedColumn(source, aggregate.expression, aggregate.newCell, aggregate.err)
}
func selectedColumn[M, V any](source ResultSource[M], expression query.ResultExpression, factory func() scalarCell[V], err error) ResultColumn[ResultOf[M], V] {
	var scalar query.ScalarExpression
	if err = firstError(err, source.validate()); err == nil {
		index := -1
		for candidate, selected := range source.scope.Shape().Expressions() {
			if selected.Equal(expression) {
				index = candidate
				break
			}
		}
		if index < 0 || factory == nil {
			err = invalidResultBuilder("result value was not selected by this input")
		} else {
			var ref query.ResultRef
			ref, err = source.scope.Column(index)
			if err == nil {
				scalar, err = query.SelectedExpression(ref)
			}
		}
	}
	return ResultColumn[ResultOf[M], V]{value: ScalarExpression[ResultOf[M], V]{expression: scalar, err: err}, newCell: factory}
}
func (column ResultColumn[M, V]) scalarOperand(M, V) (query.ScalarExpression, error) {
	return column.value.expression, column.value.err
}
func (column ResultColumn[M, V]) scalarResultField(M, V) (query.ResultExpression, func() scalarCell[V], error) {
	if column.value.err != nil {
		return query.ResultExpression{}, nil, column.value.err
	}
	if column.newCell == nil {
		return query.ResultExpression{}, nil, invalidResultBuilder("result column is uninitialized")
	}
	expression, err := query.ScalarResult(column.value.expression)
	return expression, column.newCell, err
}
func (column ResultColumn[M, V]) comparison(lookup query.Lookup, literal any) Predicate[M] {
	if column.value.err != nil {
		return Predicate[M]{err: column.value.err}
	}
	right, err := groupLiteral(literal)
	if err != nil {
		return Predicate[M]{err: err}
	}
	condition, err := query.NewScalarCondition(column.value.expression, lookup, right)
	return predicateFromCondition[M](condition, err)
}
func (column ResultColumn[M, V]) Exact(value V) Predicate[M] {
	return column.comparison(query.LookupExact, value)
}
func (column ResultColumn[M, V]) GreaterThan(value V) Predicate[M] {
	return column.comparison(query.LookupGreaterThan, value)
}
func (column ResultColumn[M, V]) GreaterThanOrEqual(value V) Predicate[M] {
	return column.comparison(query.LookupGreaterThanOrEqual, value)
}
func (column ResultColumn[M, V]) LessThan(value V) Predicate[M] {
	return column.comparison(query.LookupLessThan, value)
}
func (column ResultColumn[M, V]) LessThanOrEqual(value V) Predicate[M] {
	return column.comparison(query.LookupLessThanOrEqual, value)
}
func (column ResultColumn[M, V]) IsNull(value bool) Predicate[M] {
	return column.comparison(query.LookupIsNull, value)
}
func (column ResultColumn[M, V]) order(direction query.Direction) Ordering[M] {
	if column.value.err != nil {
		return Ordering[M]{err: column.value.err}
	}
	expression, err := query.ScalarResult(column.value.expression)
	return resultOrdering[M](expression, err, direction)
}
func (column ResultColumn[M, V]) Asc() Ordering[M]  { return column.order(query.Ascending) }
func (column ResultColumn[M, V]) Desc() Ordering[M] { return column.order(query.Descending) }

// OptionalOperand and NullableOperand remove a Go result wrapper from the
// operand type. SQL NULL remains NULL; no coalescing or value conversion occurs.
func OptionalOperand[M, V any](column ScalarOperand[M, Optional[V]]) ScalarExpression[M, V] {
	expression, err := operandExpression(column)
	return resultOperand[M, V](expression, err)
}
func NullableOperand[M, V any](column ScalarOperand[M, *V]) ScalarExpression[M, V] {
	expression, err := operandExpression(column)
	return resultOperand[M, V](expression, err)
}
func resultOperand[M, V any](expression query.ScalarExpression, err error) ScalarExpression[M, V] {
	if err == nil && (scalarKind[V]() == "" || scalarKind[V]() != expression.ResultKind()) {
		err = invalidResultBuilder("result operand type differs from its scalar domain")
	}
	return ScalarExpression[M, V]{expression: expression, err: err}
}
