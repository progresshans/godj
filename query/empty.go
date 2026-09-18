package query

// EmptyResult reports whether the predicate proves that its source has no
// rows. Backends must still validate the whole plan before skipping I/O; this
// is a value analysis, not a metadata or capability validation shortcut.
// An aggregate over this source still returns its COUNT 0 / MIN or MAX NULL row.
func (plan Plan) EmptyResult() bool {
	return predicateTruth(plan.where, false) == truthFalse
}

type knownTruth uint8

const (
	truthUnknown knownTruth = iota
	truthFalse
	truthTrue
)

func predicateTruth(expression Expression, negated bool) knownTruth {
	if expression.node == nil {
		return truthUnknown
	}
	node := expression.node
	switch node.kind {
	case ExpressionLeaf:
		condition := node.condition
		if condition.lookup != LookupIn || condition.rhs == nil || condition.rhs.kind != conditionRHSList {
			return truthUnknown
		}
		hasNull := false
		for _, value := range condition.rhs.values {
			if !value.IsNull() {
				return truthUnknown
			}
			hasNull = true
		}
		// Under odd negation a NULL-containing nullable membership adds
		// OR field IS NULL, which cannot be decided without reading the row.
		if negated && hasNull && condition.field.Nullable() {
			return truthUnknown
		}
		return truthFalse
	case ExpressionAnd, ExpressionOr:
		if len(node.children) < 2 {
			return truthUnknown
		}
		identity, decisive := truthTrue, truthFalse
		if node.kind == ExpressionOr {
			identity, decisive = truthFalse, truthTrue
		}
		result := identity
		for _, child := range node.children {
			value := predicateTruth(child, negated)
			if value == decisive {
				return decisive
			}
			if value == truthUnknown {
				result = truthUnknown
			}
		}
		return result
	case ExpressionNot:
		if len(node.children) != 1 {
			return truthUnknown
		}
		switch predicateTruth(node.children[0], !negated) {
		case truthFalse:
			return truthTrue
		case truthTrue:
			return truthFalse
		}
	}
	return truthUnknown
}
