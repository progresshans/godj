package orm

import (
	"context"
	"errors"
	"fmt"

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
	}
}

type QuerySet[M any] struct {
	backend          db.Queryer
	descriptor       ModelDescriptor[M]
	plan             query.Plan
	configurationErr error
}

func (qs QuerySet[M]) Filter(predicates ...Predicate[M]) QuerySet[M] {
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
	return qs, nil
}

func (qs QuerySet[M]) Plan() query.Plan {
	return qs.plan
}

func (qs QuerySet[M]) All(ctx context.Context) ([]M, error) {
	if qs.configurationErr != nil {
		return nil, qs.configurationErr
	}
	if ctx == nil {
		return nil, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "context is nil"}
	}
	if qs.backend == nil {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "backend is nil"}
	}
	if descriptorIsNil(qs.descriptor) {
		return nil, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan, Detail: "descriptor is nil"}
	}
	rows, err := qs.backend.Query(ctx, qs.plan)
	if err != nil {
		if !interfaceIsNil(rows) {
			if closeErr := rows.Close(); closeErr != nil {
				return nil, errors.Join(err, fmt.Errorf("close rows returned with backend error: %w", closeErr))
			}
		}
		return nil, err
	}
	if interfaceIsNil(rows) {
		return nil, &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Detail: "backend returned nil rows without an error"}
	}
	values := make([]M, 0)
	var iterationErr error
	for rows.Next() {
		value, scanErr := qs.descriptor.Scan(rows)
		if scanErr != nil {
			iterationErr = fmt.Errorf("scan model row: %w", scanErr)
			break
		}
		values = append(values, value)
	}
	if iterationErr == nil {
		if rowsErr := rows.Err(); rowsErr != nil {
			iterationErr = fmt.Errorf("iterate model rows: %w", rowsErr)
		}
	}
	if closeErr := rows.Close(); closeErr != nil {
		iterationErr = errors.Join(iterationErr, fmt.Errorf("close model rows: %w", closeErr))
	}
	if iterationErr != nil {
		return nil, iterationErr
	}
	return values, nil
}

func fieldReference(field ir.Field) query.FieldRef {
	var kind query.FieldKind
	switch field.Kind {
	case ir.FieldAuto:
		kind = query.FieldInteger
	case ir.FieldChar:
		kind = query.FieldString
	case ir.FieldBoolean:
		kind = query.FieldBoolean
	}
	return query.NewFieldRef(field.Name, field.Column, kind, field.Nullable)
}
