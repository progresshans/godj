package orm

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// resultOrigin retains the original query's session and configuration checks
// without inventing a model descriptor for a result relation.
type resultOrigin struct {
	backend  db.Queryer
	validate func(context.Context) error
}

// ResultQuery is a lazy scalar read. Copies share private successfully scanned
// cells; refinements start cold. A pure builder runs only after the complete
// cursor has closed successfully and runs again for each publication.
type ResultQuery[M, R any] struct {
	origin     resultOrigin
	plan       query.Plan
	newDecoder func() resultDecoder[R]
	evaluation *evaluationState[resultDecoder[R]]
	err        error
}

func SelectQuery[M, R any](source QuerySet[M], projection Projection[M, R]) (ResultQuery[M, R], error) {
	var zero ResultQuery[M, R]
	if err := firstError(source.configurationErr, projection.err); err != nil {
		return zero, err
	}
	if projection.newDecoder == nil {
		return zero, invalidResultBuilder("projection decoder is nil")
	}
	if err := validateProjectionSource(source.plan); err != nil {
		return zero, err
	}
	shape, err := query.NewProjectionResult(projection.expressions...)
	if err != nil {
		return zero, err
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	return newResultQuery[M](resultOrigin{source.backend, source.validateTerminal}, plan, projection.newDecoder), nil
}

func AggregateQuery[M, R any](source QuerySet[M], aggregate Aggregate[M, R]) (ResultQuery[M, R], error) {
	var zero ResultQuery[M, R]
	if err := firstError(source.configurationErr, aggregate.err); err != nil {
		return zero, err
	}
	if aggregate.newDecoder == nil {
		return zero, invalidResultBuilder("aggregate decoder is nil")
	}
	shape, err := query.NewAggregateResult(aggregate.expressions...)
	if err != nil {
		return zero, err
	}
	if !shape.IsCountAll() {
		if err := validateNonCountAggregateSource(source.plan); err != nil {
			return zero, err
		}
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	return newResultQuery[M](resultOrigin{source.backend, source.validateTerminal}, plan, aggregate.newDecoder), nil
}

func newResultQuery[M, R any](origin resultOrigin, plan query.Plan, decoder func() resultDecoder[R]) ResultQuery[M, R] {
	return ResultQuery[M, R]{origin: origin, plan: plan, newDecoder: decoder, evaluation: newEvaluationState[resultDecoder[R]]()}
}

func (q ResultQuery[M, R]) Plan() query.Plan { return q.plan }
func (q ResultQuery[M, R]) Fresh() ResultQuery[M, R] {
	q.evaluation = newEvaluationState[resultDecoder[R]]()
	return q
}
func (q ResultQuery[M, R]) sliced() bool {
	_, limit := q.plan.Limit()
	_, offset := q.plan.Offset()
	return limit || offset
}
func (q ResultQuery[M, R]) validatePlan() error {
	if q.err != nil {
		return q.err
	}
	if q.origin.validate == nil || q.newDecoder == nil || q.evaluation == nil {
		return invalidResultBuilder("result query is uninitialized")
	}
	if kind := q.plan.ResultShape().Kind(); kind != query.ResultProjection && kind != query.ResultAggregate {
		return invalidResultBuilder("result query requires a projection or aggregate row")
	}
	if err := q.plan.ValidateDerived(); err != nil {
		return err
	}
	return q.plan.ValidateOrderings()
}
func (q ResultQuery[M, R]) validate(ctx context.Context) error {
	if interfaceIsNil(ctx) {
		return invalidTerminalContext()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := q.validatePlan(); err != nil {
		return err
	}
	return q.origin.validate(ctx)
}

// Filter and OrderBy operate in the current input scope. Crossing a slice or
// referring to an aggregate's output requires an explicit Derive boundary.
func (q ResultQuery[M, R]) Filter(predicates ...Predicate[M]) ResultQuery[M, R] {
	q = q.Fresh()
	if q.err != nil {
		return q
	}
	if q.sliced() || q.plan.ResultShape().Kind() != query.ResultProjection {
		q.err = invalidResultBuilder("filtering a sliced or aggregate result requires a new result stage")
		return q
	}
	if len(predicates) > query.MaximumScalarNodes {
		q.err = invalidResultBuilder("too many result predicates")
		return q
	}
	for _, predicate := range predicates {
		if predicate.err != nil {
			q.err = predicate.err
			return q
		}
		q.plan, q.err = q.plan.WithWhere(predicate.expression)
		if q.err != nil {
			return q
		}
	}
	q.err = q.validatePlan()
	return q
}
func (q ResultQuery[M, R]) OrderBy(orderings ...Ordering[M]) ResultQuery[M, R] {
	q = q.Fresh()
	if q.err != nil {
		return q
	}
	if q.sliced() || q.plan.ResultShape().Kind() != query.ResultProjection || len(orderings) > query.MaxProjectionExpressions {
		q.err = invalidResultBuilder("ordering requires an unsliced projection and a bounded order list")
		return q
	}
	values := make([]query.Ordering, len(orderings))
	for index, ordering := range orderings {
		if ordering.err != nil {
			q.err = ordering.err
			return q
		}
		values[index] = ordering.ordering
	}
	q.plan = q.plan.WithOrderings(values...)
	q.err = q.validatePlan()
	return q
}
func (q ResultQuery[M, R]) Distinct() ResultQuery[M, R] {
	q = q.Fresh()
	if q.err != nil {
		return q
	}
	if q.sliced() || q.plan.ResultShape().Kind() != query.ResultProjection {
		q.err = invalidResultBuilder("DISTINCT requires an unsliced projection")
		return q
	}
	q.plan = q.plan.WithDistinct()
	q.err = q.validatePlan()
	return q
}
func (q ResultQuery[M, R]) Limit(limit int) (ResultQuery[M, R], error) {
	if err := q.validatePlan(); err != nil {
		return q, err
	}
	if q.plan.ResultShape().Kind() != query.ResultProjection {
		return ResultQuery[M, R]{}, invalidResultBuilder("slicing an aggregate result requires a new result stage")
	}
	plan, err := q.plan.WithLimit(limit)
	if err == nil {
		err = plan.ValidateDerived()
	}
	if err != nil {
		return ResultQuery[M, R]{}, err
	}
	q = q.Fresh()
	q.plan = plan
	return q, nil
}
func (q ResultQuery[M, R]) Offset(offset int) (ResultQuery[M, R], error) {
	if err := q.validatePlan(); err != nil {
		return q, err
	}
	if q.plan.ResultShape().Kind() != query.ResultProjection {
		return ResultQuery[M, R]{}, invalidResultBuilder("slicing an aggregate result requires a new result stage")
	}
	plan, err := q.plan.WithOffset(offset)
	if err == nil {
		err = plan.ValidateDerived()
	}
	if err != nil {
		return ResultQuery[M, R]{}, err
	}
	q = q.Fresh()
	q.plan = plan
	return q, nil
}
func (q ResultQuery[M, R]) All(ctx context.Context) ([]R, error) {
	if err := q.validate(ctx); err != nil {
		return nil, err
	}
	values, err := q.evaluation.evaluate(ctx, func(ctx context.Context) ([]resultDecoder[R], error) {
		return scanResultCells(ctx, q.origin.backend, q.plan, q.newDecoder)
	})
	if err != nil {
		return nil, err
	}
	return publishResultCells(ctx, q.origin.backend, values)
}

func (q ResultQuery[M, R]) Count(ctx context.Context) (int64, error) {
	if err := q.validate(ctx); err != nil {
		return 0, err
	}
	if values, ready := q.evaluation.cachedValues(); ready {
		return sessionReadResult(ctx, q.origin.backend, int64(len(values)), nil)
	}
	plan, _, err := query.NewDerivedPlan(q.plan)
	if err != nil {
		return 0, err
	}
	shape, err := query.NewAggregateResult(query.CountAllResult())
	if err != nil {
		return 0, err
	}
	plan, err = plan.WithResultShape(shape)
	if err != nil {
		return 0, err
	}
	factory := func() resultDecoder[int64] {
		cell := CountRows[M]().newCell()
		return resultDecoder[int64]{destinations: []any{cell.destination}, decode: cell.value}
	}
	values, err := scanResultCells(ctx, q.origin.backend, plan, factory)
	if err != nil {
		return 0, err
	}
	result, err := publishResultCells(ctx, q.origin.backend, values)
	if err != nil {
		return 0, err
	}
	if result[0] < 0 {
		return 0, invalidResultBuilder("result count is negative")
	}
	return result[0], nil
}

// One evaluates a terminal aggregate row. It rejects missing or additional
// rows before invoking any builder and never caches malformed backend output.
func (q ResultQuery[M, R]) One(ctx context.Context) (R, error) {
	var zero R
	if err := q.validate(ctx); err != nil {
		return zero, err
	}
	if q.plan.ResultShape().Kind() != query.ResultAggregate {
		return zero, invalidResultBuilder("One requires an aggregate result")
	}
	values, err := q.evaluation.evaluate(ctx, func(ctx context.Context) ([]resultDecoder[R], error) {
		return scanResultCells(ctx, q.origin.backend, q.plan, q.newDecoder)
	})
	if err != nil {
		return zero, err
	}
	result, err := publishResultCells(ctx, q.origin.backend, values)
	if err != nil {
		return zero, err
	}
	return result[0], nil
}

func scanResultCells[R any](ctx context.Context, backend db.Queryer, plan query.Plan, newDecoder func() resultDecoder[R]) ([]resultDecoder[R], error) {
	rows, err := openQueryRows(ctx, backend, plan)
	if err != nil {
		return nil, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	values := make([]resultDecoder[R], 0)
	single := plan.ResultShape().Kind() == query.ResultAggregate
	for rows.Next() {
		if single && len(values) != 0 {
			err = &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "aggregate query returned more than one row"}
			break
		}
		decoder := newDecoder()
		if err = rows.Scan(decoder.destinations...); err != nil {
			break
		}
		values = append(values, decoder)
	}
	if err = lifecycle.finish(ctx, err); err != nil {
		return sessionReadResult(ctx, backend, []resultDecoder[R](nil), err)
	}
	if single && len(values) != 1 {
		return sessionReadResult(ctx, backend, []resultDecoder[R](nil), &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "aggregate query returned no row"})
	}
	return sessionReadResult(ctx, backend, values, nil)
}
func publishResultCells[R any](ctx context.Context, backend db.Queryer, values []resultDecoder[R]) ([]R, error) {
	if err := validateQuerySession(ctx, backend); err != nil {
		return nil, err
	}
	result := make([]R, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, value.decode())
	}
	return sessionReadResult(ctx, backend, result, ctx.Err())
}
