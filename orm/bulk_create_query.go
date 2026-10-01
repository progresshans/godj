package orm

import "context"

// BulkCreate is a write terminal. Read filters, ordering, slices, locking and
// eager-loading requests do not alter its explicit input or populate its result.
// Configuration and session errors still fail; an existing read cache is kept.
func (source QuerySet[M]) BulkCreate(ctx context.Context, values []M, options ...BulkCreateOption[M]) (BulkCreateResult[M], error) {
	if err := source.validateTerminal(ctx); err != nil {
		return BulkCreateResult[M]{}, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	return manager.BulkCreate(ctx, source.backend, values, options...)
}

func (source QuerySet[M]) BulkCreateInputs(ctx context.Context, inputs []CreateInput[M], options ...BulkCreateOption[M]) (BulkCreateResult[M], error) {
	if err := source.validateTerminal(ctx); err != nil {
		return BulkCreateResult[M]{}, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	return manager.BulkCreateInputs(ctx, source.backend, inputs, options...)
}

// MaterializeBulkCreate binds owned results to the caller's original backend.
// Result construction runs inside the write scope; no relation reads are made.
// Conflict-ignore results may have no key, just as their raw input models do.
func MaterializeBulkCreate[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M], values []M, options ...BulkCreateOption[M]) (BulkCreateResult[*RelatedSelected[M]], error) {
	if err := source.validateTerminal(ctx); err != nil {
		return BulkCreateResult[*RelatedSelected[M]]{}, err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return BulkCreateResult[*RelatedSelected[M]]{}, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	return bulkCreateAndMap(ctx, manager, source.backend, len(values), options, bulkModelPreparer(values), func(value M) (*RelatedSelected[M], error) {
		return cloneWrittenSelection(source.backend, binding, source.descriptor, relatedSelectedValue[M]{source: value})
	})
}

func MaterializeBulkCreateInputs[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M], inputs []CreateInput[M], options ...BulkCreateOption[M]) (BulkCreateResult[*RelatedSelected[M]], error) {
	if err := source.validateTerminal(ctx); err != nil {
		return BulkCreateResult[*RelatedSelected[M]]{}, err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return BulkCreateResult[*RelatedSelected[M]]{}, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	return bulkCreateAndMap(ctx, manager, source.backend, len(inputs), options, bulkInputPreparer(manager, inputs), func(value M) (*RelatedSelected[M], error) {
		return cloneWrittenSelection(source.backend, binding, source.descriptor, relatedSelectedValue[M]{source: value})
	})
}
