package crud

import (
	"context"
	"maps"
	"slices"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

// UniqueMode selects the existing ORM advisory validation policy. Both modes
// retain native uniqueness enforcement and skip an unchanged scalar write.
type UniqueMode uint8

const (
	UniqueChanged UniqueMode = iota
	UniqueAll
)

// CollectionSaver is an explicit application-owned relation write. Every
// selected collection must have exactly one saver before any I/O begins.
// Save receives owned keys and must use the borrowed session, check its current
// relation policy and return whether membership actually changed.
type CollectionSaver[M any] struct {
	Field string
	Save  func(context.Context, db.Session, M, []int64) (bool, error)
}

type SaveOptions[M any] struct {
	// ServerValues are added after checking client exposure. Collisions with
	// supplied input fail rather than silently overriding client or server data.
	ServerValues map[string]query.Value
	// Before runs after fresh scoped lookup and pure model preparation, before
	// uniqueness or DML. Current is nil on create. Both pointers are detached;
	// edits to candidate are explicit server policy, and key changes fail.
	// Check current authorization, revision and the complete relation selection
	// here. Returning an error publishes no candidate and performs no CRUD DML.
	Before      func(ctx context.Context, session db.Session, current, candidate *M) error
	Unique      UniqueMode
	Collections []CollectionSaver[M]
}

// Mutation owns its model snapshots and actual changed-field names. A return
// from an InSession method is provisional: the owner must propagate errors,
// prepare output/audit and confirm commit before publishing an application result.
// Creation reports all inserted scalar fields. Before is nil only for creation.
type Mutation[M any] struct {
	Before        *M
	Value         M
	ChangedFields []string
}

func (resource Resource[M]) CreateInSession(ctx context.Context, session db.Session, input orm.ModelInput[M], options SaveOptions[M]) (Mutation[M], error) {
	options = options.owned()
	if err := resource.checkSave(ctx, session, input, options); err != nil {
		return Mutation[M]{}, err
	}
	if key, bound := input.TargetKey(); bound && !key.IsNull() {
		return Mutation[M]{}, invalid("create input is bound to an existing instance")
	}
	return resource.save(ctx, session, nil, input, options)
}

func (resource Resource[M]) UpdateInSession(ctx context.Context, session db.Session, id int64, input orm.ModelInput[M], options SaveOptions[M]) (Mutation[M], error) {
	options = options.owned()
	if err := resource.checkSave(ctx, session, input, options); err != nil {
		return Mutation[M]{}, err
	}
	if key, bound := input.TargetKey(); bound && !key.Equal(query.Integer(id)) {
		return Mutation[M]{}, invalid("update input is bound to a different instance")
	}
	current, found, err := resource.Get(ctx, session, id)
	if err != nil {
		return Mutation[M]{}, err
	}
	if !found {
		return Mutation[M]{}, ErrNotFound
	}
	return resource.save(ctx, session, &current, input, options)
}

func (options SaveOptions[M]) owned() SaveOptions[M] {
	options.ServerValues = maps.Clone(options.ServerValues)
	options.Collections = slices.Clone(options.Collections)
	return options
}

func (resource Resource[M]) checkSave(ctx context.Context, session db.Session, input orm.ModelInput[M], options SaveOptions[M]) error {
	if err := resource.check(ctx, session); err != nil {
		return err
	}
	if err := resource.checkInput(input); err != nil {
		return err
	}
	if options.Unique > UniqueAll {
		return invalid("uniqueness policy is unsupported")
	}
	known := make(map[string]bool, len(resource.model.ManyToMany))
	for _, field := range resource.model.ManyToMany {
		known[field.Name] = true
	}
	configured := make(map[string]bool, len(options.Collections))
	for _, saver := range options.Collections {
		if !known[saver.Field] || configured[saver.Field] || saver.Save == nil {
			return invalid("collection saver is unknown, repeated or nil")
		}
		configured[saver.Field] = true
	}
	for name := range input.Collections() {
		if !configured[name] {
			return invalid("selected collection has no explicit saver")
		}
	}
	return nil
}

func (resource Resource[M]) save(ctx context.Context, session db.Session, current *M, input orm.ModelInput[M], options SaveOptions[M]) (Mutation[M], error) {
	var zero Mutation[M]
	input, err := resource.manager.WithServerValues(input, options.ServerValues)
	if err != nil {
		return zero, err
	}
	prepared, err := resource.manager.PrepareInput(input, current)
	if err != nil {
		return zero, err
	}
	value, err := prepared.Model()
	if err != nil {
		return zero, err
	}
	if options.Before != nil {
		original, err := resource.manager.ModelValues(value)
		if err != nil {
			return zero, err
		}
		before, err := resource.copyPointer(current)
		if err != nil {
			return zero, err
		}
		if err := options.Before(ctx, session, before, &value); err != nil {
			return zero, err
		}
		if err := resource.check(ctx, session); err != nil {
			return zero, err
		}
		after, err := resource.manager.ModelValues(value)
		if err != nil {
			return zero, err
		}
		if !original[resource.keyName].Equal(after[resource.keyName]) {
			return zero, invalid("write policy changed the candidate primary key")
		}
	}
	changed, err := prepared.Changes(value)
	if err != nil {
		return zero, err
	}
	if len(changed) > 0 {
		var failures validation.Errors
		if options.Unique == UniqueAll {
			values, failure := resource.manager.ModelValues(value)
			if failure != nil {
				return zero, failure
			}
			delete(values, resource.keyName)
			fields, failure := resource.manager.ValidateUniqueFields(ctx, session, values, current)
			if failure != nil {
				return zero, failure
			}
			constraints, failure := resource.manager.ValidateUniqueConstraints(ctx, session, values, current)
			if failure != nil {
				return zero, failure
			}
			failures = validation.Join(fields, constraints)
		} else {
			failures, err = prepared.ValidateUnique(ctx, session, value)
			if err != nil {
				return zero, err
			}
		}
		if !failures.Empty() {
			return zero, validation.Reject(failures, nil)
		}
	}
	if err := prepared.Save(ctx, session, &value); err != nil {
		return zero, err
	}
	// Validate every saver before DML, then execute only selected collections
	// in model order. The owner propagates any error so scalar/links roll back.
	for _, field := range resource.model.ManyToMany {
		keys, selected := input.Collection(field.Name)
		if !selected {
			continue
		}
		for _, saver := range options.Collections {
			if saver.Field != field.Name {
				continue
			}
			owner, err := resource.manager.ApplyValues(value, nil)
			if err != nil {
				return zero, err
			}
			updated, err := saver.Save(ctx, session, owner, keys)
			if err != nil {
				return zero, err
			}
			if updated {
				changed = append(changed, field.Name)
			}
			break
		}
	}
	if err := resource.check(ctx, session); err != nil {
		return zero, err
	}
	before, err := resource.copyPointer(current)
	if err != nil {
		return zero, err
	}
	owned, err := resource.manager.ApplyValues(value, nil)
	if err != nil {
		return zero, err
	}
	return Mutation[M]{Before: before, Value: owned, ChangedFields: slices.Clone(changed)}, nil
}

func (resource Resource[M]) copyPointer(value *M) (*M, error) {
	if value == nil {
		return nil, nil
	}
	copy, err := resource.manager.ApplyValues(*value, nil)
	if err != nil {
		return nil, err
	}
	return &copy, nil
}

type DeleteOptions[M any] struct {
	// Before checks the fresh object and may prepare a detached representation
	// of relations that deletion will remove. The callback cannot change the
	// object used by the delete policy.
	Before func(context.Context, db.Session, M) error
	// Delete is mandatory: bind the host's generated relation deleter for
	// relation-aware models, or explicitly choose Manager.Delete for a model
	// whose application owns a simple scalar delete policy.
	Delete func(context.Context, db.Session, M) (int64, error)
}

func (resource Resource[M]) DeleteInSession(ctx context.Context, session db.Session, id int64, options DeleteOptions[M]) (M, error) {
	var zero M
	if err := resource.check(ctx, session); err != nil {
		return zero, err
	}
	if resource.readOnly {
		return zero, ErrReadOnly
	}
	if options.Delete == nil {
		return zero, invalid("deletion requires an explicit host policy")
	}
	current, found, err := resource.Get(ctx, session, id)
	if err != nil {
		return zero, err
	}
	if !found {
		return zero, ErrNotFound
	}
	if options.Before != nil {
		value, err := resource.manager.ApplyValues(current, nil)
		if err != nil {
			return zero, err
		}
		if err := options.Before(ctx, session, value); err != nil {
			return zero, err
		}
		if err := resource.check(ctx, session); err != nil {
			return zero, err
		}
	}
	value, err := resource.manager.ApplyValues(current, nil)
	if err != nil {
		return zero, err
	}
	count, err := options.Delete(ctx, session, value)
	if err != nil {
		return zero, err
	}
	if count <= 0 {
		return zero, ErrNotFound
	}
	if err := resource.check(ctx, session); err != nil {
		return zero, err
	}
	return resource.manager.ApplyValues(current, nil)
}
