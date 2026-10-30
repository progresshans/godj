package query

// AnnotationExpression refers to one already-bound definition. The reference
// retains its producer and source; a later definition with the same name cannot
// change it. Unlike a selected result cell, its producer is inlined into SQL,
// so its complete expansion contributes to the scalar depth and node budgets.
func AnnotationExpression(ref AnnotationRef) (ScalarExpression, error) {
	if !ref.Valid() {
		return ScalarExpression{}, invalidPlanError("named scalar requires a valid annotation reference")
	}
	value := ref.definition.expression
	depth := 2 + value.scalar.Depth()
	resultDepth := value.scalar.resultReferenceDepth()
	if value.filter.node != nil {
		depth = max(depth, 2+value.filter.node.depth)
		for _, condition := range expressionConditions(value.filter) {
			resultDepth = max(resultDepth, condition.scalar.resultReferenceDepth())
			if right, present := condition.RHSScalar(); present {
				resultDepth = max(resultDepth, right.resultReferenceDepth())
			}
		}
	}
	expression := ScalarExpression{&scalarNode{
		kind: ScalarAnnotation, annotation: ref, result: ref.ResultKind(), nullable: ref.Nullable(),
		depth: depth, nodes: 1 + annotationNodes(value), resultDepth: resultDepth,
		durationMean: value.RequiresDurationRounding(), durationFraction: value.RequiresDurationFraction(),
		hasRelations:   value.relation != nil || value.scalar.HasRelations() || value.filter.HasRelations(),
		hasAnnotations: true, aggregate: value.IsAggregate(),
	}}
	if err := expression.Validate(); err != nil {
		return ScalarExpression{}, err
	}
	return expression, nil
}

func (expression ScalarExpression) AnnotationReference() (AnnotationRef, bool) {
	if expression.Kind() != ScalarAnnotation {
		return AnnotationRef{}, false
	}
	return expression.node.annotation, true
}

func (expression ScalarExpression) HasAnnotations() bool {
	return expression.node != nil && expression.node.hasAnnotations
}

// ContainsAggregate reports aggregation in this SQL stage. A selected result
// cell is an ordinary row value in its next stage, even if its producer used an
// aggregate. An annotation retains its producer's aggregation in this stage.
func (expression ScalarExpression) ContainsAggregate() bool {
	return expression.node != nil && expression.node.aggregate
}

// AnnotationReferences returns direct dependencies, including both operands of
// CASE predicates. It does not flatten each definition's own source context.
// Returned handles and their ordered container are immutable/caller-owned.
func (expression ScalarExpression) AnnotationReferences() []AnnotationRef {
	var refs []AnnotationRef
	var visit func(ScalarExpression)
	visit = func(value ScalarExpression) {
		if !value.HasAnnotations() {
			return
		}
		if ref, present := value.AnnotationReference(); present {
			refs = append(refs, ref)
			return
		}
		if _, left, right, present := value.Binary(); present {
			visit(left)
			visit(right)
		} else if child, present := value.Negated(); present {
			visit(child)
		} else if _, arguments, present := value.Function(); present {
			for _, argument := range arguments {
				visit(argument)
			}
		} else if fallback, branches, present := value.Case(); present {
			visit(fallback)
			for _, branch := range branches {
				for _, condition := range expressionConditions(branch.when) {
					visit(condition.scalar)
					right, _ := condition.RHSScalar()
					visit(right)
				}
				visit(branch.value)
			}
		}
	}
	visit(expression)
	return refs
}

func (expression Expression) ContainsAggregate() bool {
	for _, condition := range expressionConditions(expression) {
		right, _ := condition.RHSScalar()
		if condition.scalar.ContainsAggregate() || right.ContainsAggregate() {
			return true
		}
	}
	return false
}

func (expression Expression) HasAnnotations() bool {
	for _, condition := range expressionConditions(expression) {
		right, _ := condition.RHSScalar()
		if condition.scalar.HasAnnotations() || right.HasAnnotations() {
			return true
		}
	}
	return false
}
