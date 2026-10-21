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
	for _, field := range p.sourceFields {
		if name == field.Name() {
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
	if index < 0 {
		scope.current = append(scope.current, ref)
	} else {
		scope.current[index] = ref
	}
	p.annotations = scope
	p = p.WithoutCollectionFilterReuse()
	return p, ref, nil
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
