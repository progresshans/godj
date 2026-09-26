package query

import "slices"

// PrefetchWindow applies the plan's limit/offset separately to each membership
// owner. It is constructed only by the owner-binding factories, which retain
// the required membership predicate and, when necessary, late output ownership.
// Its private storage is immutable and its container accessors return copies.
type PrefetchWindow struct {
	partition  ResultExpression
	membership Condition
	owner      ResultExpression
	lateOwner  bool
}

func (w PrefetchWindow) Partition() ResultExpression { return w.partition }

// OwnerFilter runs after window qualification. Moving it into the inner WHERE
// changes ranks when grouping and membership use different intermediary joins.
func (w PrefetchWindow) OwnerFilter() (ResultExpression, []Value, bool) {
	if !w.lateOwner {
		return ResultExpression{}, nil, false
	}
	owners, _ := w.membership.Values()
	return w.owner, owners, true
}

func (p Plan) PrefetchWindow() (PrefetchWindow, bool) {
	if p.prefetchWindow == nil {
		return PrefetchWindow{}, false
	}
	return *p.prefetchWindow, true
}

func (p Plan) hasPrefetchSlice() bool { return p.limit != nil || p.offset != nil && *p.offset != 0 }

func (w *PrefetchWindow) equal(other *PrefetchWindow) bool {
	if w == nil || other == nil {
		return w == other
	}
	return w.partition.Equal(other.partition) && w.membership.Equal(other.membership) &&
		w.owner.Equal(other.owner) && w.lateOwner == other.lateOwner
}

// ForPrefetchForeignKey binds a reverse collection's target rows to the given
// owner keys. A target slice is evaluated per owner, using the same window
// representation as an intermediary-backed ManyToMany query.
func (p Plan) ForPrefetchForeignKey(field FieldRef, owners []int64) (Plan, error) {
	if p.result.Kind() != ResultModel || p.prefetchWindow != nil {
		return Plan{}, invalidPlanError("prefetch foreign key requires a model result")
	}
	if field.Kind() != FieldInteger || !slices.Contains(p.sourceFields, field) {
		return Plan{}, invalidPlanError("prefetch foreign key is not an integer field of the model source")
	}
	values := make([]Value, len(owners))
	for i, key := range owners {
		values[i] = Integer(key)
	}
	condition, err := NewInCondition(field, values)
	if err != nil {
		return Plan{}, err
	}
	result, err := p.WithoutCollectionFilterReuse().WithConditions(condition)
	if err != nil {
		return Plan{}, err
	}
	if p.hasPrefetchSlice() {
		result.prefetchWindow = &PrefetchWindow{
			partition: ResultExpression{kind: ResultField, field: field}, membership: condition,
		}
	}
	return result, result.ValidatePrefetch()
}

func hasPrefetchAnchor(expression Expression, anchor Condition) bool {
	if expression.Kind() == ExpressionLeaf {
		condition, _ := expression.Condition()
		return condition.Equal(anchor)
	}
	if expression.Kind() == ExpressionAnd {
		for _, child := range expression.Children() {
			if hasPrefetchAnchor(child, anchor) {
				return true
			}
		}
	}
	return false
}

// ValidatePrefetch is also called by backends before empty-source elision.
// A result shape or derived plan cannot discard its ownership proof.
func (p Plan) ValidatePrefetch() error {
	if p.result.Kind() == ResultPrefetch {
		if p.hasPrefetchSlice() && p.prefetchWindow == nil {
			return invalidPlanError("configure the target slice before binding prefetch owners")
		}
		if err := p.validatePrefetchSource(p.result); err != nil {
			return err
		}
	}
	w := p.prefetchWindow
	if w == nil {
		return nil
	}
	if !p.hasPrefetchSlice() || !hasPrefetchAnchor(p.where, w.membership) {
		return invalidPlanError("prefetch window has no slice or required membership anchor")
	}
	if w.owner.kind != "" {
		if p.result.Kind() != ResultPrefetch || len(p.result.expressions) != 1 || !p.result.expressions[0].Equal(w.owner) {
			return invalidPlanError("prefetch window lost its owner result")
		}
	} else if p.result.Kind() != ResultModel || w.lateOwner {
		return invalidPlanError("prefetch foreign-key window requires model rows")
	}
	return nil
}
