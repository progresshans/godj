package orm

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// ValidateUniqueCreate checks the declared column uniqueness of a generated
// create input, including its resolved defaults. It uses the same mutation
// validation as Create, but only performs reads. The caller must authorize the
// operation before calling it. A successful check is advisory: a later write
// can still fail with query.CodeUniqueConstraint due to a concurrent writer.
// Database, cancellation and malformed-input errors are returned separately
// from ordered field violations; no partial violations accompany such errors.
func (m Manager[M]) ValidateUniqueCreate(ctx context.Context, backend db.Queryer, input CreateInput[M]) (validation.Errors, error) {
	write, err := m.prepareCreate(ctx, backend, input)
	if err != nil {
		return validation.Errors{}, err
	}
	return validateUniqueMutation(ctx, backend, write.model, write.mutation.assignments, nil)
}

// ValidateUniqueUpdate checks only fields explicitly assigned by a generated
// patch. The current model's presence-aware primary key excludes that row,
// including an explicitly present zero key. Omitted fields and SQL NULL values
// do not issue uniqueness queries. The caller must first authorize and load
// the current object; a client-supplied key is not an authorization decision.
func (m Manager[M]) ValidateUniqueUpdate(ctx context.Context, backend db.Queryer, current M, input PatchInput[M]) (validation.Errors, error) {
	write, err := m.prepareUpdate(ctx, backend, current, input)
	if err != nil {
		return validation.Errors{}, err
	}
	return validateUniqueMutation(ctx, backend, write.model, write.mutation.assignments, &write.key)
}

func validateUniqueMutation(ctx context.Context, backend db.Queryer, model *preparedModel, assignments []query.Assignment, exclude *query.Value) (validation.Errors, error) {
	if err := ctx.Err(); err != nil {
		return validation.Errors{}, err
	}
	values := make(map[string]query.Value, len(assignments))
	for _, assignment := range assignments {
		values[assignment.Field().Name()] = assignment.Value()
	}
	primary := fieldReference(model.primaryKey)
	projection, err := query.NewProjectionResult(query.FieldResult(primary))
	if err != nil {
		return validation.Errors{}, err
	}
	base, err := model.plan.WithResultShape(projection)
	if err != nil {
		return validation.Errors{}, err
	}
	base, err = base.WithLimit(1)
	if err != nil {
		return validation.Errors{}, err
	}
	if exclude != nil {
		self, err := query.NewExpression(query.NewCondition(primary, query.LookupExact, *exclude))
		if err != nil {
			return validation.Errors{}, err
		}
		other, err := query.NotExpression(self)
		if err != nil {
			return validation.Errors{}, err
		}
		base, err = base.WithWhere(other)
		if err != nil {
			return validation.Errors{}, err
		}
	}
	type check struct {
		field validation.Field
		plan  query.Plan
	}
	var checks []check
	// Construct every AST before any I/O, in declaration order rather than
	// patch assignment order. Plans and scalar values own their immutable data.
	for _, field := range model.metadata.Fields {
		value, present := values[field.Name]
		if !field.Unique || field.PrimaryKey || !present || value.IsNull() {
			continue
		}
		plan, err := base.WithConditions(query.NewCondition(fieldReference(field), query.LookupExact, value))
		if err != nil {
			return validation.Errors{}, err
		}
		checks = append(checks, check{field: validation.Field(field.Name), plan: plan})
	}
	var violations []validation.Violation
	for _, check := range checks {
		if err := ctx.Err(); err != nil {
			return validation.Errors{}, err
		}
		exists, err := uniqueValueExists(ctx, backend, check.plan)
		if err != nil {
			return validation.Errors{}, err
		}
		if exists {
			violations = append(violations, validation.New(check.field, validation.CodeUnique))
		}
	}
	if err := ctx.Err(); err != nil {
		return validation.Errors{}, err
	}
	return validation.NewErrors(violations...), nil
}

func uniqueValueExists(ctx context.Context, backend db.Queryer, plan query.Plan) (bool, error) {
	rows, err := openQueryRows(ctx, backend, plan)
	if err != nil {
		return false, err
	}
	lifecycle := rowsLifecycle{rows: rows}
	defer lifecycle.close()
	exists := rows.Next()
	if err := lifecycle.finish(ctx, nil); err != nil {
		return false, err
	}
	return exists, nil
}
