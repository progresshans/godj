package query

import "slices"

// ForPrefetchOwners retains a target query's predicates, ordering and DISTINCT
// while selecting the owner key from its intermediary join. The first visible
// join supplies grouping identity; membership reuses the most recent matching
// join. Separate Filter calls therefore retain their original row scopes.
func (p Plan) ForPrefetchOwners(path RelationPath, owners []int64) (Plan, error) {
	if p.result.Kind() != ResultModel || p.prefetchWindow != nil {
		return Plan{}, invalidPlanError("prefetch owners require a model result")
	}
	if err := validatePrefetchOwnerPath(path); err != nil {
		return Plan{}, err
	}
	if path.hops[0].filterScope != 0 {
		return Plan{}, invalidPlanError("prefetch owner input must be an unscoped declaration")
	}
	values := make([]Value, len(owners))
	for i, owner := range owners {
		values[i] = Integer(owner)
	}
	condition, err := NewRelatedInCondition(path, values)
	if err != nil {
		return Plan{}, err
	}
	expression, err := NewExpression(condition)
	if err != nil {
		return Plan{}, err
	}
	if err := p.validateWhereSource(expression); err != nil {
		return Plan{}, err
	}
	var first, last uint32
	found := false
	var visit func(Expression, bool)
	visit = func(expression Expression, negated bool) {
		switch expression.Kind() {
		case ExpressionLeaf:
			condition, _ := expression.Condition()
			candidate, related := condition.RelationPath()
			if !negated && related && len(candidate.hops) != 0 && samePrefetchHop(candidate.hops[0], path.hops[0]) {
				last = candidate.hops[0].filterScope
				if !found {
					first, found = last, true
				}
			}
		case ExpressionNot:
			for _, child := range expression.Children() {
				visit(child, !negated)
			}
		case ExpressionAnd, ExpressionOr:
			for _, child := range expression.Children() {
				visit(child, negated)
			}
		}
	}
	visit(p.where, false)
	result := p.WithoutCollectionFilterReuse()
	if !found {
		first, last = p.collectionFilters, p.collectionFilters
		result, err = result.WithWhere(expression)
	} else {
		result, err = result.withPrefetchMembership(expression, last)
	}
	if err != nil {
		return Plan{}, err
	}
	// A custom query can have two intermediary joins. Django's grouping
	// column may then refer to an owner outside the requested set; it drops
	// those rows while grouping. Apply that same boundary in SQL so every
	// published row has validated owner provenance, even for one-owner batches.
	if first != last && !p.hasPrefetchSlice() {
		result, err = result.withPrefetchMembership(expression, first)
		if err != nil {
			return Plan{}, err
		}
	}
	selected, _ := bindCollectionFilter(expression, first)
	ownerCondition, _ := selected.Condition()
	ownerPath, _ := ownerCondition.RelationPath()
	shape := ResultShape{kind: ResultPrefetch, expressions: []ResultExpression{{kind: ResultField, field: path.terminal, relation: &ownerPath}}}
	if p.hasPrefetchSlice() {
		membership, _ := bindCollectionFilter(expression, last)
		condition, _ := membership.Condition()
		partitionPath, _ := condition.RelationPath()
		result.prefetchWindow = &PrefetchWindow{
			partition:  ResultExpression{kind: ResultField, field: path.terminal, relation: &partitionPath},
			membership: condition, owner: shape.expressions[0], lateOwner: first != last,
		}
	}
	return result.WithResultShape(shape)
}

func (p Plan) withPrefetchMembership(expression Expression, scope uint32) (Plan, error) {
	expression, _ = bindCollectionFilter(expression, scope)
	if err := p.validateWhereSource(expression); err != nil {
		return Plan{}, err
	}
	where := expression
	if p.where.node != nil {
		var err error
		where, err = AndExpressions(p.where, expression)
		if err != nil {
			return Plan{}, err
		}
	}
	p.where = where
	p.reuseCollectionFilter = false
	return p, nil
}

func samePrefetchHop(left, right RelationHop) bool {
	left.filterScope, right.filterScope = 0, 0
	return left.Equal(right)
}

func validatePrefetchOwnerPath(path RelationPath) error {
	if err := path.Validate(); err != nil {
		return err
	}
	if len(path.hops) != 1 || path.hops[0].direction != RelationReverse || len(path.keys) != 2 ||
		path.scope != RelationTerminalRelatedField || path.terminal.Kind() != FieldInteger {
		return invalidPlanError("prefetch owner projection requires one keyed reverse intermediary hop and an integer owner field")
	}
	return nil
}

func (s ResultShape) validatePrefetch() error {
	if len(s.expressions) != 1 {
		return invalidPlanError("prefetch model result requires one owner projection")
	}
	e := s.expressions[0]
	if e.kind != ResultField || e.relation == nil || e.path.Valid() || !e.field.Equal(e.relation.terminal) {
		return invalidPlanError("prefetch owner projection is invalid")
	}
	return validatePrefetchOwnerPath(*e.relation)
}

func (p Plan) validatePrefetchSource(result ResultShape) error {
	if err := result.validatePrefetch(); err != nil {
		return err
	}
	path := *result.expressions[0].relation
	if path.hops[0].TargetTable() != p.table || !slices.Contains(p.sourceFields, path.keys[0]) {
		return invalidPlanError("prefetch owner path does not belong to the model source")
	}
	var anchored func(Expression) bool
	anchored = func(expression Expression) bool {
		if expression.Kind() == ExpressionLeaf {
			condition, _ := expression.Condition()
			candidate, present := condition.RelationPath()
			return present && condition.Lookup() == LookupIn && candidate.Equal(path)
		}
		if expression.Kind() == ExpressionAnd {
			for _, child := range expression.Children() {
				if anchored(child) {
					return true
				}
			}
		}
		return false
	}
	if !anchored(p.where) && !(p.prefetchWindow != nil && p.prefetchWindow.lateOwner && p.prefetchWindow.owner.Equal(result.expressions[0]) && hasPrefetchAnchor(p.where, p.prefetchWindow.membership)) {
		return invalidPlanError("prefetch owner projection has no required membership anchor")
	}
	return nil
}
