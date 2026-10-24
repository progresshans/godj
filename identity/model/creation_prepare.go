package model

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func (state *creationState[M]) prepareInput(ctx context.Context, input orm.ModelInput[M]) (*creationCandidate[M], error) {
	metadata, valid := input.Model()
	if !valid || !metadata.Equal(state.metadata) {
		return nil, managementError(CodeInvalidInput, nil)
	}
	if key, bound := input.TargetKey(); bound && !key.IsNull() {
		return nil, managementError(CodeInvalidInput, nil)
	}
	values := input.Values()
	for name := range values {
		if !state.allowed[name] {
			return nil, managementError(CodeInvalidInput, nil)
		}
	}
	binding := state.store.state.binding
	role := binding.state.role
	if _, selected := values[role.Login]; !selected {
		return nil, profileInputError(role.Login, "required")
	}
	if err := normalizeAccountInput(metadata, role, values); err != nil {
		return nil, err
	}
	collections := input.Collections()
	for name, keys := range collections {
		if !state.allowed[name] || (name != role.Groups && name != role.Permissions) {
			return nil, managementError(CodeInvalidInput, nil)
		}
		maximum := auth.MaximumPermissions
		if name == role.Groups {
			maximum = MaximumAccountGroups
		}
		if len(keys) > maximum {
			return nil, profileInputError(name, "max_items")
		}
		slices.Sort(keys)
		collections[name] = slices.Compact(keys)
	}
	for name, value := range state.forced {
		values[name] = value
	}
	initial, err := orm.InitialModelValues(metadata)
	if err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	for name, value := range values {
		initial[name] = value
	}
	// Validate incomplete required profile state before calling a server source.
	// Excluded defaults remain actual IR defaults, never fabricated Go zeroes.
	for _, field := range metadata.Fields {
		if field.PrimaryKey || field.Name == role.PrincipalID || field.Name == role.Password || field.Name == role.Revision || field.Name == role.CreatedAt || field.Name == role.LastLogin {
			continue
		}
		value := initial[field.Name]
		if value.IsNull() && !field.Nullable {
			return nil, profileInputError(field.Name, "required")
		}
		if text, ok := value.String(); ok && text == "" && !field.Blank && !field.NonEditable {
			return nil, profileInputError(field.Name, "required")
		}
	}
	principalID, err := state.config.PrincipalID(ctx)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, managementError(CodePersistence, err)
	}
	if _, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID}); err != nil {
		return nil, managementError(CodeInvalidConfig, err)
	}
	values[role.PrincipalID], values[role.Password], values[role.Revision] = query.String(principalID), query.String("!"), query.Integer(1)
	if role.LastLogin != "" {
		values[role.LastLogin] = query.Null()
	}
	if role.CreatedAt != "" {
		now := state.config.Now().UTC().Truncate(time.Microsecond)
		if now.IsZero() || now.Year() < 1 || now.Year() > 9999 {
			return nil, managementError(CodeInvalidConfig, nil)
		}
		values[role.CreatedAt] = query.DateTime(now)
	}
	prepared, err := binding.state.objects.PrepareValues(nil, values)
	if err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	row, err := prepared.Model()
	if err != nil {
		return nil, managementError(CodeInvalidInput, err)
	}
	return &creationCandidate[M]{prepared: prepared, row: row, groups: collections[role.Groups], permissions: collections[role.Permissions]}, nil
}

func (state *creationState[M]) preflight(ctx context.Context, scope db.Queryer, actor auth.Principal, candidate *creationCandidate[M], encoded bool) (auth.Principal, error) {
	current, allowed, err := state.store.AuthorizeIn(ctx, scope, actor, state.overlay, state.required[0], state.required[1:]...)
	if err != nil {
		return auth.Principal{}, err
	}
	if !allowed {
		return auth.Principal{}, managementError(CodePermission, nil)
	}
	namespace, _ := current.CredentialDomain()
	if err := state.checkCandidate(ctx, scope, namespace, candidate, encoded); err != nil {
		return auth.Principal{}, err
	}
	return current, nil
}

func (state *creationState[M]) checkCandidate(ctx context.Context, scope db.Queryer, namespace auth.CredentialDomain, candidate *creationCandidate[M], encoded bool) error {
	binding := state.store.state.binding
	role := binding.state.role
	grants, err := state.store.state.relations.proposed(ctx, scope, candidate.groups, candidate.permissions)
	if errors.Is(err, errAccountGrantLimit) {
		return profileInputError(role.Permissions, "max_items")
	}
	if err != nil {
		return err
	}
	if _, err := binding.prepareCandidateCredential(namespace, candidate.row, grants); err != nil {
		return err
	}
	var excluded []string
	if !encoded {
		excluded = []string{role.Password}
	}
	diagnostics, err := candidate.prepared.ValidateUniqueExcluding(ctx, scope, candidate.row, excluded...)
	if err != nil {
		return err
	}
	if !diagnostics.Empty() {
		return validation.Reject(diagnostics, nil)
	}
	if state.config.CaseInsensitiveLogin {
		values, err := binding.state.objects.ModelValues(candidate.row)
		if err != nil {
			return err
		}
		login, _ := values[role.Login].String()
		exists, err := binding.state.objects.Using(scope).Filter(state.store.state.login.IExact(login)).Exists(ctx)
		if err != nil {
			return err
		}
		if exists {
			return profileInputError(role.Login, "unique")
		}
	}
	validator, ok := scope.(db.SessionValidator)
	if !ok || nilModelValue(validator) {
		return invalidAuthorityScope()
	}
	// Each callback gets its own copy; a callback cannot alter later policy,
	// the actual hash, the prepared row or memberships by mutating its argument.
	detached := func() (M, error) {
		return binding.state.objects.ApplyValues(candidate.row, map[string]query.Value{role.Password: query.String("")})
	}
	if state.config.Validate != nil {
		row, err := detached()
		if err != nil {
			return err
		}
		selection := RoleMemberships{state: &roleMembershipState{slices.Clone(candidate.groups), slices.Clone(candidate.permissions)}}
		diagnostics, failure := state.config.Validate(ctx, scope, row, selection)
		if err := errors.Join(failure, validateAuthorityScope(ctx, validator)); err != nil {
			return err
		}
		if !diagnostics.Empty() {
			return validation.Reject(diagnostics, nil)
		}
	}
	if !candidate.unusable && state.config.ValidatePassword != nil {
		row, err := detached()
		if err != nil {
			return err
		}
		diagnostics, failure := state.config.ValidatePassword(ctx, candidate.raw, row)
		if err := errors.Join(failure, validateAuthorityScope(ctx, validator)); err != nil {
			return err
		}
		for _, diagnostic := range diagnostics.All() {
			if diagnostic.Field() != "password" && diagnostic.Field() != validation.NonField {
				return managementError(CodeInvalidConfig, nil)
			}
		}
		if !diagnostics.Empty() {
			return validation.Reject(diagnostics, nil)
		}
	}
	if err := validateAuthorityScope(ctx, validator); err != nil {
		return err
	}
	return nil
}

func (state *creationState[M]) encode(ctx context.Context, raw string, unusable bool) (string, error) {
	if unusable {
		encoded, err := auth.MakeUnusablePassword(ctx)
		if err != nil {
			return "", managementError(CodePersistence, err)
		}
		return encoded, nil
	}
	encoded, err := state.hasher.Hash(ctx, raw)
	if canceled := ctx.Err(); canceled != nil {
		return "", managementError(CodeInvalidInput, errors.Join(err, canceled))
	}
	if err != nil {
		if invalid, ok := err.(*auth.Error); ok && invalid != nil && invalid.Code == auth.CodeInvalidInput && invalid.Field == "password" && invalid.Unwrap() == nil {
			return "", profileInputError("password", "invalid")
		}
		return "", managementError(CodeInvalidInput, err)
	}
	if err := state.hasher.ValidateEncoded(encoded); err != nil {
		return "", managementError(CodeInvalidConfig, err)
	}
	return encoded, nil
}
