package orm

import (
	"context"
	"errors"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type bulkUpdateOptionKind uint8

const (
	bulkUpdateFields bulkUpdateOptionKind = iota + 1
	bulkUpdateNames
	bulkUpdateBatchSize
)

// BulkUpdateOption owns a model-specific selected-field mask or batch limit.
// A nonempty mask is required, including for an empty input. Repeated field
// names within one mask are resolved once in schema order.
type BulkUpdateOption[M any] struct {
	kind      bulkUpdateOptionKind
	fields    []WritableField[M]
	names     []string
	batchSize int
}

func BulkUpdateFields[M any](fields ...WritableField[M]) BulkUpdateOption[M] {
	return BulkUpdateOption[M]{kind: bulkUpdateFields, fields: slices.Clone(fields)}
}

// BulkUpdateFieldNames resolves dynamic names against the same Manager snapshot
// as typed fields. Primary keys and columnless relations cannot be selected.
func BulkUpdateFieldNames[M any](names ...string) BulkUpdateOption[M] {
	return BulkUpdateOption[M]{kind: bulkUpdateNames, names: slices.Clone(names)}
}

func BulkUpdateBatchSize[M any](size int) BulkUpdateOption[M] {
	return BulkUpdateOption[M]{kind: bulkUpdateBatchSize, batchSize: size}
}

type preparedBulkUpdateOptions struct {
	fields     []ir.Field
	references []query.FieldRef
	batchSize  int
}

func prepareBulkUpdateOptions[M any](prepared *preparedModel, options []BulkUpdateOption[M]) (preparedBulkUpdateOptions, error) {
	var result preparedBulkUpdateOptions
	var mask saveOptionState[M]
	batchSet := false
	for _, option := range options {
		switch option.kind {
		case bulkUpdateFields, bulkUpdateNames:
			if mask.fieldMaskSet {
				return result, invalidBulkArgument("bulk update requires exactly one field mask")
			}
			mask.fieldMaskSet, mask.typedFields, mask.dynamicNames = true, option.fields, option.names
		case bulkUpdateBatchSize:
			if batchSet || option.batchSize <= 0 {
				return result, invalidBulkArgument("bulk update batch size must be positive and supplied once")
			}
			batchSet, result.batchSize = true, option.batchSize
		default:
			return result, invalidBulkArgument("unknown bulk update option")
		}
	}
	if !mask.fieldMaskSet {
		return result, invalidBulkArgument("bulk update requires selected fields")
	}
	fields, err := resolveSaveFields(prepared, mask)
	if err != nil {
		return result, err
	}
	if len(fields) == 0 {
		return result, invalidBulkArgument("bulk update requires a nonempty field mask")
	}
	result.fields, result.references = fields, make([]query.FieldRef, len(fields))
	for index, field := range fields {
		result.references[index] = fieldReference(field)
	}
	return result, nil
}

// BulkUpdate writes selected values from explicitly keyed models in one owned
// transaction or borrowed savepoint. It neither inserts missing rows nor calls
// per-object Save/Clean. Success reports matched rows, including unchanged
// values; duplicate and missing keys may reduce the count. Borrowed success is
// provisional until the parent commits. Inputs and read caches are unchanged.
func (m Manager[M]) BulkUpdate(ctx context.Context, backend db.Queryer, values []M, options ...BulkUpdateOption[M]) (int64, error) {
	return m.Using(backend).BulkUpdate(ctx, values, options...)
}

func (source RelatedSelectQuery[M]) BulkUpdate(ctx context.Context, values []M, options ...BulkUpdateOption[M]) (int64, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return 0, err
	}
	query := newQuerySet(source.backend, source.sourceDescriptor, source.plan.WithoutRelationProjections(), source.prepared)
	return query.BulkUpdate(ctx, values, options...)
}

func (source PrefetchQuery[M]) BulkUpdate(ctx context.Context, values []M, options ...BulkUpdateOption[M]) (int64, error) {
	if err := source.validate(ctx); err != nil {
		return 0, err
	}
	return source.source.BulkUpdate(ctx, values, options...)
}

// BulkUpdate preserves the query's full predicate and collection filter scopes.
// Ordering/distinct/read locks/eager shape do not narrow a write. A nonempty
// sliced query is rejected. Empty input checks configuration, mask and native
// capability but does not execute its predicate or open a transaction.
func (source QuerySet[M]) BulkUpdate(ctx context.Context, values []M, options ...BulkUpdateOption[M]) (int64, error) {
	if err := source.validateTerminal(ctx); err != nil {
		return 0, err
	}
	manager := Manager[M]{descriptor: source.descriptor, prepared: source.prepared}
	descriptor, model, err := manager.writeConfiguration(ctx, source.backend)
	if err != nil {
		return 0, err
	}
	settings, err := prepareBulkUpdateOptions(model, options)
	if err != nil {
		return 0, err
	}
	effective, err := executionBackend(ctx, source.backend)
	if err != nil {
		return 0, err
	}
	run, borrowed, err := modelWriteScope(effective, "BulkUpdate")
	if err != nil {
		return 0, err
	}
	selection := source.plan
	if len(values) == 0 {
		selection = model.plan
	}
	spec, err := query.NewBulkUpdateSpec(selection, settings.references, fieldReference(model.primaryKey))
	if err != nil {
		return 0, err
	}
	if len(values) == 0 {
		if _, _, err := bulkUpdateCapability(ctx, effective, spec, settings.batchSize); err != nil {
			return 0, err
		}
		return 0, ctx.Err()
	}
	inputs := slices.Clone(values)
	var pending int64
	err = guardedModelWriteScope(ctx, run, func(workContext context.Context, session db.Session) error {
		capability, batchSize, err := bulkUpdateCapability(workContext, session, spec, settings.batchSize)
		if err != nil {
			return err
		}
		keys, rows := make([]int64, len(inputs)), make([][]query.Value, len(inputs))
		for index, input := range inputs {
			if err := workContext.Err(); err != nil {
				return err
			}
			value := descriptor.CloneWriteModel(input)
			key, present := descriptor.PrimaryKey(value)
			if !present {
				return &query.Error{Category: query.CategoryQuery, Code: query.CodeMissingPrimaryKey, Field: model.primaryKey.Name, Detail: "bulk update model has no explicit primary key state"}
			}
			if !mutationValueMatches(model.primaryKey, key) || key.IsNull() {
				return invalidWritePlan("bulk update model has an invalid primary key")
			}
			keys[index], _ = key.Integer()
			assignments, err := saveAssignments(descriptor, value, settings.fields)
			if err != nil {
				return err
			}
			rows[index] = make([]query.Value, len(assignments))
			for column, assignment := range assignments {
				rows[index][column] = assignment.Value()
			}
		}
		// All keys and selected values have been validated and copied before
		// the first statement, including fields used only in a later batch.
		for start := 0; start < len(inputs); {
			if err := workContext.Err(); err != nil {
				return err
			}
			end := start + min(batchSize, len(inputs)-start)
			plan, err := query.NewBulkUpdatePlan(spec, keys[start:end], rows[start:end])
			if err != nil {
				return err
			}
			count, err := capability.BulkUpdate(workContext, plan)
			if err != nil {
				return err
			}
			unique := make(map[int64]bool, end-start)
			for _, key := range keys[start:end] {
				unique[key] = true
			}
			if count < 0 || count > int64(len(unique)) {
				return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows, Detail: "bulk update returned an invalid affected count"}
			}
			pending += count
			start = end
		}
		return nil
	})
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return 0, errors.Join(err, contextErr)
		}
		return 0, err
	}
	if borrowed {
		if err := validateQuerySession(context.WithoutCancel(ctx), source.backend); err != nil {
			return 0, err
		}
	}
	return pending, nil
}

func bulkUpdateCapability(ctx context.Context, backend db.Queryer, spec query.BulkUpdateSpec, requested int) (db.BulkUpdater, int, error) {
	capability, ok := backend.(db.BulkUpdater)
	if !ok || interfaceIsNil(capability) {
		return nil, 0, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session has no native bulk update capability"}
	}
	limit, err := capability.BulkUpdateBatchSize(ctx, spec)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		return nil, 0, invalidWritePlan("bulk update capability returned an invalid batch size")
	}
	limit = min(limit, query.MaximumBulkRows, query.MaximumBulkValues/(len(spec.Fields())+1))
	if requested > 0 {
		limit = min(limit, requested)
	}
	return capability, limit, nil
}
