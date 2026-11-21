package orm

import (
	"context"
	"strings"

	"github.com/progresshans/godj/query"
)

// Snapshot stores this selection as an independent named list. The ordinary
// relation manager is not populated or constrained by this selection.
func (p ManyPrefetch[O, T, L]) Snapshot(name string) ManyPrefetch[O, T, L] {
	if name == "" {
		return p.WithConfigurationError(relationInvalidPlan("prefetch snapshot name is empty"))
	}
	p.snapshot = name
	return p.withTarget(p.targetQuery())
}
func (p ReverseCollectionPrefetch[O, T]) Snapshot(name string) ReverseCollectionPrefetch[O, T] {
	if name == "" {
		return p.WithConfigurationError(relationInvalidPlan("prefetch snapshot name is empty"))
	}
	p.snapshot = name
	return p.withTarget(p.targetQuery())
}

func (p ManyPrefetch[O, T, L]) Limit(limit int) (ManyPrefetch[O, T, L], error) {
	q, err := p.targetQuery().Limit(limit)
	if err != nil {
		return ManyPrefetch[O, T, L]{}, err
	}
	return p.withTarget(q), nil
}
func (p ManyPrefetch[O, T, L]) Offset(offset int) (ManyPrefetch[O, T, L], error) {
	q, err := p.targetQuery().Offset(offset)
	if err != nil {
		return ManyPrefetch[O, T, L]{}, err
	}
	return p.withTarget(q), nil
}
func (p ReverseCollectionPrefetch[O, T]) Limit(limit int) (ReverseCollectionPrefetch[O, T], error) {
	q, err := p.targetQuery().Limit(limit)
	if err != nil {
		return ReverseCollectionPrefetch[O, T]{}, err
	}
	return p.withTarget(q), nil
}
func (p ReverseCollectionPrefetch[O, T]) Offset(offset int) (ReverseCollectionPrefetch[O, T], error) {
	q, err := p.targetQuery().Offset(offset)
	if err != nil {
		return ReverseCollectionPrefetch[O, T]{}, err
	}
	return p.withTarget(q), nil
}

// ValidateSnapshot checks a configured named selection without reading data.
func (p ManyPrefetch[O, T, L]) ValidateSnapshot() error {
	if p.snapshot == "" {
		return relationInvalidPlan("snapshot read requires a named selection")
	}
	remaining := MaximumRelatedSelectionNodes
	_, err := p.preparePrefetch(1, &remaining)
	return err
}
func (p ReverseCollectionPrefetch[O, T]) ValidateSnapshot() error {
	if p.snapshot == "" {
		return relationInvalidPlan("snapshot read requires a named selection")
	}
	remaining := MaximumRelatedSelectionNodes
	_, err := p.preparePrefetch(1, &remaining)
	return err
}

func validatePrefetchSnapshot[O any](owner BoundModel[O], name string, plan query.Plan) error {
	if name == "" {
		_, limited := plan.Limit()
		offset, _ := plan.Offset()
		if limited || offset != 0 {
			return relationInvalidPlan("a sliced prefetch requires a named snapshot")
		}
		return nil
	}
	if strings.Contains(name, "__") {
		return relationInvalidPlan("snapshot name cannot be a relation path")
	}
	for i, c := range name {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return relationInvalidPlan("snapshot name is not an identifier")
		}
	}
	for _, field := range owner.model.Fields {
		if name == field.Name || name == field.GoName {
			return relationInvalidPlan("snapshot name collides with an owner field")
		}
	}
	for _, field := range owner.model.ManyToMany {
		if name == field.Name || name == field.GoName {
			return relationInvalidPlan("snapshot name collides with an owner collection")
		}
	}
	for _, relation := range owner.snapshot.reverse {
		if relation.Owner == owner.identity && relation.Name == name {
			return relationInvalidPlan("snapshot name collides with a reverse relation")
		}
	}
	for _, relation := range owner.snapshot.manyToMany {
		if relation.Target == owner.identity && !relation.Reverse.Disabled && relation.Reverse.Name == name {
			return relationInvalidPlan("snapshot name collides with a reverse collection")
		}
	}
	return nil
}

// Read returns new graph/model/cache handles for the named snapshot. Absence is
// distinct from a loaded empty list. It never performs a fallback query.
func (p ManyPrefetch[O, T, L]) Read(ctx context.Context, owner *RelatedSelected[O]) ([]*RelatedSelected[T], bool, error) {
	if err := validatePrefetchSnapshotRead(ctx, owner, p.relation.prefetchOwner, p.snapshot); err != nil {
		return nil, false, err
	}
	if err := p.ValidateSnapshot(); err != nil {
		return nil, false, err
	}
	cache, present := owner.collections[p.snapshot]
	if !present {
		return nil, false, nil
	}
	typed, ok := cache.(cachedManyPrefetch[T, L])
	if !ok || typed.collection == nil || typed.collection.state != p.relation.state {
		return nil, false, relationInvalidPlan("snapshot belongs to another collection")
	}
	q, err := typed.collection.Query()
	if err != nil {
		return nil, false, err
	}
	if _, _, ready := q.evaluation.cachedResult(); !ready {
		return nil, false, relationInvalidPlan("snapshot evaluation is not ready")
	}
	values, err := Materialize(ctx, q, p.relation.prefetchTarget)
	return values, err == nil, err
}
func (p ReverseCollectionPrefetch[O, T]) Read(ctx context.Context, owner *RelatedSelected[O]) ([]*RelatedSelected[T], bool, error) {
	if err := validatePrefetchSnapshotRead(ctx, owner, p.state.reverse.owner, p.snapshot); err != nil {
		return nil, false, err
	}
	if err := p.ValidateSnapshot(); err != nil {
		return nil, false, err
	}
	cache, present := owner.collections[p.snapshot]
	if !present {
		return nil, false, nil
	}
	typed, ok := cache.(cachedReversePrefetch[T])
	if !ok || typed.set == nil || typed.binding.snapshot != p.state.reverse.source.snapshot || typed.binding.identity != p.state.reverse.source.identity || typed.relation != p.state.reverse.sourceForeignKey.Name {
		return nil, false, relationInvalidPlan("snapshot belongs to another reverse collection")
	}
	q, err := typed.set.Query()
	if err != nil {
		return nil, false, err
	}
	if _, _, ready := q.evaluation.cachedResult(); !ready {
		return nil, false, relationInvalidPlan("snapshot evaluation is not ready")
	}
	values, err := Materialize(ctx, q, p.state.reverse.source)
	return values, err == nil, err
}
func validatePrefetchSnapshotRead[O any](ctx context.Context, owner *RelatedSelected[O], binding BoundModel[O], name string) error {
	if ctx == nil {
		return relationInvalidPlan("snapshot read requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" {
		return relationInvalidPlan("snapshot read requires a named selection")
	}
	if err := owner.ValidateSourceBinding(binding); err != nil {
		return err
	}
	return validateQuerySession(ctx, owner.backend)
}
