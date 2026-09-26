package orm

import (
	"context"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// materializedRows belongs to the same evaluation as its raw models. It is
// immutable after publication; each terminal creates independent handles.
type materializedRows[M any] struct {
	binding BoundModel[M]
	values  []relatedSelectedValue[M]
}

// Explicit target-query configuration survives query refinements. Implicit
// lookup-path graphs live only on the original evaluation attachment instead.
type queryMaterialization[M any] struct {
	binding    BoundModel[M]
	selections []preparedPrefetch[M]
}

func (m *queryMaterialization[M]) nodeBudget() int {
	nodes := 0
	for _, selection := range m.selections {
		nodes += selection.nodeBudget()
	}
	return nodes
}

func (m *queryMaterialization[M]) prepare(ctx context.Context, backend db.Queryer, raw []M) ([]relatedSelectedValue[M], error) {
	values := make([]relatedSelectedValue[M], len(raw))
	for i, value := range raw {
		values[i].source = value
	}
	if err := loadPrefetchValues(ctx, backend, values, m.selections); err != nil {
		return nil, err
	}
	return sessionReadResult(ctx, backend, values, nil)
}

func (source QuerySet[M]) evaluateModels(ctx context.Context) ([]M, any, error) {
	return source.evaluation.evaluateAttached(ctx, func(ctx context.Context) ([]M, any, error) {
		raw, err := source.scanAll(ctx)
		if err != nil || source.materialization == nil {
			return raw, nil, err
		}
		values, err := source.materialization.prepare(ctx, source.backend, raw)
		if err != nil {
			return nil, nil, err
		}
		return raw, &materializedRows[M]{binding: source.materialization.binding, values: values}, nil
	})
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

func materializationValues[M any](binding BoundModel[M], raw []M, attachment any) ([]relatedSelectedValue[M], error) {
	if attachment == nil {
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
	raw, attachment, err := source.evaluateModels(ctx)
	if err != nil {
		return nil, err
	}
	values, err := materializationValues(binding, raw, attachment)
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
	readSource := source
	readSource.materialization = nil
	value, present, err := readSource.First(ctx)
	if err != nil || !present {
		return nil, false, err
	}
	graphValue := relatedSelectedValue[M]{source: value}
	if raw, attachment, ready := source.evaluation.cachedResult(); ready {
		values, err := materializationValues(binding, raw, attachment)
		if err != nil {
			return nil, false, err
		}
		if len(values) == 0 {
			return nil, false, relationInvalidPlan("materialized first row is absent from its cache")
		}
		graphValue = values[0]
	} else if source.materialization != nil {
		values, err := source.materialization.prepare(ctx, source.backend, []M{value})
		if err != nil {
			return nil, false, err
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
