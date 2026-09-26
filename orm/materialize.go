package orm

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/query"
)

// materializedRows belongs to the same evaluation as its raw models. It is
// immutable after publication; each terminal creates independent handles.
type materializedRows[M any] struct {
	binding BoundModel[M]
	values  []relatedSelectedValue[M]
}

func validateMaterializationSource[M any](source QuerySet[M], binding BoundModel[M]) error {
	if err := validateObjectBoundModel(binding); err != nil {
		return err
	}
	if source.evaluation == nil || descriptorIsNil(source.descriptor) || reflect.TypeOf(source.descriptor) != reflect.TypeOf(binding.objectDescriptor) ||
		!reflect.DeepEqual(source.descriptor.Metadata(), binding.model) || source.plan.Table() != binding.model.DBTable ||
		!reflect.DeepEqual(source.plan.SourceFields(), modelFieldReferences(binding.model)) || source.plan.ResultShape().Kind() != query.ResultModel || len(source.plan.RelationProjections()) != 0 {
		return relationInvalidPlan("materialization source does not match its model binding")
	}
	return nil
}

func materializationValues[M any](source QuerySet[M], binding BoundModel[M], raw []M) ([]relatedSelectedValue[M], error) {
	_, attachment, ready := source.evaluation.cachedResult()
	if !ready || attachment == nil {
		values := make([]relatedSelectedValue[M], len(raw))
		for i, value := range raw {
			values[i].source = value
		}
		return values, nil
	}
	graph, ok := attachment.(*materializedRows[M])
	if !ok || graph == nil || graph.binding.snapshot != binding.snapshot || graph.binding.identity != binding.identity || len(graph.values) != len(raw) {
		return nil, relationInvalidPlan("model graph has a foreign binding or different row cardinality")
	}
	return graph.values, nil
}

// Materialize returns model values and selected relation caches together. A
// plain query produces source-only graphs; prefetched queries keep their graph
// on the same evaluation as their raw values. Refinements get a fresh cache.
func Materialize[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M]) ([]*RelatedSelected[M], error) {
	if err := source.validateTerminal(ctx); err != nil {
		return nil, err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return nil, err
	}
	raw, err := source.All(ctx)
	if err != nil {
		return nil, err
	}
	values, err := materializationValues(source, binding, raw)
	if err != nil {
		return nil, err
	}
	result := make([]*RelatedSelected[M], len(values))
	for i, value := range values {
		result[i], err = cloneRelatedSelection(source.backend, binding, source.descriptor, value)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, source.backend, result, nil)
}

// MaterializeFirst preserves the ordinary explicitly ordered First contract.
// A cold first row never populates the full result cache.
func MaterializeFirst[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M]) (*RelatedSelected[M], bool, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return nil, false, err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return nil, false, err
	}
	if _, _, ready := source.evaluation.cachedResult(); !ready {
		// A concurrent full evaluation must not replace this cold First's
		// source row with a graph read by another SQL statement.
		source.evaluation = newEvaluationState[M]()
	}
	value, present, err := source.First(ctx)
	if err != nil || !present {
		return nil, false, err
	}
	graphValue := relatedSelectedValue[M]{source: value}
	if raw, _, ready := source.evaluation.cachedResult(); ready {
		values, err := materializationValues(source, binding, raw)
		if err != nil {
			return nil, false, err
		}
		if len(values) == 0 {
			return nil, false, relationInvalidPlan("materialized first row is absent from its cache")
		}
		graphValue = values[0]
	}
	result, err := cloneRelatedSelection(source.backend, binding, source.descriptor, graphValue)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	result, err = sessionReadResult(ctx, source.backend, result, nil)
	return result, err == nil, err
}
