package model

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// MaximumProfileCollectionItems bounds a complete distinct profile membership
// observed by account management. It is not a query page size: overflow fails
// closed instead of publishing or validating a truncated membership set.
const MaximumProfileCollectionItems = formmodel.MaximumModelChoiceKeys

// ProfileCollectionConfig explicitly authorizes an additional account M2M
// assignment. MaximumItems must be in 1..MaximumProfileCollectionItems; it
// bounds raw submitted keys before deduplication and the final stored set.
// Filter has the same borrowed-scope and current-actor contract as
// ProfileRelationConfig. A nil Filter is not an unrestricted policy.
type ProfileCollectionConfig[T any] struct {
	MaximumItems int
	Choices      formmodel.ModelChoiceConfig[T]
	Filter       func(context.Context, db.Queryer, auth.Principal, ProfileRelationOrigin) ([]orm.Predicate[T], error)
}

type ProfileCollection[M any] struct{ state *profileCollectionState[M] }
type profileCollectionState[M any] struct {
	field        ir.ManyToManyField
	source       string
	role         [32]byte
	maximum      int
	prepare      func(M, []int64) (orm.CollectionChange, error)
	check        func(context.Context, db.Queryer, auth.Principal, ProfileRelationOrigin, []int64) error
	adminChoices func(auth.Permission, profileRelationChoiceScope) (admin.RelatedChoices, error)
}

func (ProfileCollection[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ProfileCollection{redacted}"))
}

// NewProfileCollection binds the actual typed collection and target to the
// complete account source. Role memberships keep their dedicated authority
// policy. This pure choice declaration grants no storage capability. A mutation
// owner must separately bind explicit creation for selected-identity through
// rows; a generic setter cannot create them.
func NewProfileCollection[M, T, L any](binding Binding[M], field string, collection orm.ManyToMany[M, T, L], target orm.Manager[T], config ProfileCollectionConfig[T]) (ProfileCollection[M], error) {
	var zero ProfileCollection[M]
	if binding.state == nil || config.Filter == nil || config.MaximumItems < 1 || config.MaximumItems > MaximumProfileCollectionItems {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	role, project := binding.state.role, binding.state.project
	if field == role.Groups || field == role.Permissions {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	if err := collection.ValidateSource(project, role.Model, field); err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	_, declared, err := collection.Declaration()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	choices, err := formmodel.NewRelationChoices(project, role.Model, field, target, config.Choices)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	source, err := project.SourceSHA256()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	filter := func(ctx context.Context, scope db.Queryer, actor auth.Principal, origin ProfileRelationOrigin) ([]orm.Predicate[T], error) {
		validator, ok := scope.(db.SessionValidator)
		if !ok || nilModelValue(validator) {
			return nil, invalidAuthorityScope()
		}
		if err := validateAuthorityScope(ctx, validator); err != nil {
			return nil, err
		}
		predicates, err := config.Filter(ctx, scope, actor, origin)
		if err := errors.Join(err, validateAuthorityScope(ctx, validator)); err != nil {
			return nil, err
		}
		return slices.Clone(predicates), nil
	}
	state := &profileCollectionState[M]{field: declared, source: source, role: binding.state.roleFingerprint, maximum: config.MaximumItems}
	state.prepare = func(row M, keys []int64) (orm.CollectionChange, error) { return collection.SetKeysChange(row, keys) }
	state.check = func(ctx context.Context, scope db.Queryer, actor auth.Principal, origin ProfileRelationOrigin, keys []int64) error {
		if len(keys) > state.maximum {
			return profileInputError(field, "max_items")
		}
		predicates, err := filter(ctx, scope, actor, origin)
		if err != nil {
			return err
		}
		selected, err := choices.Filter(predicates...).Resolve(ctx, scope, keys)
		if err := errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
			return err
		}
		if len(keys) == 0 && !declared.Blank {
			return profileInputError(field, "required")
		}
		if len(selected.Items()) != len(keys) {
			return profileInputError(field, "invalid_choice")
		}
		return nil
	}
	state.adminChoices = func(permission auth.Permission, withScope profileRelationChoiceScope) (admin.RelatedChoices, error) {
		return admin.NewRelatedChoices(admin.RelatedChoiceConfig[T]{Choices: choices, Permission: permission,
			Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin, visit func(db.Queryer, []orm.Predicate[T]) error) error {
				return withScope(ctx, actor, origin, func(scope db.Queryer, current auth.Principal, source ProfileRelationOrigin) error {
					predicates, err := filter(ctx, scope, current, source)
					if err != nil {
						return err
					}
					return visit(scope, predicates)
				})
			},
		})
	}
	return ProfileCollection[M]{state: state}, nil
}

func prepareProfileCollections[M any](binding Binding[M], fields []string, configured []ProfileCollection[M]) ([]ProfileCollection[M], error) {
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	source, err := binding.state.project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	byName := make(map[string]ProfileCollection[M], len(configured))
	for _, collection := range configured {
		state := collection.state
		if state == nil || state.source != source || state.role != binding.state.roleFingerprint || !slices.Contains(fields, state.field.Name) || byName[state.field.Name].state != nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		byName[state.field.Name] = collection
	}
	role := binding.state.role
	var result []ProfileCollection[M]
	for _, field := range metadata.ManyToMany {
		if field.Name == role.Groups || field.Name == role.Permissions || !slices.Contains(fields, field.Name) {
			continue
		}
		collection, present := byName[field.Name]
		if !present {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		result = append(result, collection)
	}
	if len(result) != len(configured) {
		return nil, managementError(CodeInvalidConfig, nil)
	}
	return result, nil
}

func selectProfileCollections[M any](collections []ProfileCollection[M], fields []string) []ProfileCollection[M] {
	var result []ProfileCollection[M]
	for _, collection := range collections {
		if slices.Contains(fields, collection.state.field.Name) {
			result = append(result, collection)
		}
	}
	return result
}

func checkProfileCollections[M any](ctx context.Context, scope db.Queryer, actor auth.Principal, origin ProfileRelationOrigin, policies []ProfileCollection[M], values map[string][]int64) error {
	var failures validation.Errors
	for _, policy := range policies {
		keys, present := values[policy.state.field.Name]
		if !present { // A partial-invalid Form field is not an empty assignment.
			continue
		}
		if err := policy.state.check(ctx, scope, actor, origin, keys); err != nil {
			diagnostics, rejected := validation.Rejected(err)
			if !rejected || errors.Unwrap(err) != nil {
				return err
			}
			failures = validation.Join(failures, diagnostics)
		}
	}
	return validation.Reject(failures, nil)
}

func profileCollectionLimits[M any](collections []ProfileCollection[M]) map[string]int {
	result := make(map[string]int, len(collections))
	for _, collection := range collections {
		result[collection.state.field.Name] = collection.state.maximum
	}
	return result
}

func namedCollection(metadata ir.Model, name string) (ir.ManyToManyField, bool) {
	for _, field := range metadata.ManyToMany {
		if field.Name == name {
			return field, true
		}
	}
	return ir.ManyToManyField{}, false
}

func normalizeAccountCollections[M any](role ir.AccountRole, allowed map[string]bool, policies []ProfileCollection[M], values map[string][]int64) error {
	limits := profileCollectionLimits(policies)
	limits[role.Groups], limits[role.Permissions] = MaximumAccountGroups, auth.MaximumPermissions
	for name, keys := range values {
		maximum, known := limits[name]
		if !allowed[name] || !known {
			return managementError(CodeInvalidInput, nil)
		}
		if len(keys) > maximum {
			return profileInputError(name, "max_items")
		}
		slices.Sort(keys)
		values[name] = slices.Compact(keys)
	}
	return nil
}

func checkAccountCollectionForm[M any](role ir.AccountRole, policies []ProfileCollection[M], bound formmodel.BoundForm) error {
	limits := profileCollectionLimits(policies)
	limits[role.Groups], limits[role.Permissions] = MaximumAccountGroups, auth.MaximumPermissions
	var failures validation.Errors
	for _, field := range bound.Model().ManyToMany {
		name := field.Name
		maximum, selected := limits[name]
		if !selected {
			continue
		}
		if raw, submitted := bound.Form().Submitted().Get(name); submitted && len(raw) > maximum {
			failures = validation.Join(failures, validation.NewErrors(validation.New(validation.Field(name), "max_items")))
		}
	}
	return validation.Reject(failures, nil)
}
