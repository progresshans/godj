package query

// MaximumResultStages bounds chains of selected-result schemas independently
// of the depth of any one scalar tree. A reference does not inline its producer.
const MaximumResultStages = 32

// ResultScope owns the ordered scalar columns of one query result. It is not
// model metadata. Copies share this immutable identity; constructing a second
// scope, even from an equal shape, creates a different binding authority.
// SQL aliases are assigned by the compiler and never enter this identity.
type ResultScope struct{ data *resultScope }

type resultScope struct {
	shape ResultShape
	depth int
}

func NewResultScope(shape ResultShape) (ResultScope, error) {
	return newResultScope(shape, 1)
}

func newResultScope(shape ResultShape, minimumDepth int) (ResultScope, error) {
	if err := shape.validate(); err != nil {
		return ResultScope{}, err
	}
	switch shape.Kind() {
	case ResultProjection, ResultAggregate:
	case ResultGrouped:
		if shape.GroupMode() != GroupRows {
			return ResultScope{}, invalidPlanError("result scope requires scalar rows, not a group count or page envelope")
		}
	default:
		return ResultScope{}, invalidPlanError("result scope requires explicit scalar selections")
	}
	depth := max(1, minimumDepth)
	for _, value := range shape.expressions {
		depth = max(depth, 1+value.scalar.resultReferenceDepth())
		for _, condition := range expressionConditions(value.filter) {
			depth = max(depth, 1+condition.scalar.resultReferenceDepth())
		}
	}
	if depth > MaximumResultStages {
		return ResultScope{}, invalidPlanError("selected results exceed their stage budget")
	}
	return ResultScope{&resultScope{shape: shape, depth: depth}}, nil
}

func (scope ResultScope) Valid() bool                  { return scope.data != nil }
func (scope ResultScope) Equal(other ResultScope) bool { return scope.data == other.data }
func (scope ResultScope) Shape() ResultShape {
	if scope.data == nil {
		return ResultShape{}
	}
	return scope.data.shape
}
func (scope ResultScope) Depth() int {
	if scope.data == nil {
		return 0
	}
	return scope.data.depth
}
func (scope ResultScope) ColumnCount() int {
	if scope.data == nil {
		return 0
	}
	return len(scope.data.shape.expressions)
}

// ResultRef is a position in one owned result scope. It preserves the result
// domain, including AVG's conversion and aggregate NULL, without a FieldRef.
type ResultRef struct {
	scope ResultScope
	index int
}

func (scope ResultScope) Column(index int) (ResultRef, error) {
	if !scope.Valid() || index < 0 || index >= scope.ColumnCount() {
		return ResultRef{}, invalidPlanError("selected result column is outside its scope")
	}
	return ResultRef{scope: scope, index: index}, nil
}
func (ref ResultRef) Valid() bool {
	return ref.scope.Valid() && ref.index >= 0 && ref.index < ref.scope.ColumnCount()
}
func (ref ResultRef) Equal(other ResultRef) bool {
	return ref.index == other.index && ref.scope.Equal(other.scope)
}
func (ref ResultRef) Scope() ResultScope { return ref.scope }
func (ref ResultRef) Index() (int, bool) { return ref.index, ref.Valid() }
func (ref ResultRef) ResultKind() FieldKind {
	if !ref.Valid() {
		return ""
	}
	return ref.scope.data.shape.expressions[ref.index].ResultValueKind()
}
func (ref ResultRef) Nullable() bool {
	return ref.Valid() && ref.scope.data.shape.expressions[ref.index].ResultNullable()
}

// SelectedExpression refers to a completed result cell in a subsequent stage.
// Row-source validation and assignments reject it, including inside CASE tests.
func SelectedExpression(ref ResultRef) (ScalarExpression, error) {
	if !ref.Valid() {
		return ScalarExpression{}, invalidPlanError("selected scalar requires a valid result reference")
	}
	return ScalarExpression{&scalarNode{kind: ScalarSelected, reference: ref, result: ref.ResultKind(),
		nullable: ref.Nullable(), depth: 1, nodes: 1, resultDepth: ref.scope.Depth(),
		durationMean: ref.scope.data.shape.expressions[ref.index].RequiresDurationRounding()}}, nil
}
func (expression ScalarExpression) ResultReference() (ResultRef, bool) {
	if expression.Kind() != ScalarSelected {
		return ResultRef{}, false
	}
	return expression.node.reference, true
}
func (expression ScalarExpression) resultReferenceDepth() int {
	if expression.node == nil {
		return 0
	}
	return expression.node.resultDepth
}

// ValidateResultSource accepts only literals and columns belonging to scope.
// Original row fields require an explicit selection before crossing a stage;
// matching names or kinds cannot turn them into result-column references.
func (expression ScalarExpression) ValidateResultSource(scope ResultScope) error {
	if !scope.Valid() {
		return invalidPlanError("selected scalar requires an initialized source scope")
	}
	if err := expression.Validate(); err != nil {
		return err
	}
	if ref, present := expression.ResultReference(); present {
		if !ref.scope.Equal(scope) {
			return invalidPlanError("selected scalar belongs to a different result scope")
		}
		return nil
	}
	if _, present := expression.Field(); present {
		return invalidPlanError("row field cannot cross a result stage without selection")
	}
	if _, left, right, present := expression.Binary(); present {
		if err := left.ValidateResultSource(scope); err != nil {
			return err
		}
		return right.ValidateResultSource(scope)
	}
	if child, present := expression.Negated(); present {
		return child.ValidateResultSource(scope)
	}
	if fallback, branches, present := expression.Case(); present {
		if err := fallback.ValidateResultSource(scope); err != nil {
			return err
		}
		for _, branch := range branches {
			for _, condition := range expressionConditions(branch.when) {
				if condition.scalar.node == nil {
					return invalidPlanError("result-stage predicate requires selected scalar values")
				}
				if err := condition.scalar.ValidateResultSource(scope); err != nil {
					return err
				}
			}
			if err := branch.value.ValidateResultSource(scope); err != nil {
				return err
			}
		}
	}
	return nil
}
