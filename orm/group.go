package orm

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/progresshans/godj/query"
)

// GroupedQuery owns an immutable result plan and an evaluation cache separate
// from its source model query. The cache contains private scanned cells, never
// caller-built DTOs. Each publication invokes the pure builder with fresh
// nullable values. Copies share successful evaluation; refinements are cold.
type GroupedQuery[M, R any] struct {
	source     QuerySet[M]
	plan       query.Plan
	newDecoder func() resultDecoder[R]
	evaluation *evaluationState[resultDecoder[R]]
	err        error
}

func GroupBy[M, K, A, R any](source QuerySet[M], keys Projection[M, K], aggregate Aggregate[M, A], build func(K, A) R) (GroupedQuery[M, R], error) {
	var zero GroupedQuery[M, R]
	if err := firstError(source.configurationErr, keys.err, aggregate.err); err != nil {
		return zero, err
	}
	if build == nil || keys.newDecoder == nil || aggregate.newDecoder == nil {
		return zero, invalidResultBuilder("group result requires non-nil builders")
	}
	if source.materialization != nil && (len(source.materialization.targets) > 0 || len(source.materialization.selections) > 0) {
		return zero, &query.Error{Category: query.CategoryQuery, Code: query.CodeUnsupported, Detail: "grouping requires a source without eager or prefetch selections"}
	}
	shape, err := query.NewGroupedResult(keys.expressions, aggregate.expressions)
	if err != nil {
		return zero, err
	}
	plan, err := source.plan.WithResultShape(shape)
	if err != nil {
		return zero, err
	}
	result := GroupedQuery[M, R]{source: source, plan: plan, evaluation: newEvaluationState[resultDecoder[R]]()}
	result.newDecoder = func() resultDecoder[R] {
		key, value := keys.newDecoder(), aggregate.newDecoder()
		return resultDecoder[R]{destinations: append(slices.Clone(key.destinations), value.destinations...), decode: func() R { return build(key.decode(), value.decode()) }}
	}
	return result, nil
}

func (q GroupedQuery[M, R]) Plan() query.Plan { return q.plan }
func (q GroupedQuery[M, R]) Fresh() GroupedQuery[M, R] {
	q.evaluation = newEvaluationState[resultDecoder[R]]()
	return q
}
func (q GroupedQuery[M, R]) sliced() bool {
	_, limit := q.plan.Limit()
	_, offset := q.plan.Offset()
	return limit || offset
}
func (q GroupedQuery[M, R]) Having(predicates ...GroupPredicate[M]) GroupedQuery[M, R] {
	q = q.Fresh()
	if q.err != nil {
		return q
	}
	if q.sliced() {
		q.err = invalidResultBuilder("HAVING cannot follow a group slice")
		return q
	}
	if len(predicates) > query.MaxGroupExpressionNodes {
		q.err = invalidResultBuilder("too many HAVING predicates")
		return q
	}
	for _, predicate := range predicates {
		if predicate.err != nil {
			q.err = predicate.err
			break
		}
		q.plan, q.err = q.plan.WithGroupHaving(predicate.expression)
		if q.err != nil {
			break
		}
	}
	return q
}
func (q GroupedQuery[M, R]) OrderBy(orderings ...GroupOrder[M]) GroupedQuery[M, R] {
	q = q.Fresh()
	if q.err != nil {
		return q
	}
	if q.sliced() {
		q.err = invalidResultBuilder("ordering cannot follow a group slice")
		return q
	}
	if len(orderings) > query.MaxGroupKeys+query.MaxAggregateExpressions {
		q.err = invalidResultBuilder("too many group orderings")
		return q
	}
	values := make([]query.GroupOrdering, len(orderings))
	for index, ordering := range orderings {
		if interfaceIsNil(ordering) {
			q.err = invalidResultBuilder("group ordering is nil")
			return q
		}
		values[index], q.err = ordering.groupOrdering(*new(M))
		if q.err != nil {
			return q
		}
	}
	q.plan, q.err = q.plan.WithGroupOrderings(values...)
	return q
}
func (q GroupedQuery[M, R]) Limit(limit int) (GroupedQuery[M, R], error) {
	if q.err != nil {
		return q, q.err
	}
	plan, err := q.plan.WithLimit(limit)
	if err != nil {
		return GroupedQuery[M, R]{}, err
	}
	q = q.Fresh()
	q.plan = plan
	return q, nil
}
func (q GroupedQuery[M, R]) Offset(offset int) (GroupedQuery[M, R], error) {
	if q.err != nil {
		return q, q.err
	}
	plan, err := q.plan.WithOffset(offset)
	if err != nil {
		return GroupedQuery[M, R]{}, err
	}
	q = q.Fresh()
	q.plan = plan
	return q, nil
}
func (q GroupedQuery[M, R]) validate(ctx context.Context) error {
	if err := q.source.validateTerminal(ctx); err != nil {
		return err
	}
	if q.err != nil {
		return q.err
	}
	if q.evaluation == nil || q.newDecoder == nil || q.plan.ResultShape().GroupMode() != query.GroupRows {
		return invalidResultBuilder("group query is uninitialized")
	}
	return q.plan.ValidateGrouping()
}
func (q GroupedQuery[M, R]) All(ctx context.Context) ([]R, error) {
	if err := q.validate(ctx); err != nil {
		return nil, err
	}
	values, err := q.evaluation.evaluate(ctx, q.scanAll)
	if err != nil {
		return nil, err
	}
	return q.decode(ctx, values)
}
func (q GroupedQuery[M, R]) scanAll(ctx context.Context) ([]resultDecoder[R], error) {
	rows, err := openQueryRows(ctx, q.source.backend, q.plan)
	if err != nil {
		return nil, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	values := make([]resultDecoder[R], 0)
	for rows.Next() {
		decoder := q.newDecoder()
		if err = rows.Scan(decoder.destinations...); err != nil {
			break
		}
		values = append(values, decoder)
	}
	if err = lifecycle.finish(ctx, err); err != nil {
		return sessionReadResult(ctx, q.source.backend, []resultDecoder[R](nil), err)
	}
	return sessionReadResult(ctx, q.source.backend, values, nil)
}
func (q GroupedQuery[M, R]) decode(ctx context.Context, values []resultDecoder[R]) ([]R, error) {
	if err := validateQuerySession(ctx, q.source.backend); err != nil {
		return nil, err
	}
	result := make([]R, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result = append(result, value.decode())
	}
	return sessionReadResult(ctx, q.source.backend, result, ctx.Err())
}
func (q GroupedQuery[M, R]) Count(ctx context.Context) (int64, error) {
	if err := q.validate(ctx); err != nil {
		return 0, err
	}
	if values, ready := q.evaluation.cachedValues(); ready {
		return sessionReadResult(ctx, q.source.backend, int64(len(values)), nil)
	}
	plan, err := q.plan.WithGroupMode(query.GroupCount)
	if err != nil {
		return 0, err
	}
	rows, err := openQueryRows(ctx, q.source.backend, plan)
	if err != nil {
		return 0, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	var count sql.NullInt64
	if rows.Next() {
		err = rows.Scan(&count)
		if err == nil && (!count.Valid || count.Int64 < 0 || rows.Next()) {
			err = invalidResultBuilder("group count returned an invalid result")
		}
	} else {
		if err := lifecycle.finish(ctx, nil); err != nil {
			return sessionReadResult(ctx, q.source.backend, int64(0), err)
		}
		return sessionReadResult(ctx, q.source.backend, int64(0), invalidResultBuilder("group count returned no row"))
	}
	if err = lifecycle.finish(ctx, err); err != nil {
		return sessionReadResult(ctx, q.source.backend, int64(0), err)
	}
	return sessionReadResult(ctx, q.source.backend, count.Int64, nil)
}

// GroupPage reports the total number of groups after HAVING and before the
// requested slice. Rows and total come from one statement snapshot. A page is
// a fresh read and never populates a whole-query cache with partial results.
type GroupPage[R any] struct {
	Total int64
	Rows  []R
}

func (q GroupedQuery[M, R]) Page(ctx context.Context, limit, offset int) (GroupPage[R], error) {
	var zero GroupPage[R]
	if err := q.validate(ctx); err != nil {
		return zero, err
	}
	if q.sliced() {
		return zero, invalidResultBuilder("Page requires an unsliced grouped query")
	}
	// Missing keys are explicit deterministic tie breakers. They do not add
	// hidden grouping columns or change which groups exist.
	orderings := q.plan.ResultShape().GroupOrderings()
	for _, key := range q.plan.ResultShape().GroupKeys() {
		if slices.ContainsFunc(orderings, func(order query.GroupOrdering) bool { return order.Expression().Equal(key) }) {
			continue
		}
		ordering, err := query.NewGroupOrdering(key, query.Ascending, query.NullsLast)
		if err != nil {
			return zero, err
		}
		orderings = append(orderings, ordering)
	}
	plan, err := q.plan.WithGroupOrderings(orderings...)
	if err != nil {
		return zero, err
	}
	plan, err = plan.WithLimit(limit)
	if err != nil {
		return zero, err
	}
	plan, err = plan.WithOffset(offset)
	if err != nil {
		return zero, err
	}
	plan, err = plan.WithGroupMode(query.GroupPage)
	if err != nil {
		return zero, err
	}
	rows, err := openQueryRows(ctx, q.source.backend, plan)
	if err != nil {
		return zero, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	values := make([]resultDecoder[R], 0)
	var total int64
	seen, sentinel := false, false
	for rows.Next() {
		raw := make([]any, len(plan.ResultShape().Expressions())+2)
		targets := make([]any, len(raw))
		for index := range raw {
			targets[index] = &raw[index]
		}
		if err = rows.Scan(targets...); err != nil {
			break
		}
		var count, present sql.NullInt64
		if err = count.Scan(raw[0]); err != nil {
			break
		}
		if err = present.Scan(raw[1]); err != nil {
			break
		}
		if !count.Valid || count.Int64 < 0 || seen && count.Int64 != total || sentinel {
			err = invalidResultBuilder("group page returned inconsistent totals or presence")
			break
		}
		total, seen = count.Int64, true
		if !present.Valid {
			if len(values) != 0 {
				err = invalidResultBuilder("group page mixed rows and empty sentinel")
				break
			}
			for _, value := range raw[2:] {
				if value != nil {
					err = invalidResultBuilder("empty group page contains values")
					break
				}
			}
			if err != nil {
				break
			}
			sentinel = true
			continue
		}
		if present.Int64 != 1 || len(values) >= limit {
			err = invalidResultBuilder("group page exceeded its bound or returned invalid presence")
			break
		}
		decoder := q.newDecoder()
		for index, destination := range decoder.destinations {
			if err = scanGroupValue(destination, raw[index+2]); err != nil {
				err = fmt.Errorf("scan group page cell %d: %w", index, err)
				break
			}
		}
		if err != nil {
			break
		}
		values = append(values, decoder)
	}
	if err = lifecycle.finish(ctx, err); err != nil {
		return sessionReadResult(ctx, q.source.backend, zero, err)
	}
	if err := validateQuerySession(ctx, q.source.backend); err != nil {
		return zero, err
	}
	if !seen && !plan.EmptyResult() {
		return zero, invalidResultBuilder("group page returned no count row")
	}
	expected := max(int64(0), min(int64(limit), total-int64(offset)))
	if int64(len(values)) != expected {
		return zero, invalidResultBuilder("group page count and rows disagree")
	}
	result, err := q.decode(ctx, values)
	if err != nil {
		return zero, err
	}
	return sessionReadResult(ctx, q.source.backend, GroupPage[R]{Total: total, Rows: result}, nil)
}

// Page's outer join uses NULL cells for its empty-row sentinel. Conversion is
// deferred until presence is known. Real cells use the same sql.Scanner types
// as ordinary projections; primitive destinations use database/sql conversion.
func scanGroupValue(destination, raw any) error {
	if bytes, ok := raw.([]byte); ok {
		raw = slices.Clone(bytes)
	}
	if scanner, ok := destination.(sql.Scanner); ok {
		return scanner.Scan(raw)
	}
	switch destination := destination.(type) {
	case *int64:
		return scanGroupPrimitive(destination, raw)
	case *string:
		return scanGroupPrimitive(destination, raw)
	case *bool:
		return scanGroupPrimitive(destination, raw)
	default:
		return invalidResultBuilder("group decoder has an unsupported scalar destination")
	}
}
func scanGroupPrimitive[V any](destination *V, raw any) error {
	var value sql.Null[V]
	if err := value.Scan(raw); err != nil {
		return err
	}
	if !value.Valid {
		return invalidResultBuilder("non-nullable group cell is NULL")
	}
	*destination = value.V
	return nil
}
