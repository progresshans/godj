package orm

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type Manager[M any] struct {
	descriptor ModelDescriptor[M]
	prepared   *preparedModel
}

type preparedModel struct {
	metadata    ir.Model
	plan        query.Plan
	primaryKey  ir.Field
	writeValid  bool
	byReference map[query.FieldRef]int
	byName      map[string]int
}

// NewManager snapshots Metadata once for both reads and writes. Later changes
// to descriptor metadata require a new Manager. The descriptor retains ownership
// of Scan, CloneModel and optional write callbacks; they must remain consistent
// with the snapshot and support the caller's concurrency.
func NewManager[M any](descriptor ModelDescriptor[M]) Manager[M] {
	manager := Manager[M]{descriptor: descriptor}
	if !descriptorIsNil(descriptor) {
		metadata := descriptor.Metadata().Clone()
		references := modelFieldReferences(metadata)
		manager.prepared = &preparedModel{
			metadata: metadata,
			plan:     query.NewPlan(metadata.DBTable, references),
		}
		if _, writable := descriptor.(WriteDescriptor[M]); writable {
			prepared := manager.prepared
			prepared.primaryKey, prepared.writeValid = autoPrimaryKey(metadata)
			prepared.byReference = make(map[query.FieldRef]int, len(references))
			prepared.byName = make(map[string]int, len(references))
			for index, reference := range references {
				// Preserve first-match behavior even for custom metadata with
				// duplicate names or references. Full reference identity is kept.
				if _, exists := prepared.byReference[reference]; !exists {
					prepared.byReference[reference] = index
				}
				if _, exists := prepared.byName[reference.Name()]; !exists {
					prepared.byName[reference.Name()] = index
				}
			}
		}
	}
	return manager
}

// Using binds a backend to a new QuerySet. It performs no I/O.
func (m Manager[M]) Using(backend db.Queryer) QuerySet[M] {
	if m.prepared == nil {
		return QuerySet[M]{
			backend:    backend,
			descriptor: m.descriptor,
			evaluation: newEvaluationState[M](),
			configurationErr: &query.Error{
				Category: query.CategoryQuery,
				Code:     query.CodeInvalidPlan,
				Detail:   "descriptor is nil",
			},
		}
	}
	return newQuerySet(backend, m.descriptor, m.prepared.plan)
}

func newQuerySet[M any](backend db.Queryer, descriptor ModelDescriptor[M], plan query.Plan) QuerySet[M] {
	return QuerySet[M]{
		backend:    backend,
		descriptor: descriptor,
		plan:       plan,
		evaluation: newEvaluationState[M](),
	}
}

func modelFieldReferences(model ir.Model) []query.FieldRef {
	result := make([]query.FieldRef, len(model.Fields))
	for index, field := range model.Fields {
		result[index] = fieldReference(field)
	}
	return result
}

type QuerySet[M any] struct {
	backend          db.Queryer
	descriptor       ModelDescriptor[M]
	plan             query.Plan
	evaluation       *evaluationState[M]
	configurationErr error
}

func (qs QuerySet[M]) Filter(predicates ...Predicate[M]) QuerySet[M] {
	qs.evaluation = newEvaluationState[M]()
	if qs.configurationErr != nil {
		return qs
	}
	if len(predicates) == 0 {
		return qs
	}
	expressions := make([]query.Expression, len(predicates))
	for index := range predicates {
		if predicates[index].err != nil {
			qs.configurationErr = predicates[index].err
			return qs
		}
		expressions[index] = predicates[index].expression
	}
	expression := expressions[0]
	if len(expressions) > 1 {
		var err error
		expression, err = query.AndExpressions(expressions[0], expressions[1], expressions[2:]...)
		if err != nil {
			qs.configurationErr = err
			return qs
		}
	}
	plan, err := qs.plan.WithWhere(expression)
	if err != nil {
		qs.configurationErr = err
		return qs
	}
	qs.plan = plan
	return qs
}

func (qs QuerySet[M]) OrderBy(orderings ...Ordering[M]) QuerySet[M] {
	qs.evaluation = newEvaluationState[M]()
	if qs.configurationErr != nil {
		return qs
	}
	values := make([]query.Ordering, len(orderings))
	for index := range orderings {
		if orderings[index].err != nil {
			qs.configurationErr = orderings[index].err
			return qs
		}
		values[index] = orderings[index].ordering
	}
	qs.plan = qs.plan.WithOrderings(values...)
	return qs
}

func (qs QuerySet[M]) Limit(limit int) (QuerySet[M], error) {
	if qs.configurationErr != nil {
		return qs, qs.configurationErr
	}
	plan, err := qs.plan.WithLimit(limit)
	if err != nil {
		return QuerySet[M]{}, err
	}
	qs.plan = plan
	qs.evaluation = newEvaluationState[M]()
	return qs, nil
}

// Offset derives a new QuerySet that skips the first offset rows. It performs
// no I/O and never shares the source evaluation cache.
func (qs QuerySet[M]) Offset(offset int) (QuerySet[M], error) {
	if qs.configurationErr != nil {
		return qs, qs.configurationErr
	}
	plan, err := qs.plan.WithOffset(offset)
	if err != nil {
		return QuerySet[M]{}, err
	}
	qs.plan = plan
	qs.evaluation = newEvaluationState[M]()
	return qs, nil
}

// Distinct derives a new QuerySet whose complete selected rows are unique. It
// performs no I/O and never shares the source evaluation cache.
func (qs QuerySet[M]) Distinct() QuerySet[M] {
	qs.evaluation = newEvaluationState[M]()
	if qs.configurationErr == nil {
		qs.plan = qs.plan.WithDistinct()
	}
	return qs
}

// Fresh returns the same immutable query plan with a new, unpopulated
// evaluation state. It performs no backend I/O.
func (qs QuerySet[M]) Fresh() QuerySet[M] {
	qs.evaluation = newEvaluationState[M]()
	return qs
}

func (qs QuerySet[M]) Plan() query.Plan {
	return qs.plan
}

func (qs QuerySet[M]) All(ctx context.Context) ([]M, error) {
	if err := qs.validateTerminal(ctx); err != nil {
		return nil, err
	}
	values, err := qs.evaluation.evaluate(ctx, qs.scanAll)
	if err != nil {
		return nil, err
	}
	return qs.cloneModels(values), nil
}

// Count returns the number of rows represented by the plan. A warm full
// result cache is reused; a cold count compiles a scalar COUNT over the
// logical sliced/distinct source without populating the model cache.
func (qs QuerySet[M]) Count(ctx context.Context) (int64, error) {
	if err := qs.validateTerminal(ctx); err != nil {
		return 0, err
	}
	if values, ok := qs.evaluation.cachedValues(); ok {
		return int64(len(values)), nil
	}
	return AggregateInto(
		ctx,
		qs,
		Aggregate1(CountRows[M](), func(count int64) int64 { return count }),
	)
}

// Exists reports whether the plan contains at least one row. Cold evaluation
// uses an effective limit of one and does not fill the full result cache.
func (qs QuerySet[M]) Exists(ctx context.Context) (bool, error) {
	if err := qs.validateTerminal(ctx); err != nil {
		return false, err
	}
	if values, ok := qs.evaluation.cachedValues(); ok {
		return len(values) != 0, nil
	}
	plan := planWithMaximumRows(qs.plan, 1)
	rows, err := openQueryRows(ctx, qs.backend, plan)
	if err != nil {
		return false, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	exists := rows.Next()
	err = lifecycle.finish(ctx, nil)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// At returns the model at a zero-based index for an explicitly ordered plan.
// Cold evaluation requests the indexed row with OFFSET/LIMIT and leaves the
// full result cache untouched. Indexes beyond the backend-independent offset
// range retain the bounded row-drain path.
func (qs QuerySet[M]) At(ctx context.Context, index int) (M, bool, error) {
	var zero M
	if err := qs.validateTerminal(ctx); err != nil {
		return zero, false, err
	}
	if index < 0 || index == int(^uint(0)>>1) {
		return zero, false, &query.Error{
			Category: query.CategoryArgument,
			Code:     query.CodeInvalidIndex,
			Detail:   "index must be non-negative and representable as a query limit",
		}
	}
	if len(qs.plan.Orderings()) == 0 {
		return zero, false, &query.Error{
			Category: query.CategoryQuery,
			Code:     query.CodeUnorderedQuery,
			Detail:   "At requires an explicit ordering",
		}
	}
	if values, ok := qs.evaluation.cachedValues(); ok {
		if index >= len(values) {
			return zero, false, nil
		}
		return qs.descriptor.CloneModel(values[index]), true, nil
	}

	plan, scanIndex := planForIndex(qs.plan, index)
	rows, err := openQueryRows(ctx, qs.backend, plan)
	if err != nil {
		return zero, false, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	found := false
	var value M
	for position := 0; rows.Next(); position++ {
		if position != scanIndex {
			continue
		}
		value, err = qs.descriptor.Scan(rows)
		if err != nil {
			err = fmt.Errorf("scan model row: %w", err)
		} else {
			value = qs.descriptor.CloneModel(value)
			found = true
		}
		break
	}
	err = lifecycle.finish(ctx, err)
	if err != nil {
		return zero, false, err
	}
	return value, found, nil
}

func planForIndex(plan query.Plan, index int) (query.Plan, int) {
	if limit, limited := plan.Limit(); limited && index >= limit {
		empty, _ := plan.WithLimit(0)
		return empty, 0
	}
	offset, _ := plan.Offset()
	if index > math.MaxInt32-offset {
		return planWithMaximumRows(plan, index+1), index
	}
	if index != 0 {
		plan, _ = plan.WithOffset(offset + index)
	}
	return planWithMaximumRows(plan, 1), 0
}

// First returns the first model for an explicitly ordered plan.
func (qs QuerySet[M]) First(ctx context.Context) (M, bool, error) {
	return qs.At(ctx, 0)
}

// Iterate streams decoded models to callback while always bypassing and
// preserving the full evaluation cache. Rows remain owned by this call and
// are closed before it returns.
func (qs QuerySet[M]) Iterate(ctx context.Context, callback func(M) error) error {
	if err := qs.validateTerminal(ctx); err != nil {
		return err
	}
	if callback == nil {
		return &query.Error{
			Category: query.CategoryArgument,
			Code:     query.CodeInvalidValue,
			Detail:   "iterate callback is nil",
		}
	}

	rows, err := openQueryRows(ctx, qs.backend, qs.plan)
	if err != nil {
		return err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	for rows.Next() {
		value, scanErr := qs.descriptor.Scan(rows)
		if scanErr != nil {
			err = fmt.Errorf("scan model row: %w", scanErr)
			break
		}
		if callbackErr := callback(qs.descriptor.CloneModel(value)); callbackErr != nil {
			err = callbackErr
			break
		}
	}
	return lifecycle.finish(ctx, err)
}

func (qs QuerySet[M]) validateTerminal(ctx context.Context) error {
	if interfaceIsNil(ctx) {
		return invalidTerminalContext()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if qs.configurationErr != nil {
		return qs.configurationErr
	}
	if interfaceIsNil(qs.backend) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "backend is nil"}
	}
	if descriptorIsNil(qs.descriptor) {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "descriptor is nil"}
	}
	if qs.evaluation == nil {
		return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "evaluation state is nil"}
	}
	return nil
}

func (qs QuerySet[M]) scanAll(ctx context.Context) ([]M, error) {
	rows, err := openQueryRows(ctx, qs.backend, qs.plan)
	if err != nil {
		return nil, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	values := make([]M, 0)
	for rows.Next() {
		value, scanErr := qs.descriptor.Scan(rows)
		if scanErr != nil {
			err = fmt.Errorf("scan model row: %w", scanErr)
			break
		}
		// The descriptor clone separates backend/Scan aliases from the
		// canonical cache. A second clone is made for every caller below.
		values = append(values, qs.descriptor.CloneModel(value))
	}
	err = lifecycle.finish(ctx, err)
	if err != nil {
		return nil, err
	}
	return values, nil
}

func (qs QuerySet[M]) cloneModels(values []M) []M {
	clones := make([]M, len(values))
	for index := range values {
		clones[index] = qs.descriptor.CloneModel(values[index])
	}
	return clones
}

func openQueryRows(ctx context.Context, backend db.Queryer, plan query.Plan) (db.Rows, error) {
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		if !interfaceIsNil(rows) {
			if closeErr := rows.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close rows returned with backend error: %w", closeErr))
			}
		}
		return nil, joinContextErr(err, ctx)
	}
	if interfaceIsNil(rows) {
		return nil, joinContextErr(&query.Error{
			Category: query.CategoryBackend,
			Code:     query.CodeInvalidPlan,
			Detail:   "backend returned nil rows without an error",
		}, ctx)
	}
	return rows, nil
}

// rowsLifecycle owns an acquired cursor until either normal completion or
// stack unwinding. Callers defer close immediately, then use finish to retain
// iteration, close, and context errors on normal returns.
type rowsLifecycle struct {
	rows db.Rows
}

func (lifecycle *rowsLifecycle) close() error {
	rows := lifecycle.rows
	if rows == nil {
		return nil
	}
	lifecycle.rows = nil
	return rows.Close()
}

func (lifecycle *rowsLifecycle) finish(ctx context.Context, err error) error {
	err = joinRowsErr(err, lifecycle.rows)
	if closeErr := lifecycle.close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close model rows: %w", closeErr))
	}
	return joinContextErr(err, ctx)
}

func joinContextErr(err error, ctx context.Context) error {
	if interfaceIsNil(ctx) {
		contextErr := invalidTerminalContext()
		if err == nil {
			return contextErr
		}
		return errors.Join(err, contextErr)
	}
	contextErr := ctx.Err()
	if contextErr == nil {
		return err
	}
	if err == nil {
		return contextErr
	}
	return errors.Join(err, contextErr)
}

func invalidTerminalContext() error {
	return &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "context is nil"}
}

func joinRowsErr(err error, rows db.Rows) error {
	if rowsErr := rows.Err(); rowsErr != nil {
		err = errors.Join(err, fmt.Errorf("iterate model rows: %w", rowsErr))
	}
	return err
}

func planWithMaximumRows(plan query.Plan, maximum int) query.Plan {
	if current, ok := plan.Limit(); ok && current <= maximum {
		return plan
	}
	limited, err := plan.WithLimit(maximum)
	if err != nil {
		// maximum is built from a checked non-negative terminal argument.
		// Keep this helper total without introducing panic into normal paths.
		return plan
	}
	return limited
}

func fieldReference(field ir.Field) query.FieldRef {
	var kind query.FieldKind
	switch field.Kind {
	case ir.FieldAuto:
		kind = query.FieldInteger
	case ir.FieldForeignKey:
		kind = query.FieldInteger
	case ir.FieldChar:
		kind = query.FieldString
	case ir.FieldBoolean:
		kind = query.FieldBoolean
	}
	return query.NewFieldRef(field.Name, field.Column, kind, field.Nullable)
}
