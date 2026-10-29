package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

// A draft owns intent with one unresolved parent FK, never a typed row whose
// zero key could be mistaken for an existing parent. Hashing closes over only
// bounded credential input. Complete model policies run after real key binding.
type throughDraft struct {
	check  func(context.Context, db.Queryer, auth.Principal, bool) error
	encode func(context.Context) error
	bind   func(context.Context, ThroughEndpoints, auth.CredentialDomain) (*throughCandidate, error)
}

func prepareThroughDeclaration(model ir.ModelIdentity, fields []string, required []auth.Permission, related RelatedRowPermissions, passwordBytes int, iexact bool) (string, error) {
	encoded, err := json.Marshal(struct {
		Model         ir.ModelIdentity
		Fields        []string
		Required      []auth.Permission
		Related       RelatedRowPermissions
		PasswordBytes int
		IExact        bool
	}{model, fields, required, related, passwordBytes, iexact})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte("godj.through-creation.v1\x00"), encoded...))
	return hex.EncodeToString(digest[:]), nil
}

func throughDraftAuthority(ctx context.Context, scope db.Queryer, actor auth.Principal, operator bool, overlay auth.Authorizer, required []auth.Permission) error {
	if operator {
		// Only FirstAccount calls this branch, after same-snapshot freshness.
		// Its operator declaration is the pre-INSERT authority; a provisional
		// principal is never fabricated. The real inserted actor checks the
		// complete grants and overlay again before any nested identity INSERT.
		validator, ok := scope.(db.SessionValidator)
		if !ok || actor.Authenticated() {
			return invalidAuthorityScope()
		}
		return validateAuthorityScope(ctx, validator)
	}
	return authorizeThroughActor(ctx, scope, actor, overlay, required)
}

func throughDraftValues(metadata ir.Model, supplied map[string]query.Value, source string) (map[string]query.Value, error) {
	values, err := orm.InitialModelValues(metadata)
	if err != nil {
		return nil, err
	}
	maps.Copy(values, supplied)
	delete(values, source)
	return values, nil
}

func bindAccountThroughDraft[M any](policy *throughCreationState, state *creationState[M], memberships []deletionMembership, config AccountThroughConfig[M]) {
	policy.draft = func(ctx context.Context, scope db.Queryer, actor auth.Principal, operator bool, pair ThroughEndpoints, _ auth.CredentialDomain) (*throughDraft, error) {
		if pair.sourcePresent {
			return nil, managementError(CodeInvalidInput, nil)
		}
		if err := throughDraftAuthority(ctx, scope, actor, operator, state.overlay, state.required); err != nil {
			return nil, err
		}
		intent, err := config.Prepare(ctx, pair)
		if err = errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
			return nil, err
		}
		if len(intent.Input.Collections()) != 0 || intent.Unusable && intent.Password != "" {
			return nil, managementError(CodeInvalidInput, nil)
		}
		if !intent.Unusable {
			if intent.Password == "" {
				return nil, passwordInputError("required")
			}
			if len(intent.Password) > state.config.MaxPasswordBytes {
				return nil, passwordInputError("max_length")
			}
		}
		input, err := state.binding.state.objects.WithServerValues(intent.Input, map[string]query.Value{pair.target.Name: query.Integer(pair.targetKey)})
		if err != nil {
			return nil, managementError(CodeInvalidInput, err)
		}
		values, _, err := state.prepareCreationValues(ctx, input, false, nil, map[string]bool{pair.source.Name: true})
		if err != nil {
			return nil, err
		}
		initial, err := throughDraftValues(state.metadata, values, pair.source.Name)
		if err != nil {
			return nil, err
		}
		required, err := throughEffectPermissions(state.binding.state.role, policy.model, state.required, state.config.RelatedPermissions, initial, memberships, pair.source.Name)
		if err != nil {
			return nil, err
		}
		draft := &throughDraft{}
		draft.check = func(ctx context.Context, scope db.Queryer, actor auth.Principal, operator bool) error {
			return throughDraftAuthority(ctx, scope, actor, operator, state.overlay, required)
		}
		encoded := false
		draft.encode = func(ctx context.Context) error {
			if encoded {
				return managementError(CodeConflict, nil)
			}
			value, err := state.encode(ctx, intent.Password, intent.Unusable)
			if err != nil {
				return credentialPasswordRejection(err)
			}
			values[state.binding.state.role.Password], encoded = query.String(value), true
			return ctx.Err()
		}
		draft.bind = func(ctx context.Context, actual ThroughEndpoints, domain auth.CredentialDomain) (*throughCandidate, error) {
			if !encoded || !actual.sourcePresent || actual.source.Name != pair.source.Name || actual.target.Name != pair.target.Name || actual.targetKey != pair.targetKey {
				return nil, managementError(CodeInvalidInput, nil)
			}
			bound := maps.Clone(values)
			bound[pair.source.Name] = query.Integer(actual.sourceKey)
			prepared, err := state.binding.state.objects.PrepareValues(nil, bound)
			if err != nil {
				return nil, err
			}
			row, err := prepared.Model()
			if err != nil {
				return nil, err
			}
			credential, err := state.binding.prepareCandidateCredential(domain, row, nil)
			if err != nil || credential.HasUsablePassword() == intent.Unusable {
				return nil, managementError(CodeInvalidConfig, err)
			}
			candidate := &creationCandidate[M]{prepared: prepared, row: row, raw: intent.Password, unusable: intent.Unusable, profile: map[string][]int64{}}
			return newThroughAccountCandidate(state, policy, candidate, domain, memberships)
		}
		if err := draft.check(ctx, scope, actor, operator); err != nil {
			return nil, err
		}
		return draft, nil
	}
}

func bindCatalogThroughDraft[M any](policy *throughCreationState, state *catalogModelState[M], role ir.AccountRole, overlay auth.Authorizer, config CatalogThroughConfig[M], memberships, owned []deletionMembership) {
	policy.draft = func(ctx context.Context, scope db.Queryer, actor auth.Principal, operator bool, pair ThroughEndpoints, _ auth.CredentialDomain) (*throughDraft, error) {
		if pair.sourcePresent {
			return nil, managementError(CodeInvalidInput, nil)
		}
		if err := throughDraftAuthority(ctx, scope, actor, operator, overlay, state.permissions.add); err != nil {
			return nil, err
		}
		input, err := config.Prepare(ctx, pair)
		if err = errors.Join(err, validateAuthorityScope(ctx, scope.(db.SessionValidator))); err != nil {
			return nil, err
		}
		if len(input.Collections()) != 0 {
			return nil, managementError(CodeInvalidInput, nil)
		}
		input, err = (CatalogResource[M]{state: state}).prepareInput(input, 0, true)
		if err != nil {
			return nil, err
		}
		values, err := throughDraftValues(state.model, input.Values(), pair.source.Name)
		if err != nil {
			return nil, err
		}
		values[pair.target.Name] = query.Integer(pair.targetKey)
		values[state.revision] = query.Integer(1)
		if err := state.checkScalars(values); err != nil {
			return nil, err
		}
		for _, field := range state.model.Fields {
			if !field.PrimaryKey && field.Name != pair.source.Name && values[field.Name].IsNull() && !field.Nullable {
				return nil, profileInputError(field.Name, "required")
			}
		}
		required, err := throughEffectPermissions(role, policy.model, state.permissions.add, config.RelatedPermissions, values, memberships, pair.source.Name)
		if err != nil {
			return nil, err
		}
		draft := &throughDraft{encode: func(ctx context.Context) error { return ctx.Err() }}
		draft.check = func(ctx context.Context, scope db.Queryer, actor auth.Principal, operator bool) error {
			return throughDraftAuthority(ctx, scope, actor, operator, overlay, required)
		}
		draft.bind = func(ctx context.Context, actual ThroughEndpoints, _ auth.CredentialDomain) (*throughCandidate, error) {
			if !actual.sourcePresent || actual.source.Name != pair.source.Name || actual.target.Name != pair.target.Name || actual.targetKey != pair.targetKey {
				return nil, managementError(CodeInvalidInput, nil)
			}
			return prepareCatalogThrough(state, policy, role, overlay, config.RelatedPermissions, memberships, owned, input, actual)
		}
		if err := draft.check(ctx, scope, actor, operator); err != nil {
			return nil, err
		}
		return draft, nil
	}
}
