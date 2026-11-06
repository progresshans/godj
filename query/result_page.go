package query

import "slices"

// PageOrdering addresses an actual input cell, including private ordering
// cells added to a non-distinct projection. It is never a model field alias.
type PageOrdering struct {
	index     int
	direction Direction
	nulls     NullOrder
}

func (order PageOrdering) Index() int           { return order.index }
func (order PageOrdering) Direction() Direction { return order.direction }
func (order PageOrdering) Nulls() NullOrder     { return order.nulls }

type resultPageSource struct {
	input Plan
	order []PageOrdering
	depth int
	nodes int
}

// NewPagePlan captures scalar rows once for their total and requested page.
// Input filtering, DISTINCT, grouping and selected arithmetic stay inside the
// snapshot. Hidden order cells never enter the published row or DISTINCT key.
// Existing slices require an explicit preceding result stage.
func NewPagePlan(input Plan, limit, offset int) (Plan, error) {
	var zero Plan
	if err := input.ValidatePage(); err != nil {
		return zero, err
	}
	if limit < 0 || offset < 0 {
		return zero, invalidPlanError("result page requires non-negative limit and offset")
	}
	if input.ResultStageDepth() >= MaximumResultStages {
		return zero, invalidPlanError("result page exceeds its stage budget")
	}
	if _, present := input.Limit(); present {
		return zero, groupUnsupported("paging a sliced result requires a new result stage")
	}
	if _, present := input.Offset(); present {
		return zero, groupUnsupported("paging a sliced result requires a new result stage")
	}
	if _, locked := input.RowLock(); locked {
		return zero, groupUnsupported("result pages cannot transfer row locks")
	}
	if err := input.ValidateDerived(); err != nil {
		return zero, err
	}
	if err := input.ValidateGrouping(); err != nil {
		return zero, err
	}
	if err := input.ValidatePrefetch(); err != nil {
		return zero, err
	}
	if err := input.ValidateOrderings(); err != nil {
		return zero, err
	}
	shape := input.ResultShape()
	if shape.Kind() != ResultProjection && (shape.Kind() != ResultGrouped || shape.GroupMode() != GroupRows) {
		return zero, invalidPlanError("result page requires explicit projection or group rows")
	}
	if len(input.orderings) > MaxProjectionExpressions {
		return zero, invalidPlanError("too many page orderings")
	}
	selected := shape.Expressions()
	published := slices.Clone(selected)
	order := make([]PageOrdering, 0)
	addOrder := func(value ResultExpression, direction Direction, nulls NullOrder) error {
		index := slices.IndexFunc(selected, func(selected ResultExpression) bool { return selected.Equal(value) })
		if index < 0 {
			if input.Distinct() || shape.Kind() != ResultProjection {
				return groupUnsupported("a distinct or grouped page can only order by selected values")
			}
			if len(selected) == MaxProjectionExpressions {
				return invalidPlanError("page ordering exceeds its selection budget")
			}
			index = len(selected)
			selected = append(selected, value)
		}
		order = append(order, PageOrdering{index: index, direction: direction, nulls: nulls})
		return nil
	}
	for _, ordering := range input.Orderings() {
		if err := addOrder(ordering.Expression(), ordering.Direction(), ordering.Nulls()); err != nil {
			return zero, err
		}
	}
	for _, ordering := range shape.GroupOrderings() {
		if err := addOrder(ordering.Expression(), ordering.Direction(), ordering.Nulls()); err != nil {
			return zero, err
		}
	}
	// Equal complete rows are indistinguishable to this scalar API. Appending
	// selected keys gives deterministic ties without changing multiplicity.
	ties := published
	if shape.Kind() == ResultGrouped {
		ties = shape.GroupKeys()
	}
	for _, key := range ties {
		index := slices.IndexFunc(selected, func(selected ResultExpression) bool { return selected.Equal(key) })
		if slices.ContainsFunc(order, func(order PageOrdering) bool { return order.index == index }) {
			continue
		}
		if err := addOrder(key, Ascending, NullsLast); err != nil {
			return zero, err
		}
	}
	if len(selected) != len(published) {
		extended, err := NewProjectionResult(selected...)
		if err != nil {
			return zero, err
		}
		input, err = input.WithResultShape(extended)
		if err != nil {
			return zero, err
		}
	}
	plan := Plan{result: ResultShape{kind: ResultPage, expressions: published}, limit: &limit, offset: &offset,
		page: &resultPageSource{input: input, order: order, depth: input.ResultStageDepth() + 1, nodes: input.resultStageNodes()}}
	if err := plan.ValidatePage(); err != nil {
		return zero, err
	}
	return plan, nil
}

func (plan Plan) PageInput() (Plan, []PageOrdering, bool) {
	if plan.page == nil {
		return Plan{}, nil, false
	}
	return plan.page.input, slices.Clone(plan.page.order), true
}
func (plan Plan) ValidatePage() error {
	if plan.page == nil {
		if plan.result.kind == ResultPage {
			return invalidPlanError("page result has no input")
		}
		return nil
	}
	p := plan.page
	if plan.result.kind != ResultPage || plan.table != "" || len(plan.sourceFields) != 0 || plan.where.node != nil ||
		plan.derived != nil || len(plan.orderings) != 0 || plan.distinct || plan.rowLock != nil || plan.prefetchWindow != nil ||
		len(plan.relationProjections) != 0 || plan.collectionFilters != 0 || plan.reuseCollectionFilter || plan.result.group != nil ||
		plan.limit == nil || plan.offset == nil || *plan.limit < 0 || *plan.offset < 0 {
		return invalidPlanError("page envelope contains invalid source or result state")
	}
	if p.depth < 2 || p.depth > MaximumResultStages || p.depth != p.input.ResultStageDepth()+1 || plan.resultStageNodes() > MaximumDerivedNodes {
		return invalidPlanError("result page exceeds its stage or node budget")
	}
	selected := p.input.ResultShape().expressions
	if len(plan.result.expressions) == 0 || len(plan.result.expressions) > len(selected) ||
		!slices.EqualFunc(plan.result.expressions, selected[:len(plan.result.expressions)], ResultExpression.Equal) || len(p.order) == 0 {
		return invalidPlanError("page envelope has an invalid selection")
	}
	for _, order := range p.order {
		if order.index < 0 || order.index >= len(selected) || (order.direction != Ascending && order.direction != Descending) ||
			(order.nulls != NullsNative && order.nulls != NullsFirst && order.nulls != NullsLast) {
			return invalidPlanError("page envelope has invalid ordering")
		}
	}
	return nil
}
func equalPageSources(left, right *resultPageSource) bool {
	if left == right {
		return true
	}
	return left != nil && right != nil && left.input.Equal(right.input) && slices.Equal(left.order, right.order)
}
