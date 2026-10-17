package orm

import (
	"context"
	"database/sql"

	"github.com/progresshans/godj/query"
)

// Optional is the explicit nullable scalar returned by aggregate expressions
// such as MAX over an empty result set.
type Optional[V any] struct {
	value V
	valid bool
}

func (o Optional[V]) Get() (V, bool) { return o.value, o.valid }
func (o Optional[V]) Valid() bool    { return o.valid }

// ScalarField is the sealed model/value capability accepted by typed result
// builders. The private methods prevent cross-model or arbitrary external
// implementations while retaining compile-time result value types.
type ScalarField[M, V any] interface {
	scalarResultField(M, V) (query.ResultExpression, func() scalarCell[V], error)
}

// OrderedField is the sealed scalar subset with nullable MIN/MAX results.
type OrderedField[M, V any] interface {
	scalarOrderedField(M, V, query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[V]], error)
}

type scalarCell[V any] struct {
	destination any
	value       func() V
}

func (f integerField[M]) scalarResultField(M, int64) (query.ResultExpression, func() scalarCell[int64], error) {
	return query.FieldResult(f.reference), func() scalarCell[int64] {
		var value int64
		return scalarCell[int64]{destination: &value, value: func() int64 { return value }}
	}, f.err
}

func (f integerField[M]) scalarOrderedField(_ M, _ int64, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[int64]], error) {
	expression, err := aggregateFieldResult(kind, f.reference, f.err)
	return expression, func() scalarCell[Optional[int64]] {
		var value sql.NullInt64
		return scalarCell[Optional[int64]]{
			destination: &value,
			value: func() Optional[int64] {
				return Optional[int64]{value: value.Int64, valid: value.Valid}
			},
		}
	}, err
}

func (f NullableIntegerField[M]) scalarResultField(M, *int64) (query.ResultExpression, func() scalarCell[*int64], error) {
	return query.FieldResult(f.reference), nullableIntegerResultCell, f.err
}

func nullableIntegerResultCell() scalarCell[*int64] {
	var value sql.NullInt64
	return scalarCell[*int64]{
		destination: &value,
		value: func() *int64 {
			if !value.Valid {
				return nil
			}
			copy := value.Int64
			return &copy
		},
	}
}

func (f StringField[M]) scalarResultField(M, string) (query.ResultExpression, func() scalarCell[string], error) {
	return query.FieldResult(f.reference), func() scalarCell[string] {
		var value string
		return scalarCell[string]{destination: &value, value: func() string { return value }}
	}, f.err
}

func (f StringField[M]) scalarOrderedField(_ M, _ string, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[string]], error) {
	expression, err := aggregateFieldResult(kind, f.reference, f.err)
	return expression, func() scalarCell[Optional[string]] {
		var value sql.NullString
		return scalarCell[Optional[string]]{
			destination: &value,
			value: func() Optional[string] {
				return Optional[string]{value: value.String, valid: value.Valid}
			},
		}
	}, err
}

func (f NullableStringField[M]) scalarResultField(M, *string) (query.ResultExpression, func() scalarCell[*string], error) {
	return query.FieldResult(f.reference), nullableStringResultCell, f.err
}

func nullableStringResultCell() scalarCell[*string] {
	var value sql.NullString
	return scalarCell[*string]{
		destination: &value,
		value: func() *string {
			if !value.Valid {
				return nil
			}
			copy := value.String
			return &copy
		},
	}
}

func (f NullableStringField[M]) scalarOrderedField(_ M, _ string, kind query.ResultExpressionKind) (query.ResultExpression, func() scalarCell[Optional[string]], error) {
	expression, err := aggregateFieldResult(kind, f.reference, f.err)
	return expression, func() scalarCell[Optional[string]] {
		var value sql.NullString
		return scalarCell[Optional[string]]{
			destination: &value,
			value: func() Optional[string] {
				if !value.Valid {
					return Optional[string]{}
				}
				return Optional[string]{value: value.String, valid: true}
			},
		}
	}, err
}

func (f BooleanField[M]) scalarResultField(M, bool) (query.ResultExpression, func() scalarCell[bool], error) {
	return query.FieldResult(f.reference), func() scalarCell[bool] {
		var value bool
		return scalarCell[bool]{destination: &value, value: func() bool { return value }}
	}, f.err
}

type resultDecoder[R any] struct {
	destinations []any
	decode       func() R
}

// Projection describes an ordered scalar row and a pure typed DTO builder.
type Projection[M, R any] struct {
	expressions []query.ResultExpression
	newDecoder  func() resultDecoder[R]
	err         error
	marker      [0]func(M)
}

func Project1[M, A, R any](first ScalarField[M, A], build func(A) R) Projection[M, R] {
	firstField, firstCell, err := scalarResult(first)
	result := Projection[M, R]{expressions: []query.ResultExpression{firstField}, err: err}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("projection builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue := firstCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination},
			decode:       func() R { return build(firstValue.value()) },
		}
	}
	return result
}

func Project2[M, A, B, R any](first ScalarField[M, A], second ScalarField[M, B], build func(A, B) R) Projection[M, R] {
	firstField, firstCell, err := scalarResult(first)
	secondField, secondCell, secondErr := scalarResult(second)
	result := Projection[M, R]{expressions: []query.ResultExpression{firstField, secondField}, err: firstError(err, secondErr)}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("projection builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue := firstCell(), secondCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination},
			decode:       func() R { return build(firstValue.value(), secondValue.value()) },
		}
	}
	return result
}

func Project3[M, A, B, C, R any](first ScalarField[M, A], second ScalarField[M, B], third ScalarField[M, C], build func(A, B, C) R) Projection[M, R] {
	firstField, firstCell, err := scalarResult(first)
	secondField, secondCell, secondErr := scalarResult(second)
	thirdField, thirdCell, thirdErr := scalarResult(third)
	result := Projection[M, R]{
		expressions: []query.ResultExpression{firstField, secondField, thirdField},
		err:         firstError(err, secondErr, thirdErr),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("projection builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue, thirdValue := firstCell(), secondCell(), thirdCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination, thirdValue.destination},
			decode:       func() R { return build(firstValue.value(), secondValue.value(), thirdValue.value()) },
		}
	}
	return result
}

func Project4[M, A, B, C, D, R any](first ScalarField[M, A], second ScalarField[M, B], third ScalarField[M, C], fourth ScalarField[M, D], build func(A, B, C, D) R) Projection[M, R] {
	firstField, firstCell, err := scalarResult(first)
	secondField, secondCell, secondErr := scalarResult(second)
	thirdField, thirdCell, thirdErr := scalarResult(third)
	fourthField, fourthCell, fourthErr := scalarResult(fourth)
	result := Projection[M, R]{
		expressions: []query.ResultExpression{firstField, secondField, thirdField, fourthField},
		err:         firstError(err, secondErr, thirdErr, fourthErr),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("projection builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue, thirdValue, fourthValue := firstCell(), secondCell(), thirdCell(), fourthCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination, thirdValue.destination, fourthValue.destination},
			decode: func() R {
				return build(firstValue.value(), secondValue.value(), thirdValue.value(), fourthValue.value())
			},
		}
	}
	return result
}

func Project5[M, A, B, C, D, E, R any](first ScalarField[M, A], second ScalarField[M, B], third ScalarField[M, C], fourth ScalarField[M, D], fifth ScalarField[M, E], build func(A, B, C, D, E) R) Projection[M, R] {
	firstField, firstCell, err := scalarResult(first)
	secondField, secondCell, secondErr := scalarResult(second)
	thirdField, thirdCell, thirdErr := scalarResult(third)
	fourthField, fourthCell, fourthErr := scalarResult(fourth)
	fifthField, fifthCell, fifthErr := scalarResult(fifth)
	result := Projection[M, R]{
		expressions: []query.ResultExpression{firstField, secondField, thirdField, fourthField, fifthField},
		err:         firstError(err, secondErr, thirdErr, fourthErr, fifthErr),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("projection builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue, thirdValue, fourthValue, fifthValue := firstCell(), secondCell(), thirdCell(), fourthCell(), fifthCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination, thirdValue.destination, fourthValue.destination, fifthValue.destination},
			decode: func() R {
				return build(firstValue.value(), secondValue.value(), thirdValue.value(), fourthValue.value(), fifthValue.value())
			},
		}
	}
	return result
}

type AggregateExpression[M, V any] struct {
	expression query.ResultExpression
	newCell    func() scalarCell[V]
	err        error
	marker     [0]func(M)
}

func CountRows[M any]() AggregateExpression[M, int64] {
	return AggregateExpression[M, int64]{
		expression: query.CountAllResult(),
		newCell: func() scalarCell[int64] {
			var value int64
			return scalarCell[int64]{destination: &value, value: func() int64 { return value }}
		},
	}
}

func Max[M, V any](field OrderedField[M, V]) AggregateExpression[M, Optional[V]] {
	if interfaceIsNil(field) {
		return AggregateExpression[M, Optional[V]]{err: invalidResultBuilder("MAX field is nil")}
	}
	expression, newCell, err := field.scalarOrderedField(*new(M), *new(V), query.ResultMax)
	return AggregateExpression[M, Optional[V]]{
		expression: expression,
		newCell:    newCell,
		err:        err,
	}
}

func Min[M, V any](field OrderedField[M, V]) AggregateExpression[M, Optional[V]] {
	if interfaceIsNil(field) {
		return AggregateExpression[M, Optional[V]]{err: invalidResultBuilder("MIN field is nil")}
	}
	expression, newCell, err := field.scalarOrderedField(*new(M), *new(V), query.ResultMin)
	return AggregateExpression[M, Optional[V]]{
		expression: expression,
		newCell:    newCell,
		err:        err,
	}
}

// Aggregate describes an ordered scalar aggregate row and typed builder.
type Aggregate[M, R any] struct {
	expressions []query.ResultExpression
	newDecoder  func() resultDecoder[R]
	err         error
	marker      [0]func(M)
}

func Aggregate1[M, A, R any](first AggregateExpression[M, A], build func(A) R) Aggregate[M, R] {
	result := Aggregate[M, R]{expressions: []query.ResultExpression{first.expression}, err: first.err}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("aggregate builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue := first.newCell()
		return resultDecoder[R]{destinations: []any{firstValue.destination}, decode: func() R { return build(firstValue.value()) }}
	}
	return result
}

func Aggregate2[M, A, B, R any](first AggregateExpression[M, A], second AggregateExpression[M, B], build func(A, B) R) Aggregate[M, R] {
	result := Aggregate[M, R]{
		expressions: []query.ResultExpression{first.expression, second.expression},
		err:         firstError(first.err, second.err),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("aggregate builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue := first.newCell(), second.newCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination},
			decode:       func() R { return build(firstValue.value(), secondValue.value()) },
		}
	}
	return result
}

func Aggregate3[M, A, B, C, R any](first AggregateExpression[M, A], second AggregateExpression[M, B], third AggregateExpression[M, C], build func(A, B, C) R) Aggregate[M, R] {
	result := Aggregate[M, R]{
		expressions: []query.ResultExpression{first.expression, second.expression, third.expression},
		err:         firstError(first.err, second.err, third.err),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("aggregate builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue, thirdValue := first.newCell(), second.newCell(), third.newCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination, thirdValue.destination},
			decode:       func() R { return build(firstValue.value(), secondValue.value(), thirdValue.value()) },
		}
	}
	return result
}

func Aggregate4[M, A, B, C, D, R any](first AggregateExpression[M, A], second AggregateExpression[M, B], third AggregateExpression[M, C], fourth AggregateExpression[M, D], build func(A, B, C, D) R) Aggregate[M, R] {
	result := Aggregate[M, R]{
		expressions: []query.ResultExpression{first.expression, second.expression, third.expression, fourth.expression},
		err:         firstError(first.err, second.err, third.err, fourth.err),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("aggregate builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		firstValue, secondValue, thirdValue, fourthValue := first.newCell(), second.newCell(), third.newCell(), fourth.newCell()
		return resultDecoder[R]{
			destinations: []any{firstValue.destination, secondValue.destination, thirdValue.destination, fourthValue.destination},
			decode: func() R {
				return build(firstValue.value(), secondValue.value(), thirdValue.value(), fourthValue.value())
			},
		}
	}
	return result
}

func Aggregate5[M, A, B, C, D, E, R any](first AggregateExpression[M, A], second AggregateExpression[M, B], third AggregateExpression[M, C], fourth AggregateExpression[M, D], fifth AggregateExpression[M, E], build func(A, B, C, D, E) R) Aggregate[M, R] {
	result := Aggregate[M, R]{
		expressions: []query.ResultExpression{first.expression, second.expression, third.expression, fourth.expression, fifth.expression},
		err:         firstError(first.err, second.err, third.err, fourth.err, fifth.err),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("aggregate builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		a, b, c, d, e := first.newCell(), second.newCell(), third.newCell(), fourth.newCell(), fifth.newCell()
		return resultDecoder[R]{destinations: []any{a.destination, b.destination, c.destination, d.destination, e.destination},
			decode: func() R { return build(a.value(), b.value(), c.value(), d.value(), e.value()) },
		}
	}
	return result
}

func Aggregate6[M, A, B, C, D, E, F, R any](first AggregateExpression[M, A], second AggregateExpression[M, B], third AggregateExpression[M, C], fourth AggregateExpression[M, D], fifth AggregateExpression[M, E], sixth AggregateExpression[M, F], build func(A, B, C, D, E, F) R) Aggregate[M, R] {
	result := Aggregate[M, R]{
		expressions: []query.ResultExpression{first.expression, second.expression, third.expression, fourth.expression, fifth.expression, sixth.expression},
		err:         firstError(first.err, second.err, third.err, fourth.err, fifth.err, sixth.err),
	}
	if build == nil && result.err == nil {
		result.err = invalidResultBuilder("aggregate builder is nil")
	}
	result.newDecoder = func() resultDecoder[R] {
		a, b, c, d, e, f := first.newCell(), second.newCell(), third.newCell(), fourth.newCell(), fifth.newCell(), sixth.newCell()
		return resultDecoder[R]{destinations: []any{a.destination, b.destination, c.destination, d.destination, e.destination, f.destination},
			decode: func() R { return build(a.value(), b.value(), c.value(), d.value(), e.value(), f.value()) },
		}
	}
	return result
}

func SelectInto[M, R any](ctx context.Context, source QuerySet[M], projection Projection[M, R]) ([]R, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return nil, err
	}
	selected, err := SelectQuery(source, projection)
	if err != nil {
		return nil, err
	}
	return selected.All(ctx)
}

func AggregateInto[M, R any](ctx context.Context, source QuerySet[M], aggregate Aggregate[M, R]) (R, error) {
	var zero R
	if err := source.validateTerminal(ctx); err != nil {
		return zero, err
	}
	selected, err := AggregateQuery(source, aggregate)
	if err != nil {
		return zero, err
	}
	return selected.One(ctx)
}

func scalarResult[M, V any](field ScalarField[M, V]) (query.ResultExpression, func() scalarCell[V], error) {
	if interfaceIsNil(field) {
		return query.ResultExpression{}, nil, invalidResultBuilder("scalar result field is nil")
	}
	return field.scalarResultField(*new(M), *new(V))
}

func validateProjectionSource(plan query.Plan) error {
	if len(plan.RelationProjections()) != 0 {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "typed scalar result cannot combine with relation projection"}
	}
	return nil
}

func validateNonCountAggregateSource(plan query.Plan) error {
	for _, ordering := range plan.Orderings() {
		if _, related := ordering.Expression().RelationPath(); related {
			return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "typed non-count aggregate cannot combine with relation traversal"}
		}
	}
	// Relation filters share the prepared grouping compiler for all aggregate
	// kinds. Its source slice/distinct restrictions still apply.
	return validateProjectionSource(plan)
}

func invalidResultBuilder(detail string) error {
	return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: detail}
}

func firstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}
