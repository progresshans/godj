package orm

import (
	"context"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// ValidateUniqueFields checks only supplied scalar unique fields. Unlike a
// write input, this candidate may omit fields rejected by a form or model
// validator. Omission excludes a field; it never means a default or SQL NULL.
// Values must use the manager's model field names and scalar types. SQL NULL
// candidates are not queried, even for a nonnullable stored field: model input
// validation and the final write's nullability checks are separate owners.
// The caller must authorize the read and, for an update, load current in that
// authorized scope. A present zero primary key excludes that row correctly.
// These advisory reads grant no write authority and do not reserve values.
func (m Manager[M]) ValidateUniqueFields(ctx context.Context, backend db.Queryer, values map[string]query.Value, current *M) (validation.Errors, error) {
	model, base, candidate, err := m.prepareUniqueCandidate(ctx, backend, values, current)
	if err != nil {
		return validation.Errors{}, err
	}
	var checks []uniqueCheck
	for _, field := range model.metadata.Fields {
		value, present := candidate[field.Name]
		if !field.Unique || field.PrimaryKey || !present || value.IsNull() {
			continue
		}
		plan, err := base.WithConditions(query.NewCondition(fieldReference(field), query.LookupExact, value))
		if err != nil {
			return validation.Errors{}, err
		}
		checks = append(checks, uniqueCheck{plan: plan, violation: validation.New(validation.Field(field.Name), validation.CodeUnique)})
	}
	return runUniqueChecks(ctx, backend, checks)
}

// ValidateUniqueConstraints checks declared model uniqueness constraints only
// when every member is supplied and non-null. Call it after applying field
// uniqueness errors and removing their fields from the candidate. This keeps
// model constraints separate from field uniqueness without requiring an
// otherwise valid write mutation. No omitted member is read from current.
// Storage/cancellation failures return no partial diagnostics.
func (m Manager[M]) ValidateUniqueConstraints(ctx context.Context, backend db.Queryer, values map[string]query.Value, current *M) (validation.Errors, error) {
	model, base, candidate, err := m.prepareUniqueCandidate(ctx, backend, values, current)
	if err != nil {
		return validation.Errors{}, err
	}
	var checks []uniqueCheck
	for _, constraint := range model.unique {
		conditions := make([]query.Condition, 0, len(constraint.fields))
		for _, field := range constraint.fields {
			value, present := candidate[field.Name]
			if !present || value.IsNull() {
				conditions = nil
				break
			}
			conditions = append(conditions, query.NewCondition(fieldReference(field), query.LookupExact, value))
		}
		if len(conditions) == 0 {
			continue
		}
		plan, err := base.WithConditions(conditions...)
		if err != nil {
			return validation.Errors{}, err
		}
		checks = append(checks, uniqueCheck{plan: plan, violation: constraint.violation})
	}
	return runUniqueChecks(ctx, backend, checks)
}

func (m Manager[M]) prepareUniqueCandidate(ctx context.Context, backend db.Queryer, values map[string]query.Value, current *M) (*preparedModel, query.Plan, map[string]query.Value, error) {
	descriptor, model, err := m.writeConfiguration(ctx, backend)
	if err != nil {
		return nil, query.Plan{}, nil, err
	}
	if model.uniqueErr != "" {
		return nil, query.Plan{}, nil, invalidWritePlan(model.uniqueErr)
	}
	var key query.Value
	if current != nil {
		var present bool
		key, present = descriptor.PrimaryKey(descriptor.CloneWriteModel(*current))
		if !present {
			return nil, query.Plan{}, nil, &query.Error{Category: query.CategoryQuery, Code: query.CodeMissingPrimaryKey, Field: model.primaryKey.Name, Detail: "model instance has no explicit primary key state"}
		}
		if key.IsNull() || !mutationValueMatches(model.primaryKey, key) {
			return nil, query.Plan{}, nil, invalidWritePlan("descriptor returned an invalid primary key value")
		}
	}
	// Validate all entries, including non-unique fields, before any query. Sort
	// names for deterministic malformed-input errors; query order remains IR order.
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	candidate := make(map[string]query.Value, len(values))
	for _, name := range names {
		index, found := model.byName[name]
		if !found {
			return nil, query.Plan{}, nil, &query.Error{Category: query.CategoryField, Code: query.CodeUnknownField, Field: name, Detail: "candidate field is not descriptor metadata"}
		}
		field, value := model.metadata.Fields[index], values[name]
		if !value.IsNull() && !mutationValueMatches(field, value) {
			return nil, query.Plan{}, nil, &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue, Field: name, Detail: "candidate value does not match descriptor metadata"}
		}
		if field.PrimaryKey && (current == nil || !value.Equal(key)) {
			return nil, query.Plan{}, nil, invalidWritePlan("candidate primary key is not the current model's key")
		}
		candidate[name] = value
	}
	base, err := uniqueBasePlan(model, key, current != nil)
	if err != nil {
		return nil, query.Plan{}, nil, err
	}
	return model, base, candidate, nil
}
