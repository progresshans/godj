package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// MaterializeBatches bypasses and preserves the query's full evaluation cache.
// Each complete batch, including selected graphs, is prepared before callback.
// Its context must be used for interleaved ORM reads and writes, including work
// on previously obtained models from this backend. This preserves their origin,
// identity and cache while selecting the source's pinned execution connection.
// Calls using that context are synchronous and serialized by the callback.
// Direct backend calls use db.BatchQueryer instead.
//
// A positive explicit size, db.BatchQueryer and comparable backend identity
// (normally a pointer) are required even for an empty source. Returning false
// stops normally. A failed batch is never published;
// previous callbacks remain observable. Held root models keep ordinary root
// lifetime; models borrowed from a transaction still expire with that session.
func MaterializeBatches[M any](ctx context.Context, source QuerySet[M], binding BoundModel[M], size int, callback func(context.Context, []*RelatedSelected[M]) (bool, error)) error {
	if err := source.validateTerminal(ctx); err != nil {
		return err
	}
	if err := validateMaterializationSource(source, binding); err != nil {
		return err
	}
	if source.materialization != nil && len(source.materialization.targets) > 0 {
		related, err := source.materialization.relatedQuery(source)
		if err != nil {
			return err
		}
		return related.IterateBatches(ctx, size, callback)
	}
	decode := func(row db.Row) (M, error) {
		value, err := source.descriptor.Scan(row)
		if err != nil {
			var zero M
			return zero, fmt.Errorf("scan model row: %w", err)
		}
		return source.descriptor.CloneModel(value), nil
	}
	prepare := func(ctx context.Context, raw []M) ([]relatedSelectedValue[M], error) {
		if source.materialization != nil {
			return source.materialization.prepare(ctx, source.backend, raw)
		}
		return materializationValues(binding, raw, nil)
	}
	return streamMaterialized(ctx, source.backend, source.plan, binding, source.descriptor, size, decode, prepare, callback)
}

// IterateBatches preserves the exact owner universe of each explicit batch,
// including custom target windows and nested selections.
func (q PrefetchQuery[M]) IterateBatches(ctx context.Context, size int, callback func(context.Context, []*RelatedSelected[M]) (bool, error)) error {
	if err := q.validate(ctx); err != nil {
		return err
	}
	source := q.source
	source.materialization = &queryMaterialization[M]{binding: q.binding, selections: q.selections}
	if q.related != nil {
		source.materialization.targets = q.related.targets
		source.materialization.eagerNodes = q.related.nodes
	}
	return MaterializeBatches(ctx, source, q.binding, size, callback)
}

// IterateBatches bounds decoded graph storage by the current batch. Reverse
// single-valued eager selections also retain a key-only cardinality ledger
// across batches, so conflicting children cannot evade integrity validation by
// falling on opposite sides of a batch boundary. No model cache is retained.
func (q RelatedSelectQuery[M]) IterateBatches(ctx context.Context, size int, callback func(context.Context, []*RelatedSelected[M]) (bool, error)) error {
	if err := q.validateTerminal(ctx); err != nil {
		return err
	}
	seen := selectedCardinality{}
	decode := func(row db.Row) (projectedRelatedRow[M], error) {
		value, _, err := q.decodeProjectedRow(ctx, row, len(q.plan.SourceFields()), nil, "")
		return value, err
	}
	prepare := func(ctx context.Context, rows []projectedRelatedRow[M]) ([]relatedSelectedValue[M], error) {
		return q.prepareProjectedRows(ctx, rows, seen)
	}
	return streamMaterialized(ctx, q.backend, q.plan, q.binding, q.sourceDescriptor, size, decode, prepare, callback)
}

func streamMaterialized[M, R any](ctx context.Context, origin db.Queryer, plan query.Plan, binding BoundModel[M], descriptor ModelDescriptor[M], size int, decode func(db.Row) (R, error), prepare func(context.Context, []R) ([]relatedSelectedValue[M], error), callback func(context.Context, []*RelatedSelected[M]) (bool, error)) error {
	if size <= 0 || callback == nil {
		return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidValue, Detail: "materialized streaming requires a positive batch size and a non-nil callback"}
	}
	if !reflect.ValueOf(origin).Comparable() {
		return relationBackendInvalidPlan("batch execution requires a comparable backend identity")
	}
	effective, err := executionBackend(ctx, origin)
	if err != nil {
		return err
	}
	backend, ok := effective.(db.BatchQueryer)
	if !ok || interfaceIsNil(backend) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "backend does not support batched query execution"}
	}
	var batch []R
	var failure error
	stopped := false
	fail := func(err error) error { failure = errors.Join(failure, err); return err }
	err = backend.QueryBatches(ctx, plan, size, func(row db.Row) error {
		if failure != nil {
			return failure
		}
		if stopped || len(batch) >= size {
			return fail(relationBackendInvalidPlan("batch backend scanned beyond its publication boundary"))
		}
		if err := validateQuerySession(ctx, origin); err != nil {
			return fail(err)
		}
		if interfaceIsNil(row) {
			return fail(relationBackendInvalidPlan("batch backend supplied a nil row"))
		}
		value, err := decode(row)
		if err != nil {
			return fail(err)
		}
		batch = append(batch, value)
		return nil
	}, func(executor db.Queryer) (bool, error) {
		if failure != nil {
			return false, failure
		}
		if stopped || len(batch) == 0 {
			return false, fail(relationBackendInvalidPlan("batch backend yielded outside a decoded batch"))
		}
		scoped, err := withExecutionScope(ctx, origin, executor)
		if err != nil {
			return false, fail(err)
		}
		values, err := prepare(scoped, batch)
		if err != nil {
			return false, fail(err)
		}
		if len(values) != len(batch) {
			return false, fail(relationBackendInvalidPlan("materialization changed source row cardinality"))
		}
		result := make([]*RelatedSelected[M], len(values))
		for i, value := range values {
			result[i], err = cloneRelatedSelection(origin, binding, descriptor, value)
			if err != nil {
				return false, fail(err)
			}
		}
		// Release our raw references before publishing. The callback owns its graph.
		batch = nil
		if err := validateQuerySession(scoped, origin); err != nil {
			return false, fail(err)
		}
		more, err := callback(scoped, result)
		if err != nil {
			return false, fail(err)
		}
		if err := validateQuerySession(scoped, origin); err != nil {
			return false, fail(err)
		}
		stopped = !more
		return more, nil
	})
	if err == nil && failure == nil && len(batch) != 0 {
		failure = relationBackendInvalidPlan("batch backend discarded an unpublished source batch")
	}
	_, err = sessionReadResult(ctx, origin, struct{}{}, joinContextErr(errors.Join(err, failure), ctx))
	return err
}
