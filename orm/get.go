package orm

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/query"
)

// getResultLimit preserves useful small-cardinality diagnostics while bounding
// a single-object lookup. A row count at the limit means at least that many.
const getResultLimit = 21

// Get evaluates a fresh, private query and returns exactly one model. It never
// reads or populates this QuerySet's full-result cache. No match and multiple
// matches are distinguished by CodeDoesNotExist and CodeMultipleObjectsReturned.
func (qs QuerySet[M]) Get(ctx context.Context) (M, error) {
	return requireGet(qs.lookupGet(ctx))
}

// Absence is carried separately from an error returned by a backend/descriptor.
// A downstream does_not_exist error must never manufacture a creation attempt.
func (qs QuerySet[M]) lookupGet(ctx context.Context) (M, bool, error) {
	var zero M
	if err := qs.validateTerminal(ctx); err != nil {
		return zero, false, err
	}
	values, _, err := qs.getValues(ctx)
	if err != nil {
		return zero, false, err
	}
	if len(values) == 0 {
		return zero, false, nil
	}
	value, err := sessionReadResult(ctx, qs.backend, qs.descriptor.CloneModel(values[0]), nil)
	return value, err == nil, err
}

func (qs QuerySet[M]) getValues(ctx context.Context) ([]M, any, error) {
	plan, err := singleObjectPlan(qs.plan)
	if err != nil {
		return nil, nil, err
	}
	qs.plan = plan
	qs.evaluation = newEvaluationState[M]()
	values, attachment, err := qs.evaluateModels(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := validateQuerySession(ctx, qs.backend); err != nil {
		return nil, nil, err
	}
	if len(values) > 1 {
		err := singleObjectError(len(values))
		return nil, nil, err
	}
	return values, attachment, nil
}

func requireGet[M any](value M, found bool, err error) (M, error) {
	if err != nil {
		return value, err
	}
	if !found {
		var zero M
		return zero, singleObjectError(0)
	}
	return value, nil
}

func singleObjectPlan(plan query.Plan) (query.Plan, error) {
	// Clearing an ordering must not hide a foreign or malformed field reference.
	if err := plan.ValidateOrderings(); err != nil {
		return query.Plan{}, err
	}
	offset, _ := plan.Offset()
	if _, limited := plan.Limit(); !limited && offset == 0 {
		plan = plan.WithOrderings()
	}
	return planWithMaximumRows(plan, getResultLimit), nil
}

func singleObjectError(count int) error {
	switch count {
	case 0:
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeDoesNotExist, Detail: "query returned no matching model"}
	case 1:
		return nil
	default:
		detail := fmt.Sprintf("query returned %d models, expected exactly one", count)
		if count >= getResultLimit {
			detail = fmt.Sprintf("query returned more than %d models, expected exactly one", getResultLimit-1)
		}
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeMultipleObjectsReturned, Detail: detail}
	}
}

// MaterializeGet returns the single model and its privately owned eager and
// prefetched graph. It has the same fresh evaluation and cardinality as Get.
func MaterializeGet[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M]) (*RelatedSelected[M], error) {
	return requireGet(materializeLookupGet(ctx, source, binding))
}

func materializeLookupGet[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M]) (*RelatedSelected[M], bool, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return nil, false, err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return nil, false, err
	}
	raw, attachment, err := source.getValues(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		return nil, false, nil
	}
	values, err := materializationValues(binding, raw, attachment)
	if err != nil {
		return nil, false, err
	}
	value, err := cloneRelatedSelection(source.backend, binding, source.descriptor, values[0])
	value, err = sessionReadResult(ctx, source.backend, value, err)
	return value, err == nil, err
}

// Get preserves the selected graph while evaluating a fresh single-object read.
func (q RelatedSelectQuery[S]) Get(ctx context.Context) (*RelatedSelected[S], error) {
	return requireGet(q.lookupGet(ctx))
}

func (q RelatedSelectQuery[S]) lookupGet(ctx context.Context) (*RelatedSelected[S], bool, error) {
	if err := q.validateTerminal(ctx); err != nil {
		return nil, false, err
	}
	plan, err := singleObjectPlan(q.plan)
	if err != nil {
		return nil, false, err
	}
	values, err := q.scan(ctx, plan, 0)
	if err != nil {
		return nil, false, err
	}
	if err := validateQuerySession(ctx, q.backend); err != nil {
		return nil, false, err
	}
	if len(values) == 0 {
		return nil, false, nil
	}
	if err := singleObjectError(len(values)); err != nil {
		return nil, false, err
	}
	value, err := q.cloneSelection(values[0])
	value, err = sessionReadResult(ctx, q.backend, value, err)
	return value, err == nil, err
}

// Get preserves configured prefetches without using or changing a warm cache.
func (q PrefetchQuery[S]) Get(ctx context.Context) (*RelatedSelected[S], error) {
	return requireGet(q.lookupGet(ctx))
}

func (q PrefetchQuery[S]) lookupGet(ctx context.Context) (*RelatedSelected[S], bool, error) {
	if err := q.validate(ctx); err != nil {
		return nil, false, err
	}
	plan, err := singleObjectPlan(q.source.plan)
	if err != nil {
		return nil, false, err
	}
	values, err := q.load(ctx, plan, 0)
	if err != nil {
		return nil, false, err
	}
	if err := validateQuerySession(ctx, q.source.backend); err != nil {
		return nil, false, err
	}
	if len(values) == 0 {
		return nil, false, nil
	}
	if err := singleObjectError(len(values)); err != nil {
		return nil, false, err
	}
	value, err := q.clone(values[0])
	value, err = sessionReadResult(ctx, q.source.backend, value, err)
	return value, err == nil, err
}
