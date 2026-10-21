package query

import (
	"slices"
	"strings"
)

const (
	MaximumAnnotationNames    = 128
	MaximumAnnotationBindings = 1024
	MaximumAnnotationNodes    = MaximumDerivedNodes
)

// AnnotationRef identifies one immutable definition, not the current meaning
// of its name. Rebinding that name cannot change an earlier dependency. Model
// fields, selected result columns, and SQL aliases remain separate identities.
type AnnotationRef struct{ definition *annotationDefinition }

type annotationDefinition struct {
	name       string
	expression ResultExpression
	selected   bool
	source     Plan
}

type annotationScope struct {
	current []AnnotationRef
	history []AnnotationRef
	nodes   int
	grouped bool
	keys    []ResultExpression
}

func (ref AnnotationRef) Valid() bool { return ref.definition != nil }
func (ref AnnotationRef) Equal(other AnnotationRef) bool {
	return ref.definition == other.definition
}
func (ref AnnotationRef) Name() string {
	if !ref.Valid() {
		return ""
	}
	return ref.definition.name
}
func (ref AnnotationRef) Selected() bool {
	return ref.Valid() && ref.definition.selected
}
func (ref AnnotationRef) Expression() (ResultExpression, bool) {
	if !ref.Valid() {
		return ResultExpression{}, false
	}
	return ref.definition.expression, true
}

// Source is the plan at definition time. In particular a later relation
// filter cannot retroactively become the source of an aggregate definition.
// The handle is immutable; refinements of it leave this definition unchanged.
func (ref AnnotationRef) Source() (Plan, bool) {
	if !ref.Valid() {
		return Plan{}, false
	}
	return ref.definition.source, true
}

func (ref AnnotationRef) ResultKind() FieldKind {
	if !ref.Valid() {
		return ""
	}
	return ref.definition.expression.ResultValueKind()
}
func (ref AnnotationRef) Nullable() bool {
	return ref.Valid() && ref.definition.expression.ResultNullable()
}

// WithAnnotation binds one definition to the current row or result source.
// It records selection intent without inventing stored fields or changing the
// result shape. The ORM's annotation planner owns the explicit output shape.
// References created before a rebind remain owned by the derived plan.
func (p Plan) WithAnnotation(name string, value ResultExpression, selected bool) (Plan, AnnotationRef, error) {
	if !validAnnotationName(name) {
		return Plan{}, AnnotationRef{}, invalidPlanError("annotation name must be a bounded identifier without relation separators")
	}
	if p.page != nil || p.prefetchWindow != nil || p.result.Kind() == ResultPrefetch || p.table == "" && p.derived == nil {
		return Plan{}, AnnotationRef{}, invalidPlanError("annotation requires a row or explicit result source")
	}
	if p.result.Kind() == ResultAggregate {
		return Plan{}, AnnotationRef{}, groupUnsupported("annotating a terminal aggregate requires an explicit result stage")
	}
	for _, field := range p.sourceFields {
		if name == field.Name() || name == field.Column() {
			return Plan{}, AnnotationRef{}, invalidPlanError("annotation name conflicts with source field metadata")
		}
	}
	var err error
	if value.IsAggregate() {
		_, err = NewAggregateResult(value)
	} else {
		_, err = NewProjectionResult(value)
	}
	if err != nil {
		return Plan{}, AnnotationRef{}, err
	}
	if err := p.validateResultSource(value); err != nil {
		return Plan{}, AnnotationRef{}, err
	}

	var current, history []AnnotationRef
	nodes := annotationNodes(value)
	if p.where.node != nil {
		nodes += p.where.node.nodes
	}
	if p.annotations != nil {
		current, history = p.annotations.current, p.annotations.history
		nodes += p.annotations.nodes
	}
	index := slices.IndexFunc(current, func(ref AnnotationRef) bool { return ref.Name() == name })
	if (index < 0 && len(current) >= MaximumAnnotationNames) || len(history) >= MaximumAnnotationBindings || nodes > MaximumAnnotationNodes {
		return Plan{}, AnnotationRef{}, invalidPlanError("annotation definitions exceed their resource budget")
	}
	ref := AnnotationRef{&annotationDefinition{name: name, expression: value, selected: selected, source: p}}
	scope := &annotationScope{current: slices.Clone(current), history: append(slices.Clone(history), ref), nodes: nodes}
	if p.annotations != nil {
		scope.grouped, scope.keys = p.annotations.grouped, p.annotations.keys
	}
	if value.IsAggregate() && !scope.grouped {
		scope.grouped = true
		switch p.result.Kind() {
		case ResultModel:
			for _, field := range p.sourceFields {
				scope.keys = append(scope.keys, FieldResult(field))
			}
		case ResultProjection:
			scope.keys = p.result.Expressions()
		case ResultGrouped:
			scope.keys = p.result.GroupKeys()
		default:
			return Plan{}, AnnotationRef{}, invalidPlanError("aggregate annotation requires a row or grouped source")
		}
		for _, previous := range current {
			if previous.Selected() && !previous.definition.expression.IsAggregate() {
				scalar, err := AnnotationExpression(previous)
				if err != nil {
					return Plan{}, AnnotationRef{}, err
				}
				selected, err := ScalarResult(scalar)
				if err != nil {
					return Plan{}, AnnotationRef{}, err
				}
				scope.keys = append(scope.keys, selected)
			}
		}
	}
	if index < 0 {
		scope.current = append(scope.current, ref)
	} else {
		scope.current[index] = ref
	}
	p.annotations = scope
	// Scope zero is also the unfiltered collection occurrence. Reserve it
	// before later Filter calls so they cannot rewrite an earlier definition's
	// unfiltered JOIN. A prior collection Filter already consumed this scope.
	if p.derived == nil {
		p.collectionFilters = max(1, p.collectionFilters)
	}
	p = p.WithoutCollectionFilterReuse()
	if err := p.ValidateAnnotations(); err != nil {
		return Plan{}, AnnotationRef{}, err
	}
	return p, ref, nil
}

// AnnotationBindings returns declaration order, including definitions retained
// by old dependencies. Compilers use this to preserve JOIN introduction order;
// Annotations remains the current public name inventory.
func (p Plan) AnnotationBindings() []AnnotationRef {
	if p.annotations == nil {
		return nil
	}
	return slices.Clone(p.annotations.history)
}

func (p Plan) HasAnnotations() bool { return p.annotations != nil }

// AnnotationGroupKeys captures the grouping boundary introduced by the first
// aggregate definition. A later projection or rebind cannot merge those groups.
func (p Plan) AnnotationGroupKeys() ([]ResultExpression, bool) {
	if p.annotations == nil || !p.annotations.grouped {
		return nil, false
	}
	return slices.Clone(p.annotations.keys), true
}

// CollectionFilterLimit is the exclusive bound of filter occurrences visible
// in this immutable source. Annotation compilers must not reuse a later JOIN.
func (p Plan) CollectionFilterLimit() uint32 { return p.collectionFilters }

func (p Plan) ValidateAnnotations() error {
	if p.annotations == nil {
		return nil
	}
	if p.page != nil || p.prefetchWindow != nil || p.result.Kind() == ResultPrefetch {
		return groupUnsupported("annotations require an ordinary row or explicit result stage")
	}
	if p.annotations.grouped {
		if p.rowLock != nil || len(p.relationProjections) != 0 {
			return groupUnsupported("aggregate annotations cannot combine with row locks or eager model selection")
		}
		if p.result.Kind() == ResultAggregate {
			return groupUnsupported("aggregate annotated groups through an explicit result stage")
		}
	}
	if p.resultStageNodes() > MaximumDerivedNodes {
		return invalidPlanError("annotated query exceeds its total expression budget")
	}
	return nil
}

// Annotation resolves the latest definition. Callers cannot resolve an old
// dependency by name: that dependency retains its original AnnotationRef.
func (p Plan) Annotation(name string) (AnnotationRef, bool) {
	if p.annotations != nil {
		for _, ref := range p.annotations.current {
			if ref.Name() == name {
				return ref, true
			}
		}
	}
	return AnnotationRef{}, false
}

// Annotations returns the current definitions in their first-declaration
// order. The returned slice is owned by the caller and includes hidden aliases.
func (p Plan) Annotations() []AnnotationRef {
	if p.annotations == nil {
		return nil
	}
	return slices.Clone(p.annotations.current)
}

func (p Plan) OwnsAnnotation(ref AnnotationRef) bool {
	return ref.Valid() && p.annotations != nil && slices.ContainsFunc(p.annotations.history, ref.Equal)
}

func annotationNodes(value ResultExpression) int {
	nodes := 1 + value.scalar.NodeCount()
	if value.filter.node != nil {
		nodes += value.filter.node.nodes
	}
	if value.relation != nil {
		nodes += len(value.relation.hops)
	}
	return nodes
}

func validAnnotationName(name string) bool {
	if name == "" || len(name) > 128 || strings.Contains(name, "__") {
		return false
	}
	for index := range len(name) {
		char := name[index]
		if char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}
