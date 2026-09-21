package orm

import (
	"context"
	"reflect"
)

type relatedSelectedState[T any] struct {
	binding    BoundModel[T]
	descriptor ProjectionDescriptor[T]
	value      relatedSelectedValue[T]
}

// SelectedGraph returns an owned clone of an already selected descendant graph.
// It never evaluates a lazy relation. False means no descendant graph was
// selected (including an absent relation); Fresh deliberately drops that graph.
func (related *RelatedObject[T]) SelectedGraph(ctx context.Context) (*RelatedSelected[T], bool, error) {
	if err := related.validate(); err != nil {
		return nil, false, err
	}
	if interfaceIsNil(ctx) {
		return nil, false, relationInvalidPlan("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if related.selected == nil {
		return nil, false, nil
	}
	if related.absent {
		return nil, false, relationInvalidPlan("absent relation contains a selected descendant graph")
	}
	state := related.selected
	if err := validateObjectBoundModel(state.binding); err != nil {
		return nil, false, err
	}
	descriptor, err := projectionDescriptorFor(state.binding)
	if err != nil {
		return nil, false, err
	}
	if interfaceIsNil(state.descriptor) || reflect.TypeOf(descriptor) != reflect.TypeOf(state.descriptor) || len(state.value.targets) == 0 {
		return nil, false, relationInvalidPlan("selected descendant graph is incomplete")
	}
	result := cloneRelatedSelection(related.querySet.backend, state.binding, state.descriptor, state.value)
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return result, true, nil
}
