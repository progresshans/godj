package orm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type Manager[M any] struct {
	descriptor ModelDescriptor[M]
}

func NewManager[M any](descriptor ModelDescriptor[M]) Manager[M] {
	return Manager[M]{descriptor: descriptor}
}

// Using binds a backend to a new QuerySet. It performs no I/O.
func (m Manager[M]) Using(backend db.Queryer) QuerySet[M] {
	if descriptorIsNil(m.descriptor) {
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
	metadata := m.descriptor.Metadata()
	columns := make([]query.FieldRef, len(metadata.Fields))
	for index, field := range metadata.Fields {
		columns[index] = fieldReference(field)
	}
	return QuerySet[M]{
		backend:    backend,
		descriptor: m.descriptor,
		plan:       query.NewPlan(metadata.DBTable, columns),
		evaluation: newEvaluationState[M](),
	}
}

type QuerySet[M any] struct {
	backend          db.Queryer
	descriptor       ModelDescriptor[M]
	plan             query.Plan
	evaluation       *evaluationState[M]
	configurationErr error
}

type evaluationState[M any] struct {
	mu     sync.Mutex
	ready  bool
	values []M
	flight *evaluationFlight
}

type evaluationFlight struct {
	done chan struct{}
	err  error
}

func newEvaluationState[M any]() *evaluationState[M] {
	return &evaluationState[M]{}
}

func (qs QuerySet[M]) Filter(predicates ...Predicate[M]) QuerySet[M] {
	qs.evaluation = newEvaluationState[M]()
	if qs.configurationErr != nil {
		return qs
	}
	conditions := make([]query.Condition, len(predicates))
	for index := range predicates {
		if predicates[index].err != nil {
			qs.configurationErr = predicates[index].err
			return qs
		}
		conditions[index] = predicates[index].condition
	}
	qs.plan = qs.plan.WithConditions(conditions...)
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

	for {
		// A waiter may wake from a canceled owner flight at the same instant
		// its own context is canceled. Recheck before it can claim the next
		// flight so a canceled waiter never starts backend I/O.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state := qs.evaluation
		state.mu.Lock()
		if state.ready {
			values := state.values
			state.mu.Unlock()
			return qs.cloneModels(values), nil
		}
		if flight := state.flight; flight != nil {
			state.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight.done:
			}
			if flight.err == nil {
				continue
			}
			if errors.Is(flight.err, context.Canceled) || errors.Is(flight.err, context.DeadlineExceeded) {
				// A live waiter retries with its own context after the owner of
				// the completed flight was canceled.
				continue
			}
			// All callers that were waiting on a non-context owner failure
			// observe that same error. A later independent call may retry.
			return nil, flight.err
		}

		flight := &evaluationFlight{done: make(chan struct{})}
		state.flight = flight
		state.mu.Unlock()

		values, err := qs.scanAll(ctx)

		state.mu.Lock()
		if err == nil {
			state.values = values
			state.ready = true
		}
		flight.err = err
		state.flight = nil
		close(flight.done)
		state.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return qs.cloneModels(values), nil
	}
}

// Count returns the number of rows represented by the plan. A warm full
// result cache is reused; a cold count compiles a scalar COUNT over the
// logical sliced/distinct source without populating the model cache.
func (qs QuerySet[M]) Count(ctx context.Context) (int64, error) {
	if err := qs.validateTerminal(ctx); err != nil {
		return 0, err
	}
	if values, ok := qs.cachedValues(); ok {
		return int64(len(values)), nil
	}
	// Scalar aggregate terminals intentionally reject relation traversal.
	// Preserve Count's established cold/warm behavior for relation-backed
	// QuerySets by retaining the legacy row-drain path until relation
	// aggregates become an explicit compiler capability.
	if planUsesRelations(qs.plan) {
		return qs.countRowsByIteration(ctx)
	}
	return AggregateInto(
		ctx,
		qs,
		Aggregate1(CountRows[M](), func(count int64) int64 { return count }),
	)
}

func (qs QuerySet[M]) countRowsByIteration(ctx context.Context) (int64, error) {
	rows, err := qs.openRows(ctx, qs.plan)
	if err != nil {
		return 0, err
	}
	var count int64
	for rows.Next() {
		count++
	}
	err = finishRowsLifecycle(ctx, err, rows)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func planUsesRelations(plan query.Plan) bool {
	if _, selected := plan.RelationProjection(); selected {
		return true
	}
	for _, condition := range plan.Conditions() {
		if _, related := condition.RelationPath(); related {
			return true
		}
	}
	return false
}

// Exists reports whether the plan contains at least one row. Cold evaluation
// uses an effective limit of one and does not fill the full result cache.
func (qs QuerySet[M]) Exists(ctx context.Context) (bool, error) {
	if err := qs.validateTerminal(ctx); err != nil {
		return false, err
	}
	if values, ok := qs.cachedValues(); ok {
		return len(values) != 0, nil
	}
	plan := planWithMaximumRows(qs.plan, 1)
	rows, err := qs.openRows(ctx, plan)
	if err != nil {
		return false, err
	}
	exists := rows.Next()
	err = finishRowsLifecycle(ctx, nil, rows)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// At returns the model at a zero-based index for an explicitly ordered plan.
// Cold evaluation limits and drains only as many rows as required and leaves
// the full result cache untouched.
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
	if values, ok := qs.cachedValues(); ok {
		if index >= len(values) {
			return zero, false, nil
		}
		return qs.descriptor.CloneModel(values[index]), true, nil
	}

	plan := planWithMaximumRows(qs.plan, index+1)
	rows, err := qs.openRows(ctx, plan)
	if err != nil {
		return zero, false, err
	}
	found := false
	var value M
	for position := 0; rows.Next(); position++ {
		if position != index {
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
	err = finishRowsLifecycle(ctx, err, rows)
	if err != nil {
		return zero, false, err
	}
	return value, found, nil
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

	rows, err := qs.openRows(ctx, qs.plan)
	if err != nil {
		return err
	}
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
	return finishRowsLifecycle(ctx, err, rows)
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

func (qs QuerySet[M]) cachedValues() ([]M, bool) {
	state := qs.evaluation
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.ready {
		return nil, false
	}
	return state.values, true
}

func (qs QuerySet[M]) scanAll(ctx context.Context) ([]M, error) {
	rows, err := qs.openRows(ctx, qs.plan)
	if err != nil {
		return nil, err
	}
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
	err = finishRowsLifecycle(ctx, err, rows)
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

func (qs QuerySet[M]) openRows(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := qs.backend.Query(ctx, plan)
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

func finishRowsLifecycle(ctx context.Context, err error, rows db.Rows) error {
	err = joinRowsErr(err, rows)
	err = closeRows(err, rows)
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

func closeRows(err error, rows db.Rows) error {
	if closeErr := rows.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close model rows: %w", closeErr))
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
