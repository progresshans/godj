package orm

import (
	"context"
	"slices"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/internal/identifiers"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// ValidateUniqueCreate checks the declared field and model uniqueness of a generated
// create input, including its resolved defaults. It uses the same mutation
// validation as Create, but only performs reads. The caller must authorize the
// operation before calling it. A successful check is advisory: a later write
// can still fail with query.CodeUniqueConstraint due to a concurrent writer.
// Database, cancellation and malformed-input errors are returned separately
// from ordered violations; no partial violations accompany such errors.
// Constraints containing the generated primary key wait for native enforcement.
func (m Manager[M]) ValidateUniqueCreate(ctx context.Context, backend db.Queryer, input CreateInput[M]) (validation.Errors, error) {
	write, err := m.prepareCreate(ctx, backend, input)
	if err != nil {
		return validation.Errors{}, err
	}
	return validateUniqueMutation(ctx, backend, write)
}

// ValidateUniqueUpdate checks assigned unique fields and model constraints
// touched by a generated patch. Each constraint uses the complete candidate,
// including omitted members retained from current. The presence-aware primary
// key excludes that row, including an explicitly present zero key. Untouched
// constraints and tuples containing SQL NULL do not issue queries. The caller
// must first authorize and load current; a client key is not authorization.
func (m Manager[M]) ValidateUniqueUpdate(ctx context.Context, backend db.Queryer, current M, input PatchInput[M]) (validation.Errors, error) {
	write, err := m.prepareUpdate(ctx, backend, current, input)
	if err != nil {
		return validation.Errors{}, err
	}
	return validateUniqueMutation(ctx, backend, write)
}

type preparedUniqueConstraint struct {
	name      string
	fields    []ir.Field
	violation validation.Violation
}

// Bind the immutable metadata once. Custom descriptors must not turn a missing
// or repeated member into a shorter, apparently valid uniqueness query.
func prepareUniqueConstraints(model ir.Model, byName map[string]int) ([]preparedUniqueConstraint, string) {
	constraints := make([]preparedUniqueConstraint, 0, len(model.UniqueConstraints))
	names := make(map[string]bool, len(model.UniqueConstraints))
	for _, constraint := range model.UniqueConstraints {
		if !identifiers.SQL(constraint.Name) || names[constraint.Name] || len(constraint.Fields) == 0 {
			return nil, "unique constraint has an invalid or repeated name, or no members"
		}
		names[constraint.Name] = true
		fields := make([]ir.Field, len(constraint.Fields))
		members := make(map[string]bool, len(fields))
		for position, name := range constraint.Fields {
			index, present := byName[name]
			if !present || members[name] || !identifiers.SQL(name) {
				return nil, "unique constraint has an invalid, missing or repeated member"
			}
			members[name] = true
			fields[position] = model.Fields[index]
		}
		violation := validation.New(validation.NonField, validation.CodeUniqueTogether)
		if len(fields) == 1 {
			violation = validation.New(validation.Field(fields[0].Name), validation.CodeUnique)
		}
		constraints = append(constraints, preparedUniqueConstraint{name: constraint.Name, fields: fields, violation: violation})
	}
	// Normalized IR already has this order. Keep custom descriptors consistent
	// without modifying their metadata or reordering each constraint's members.
	slices.SortFunc(constraints, func(a, b preparedUniqueConstraint) int { return strings.Compare(a.name, b.name) })
	return constraints, ""
}

func validateUniqueMutation[M any](ctx context.Context, backend db.Queryer, write preparedWrite[M]) (validation.Errors, error) {
	if err := ctx.Err(); err != nil {
		return validation.Errors{}, err
	}
	model, assignments := write.model, write.mutation.assignments
	if model.uniqueErr != "" {
		return validation.Errors{}, invalidWritePlan(model.uniqueErr)
	}
	values := make(map[string]query.Value, len(assignments))
	assigned := make(map[string]bool, len(assignments))
	for _, assignment := range assignments {
		values[assignment.Field().Name()] = assignment.Value()
		assigned[assignment.Field().Name()] = true
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
	if write.mutation.kind == MutationPatch {
		self, err := query.NewExpression(query.NewCondition(primary, query.LookupExact, write.key))
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
		violation validation.Violation
		plan      query.Plan
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
		checks = append(checks, check{violation: validation.New(validation.Field(field.Name), validation.CodeUnique), plan: plan})
	}
	for _, constraint := range model.unique {
		touched, generatedKey := false, false
		for _, field := range constraint.fields {
			touched = touched || assigned[field.Name]
			generatedKey = generatedKey || (field.PrimaryKey && write.mutation.kind == MutationCreate)
		}
		if !touched || generatedKey {
			continue
		}
		conditions := make([]query.Condition, 0, len(constraint.fields))
		null := false
		for _, field := range constraint.fields {
			value, present := values[field.Name]
			if field.PrimaryKey {
				value, present = write.key, true
			} else if !present {
				value, present = write.descriptor.WriteFieldValue(write.mutation.value, field.Clone())
				values[field.Name] = value
			}
			if !present || !mutationValueMatches(field, value) {
				return validation.Errors{}, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: field.Name, Detail: "constraint member is missing or invalid in the candidate model"}
			}
			null = null || value.IsNull()
			conditions = append(conditions, query.NewCondition(fieldReference(field), query.LookupExact, value))
		}
		if null {
			continue
		}
		plan, err := base.WithConditions(conditions...)
		if err != nil {
			return validation.Errors{}, err
		}
		checks = append(checks, check{violation: constraint.violation, plan: plan})
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
			violations = append(violations, check.violation)
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
