package orm

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

// BulkCreateResult owns the input-order objects and native affected count.
// ReturnedKeys is false under conflict-ignore: generated keys are unavailable
// and an explicit input key is not proof that the corresponding row was saved.
// Objects preserve input field values; conflict-update doesn't refresh fields
// outside its update mask. Read current rows when a fresh stored view is needed.
type BulkCreateResult[M any] struct {
	Objects      []M
	RowsAffected int64
	ReturnedKeys bool
}

type bulkCreateItem[M any] struct {
	value       M
	assignments []query.Assignment
	explicitKey bool
	key         int64
}

// BulkCreate inserts the supplied model values, including explicitly present
// primary keys. Like Save, raw model values are already constructed: this path
// does not reapply defaults or call per-object save/clean hooks. Inputs and
// generated keys are never mutated in place. All batches share one transaction
// or borrowed savepoint; borrowed success is provisional until parent commit.
func (m Manager[M]) BulkCreate(ctx context.Context, backend db.Queryer, values []M, options ...BulkCreateOption[M]) (BulkCreateResult[M], error) {
	return bulkCreateAndMap(ctx, m, backend, len(values), options, bulkModelPreparer(values), func(value M) (M, error) { return value, nil })
}

func bulkModelPreparer[M any](values []M) func(context.Context, db.Session, WriteDescriptor[M], *preparedModel, int) (bulkCreateItem[M], error) {
	inputs := slices.Clone(values)
	return func(ctx context.Context, session db.Session, descriptor WriteDescriptor[M], prepared *preparedModel, index int) (bulkCreateItem[M], error) {
		value := descriptor.CloneWriteModel(inputs[index])
		key, present := descriptor.PrimaryKey(value)
		if !mutationValueMatches(prepared.primaryKey, key) || key.IsNull() {
			return bulkCreateItem[M]{}, invalidWritePlan("bulk model has an invalid primary key")
		}
		integer, _ := key.Integer()
		if !present && integer != 0 {
			return bulkCreateItem[M]{}, invalidWritePlan("bulk model has a nonzero key without explicit presence")
		}
		assignments, err := saveAssignments(descriptor, value, nonPrimaryFields(prepared.metadata))
		if err == nil && present {
			withKey := make([]query.Assignment, 0, len(assignments)+1)
			next := 0
			for _, field := range prepared.metadata.Fields {
				if field.PrimaryKey {
					withKey = append(withKey, NewAssignment(field, key))
				} else {
					withKey = append(withKey, assignments[next])
					next++
				}
			}
			assignments = withKey
		}
		return bulkCreateItem[M]{value: value, assignments: assignments, explicitKey: present, key: integer}, err
	}
}

// BulkCreateInputs uses generated create builders, preserving their defaults
// and required/null semantics. Only automatic keys are requested on this path.
// Every builder is evaluated once before any INSERT in the owned write scope.
func (m Manager[M]) BulkCreateInputs(ctx context.Context, backend db.Queryer, inputs []CreateInput[M], options ...BulkCreateOption[M]) (BulkCreateResult[M], error) {
	return bulkCreateAndMap(ctx, m, backend, len(inputs), options, bulkInputPreparer(m, inputs), func(value M) (M, error) { return value, nil })
}

func bulkInputPreparer[M any](m Manager[M], inputs []CreateInput[M]) func(context.Context, db.Session, WriteDescriptor[M], *preparedModel, int) (bulkCreateItem[M], error) {
	inputs = slices.Clone(inputs)
	return func(ctx context.Context, session db.Session, descriptor WriteDescriptor[M], prepared *preparedModel, index int) (bulkCreateItem[M], error) {
		write, err := m.prepareCreate(ctx, session, inputs[index])
		if err != nil {
			return bulkCreateItem[M]{}, err
		}
		value := descriptor.CloneWriteModel(write.mutation.value)
		descriptor.ClearPrimaryKey(&value)
		assignments, err := saveAssignments(descriptor, value, nonPrimaryFields(prepared.metadata))
		return bulkCreateItem[M]{value: value, assignments: assignments}, err
	}
}

// CreateInputs adapts an owned slice of concrete generated builders without
// losing their model type. Builders themselves retain their documented value
// or callback ownership; callers serialize access to mutable custom builders.
func CreateInputs[M any, I CreateInput[M]](inputs []I) []CreateInput[M] {
	result := make([]CreateInput[M], len(inputs))
	for index, input := range inputs {
		result[index] = input
	}
	return result
}

func bulkCreateAndMap[M, R any](ctx context.Context, m Manager[M], backend db.Queryer, count int, options []BulkCreateOption[M], prepare func(context.Context, db.Session, WriteDescriptor[M], *preparedModel, int) (bulkCreateItem[M], error), construct func(M) (R, error)) (BulkCreateResult[R], error) {
	var zero BulkCreateResult[R]
	descriptor, model, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return zero, err
	}
	settings, err := prepareBulkCreateOptions(model, options)
	if err != nil {
		return zero, err
	}
	effective, err := executionBackend(ctx, backend)
	if err != nil {
		return zero, err
	}
	run, borrowed, err := modelWriteScope(effective, "BulkCreate")
	if err != nil {
		return zero, err
	}
	if count == 0 {
		if _, _, err := bulkInsertCapability(ctx, effective); err != nil {
			return zero, err
		}
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return BulkCreateResult[R]{Objects: []R{}, ReturnedKeys: settings.conflict.Mode() != query.BulkConflictIgnore}, nil
	}
	var pending BulkCreateResult[R]
	err = guardedModelWriteScope(ctx, run, func(workContext context.Context, session db.Session) error {
		capability, limits, err := bulkInsertCapability(workContext, session)
		if err != nil {
			return err
		}
		items := make([]bulkCreateItem[M], count)
		for index := range items {
			if err := workContext.Err(); err != nil {
				return err
			}
			items[index], err = prepare(workContext, session, descriptor, model, index)
			if err != nil {
				return err
			}
		}
		pending = BulkCreateResult[R]{Objects: make([]R, count), ReturnedKeys: settings.conflict.Mode() != query.BulkConflictIgnore}
		returnedKeys := make(map[int64]bool)
		for _, explicit := range []bool{true, false} {
			indices := make([]int, 0, count)
			for index, item := range items {
				if item.explicitKey == explicit {
					indices = append(indices, index)
				}
			}
			if len(indices) == 0 {
				continue
			}
			fields := make([]query.FieldRef, len(items[indices[0]].assignments))
			for index, assignment := range items[indices[0]].assignments {
				fields[index] = assignment.Field()
			}
			batchSize, err := bulkInsertBatchSize(limits, settings.batchSize, len(fields))
			if err != nil {
				return err
			}
			for start := 0; start < len(indices); {
				if err := workContext.Err(); err != nil {
					return err
				}
				end := start + min(batchSize, len(indices)-start)
				rows := make([][]query.Value, end-start)
				for position, index := range indices[start:end] {
					item := items[index]
					if len(item.assignments) != len(fields) {
						return invalidWritePlan("prepared bulk inputs disagree on fields")
					}
					rows[position] = make([]query.Value, len(fields))
					for column, assignment := range item.assignments {
						if assignment.Field() != fields[column] {
							return invalidWritePlan("prepared bulk inputs disagree on field order")
						}
						rows[position][column] = assignment.Value()
					}
				}
				plan, err := query.NewBulkInsertPlan(model.metadata.DBTable, fields, rows, fieldReference(model.primaryKey), settings.conflict)
				if err != nil {
					return err
				}
				result, err := capability.BulkInsert(workContext, plan)
				if err != nil {
					return err
				}
				if err := validateBulkInsertResult(plan, result); err != nil {
					return err
				}
				if settings.conflict.Mode() == query.BulkConflictError {
					for _, key := range result.Keys {
						if returnedKeys[key] {
							return invalidWritePlan("ordinary bulk insert repeated a primary key across batches")
						}
						returnedKeys[key] = true
					}
				}
				pending.RowsAffected += result.RowsAffected
				for position, index := range indices[start:end] {
					item := items[index]
					if plan.ReturnsKeys() {
						if explicit && settings.conflict.Mode() == query.BulkConflictError && result.Keys[position] != item.key {
							return invalidWritePlan("bulk insert returned a different explicit key")
						}
						descriptor.SetPrimaryKey(&item.value, result.Keys[position])
					}
					pending.Objects[index], err = construct(descriptor.CloneWriteModel(item.value))
					if err != nil {
						return err
					}
				}
				start = end
			}
		}
		return nil
	})
	if err != nil {
		return zero, errors.Join(err, ctx.Err())
	}
	if borrowed {
		if err := validateQuerySession(context.WithoutCancel(ctx), backend); err != nil {
			return zero, err
		}
	}
	return pending, nil
}

func bulkInsertCapability(ctx context.Context, backend db.Queryer) (db.BulkInserter, db.BulkInsertLimits, error) {
	capability, ok := backend.(db.BulkInserter)
	if !ok || interfaceIsNil(capability) {
		return nil, db.BulkInsertLimits{}, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "session has no native bulk insert capability"}
	}
	limits, err := capability.BulkInsertLimits(ctx)
	if err != nil {
		return nil, db.BulkInsertLimits{}, err
	}
	if limits.Rows <= 0 || limits.Parameters <= 0 {
		return nil, db.BulkInsertLimits{}, invalidWritePlan("bulk insert capability returned invalid limits")
	}
	return capability, limits, nil
}

func bulkInsertBatchSize(limits db.BulkInsertLimits, requested, columns int) (int, error) {
	rows := min(limits.Rows, query.MaximumBulkRows)
	if columns > 0 {
		rows = min(rows, min(limits.Parameters, query.MaximumBulkValues)/columns)
	}
	if requested > 0 {
		rows = min(rows, requested)
	}
	if rows <= 0 {
		return 0, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "one bulk input exceeds the backend parameter budget"}
	}
	return rows, nil
}

func validateBulkInsertResult(plan query.BulkInsertPlan, result db.BulkInsertResult) error {
	if !plan.ReturnsKeys() {
		if len(result.Keys) != 0 || result.RowsAffected < 0 || result.RowsAffected > int64(plan.RowCount()) {
			return invalidWritePlan("ignored bulk insert returned ambiguous keys or an invalid count")
		}
		return nil
	}
	if len(result.Keys) != plan.RowCount() || result.RowsAffected != int64(plan.RowCount()) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeUnexpectedRows,
			Detail: fmt.Sprintf("bulk insert returned %d keys and count %d for %d input rows", len(result.Keys), result.RowsAffected, plan.RowCount())}
	}
	if plan.Conflict().Mode() == query.BulkConflictError {
		seen := make(map[int64]bool, len(result.Keys))
		for _, key := range result.Keys {
			if seen[key] {
				return invalidWritePlan("ordinary bulk insert returned a repeated primary key")
			}
			seen[key] = true
		}
	}
	return nil
}
