package model

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CatalogDescription is detached source and operation metadata for transport
// declarations. It grants no current access to rows or permission assignments.
type CatalogDescription struct {
	Identity                                             ir.ModelIdentity
	KeyField, LabelField, RevisionField, CollectionField string
	Permissions                                          CatalogPermissions
}

func (resource CatalogResource[M]) Description() (CatalogDescription, error) {
	if resource.state == nil {
		return CatalogDescription{}, managementError(CodeInvalidConfig, nil)
	}
	state := resource.state
	return CatalogDescription{
		Identity: state.identity, KeyField: state.keyName, LabelField: state.name,
		RevisionField: state.revision, CollectionField: state.collection,
		Permissions: CatalogPermissions{View: state.permissions.view[0], Add: state.permissions.add[0],
			Change: state.permissions.change[0], Delete: state.permissions.delete[0],
			Additional: slices.Clone(state.permissions.view[1:])},
	}, nil
}

// CheckCreateForm checks even partially invalid input in one current-authorized
// read snapshot. Successful advisory validation neither reserves a unique value
// nor replaces Create's final authority, collection and model-policy checks.
func (resource CatalogResource[M]) CheckCreateForm(ctx context.Context, actor auth.Principal, bound formmodel.BoundForm) error {
	return resource.checkForm(ctx, actor, 0, 0, bound, true)
}

// CheckChangeForm distinguishes an existing zero/negative key from creation.
// The form must belong to this exact model and observed target/revision.
func (resource CatalogResource[M]) CheckChangeForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm) error {
	return resource.checkForm(ctx, actor, id, revision, bound, false)
}

type catalogFormCheck struct {
	failures validation.Errors
	conflict bool
}

func (resource CatalogResource[M]) checkForm(ctx context.Context, actor auth.Principal, id, revision int64, bound formmodel.BoundForm, create bool) error {
	if err := resource.validCall(ctx, actor); err != nil {
		return err
	}
	state := resource.state
	if !bound.Form().Bound() || bound.Form().ReadOnly() || !bound.Model().Equal(state.model) || !create && (revision <= 0 || revision == math.MaxInt64) {
		return managementError(CodeInvalidInput, nil)
	}
	key, present := bound.Candidate().Get(state.keyName)
	if !present || create && !key.IsNull() {
		return managementError(CodeInvalidInput, nil)
	}
	if !create {
		version, versionPresent := bound.Candidate().Integer(state.revision)
		actual, integer := key.AsInteger()
		if !integer || actual != id || !versionPresent || version != revision {
			return managementError(CodeInvalidInput, nil)
		}
	}
	values, err := bound.ValidationValues()
	if err != nil {
		return managementError(CodeInvalidInput, err)
	}
	for name := range values {
		if !state.allowed[name] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	for _, entry := range bound.Form().Cleaned().All() {
		if !state.allowed[entry.Name()] {
			return managementError(CodeInvalidInput, nil)
		}
	}
	required := state.permissions.change
	if create {
		required = state.permissions.add
	}
	result, err := managementSnapshot(ctx, state.shared.reader, func(work context.Context, scope db.Queryer) (auth.Principal, bool, error) {
		return state.shared.authorize(work, scope, actor, required)
	}, func(work context.Context, scope db.Queryer, _ auth.CredentialDomain) (catalogFormCheck, bool, error) {
		var current *M
		if !create {
			row, present, err := state.resource.Get(work, scope, id)
			if err != nil || !present {
				return catalogFormCheck{}, present, err
			}
			_, actualKey, actualRevision, err := state.inspect(row, true)
			if err != nil || actualKey != id {
				return catalogFormCheck{}, true, managementError(CodePersistence, err)
			}
			if actualRevision != revision {
				return catalogFormCheck{conflict: true}, true, nil
			}
			current = &row
		}
		var related validation.Errors
		if state.collection != "" {
			if keys, selected := bound.Form().Cleaned().Integers(state.collection); selected {
				if len(keys) > auth.MaximumPermissions {
					related = validation.NewErrors(validation.New(validation.Field(state.collection), "max_items"))
				} else {
					slices.Sort(keys)
					err := state.checkKeys(work, scope, slices.Compact(keys))
					if err != nil {
						var rejected bool
						related, rejected = validation.Rejected(err)
						if !rejected || errors.Unwrap(err) != nil {
							return catalogFormCheck{}, true, err
						}
					}
				}
			}
		}
		checked, err := bound.WithErrors(related)
		if err != nil {
			return catalogFormCheck{}, true, err
		}
		objects := state.resource.Manager()
		failures, err := checked.CheckDatabase(work, formmodel.DatabaseChecks{
			UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				return objects.ValidateUniqueFields(ctx, scope, values, current)
			},
			Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
				return objects.ValidateUniqueConstraints(ctx, scope, values, current)
			},
		})
		return catalogFormCheck{failures: validation.Join(related, failures)}, true, err
	})
	if err != nil {
		return err
	}
	if result.conflict {
		return managementError(CodeConflict, nil)
	}
	return validation.Reject(result.failures, nil)
}
