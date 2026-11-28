package orm

import (
	"slices"

	"github.com/progresshans/godj/query"
)

// CombineProjections concatenates ordered selections and composes their pure
// builders. It adds no SQL stage and retains each cell's decoder and NULL type.
// Repeated composition supports wide DTOs without adding another arity API.
func CombineProjections[M, A, B, R any](left Projection[M, A], right Projection[M, B], build func(A, B) R) Projection[M, R] {
	result := Projection[M, R]{err: firstError(left.err, right.err)}
	if result.err != nil {
		return result
	}
	if left.newDecoder == nil || right.newDecoder == nil || build == nil {
		result.err = invalidResultBuilder("projection composition requires initialized selections and a builder")
		return result
	}
	if len(left.expressions) > query.MaxProjectionExpressions-len(right.expressions) {
		result.err = invalidResultBuilder("projection composition exceeds its selection budget")
		return result
	}
	result.expressions = append(slices.Clone(left.expressions), right.expressions...)
	if _, result.err = query.NewProjectionResult(result.expressions...); result.err == nil {
		result.newDecoder = combineResultDecoders(left.newDecoder, right.newDecoder, build)
	}
	return result
}

// CombineAggregates concatenates one aggregate row's selections. Each input
// remains in the same aggregate stage; this does not nest aggregate functions.
func CombineAggregates[M, A, B, R any](left Aggregate[M, A], right Aggregate[M, B], build func(A, B) R) Aggregate[M, R] {
	result := Aggregate[M, R]{err: firstError(left.err, right.err)}
	if result.err != nil {
		return result
	}
	if left.newDecoder == nil || right.newDecoder == nil || build == nil {
		result.err = invalidResultBuilder("aggregate composition requires initialized selections and a builder")
		return result
	}
	if len(left.expressions) > query.MaxAggregateExpressions-len(right.expressions) {
		result.err = invalidResultBuilder("aggregate composition exceeds its selection budget")
		return result
	}
	result.expressions = append(slices.Clone(left.expressions), right.expressions...)
	if _, result.err = query.NewAggregateResult(result.expressions...); result.err == nil {
		result.newDecoder = combineResultDecoders(left.newDecoder, right.newDecoder, build)
	}
	return result
}

func combineResultDecoders[A, B, R any](left func() resultDecoder[A], right func() resultDecoder[B], build func(A, B) R) func() resultDecoder[R] {
	return func() resultDecoder[R] {
		a, b := left(), right()
		return resultDecoder[R]{
			destinations: append(slices.Clone(a.destinations), b.destinations...),
			decode:       func() R { return build(a.decode(), b.decode()) },
			err:          firstError(a.err, b.err),
			validate: func() error {
				if err := a.validateScanned(); err != nil {
					return err
				}
				return b.validateScanned()
			},
		}
	}
}
