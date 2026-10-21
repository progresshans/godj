package queryplan

import "github.com/progresshans/godj/query"

// Only conjunctions may move independent terms across the grouping boundary.
// An OR or NOT containing an aggregate stays whole after grouping, preserving
// Boolean structure and SQL NULL semantics. Original leaf positions keep the
// already-prepared collection existence correlations attached to each term.
func splitAnnotationPredicate(expression query.Expression) (annotationPredicate, annotationPredicate, error) {
	next := 0
	var positions func(query.Expression) []int
	positions = func(value query.Expression) []int {
		if _, leaf := value.Condition(); leaf {
			index := next
			next++
			return []int{index}
		}
		var result []int
		for _, child := range value.Children() {
			result = append(result, positions(child)...)
		}
		return result
	}
	combine := func(left, right annotationPredicate) (annotationPredicate, error) {
		if left.value.Kind() == 0 {
			return right, nil
		}
		if right.value.Kind() == 0 {
			return left, nil
		}
		value, err := query.AndExpressions(left.value, right.value)
		if err != nil {
			return annotationPredicate{}, err
		}
		return annotationPredicate{value, append(left.indices, right.indices...)}, nil
	}
	var split func(query.Expression) (annotationPredicate, annotationPredicate, error)
	split = func(value query.Expression) (annotationPredicate, annotationPredicate, error) {
		var before, after annotationPredicate
		if value.Kind() == 0 {
			return before, after, nil
		}
		if !value.ContainsAggregate() {
			return annotationPredicate{value, positions(value)}, after, nil
		}
		if value.Kind() != query.ExpressionAnd {
			return before, annotationPredicate{value, positions(value)}, nil
		}
		for _, child := range value.Children() {
			childBefore, childAfter, err := split(child)
			if err != nil {
				return before, after, err
			}
			before, err = combine(before, childBefore)
			if err != nil {
				return before, after, err
			}
			after, err = combine(after, childAfter)
			if err != nil {
				return before, after, err
			}
		}
		return before, after, nil
	}
	return split(expression)
}

func (r *annotationRead) collectValue(value query.ResultExpression, owner query.AnnotationRef) error {
	if value.IsAggregateCall() {
		for _, atom := range r.atoms {
			if atom.owner.Equal(owner) && atom.value.Equal(value) {
				return nil
			}
		}
		r.atoms = append(r.atoms, annotationAggregate{value, owner})
		return nil
	}
	if scalar, present := value.Scalar(); present {
		return r.collectScalar(scalar)
	}
	return r.addKey(value)
}

func (r *annotationRead) addKey(value query.ResultExpression) error {
	for _, key := range r.keys {
		if key.field == (query.FieldRef{}) && key.value.Equal(value) {
			return nil
		}
	}
	if len(r.keys)+len(r.atoms) >= query.MaxProjectionExpressions {
		return invalidPlan("annotation groups exceed their selected-cell budget")
	}
	index := len(r.keys)
	r.keys = append(r.keys, annotationGroupKey{value: value})
	if value.Kind() == query.ResultField {
		field, _ := value.Field()
		alias := "t0"
		if path, related := value.RelationPath(); related {
			key, joined := ConditionJoinKey(path)
			if joined {
				entry, found := r.joins.ByKey[key]
				if !found {
					return invalidPlan("group key relation has no owned JOIN")
				}
				alias = entry.Alias
			}
		}
		key := annotationPhysicalKey{alias, field.Column()}
		if _, present := r.physical[key]; !present {
			r.physical[key] = index
		}
	}
	return nil
}

func (r *annotationRead) addPhysicalKey(alias string, field query.FieldRef) error {
	key := annotationPhysicalKey{alias, field.Column()}
	if _, present := r.physical[key]; present {
		return nil
	}
	if len(r.keys)+len(r.atoms) >= query.MaxProjectionExpressions {
		return invalidPlan("annotation groups exceed their selected-cell budget")
	}
	r.physical[key] = len(r.keys)
	r.keys = append(r.keys, annotationGroupKey{physical: key, field: field})
	return nil
}

func (r *annotationRead) collectScalar(value query.ScalarExpression) error {
	if _, literal := value.Literal(); literal {
		return nil
	}
	if !value.ContainsAggregate() {
		if ref, named := value.AnnotationReference(); named {
			producer, _ := ref.Expression()
			if scalar, present := producer.Scalar(); present {
				if _, literal := scalar.Literal(); literal {
					return nil
				}
			}
		}
		selected, err := query.ScalarResult(value)
		if err != nil {
			return err
		}
		return r.addKey(selected)
	}
	if ref, present := value.AnnotationReference(); present {
		producer, _ := ref.Expression()
		return r.collectValue(producer, ref)
	}
	if _, left, right, present := value.Binary(); present {
		if err := r.collectScalar(left); err != nil {
			return err
		}
		return r.collectScalar(right)
	}
	if child, present := value.Negated(); present {
		return r.collectScalar(child)
	}
	if _, arguments, present := value.Function(); present {
		for _, argument := range arguments {
			if err := r.collectScalar(argument); err != nil {
				return err
			}
		}
	}
	if fallback, branches, present := value.Case(); present {
		if err := r.collectScalar(fallback); err != nil {
			return err
		}
		for _, branch := range branches {
			if err := r.collectPredicate(annotationPredicate{value: branch.Predicate()}); err != nil {
				return err
			}
			if err := r.collectScalar(branch.Value()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *annotationRead) collectPredicate(predicate annotationPredicate) error {
	index := 0
	var visit func(query.Expression) error
	visit = func(value query.Expression) error {
		if condition, leaf := value.Condition(); leaf {
			var existence *CollectionExists
			if index < len(predicate.indices) {
				if exists, present := r.joins.Exists[predicate.indices[index]]; present {
					existence = &exists
				}
			}
			index++
			if left, computed := condition.Scalar(); computed {
				if err := r.collectScalar(left); err != nil {
					return err
				}
				if right, present := condition.RHSScalar(); present {
					return r.collectScalar(right)
				}
				return nil
			}
			alias, field := "t0", condition.Field()
			if existence != nil {
				path, _ := condition.RelationPath()
				keys := path.PrimaryKeys()
				field, alias = keys[0], existence.OuterAlias
				if alias != "t0" {
					for depth := range path.Hops() {
						if joined, present := r.joins.ByKey[KeyForPath(path.Hops()[:depth+1])]; present && joined.Alias == alias {
							field = keys[depth+1]
							break
						}
					}
				}
				if field.Column() != existence.KeyColumn {
					return invalidPlan("group existence correlation has no canonical row key")
				}
			} else if path, related := condition.RelationPath(); related {
				if key, joined := ConditionJoinKey(path); joined {
					entry, found := r.joins.ByKey[key]
					if !found {
						return invalidPlan("group predicate relation has no materialized JOIN")
					}
					alias = entry.Alias
				}
			}
			if err := r.addPhysicalKey(alias, field); err != nil {
				return err
			}
			if right, present := condition.RHSField(); present {
				return r.addPhysicalKey("t0", right)
			}
			return nil
		}
		for _, child := range value.Children() {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(predicate.value)
}
