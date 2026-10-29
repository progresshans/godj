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
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// ProfileRelationOperation identifies the owner requesting a relation choice.
// First-account provisioning has no authenticated actor and is an explicit
// operator operation, never an implicit fallback from denied account creation.
type ProfileRelationOperation string

const (
	ProfileRelationFirstAccount ProfileRelationOperation = "first-account"
	ProfileRelationCreate       ProfileRelationOperation = "create"
	ProfileRelationTransition   ProfileRelationOperation = "transition"
	ProfileRelationChange       ProfileRelationOperation = "change"
)

// ProfileRelationOrigin is supplied by the account owner after its current
// admission checks. A change retains an explicitly present zero or negative
// target key. It carries no submitted candidate, credential or request data.
type ProfileRelationOrigin struct {
	operation ProfileRelationOperation
	id        int64
}

func (origin ProfileRelationOrigin) Operation() ProfileRelationOperation { return origin.operation }
func (origin ProfileRelationOrigin) TargetID() (int64, bool) {
	return origin.id, origin.operation == ProfileRelationChange
}
func (ProfileRelationOrigin) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ProfileRelationOrigin{redacted}"))
}

// ProfileRelationConfig uses the same typed target selection for bounded
// Form/Admin choices, advisory checks and final writes. Filter is required;
// returning no predicates explicitly permits every current target row.
// It receives a current actor (zero only for FirstAccount) and a borrowed
// native reader. It must not mutate, retain or re-enter that scope, publish
// external effects, or infer authority from the target key. All callbacks
// must be safe for concurrent calls. Choice presentation is pure.
type ProfileRelationConfig[T any] struct {
	Choices formmodel.ModelChoiceConfig[T]
	Filter  func(context.Context, db.Queryer, auth.Principal, ProfileRelationOrigin) ([]orm.Predicate[T], error)
}

// ProfileRelation binds one FK or OneToOne profile field to its actual typed
// target and complete account project source. It retains no backend, actor,
// borrowed scope, evaluated rows or durable selection permission.
type ProfileRelation[M any] struct{ state *profileRelationState }
type profileRelationState struct {
	field        ir.Field
	model        ir.ModelIdentity
	creationOnly bool
	source       string
	role         [32]byte
	check        func(context.Context, db.Queryer, auth.Principal, ProfileRelationOrigin, query.Value) error
	adminChoices func(auth.Permission, profileRelationChoiceScope) (admin.RelatedChoices, error)
}
type profileRelationChoiceScope func(context.Context, auth.Principal, admin.ChoiceOrigin, func(db.Queryer, auth.Principal, ProfileRelationOrigin) error) error

func (ProfileRelation[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.ProfileRelation{redacted}"))
}

// NewProfileRelation is pure construction. Binding and target must describe
// the complete canonical source, not a same-shaped foreign model. Collection
// profiles require their own coordinated mutation capability and are rejected.
func NewProfileRelation[M, T any](binding Binding[M], field string, target orm.Manager[T], config ProfileRelationConfig[T]) (ProfileRelation[M], error) {
	var zero ProfileRelation[M]
	if binding.state == nil || config.Filter == nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	declared, found := namedField(metadata, field)
	if !found || declared.Relation == nil || !declared.Relation.Cardinality.SingleValued() || declared.PrimaryKey || declared.NonEditable {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	// A selected account may itself be a through model. Its membership
	// endpoints change other owners' memberships and possibly authority. They are accepted only by
	// collected creation, never as ordinary scalar profile edits.
	role := binding.state.role
	creationOnly := false
	for _, relation := range binding.state.project.ManyToManyRelations() {
		critical := relation.Source == role.Model ||
			relation.Source == role.Catalog.Group && relation.Field == role.Catalog.GroupPermissions
		if critical && relation.Through.Model == role.Model && (relation.Through.SourceField == field || relation.Through.TargetField == field) {
			creationOnly = true
		}
	}
	state, err := newProfileRelationState(binding.state.project, binding.state.roleFingerprint, role.Model, declared, target, config, creationOnly)
	return ProfileRelation[M]{state: state}, err
}

func newProfileRelationState[T any](project orm.ProjectBinding, role [32]byte, model ir.ModelIdentity, declared ir.Field, target orm.Manager[T], config ProfileRelationConfig[T], creationOnly bool) (*profileRelationState, error) {
	choices, err := formmodel.NewRelationChoices(project, model, declared.Name, target, config.Choices)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	source, err := project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
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
	state := &profileRelationState{creationOnly: creationOnly, field: declared.Clone(), model: model, source: source, role: role}
	state.check = func(ctx context.Context, scope db.Queryer, actor auth.Principal, origin ProfileRelationOrigin, value query.Value) error {
		predicates, err := filter(ctx, scope, actor, origin)
		if err != nil {
			return err
		}
		var keys []int64
		if !value.IsNull() {
			key, valid := value.Integer()
			if !valid {
				return managementError(CodeInvalidInput, nil)
			}
			keys = []int64{key}
		}
		// Even a clear validates the current predicate/source without querying
		// an empty key set. Invalid policy cannot hide behind a NULL input.
		selected, err := choices.Filter(predicates...).Resolve(ctx, scope, keys)
		if err := errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
			return err
		}
		if value.IsNull() {
			if !declared.Nullable {
				return profileInputError(declared.Name, "required")
			}
			return nil
		}
		if len(selected.Items()) != 1 {
			return profileInputError(declared.Name, "invalid_choice")
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
	return state, nil
}

// prepareProfileRelations requires exactly one policy for every selected
// single-valued relation. A policy cannot silently expose an unselected field.
func prepareProfileRelations[M any](binding Binding[M], fields []string, configured []ProfileRelation[M]) ([]ProfileRelation[M], error) {
	metadata, err := binding.state.objects.Metadata()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	needed := false
	for _, field := range metadata.Fields {
		needed = needed || field.Relation != nil && slices.Contains(fields, field.Name)
	}
	if !needed && len(configured) == 0 {
		return nil, nil
	}
	source, err := binding.state.project.SourceSHA256()
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	byName := make(map[string]ProfileRelation[M], len(configured))
	for _, relation := range configured {
		state := relation.state
		if state == nil || state.source != source || state.role != binding.state.roleFingerprint || state.model != binding.state.role.Model || !slices.Contains(fields, state.field.Name) || byName[state.field.Name].state != nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		byName[state.field.Name] = relation
	}
	var result []ProfileRelation[M]
	for _, field := range metadata.Fields {
		if field.Relation == nil || !slices.Contains(fields, field.Name) {
			continue
		}
		relation, present := byName[field.Name]
		if !present {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		result = append(result, relation)
	}
	return result, nil
}

func selectProfileRelations[M any](relations []ProfileRelation[M], fields []string) []ProfileRelation[M] {
	var result []ProfileRelation[M]
	for _, relation := range relations {
		if slices.Contains(fields, relation.state.field.Name) {
			result = append(result, relation)
		}
	}
	return result
}

func checkProfileRelations[M any](ctx context.Context, scope db.Queryer, actor auth.Principal, origin ProfileRelationOrigin, relations []ProfileRelation[M], values map[string]query.Value) error {
	var failures validation.Errors
	for _, relation := range relations {
		if relation.state.creationOnly && origin.operation == ProfileRelationChange {
			return managementError(CodeInvalidConfig, nil)
		}
		value, present := values[relation.state.field.Name]
		if !present { // Partial-invalid Form fields are not empty assignments.
			continue
		}
		if err := relation.state.check(ctx, scope, actor, origin, value); err != nil {
			diagnostics, rejected := validation.Rejected(err)
			if !rejected || errors.Unwrap(err) != nil {
				return err
			}
			failures = validation.Join(failures, diagnostics)
		}
	}
	return validation.Reject(failures, nil)
}

func requireEditableProfileRelations[M any](relations []ProfileRelation[M]) error {
	for _, relation := range relations {
		if relation.state.creationOnly {
			return managementError(CodeInvalidConfig, nil)
		}
	}
	return nil
}
