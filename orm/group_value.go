package orm

import (
	"reflect"

	"github.com/progresshans/godj/query"
)

// Some constructs a present nullable aggregate value for a typed comparison.
func Some[V any](value V) Optional[V] { return Optional[V]{value: value, valid: true} }
func (o Optional[V]) groupLiteral() (query.Value, error) {
	if !o.valid {
		return query.Null(), nil
	}
	return scalarValue(o.value)
}

func Count[M, V any](field ScalarField[M, V]) AggregateExpression[M, int64] {
	expression, _, err := scalarResult(field)
	if err == nil {
		expression, err = query.CountResult(expression)
	}
	value := CountRows[M]()
	value.expression, value.err = expression, err
	return value
}
func (a AggregateExpression[M, V]) Distinct() AggregateExpression[M, V] {
	if a.err == nil {
		a.expression, a.err = a.expression.WithDistinct()
	}
	return a
}
func (a AggregateExpression[M, V]) Where(predicates ...Predicate[M]) AggregateExpression[M, V] {
	if a.err != nil || len(predicates) == 0 {
		return a
	}
	if len(predicates) > query.MaxGroupExpressionNodes {
		a.err = invalidResultBuilder("too many aggregate filter predicates")
		return a
	}
	values := make([]query.Expression, len(predicates))
	for index, value := range predicates {
		if value.err != nil {
			a.err = value.err
			return a
		}
		values[index] = value.expression
	}
	expression := values[0]
	if len(values) > 1 {
		expression, a.err = query.AndExpressions(values[0], values[1], values[2:]...)
	}
	if a.err == nil {
		a.expression, a.err = a.expression.WithFilter(expression)
	}
	return a
}

// GroupValue retains the selected value's type and model. A HAVING predicate
// must use this same expression in the group result, including its filter.
type GroupValue[M, V any] struct {
	expression query.ResultExpression
	err        error
	marker     [0]func(M, V)
}

func GroupKey[M, V any](field ScalarField[M, V]) GroupValue[M, V] {
	expression, _, err := scalarResult(field)
	return GroupValue[M, V]{expression: expression, err: err}
}
func (a AggregateExpression[M, V]) groupValue() GroupValue[M, V] {
	return GroupValue[M, V]{expression: a.expression, err: a.err}
}
func (a AggregateExpression[M, V]) Exact(value V) GroupPredicate[M] {
	return a.groupValue().Exact(value)
}
func (a AggregateExpression[M, V]) GreaterThan(value V) GroupPredicate[M] {
	return a.groupValue().GreaterThan(value)
}
func (a AggregateExpression[M, V]) GreaterThanOrEqual(value V) GroupPredicate[M] {
	return a.groupValue().GreaterThanOrEqual(value)
}
func (a AggregateExpression[M, V]) LessThan(value V) GroupPredicate[M] {
	return a.groupValue().LessThan(value)
}
func (a AggregateExpression[M, V]) LessThanOrEqual(value V) GroupPredicate[M] {
	return a.groupValue().LessThanOrEqual(value)
}
func (a AggregateExpression[M, V]) IsNull(value bool) GroupPredicate[M] {
	return a.groupValue().IsNull(value)
}
func (a AggregateExpression[M, V]) Asc() GroupOrdering[M]  { return a.groupValue().Asc() }
func (a AggregateExpression[M, V]) Desc() GroupOrdering[M] { return a.groupValue().Desc() }

type GroupPredicate[M any] struct {
	expression query.GroupExpression
	err        error
	marker     [0]func(M)
}

func (v GroupValue[M, V]) comparison(lookup query.Lookup, value any) GroupPredicate[M] {
	if v.err != nil {
		return GroupPredicate[M]{err: v.err}
	}
	literal, err := groupLiteral(value)
	if err != nil {
		return GroupPredicate[M]{err: err}
	}
	expression, err := query.NewGroupExpression(v.expression, lookup, literal)
	return GroupPredicate[M]{expression: expression, err: err}
}
func groupLiteral(value any) (query.Value, error) {
	if optional, ok := value.(interface{ groupLiteral() (query.Value, error) }); ok {
		return optional.groupLiteral()
	} else {
		// ScalarField is sealed. Only nullable scalar fields reach this
		// pointer boundary; no arbitrary DTO reflection participates in I/O.
		reflected := reflect.ValueOf(value)
		if reflected.IsValid() && reflected.Kind() == reflect.Pointer {
			if reflected.IsNil() {
				value = nil
			} else {
				value = reflected.Elem().Interface()
			}
		}
		return scalarValue(value)
	}
}
func (v GroupValue[M, V]) Exact(value V) GroupPredicate[M] {
	return v.comparison(query.LookupExact, value)
}
func (v GroupValue[M, V]) GreaterThan(value V) GroupPredicate[M] {
	return v.comparison(query.LookupGreaterThan, value)
}
func (v GroupValue[M, V]) GreaterThanOrEqual(value V) GroupPredicate[M] {
	return v.comparison(query.LookupGreaterThanOrEqual, value)
}
func (v GroupValue[M, V]) LessThan(value V) GroupPredicate[M] {
	return v.comparison(query.LookupLessThan, value)
}
func (v GroupValue[M, V]) LessThanOrEqual(value V) GroupPredicate[M] {
	return v.comparison(query.LookupLessThanOrEqual, value)
}
func (v GroupValue[M, V]) IsNull(value bool) GroupPredicate[M] {
	return v.comparison(query.LookupIsNull, value)
}

func GroupAnd[M any](left, right GroupPredicate[M], rest ...GroupPredicate[M]) GroupPredicate[M] {
	return connectGroupPredicates(true, left, right, rest...)
}
func GroupOr[M any](left, right GroupPredicate[M], rest ...GroupPredicate[M]) GroupPredicate[M] {
	return connectGroupPredicates(false, left, right, rest...)
}
func GroupNot[M any](value GroupPredicate[M]) GroupPredicate[M] {
	if value.err == nil {
		value.expression, value.err = query.NotGroupExpression(value.expression)
	}
	return value
}
func connectGroupPredicates[M any](and bool, left, right GroupPredicate[M], rest ...GroupPredicate[M]) GroupPredicate[M] {
	if len(rest) > query.MaxGroupExpressionNodes {
		return GroupPredicate[M]{err: invalidResultBuilder("too many HAVING predicates")}
	}
	if err := firstError(left.err, right.err); err != nil {
		return GroupPredicate[M]{err: err}
	}
	expressions := make([]query.GroupExpression, len(rest))
	for index, value := range rest {
		if value.err != nil {
			return GroupPredicate[M]{err: value.err}
		}
		expressions[index] = value.expression
	}
	var expression query.GroupExpression
	var err error
	if and {
		expression, err = query.AndGroupExpressions(left.expression, right.expression, expressions...)
	} else {
		expression, err = query.OrGroupExpressions(left.expression, right.expression, expressions...)
	}
	return GroupPredicate[M]{expression: expression, err: err}
}

// GroupOrder admits existing typed field orderings and aggregate orderings.
// The private method rejects cross-model and externally fabricated selectors.
type GroupOrder[M any] interface {
	groupOrdering(M) (query.GroupOrdering, error)
}
type GroupOrdering[M any] struct {
	ordering query.GroupOrdering
	err      error
	marker   [0]func(M)
}

func (o GroupOrdering[M]) groupOrdering(M) (query.GroupOrdering, error) { return o.ordering, o.err }
func (o Ordering[M]) groupOrdering(M) (query.GroupOrdering, error) {
	if o.err != nil {
		return query.GroupOrdering{}, o.err
	}
	return query.NewGroupOrdering(o.ordering.Expression(), o.ordering.Direction(), query.NullsNative)
}
func (v GroupValue[M, V]) order(direction query.Direction) GroupOrdering[M] {
	if v.err != nil {
		return GroupOrdering[M]{err: v.err}
	}
	value, err := query.NewGroupOrdering(v.expression, direction, query.NullsNative)
	return GroupOrdering[M]{ordering: value, err: err}
}
func (v GroupValue[M, V]) Asc() GroupOrdering[M]  { return v.order(query.Ascending) }
func (v GroupValue[M, V]) Desc() GroupOrdering[M] { return v.order(query.Descending) }
func (o GroupOrdering[M]) withNulls(nulls query.NullOrder) GroupOrdering[M] {
	if o.err == nil {
		o.ordering, o.err = query.NewGroupOrdering(o.ordering.Expression(), o.ordering.Direction(), nulls)
	}
	return o
}
func (o GroupOrdering[M]) NullsFirst() GroupOrdering[M] { return o.withNulls(query.NullsFirst) }
func (o GroupOrdering[M]) NullsLast() GroupOrdering[M]  { return o.withNulls(query.NullsLast) }
func (o Ordering[M]) NullsFirst() GroupOrdering[M] {
	value, err := o.groupOrdering(*new(M))
	return (GroupOrdering[M]{ordering: value, err: err}).NullsFirst()
}
func (o Ordering[M]) NullsLast() GroupOrdering[M] {
	value, err := o.groupOrdering(*new(M))
	return (GroupOrdering[M]{ordering: value, err: err}).NullsLast()
}
