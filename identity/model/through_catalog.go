package model

import (
	"context"
	"errors"
	"slices"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CatalogThroughConfig reuses the catalog's scalar creation policy. The actual
// collection owner supplies both endpoint FKs, independently of WriteFields;
// the factory cannot assign them, revision, keys, collections or file values.
// Prepare is pure and bounded. Creation.Validate keeps CatalogValidator's
// actual-key contract and runs after the provisional INSERT, before commit.
// RelatedPermissions covers direct membership changes caused by the new row;
// the enclosing edit retains its independent deletion/change policies.
type CatalogThroughConfig[M any] struct {
	Creation           CatalogModelConfig[M]
	RelatedPermissions RelatedRowPermissions
	Prepare            func(context.Context, ThroughEndpoints) (orm.ModelInput[M], error)
}

func NewGroupThroughCreation[M, G any](binding Binding[M], descriptor orm.RelationObjectDescriptor[G], overlay auth.Authorizer, config CatalogThroughConfig[G]) (ThroughCreation, error) {
	if binding.state == nil {
		return ThroughCreation{}, managementError(CodeInvalidConfig, nil)
	}
	role := binding.state.role.Catalog
	return newCatalogThroughCreation(binding, descriptor, role.Group, role.GroupName, "", role.GroupRevision, role.GroupPermissions, overlay, config)
}

func NewPermissionThroughCreation[M, P any](binding Binding[M], descriptor orm.RelationObjectDescriptor[P], overlay auth.Authorizer, config CatalogThroughConfig[P]) (ThroughCreation, error) {
	if binding.state == nil {
		return ThroughCreation{}, managementError(CodeInvalidConfig, nil)
	}
	role := binding.state.role.Catalog
	return newCatalogThroughCreation(binding, descriptor, role.Permission, role.PermissionName, role.PermissionCode, role.PermissionRevision, "", overlay, config)
}

func newCatalogThroughCreation[M, C any](binding Binding[M], descriptor orm.RelationObjectDescriptor[C], identity ir.ModelIdentity, name, code, revision, collection string, overlay auth.Authorizer, config CatalogThroughConfig[C]) (ThroughCreation, error) {
	var zero ThroughCreation
	if !zeroModelDescriptor(descriptor) || nilModelValue(overlay) || config.Prepare == nil {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	project, role := binding.state.project, binding.state.role
	bound, err := orm.BindModel(project, identity, descriptor)
	if err != nil {
		return zero, managementError(CodeInvalidConfig, err)
	}
	state, err := newCatalogModel(nil, orm.NewManager[C](descriptor), identity, name, code, revision, collection, config.Creation)
	if err != nil {
		return zero, err
	}
	if state.allowed[collection] {
		return zero, managementError(CodeInvalidConfig, nil)
	}
	for _, field := range state.model.ManyToMany {
		if !field.Blank && field.Name != collection {
			return zero, managementError(CodeInvalidConfig, nil)
		}
	}
	for _, permission := range []*DeletionPermissions{&config.RelatedPermissions.Account, &config.RelatedPermissions.Group, &config.RelatedPermissions.Permission} {
		if _, err := lifecycleRequiredPermissions(permission.Change, permission.Additional, false); err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
		if _, err := lifecycleRequiredPermissions(permission.Delete, permission.Additional, false); err != nil {
			return zero, managementError(CodeInvalidConfig, err)
		}
		permission.Additional = slices.Clone(permission.Additional)
	}
	memberships, err := bindDeletionMemberships(project, role)
	if err != nil {
		return zero, err
	}
	owned, err := bindOwnedMemberships(project, identity)
	if err != nil {
		return zero, err
	}
	key := name
	if code != "" {
		key = code
	}
	policy := &throughCreationState{project: project, role: binding.state.roleFingerprint, model: identity, identity: key}
	policy.endpoints = func(source, target string) error {
		if source == target {
			return managementError(CodeInvalidConfig, nil)
		}
		for _, name := range []string{source, target} {
			field, found := namedField(state.model, name)
			if !found || field.Relation == nil || field.PrimaryKey || state.allowed[name] {
				return managementError(CodeInvalidConfig, nil)
			}
		}
		return nil
	}
	policy.attach = func(owner any) (*throughCreationState, error) {
		sessions, ok := owner.(*SessionStore[M])
		if !ok || sessions == nil || sessions.state == nil {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		if err := sessions.state.credentials.valid(); err != nil {
			return nil, managementError(CodeInvalidConfig, err)
		}
		attached := *policy
		attached.attach = nil
		err := bindTypedThroughInput(&attached, bound, state.resource.Manager(), overlay, state.permissions.add,
			func(ctx context.Context, scope db.Queryer, pair ThroughEndpoints, _ auth.CredentialDomain) (*throughCandidate, error) {
				input, err := config.Prepare(ctx, pair)
				if err = errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
					return nil, err
				}
				return prepareCatalogThrough(state, &attached, role, overlay, config.RelatedPermissions, memberships, owned, input, pair)
			},
			func(candidate *throughCandidate) (C, error) {
				value, ok := candidate.value.(*catalogThroughCandidate[C])
				if !ok || value == nil {
					var zero C
					return zero, managementError(CodeInvalidConfig, nil)
				}
				return value.row, nil
			})
		if err != nil {
			return nil, err
		}
		return &attached, nil
	}
	return ThroughCreation{state: policy}, nil
}

type catalogThroughCandidate[M any] struct {
	prepared orm.PreparedModel[M]
	row      M
}

func prepareCatalogThrough[M any](state *catalogModelState[M], policy *throughCreationState, role ir.AccountRole, overlay auth.Authorizer, related RelatedRowPermissions, memberships, owned []deletionMembership, input orm.ModelInput[M], pair ThroughEndpoints) (*throughCandidate, error) {
	if len(input.Collections()) != 0 {
		return nil, managementError(CodeInvalidInput, nil)
	}
	resource := CatalogResource[M]{state: state}
	input, err := resource.prepareInput(input, 0, true)
	if err != nil {
		return nil, err
	}
	objects := state.resource.Manager()
	input, err = objects.WithServerValues(input, map[string]query.Value{
		state.revision: query.Integer(1), pair.source.Name: query.Integer(pair.sourceKey), pair.target.Name: query.Integer(pair.targetKey),
	})
	if err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	prepared, err := objects.PrepareInput(input, nil)
	if err != nil {
		return nil, err
	}
	row, err := prepared.Model()
	if err != nil {
		return nil, err
	}
	values, _, revision, err := state.inspect(row, false)
	if err != nil || revision != 1 {
		return nil, managementError(CodeInvalidInput, err)
	}
	natural, valid := values[policy.identity].String()
	if !valid || natural == "" {
		return nil, managementError(CodeInvalidInput, nil)
	}
	required, err := throughEffectPermissions(role, policy.model, state.permissions.add, related, values, memberships)
	if err != nil {
		return nil, err
	}
	var fields []string
	for _, field := range state.model.Fields {
		if state.allowed[field.Name] || field.Name == pair.source.Name || field.Name == pair.target.Name {
			fields = append(fields, field.Name)
		}
	}
	if _, err := admin.PrepareEventTemplate("catalog-through-startup", state.modelName(), admin.ActionAdd, fields, ""); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	candidate := &catalogThroughCandidate[M]{prepared: prepared, row: row}
	result := &throughCandidate{policy: policy, identity: throughIdentity{policy.model, natural}, value: candidate}
	result.encode = func(ctx context.Context) error { return ctx.Err() }
	result.check = func(ctx context.Context, scope db.Queryer, actor auth.Principal, _ bool, deleted []int64) error {
		if err := authorizeThroughActor(ctx, scope, actor, overlay, required); err != nil {
			return err
		}
		failures, err := candidate.prepared.ValidateUniqueAfterDeletion(ctx, scope, candidate.row, deleted)
		if err != nil {
			return err
		}
		return validation.Reject(failures, nil)
	}
	result.finish = func(ctx context.Context, scope db.RelationSession, actor auth.Principal, id int64, at time.Time) (accountaudit.Event, lifecycleVerifier, error) {
		var zero accountaudit.Event
		actual, err := objects.Using(scope).Filter(state.key.Exact(id)).Get(ctx)
		if err != nil {
			return zero, nil, err
		}
		_, key, revision, err := state.inspect(actual, true)
		if err != nil || key != id || revision != 1 {
			return zero, nil, managementError(CodePersistence, err)
		}
		verify := func(ctx context.Context, scope db.Session) error {
			return verifyEmptyOwnedMemberships(ctx, scope, owned, policy.model, id)
		}
		if err := verify(ctx, scope); err != nil {
			return zero, nil, err
		}
		// Catalog custom validation retains its actual generated-key contract.
		// Authority is the actor captured before this operation's first write.
		if err := state.validateCandidate(ctx, scope, nil, actual); err != nil {
			return zero, nil, err
		}
		event, err := accountaudit.Prepare(actor, state.modelName(), id, admin.ActionAdd, fields, at)
		return event, verify, err
	}
	return result, nil
}
