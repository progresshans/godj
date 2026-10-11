package query

// MaximumDerivedNodes bounds the complete staged query, not just each scalar
// expression or selection. Input snapshots retain their already checked cost.
const MaximumDerivedNodes = 65536

type derivedSource struct {
	input Plan
	scope ResultScope
	depth int
	nodes int
}

// NewDerivedPlan starts a read stage over the complete ordered input plan.
// In particular it does not move a filter, DISTINCT, ordering or slice across
// the boundary. The returned scope is the only authority for input columns.
func NewDerivedPlan(input Plan) (Plan, ResultScope, error) {
	var zero Plan
	if err := input.ValidatePage(); err != nil {
		return zero, ResultScope{}, err
	}
	if input.ResultStageDepth() >= MaximumResultStages {
		return zero, ResultScope{}, invalidPlanError("derived query exceeds its stage budget")
	}
	if _, locked := input.RowLock(); locked {
		return zero, ResultScope{}, groupUnsupported("derived results do not transfer row locks across a result boundary")
	}
	if err := input.ValidateDerived(); err != nil {
		return zero, ResultScope{}, err
	}
	if err := input.ValidateGrouping(); err != nil {
		return zero, ResultScope{}, err
	}
	if err := input.ValidatePrefetch(); err != nil {
		return zero, ResultScope{}, err
	}
	if err := input.ValidateOrderings(); err != nil {
		return zero, ResultScope{}, err
	}
	scope, err := newResultScope(input.result, input.ResultStageDepth())
	if err != nil {
		return zero, ResultScope{}, err
	}
	columns := make([]ResultExpression, scope.ColumnCount())
	for index := range columns {
		ref, err := scope.Column(index)
		if err != nil {
			return zero, ResultScope{}, err
		}
		value, err := SelectedExpression(ref)
		if err != nil {
			return zero, ResultScope{}, err
		}
		columns[index], err = ScalarResult(value)
		if err != nil {
			return zero, ResultScope{}, err
		}
	}
	result, err := NewProjectionResult(columns...)
	if err != nil {
		return zero, ResultScope{}, err
	}
	plan := Plan{result: result, derived: &derivedSource{input: input, scope: scope,
		depth: input.ResultStageDepth() + 1, nodes: input.resultStageNodes()}}
	if err := plan.ValidateDerived(); err != nil {
		return zero, ResultScope{}, err
	}
	return plan, scope, nil
}

// DerivedSource returns immutable handles. Refining the returned input cannot
// change the source retained by this plan or the identity of its column scope.
func (p Plan) DerivedSource() (Plan, ResultScope, bool) {
	if p.derived == nil {
		return Plan{}, ResultScope{}, false
	}
	return p.derived.input, p.derived.scope, true
}
func (p Plan) ResultStageDepth() int {
	if p.page != nil {
		return p.page.depth
	}
	if p.derived == nil {
		return 1
	}
	return p.derived.depth
}

func (p Plan) resultStageNodes() int {
	nodes := len(p.sourceFields) + len(p.orderings) + len(p.result.expressions) + 1
	if p.derived != nil {
		nodes += p.derived.nodes
	}
	if p.page != nil {
		nodes += p.page.nodes + len(p.page.order)
	}
	if p.where.node != nil {
		nodes += p.where.node.nodes
	}
	for _, selected := range p.result.expressions {
		nodes += selected.scalar.NodeCount()
		if selected.filter.node != nil {
			nodes += selected.filter.node.nodes
		}
		if selected.relation != nil {
			nodes += len(selected.relation.hops)
		}
	}
	for _, ordering := range p.orderings {
		nodes += ordering.expression.scalar.NodeCount()
	}
	if p.result.group != nil && p.result.group.having.node != nil {
		nodes += p.result.group.having.node.nodes
	}
	return nodes
}

// ValidateDerived checks the active stage before empty/aggregate execution can
// elide its expressions. Its private input was validated when snapshotted.
func (p Plan) ValidateDerived() error {
	if p.derived == nil {
		return nil
	}
	d := p.derived
	if d.depth < 2 || d.depth > MaximumResultStages || d.depth != d.input.ResultStageDepth()+1 ||
		!d.scope.Valid() || d.scope.Depth() != d.input.ResultStageDepth() || !d.scope.Shape().Equal(d.input.result) {
		return invalidPlanError("derived query has an invalid input scope")
	}
	if p.page != nil || p.table != "" || len(p.sourceFields) != 0 || len(p.relationProjections) != 0 || p.prefetchWindow != nil || p.rowLock != nil ||
		p.collectionFilters != 0 || p.reuseCollectionFilter {
		return invalidPlanError("derived query contains model-source state")
	}
	if p.result.kind != ResultProjection && p.result.kind != ResultAggregate && p.result.kind != ResultGrouped {
		return invalidPlanError("derived query requires an explicit scalar result")
	}
	if p.result.kind == ResultAggregate && (p.limit != nil || p.offset != nil || p.distinct || len(p.orderings) != 0) {
		return groupUnsupported("aggregate a sliced, distinct or ordered result through a new input stage")
	}
	if len(p.orderings) > MaxProjectionExpressions || p.resultStageNodes() > MaximumDerivedNodes {
		return invalidPlanError("derived query exceeds its total expression budget")
	}
	if err := p.result.validate(); err != nil {
		return err
	}
	for _, value := range p.result.expressions {
		if err := p.validateResultSource(value); err != nil {
			return err
		}
	}
	if p.where.node != nil {
		if err := p.validateWhereSource(p.where); err != nil {
			return err
		}
	}
	if err := p.ValidateGrouping(); err != nil {
		return err
	}
	return p.ValidateOrderings()
}

func equalDerivedSources(left, right *derivedSource) bool {
	if left == right {
		return true
	}
	return left != nil && right != nil && left.scope.Equal(right.scope) && left.input.Equal(right.input)
}
