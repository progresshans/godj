package orm

import (
	"context"
	"database/sql"
	"fmt"

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
	scalarResultField(M, V) (query.FieldRef, func() scalarCell[V], error)
}

// MaxField is the sealed scalar subset with a defined nullable MAX result.
type MaxField[M, V any] interface {
	scalarMaxField(M, V) (query.FieldRef, func() scalarCell[Optional[V]], error)
}

type scalarCell[V any] struct {
	destination any
	value       func() V
}

func (f IntegerField[M]) scalarResultField(M, int64) (query.FieldRef, func() scalarCell[int64], error) {
	return f.reference, func() scalarCell[int64] {
		var value int64
		return scalarCell[int64]{destination: &value, value: func() int64 { return value }}
	}, f.err
}

func (f IntegerField[M]) scalarMaxField(M, int64) (query.FieldRef, func() scalarCell[Optional[int64]], error) {
	return f.reference, func() scalarCell[Optional[int64]] {
		var value sql.NullInt64
		return scalarCell[Optional[int64]]{
			destination: &value,
			value: func() Optional[int64] {
				return Optional[int64]{value: value.Int64, valid: value.Valid}
			},
		}
	}, f.err
}

func (f StringField[M]) scalarResultField(M, string) (query.FieldRef, func() scalarCell[string], error) {
	return f.reference, func() scalarCell[string] {
		var value string
		return scalarCell[string]{destination: &value, value: func() string { return value }}
	}, f.err
}

func (f StringField[M]) scalarMaxField(M, string) (query.FieldRef, func() scalarCell[Optional[string]], error) {
	return f.reference, func() scalarCell[Optional[string]] {
		var value sql.NullString
		return scalarCell[Optional[string]]{
			destination: &value,
			value: func() Optional[string] {
				return Optional[string]{value: value.String, valid: value.Valid}
			},
		}
	}, f.err
}

func (f NullableStringField[M]) scalarResultField(M, *string) (query.FieldRef, func() scalarCell[*string], error) {
	return f.reference, func() scalarCell[*string] {
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
	}, f.err
}

func (f NullableStringField[M]) scalarMaxField(M, string) (query.FieldRef, func() scalarCell[Optional[string]], error) {
	return f.reference, func() scalarCell[Optional[string]] {
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
	}, f.err
}

func (f BooleanField[M]) scalarResultField(M, bool) (query.FieldRef, func() scalarCell[bool], error) {
	return f.reference, func() scalarCell[bool] {
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
	fields     []query.FieldRef
	newDecoder func() resultDecoder[R]
	err        error
	marker     [0]func(M)
}

func Project1[M, A, R any](first ScalarField[M, A], build func(A) R) Projection[M, R] {
	firstField, firstCell, err := scalarResult(first)
	result := Projection[M, R]{fields: []query.FieldRef{firstField}, err: err}
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
	result := Projection[M, R]{fields: []query.FieldRef{firstField, secondField}, err: firstError(err, secondErr)}
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
		fields: []query.FieldRef{firstField, secondField, thirdField},
		err:    firstError(err, secondErr, thirdErr),
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
		fields: []query.FieldRef{firstField, secondField, thirdField, fourthField},
		err:    firstError(err, secondErr, thirdErr, fourthErr),
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

func Max[M, V any](field MaxField[M, V]) AggregateExpression[M, Optional[V]] {
	if interfaceIsNil(field) {
		return AggregateExpression[M, Optional[V]]{err: invalidResultBuilder("MAX field is nil")}
	}
	reference, newCell, err := field.scalarMaxField(*new(M), *new(V))
	return AggregateExpression[M, Optional[V]]{
		expression: query.MaxResult(reference),
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

func SelectInto[M, R any](ctx context.Context, source QuerySet[M], projection Projection[M, R]) ([]R, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return nil, err
	}
	if projection.err != nil {
		return nil, projection.err
	}
	if projection.newDecoder == nil {
		return nil, invalidResultBuilder("projection decoder is nil")
	}
	if err := validateScalarResultSource(source.plan); err != nil {
		return nil, err
	}
	shape, err := query.NewProjectionResult(projection.fields...)
	if err != nil {
		return nil, err
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return nil, err
	}
	rows, err := source.openRows(ctx, plan)
	if err != nil {
		return nil, err
	}
	values := make([]R, 0)
	for rows.Next() {
		decoder := projection.newDecoder()
		if scanErr := rows.Scan(decoder.destinations...); scanErr != nil {
			err = fmt.Errorf("scan projection row: %w", scanErr)
			break
		}
		values = append(values, decoder.decode())
	}
	err = finishRowsLifecycle(ctx, err, rows)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func AggregateInto[M, R any](ctx context.Context, source QuerySet[M], aggregate Aggregate[M, R]) (R, error) {
	var zero R
	if err := source.validateTerminal(ctx); err != nil {
		return zero, err
	}
	if aggregate.err != nil {
		return zero, aggregate.err
	}
	if aggregate.newDecoder == nil {
		return zero, invalidResultBuilder("aggregate decoder is nil")
	}
	if err := validateScalarResultSource(source.plan); err != nil {
		return zero, err
	}
	shape, err := query.NewAggregateResult(aggregate.expressions...)
	if err != nil {
		return zero, err
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	rows, err := source.openRows(ctx, plan)
	if err != nil {
		return zero, err
	}
	decoder := aggregate.newDecoder()
	if !rows.Next() {
		err = &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "aggregate query returned no row"}
	} else if scanErr := rows.Scan(decoder.destinations...); scanErr != nil {
		err = fmt.Errorf("scan aggregate row: %w", scanErr)
	} else if rows.Next() {
		err = &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "aggregate query returned more than one row"}
	}
	err = finishRowsLifecycle(ctx, err, rows)
	if err != nil {
		return zero, err
	}
	return decoder.decode(), nil
}

func scalarResult[M, V any](field ScalarField[M, V]) (query.FieldRef, func() scalarCell[V], error) {
	if interfaceIsNil(field) {
		return query.FieldRef{}, nil, invalidResultBuilder("scalar result field is nil")
	}
	return field.scalarResultField(*new(M), *new(V))
}

func validateScalarResultSource(plan query.Plan) error {
	if _, selected := plan.RelationProjection(); selected {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "typed scalar result cannot combine with relation projection"}
	}
	for _, condition := range plan.Conditions() {
		if _, related := condition.RelationPath(); related {
			return &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "typed scalar result cannot combine with relation traversal"}
		}
	}
	return nil
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
