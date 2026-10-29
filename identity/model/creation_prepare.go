package model

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func (state *creationState[M]) prepareInput(ctx context.Context, input orm.ModelInput[M], form *formmodel.BoundForm) (*creationCandidate[M], error) {
	var pendingFiles []string
	if form != nil {
		if err := state.checkFileForm(*form); err != nil {
			return nil, err
		}
		if err := checkAccountCollectionForm(state.binding.state.role, state.collections, *form); err != nil {
			return nil, err
		}
		var err error
		input, pendingFiles, err = formmodel.PrepareInputBeforeFiles(state.binding.state.objects, *form)
		if err != nil {
			return nil, managementError(CodeInvalidInput, err)
		}
	}
	metadata, valid := input.Model()
	if !valid || !metadata.Equal(state.metadata) {
		return nil, managementError(CodeInvalidInput, nil)
	}
	if key, bound := input.TargetKey(); bound && !key.IsNull() {
		return nil, managementError(CodeInvalidInput, nil)
	}
	values := input.Values()
	for name := range values {
		field, found := namedField(state.metadata, name)
		allowed := state.allowed[name] || form != nil && state.allowed[state.dimensions[name]]
		if !found || !allowed || form == nil && (field.Kind.IsFile() || state.dimensions[name] != "") {
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
	if err := normalizeAccountCollections(role, state.allowed, state.collections, collections); err != nil {
		return nil, err
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
		if field.PrimaryKey || field.Name == role.PrincipalID || field.Name == role.Password || field.Name == role.Revision || field.Name == role.CreatedAt || field.Name == role.LastLogin || slices.Contains(pendingFiles, field.Name) {
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
	profile := make(map[string][]int64)
	for _, field := range metadata.ManyToMany {
		if field.Name != role.Groups && field.Name != role.Permissions {
			profile[field.Name] = slices.Clone(collections[field.Name])
		}
	}
	return &creationCandidate[M]{prepared: prepared, row: row, groups: collections[role.Groups], permissions: collections[role.Permissions], profile: profile, form: form, pendingFiles: pendingFiles}, nil
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
	if err := state.checkCandidate(ctx, scope, namespace, current, ProfileRelationCreate, candidate, encoded); err != nil {
		return auth.Principal{}, err
	}
	return current, nil
}

func (state *creationState[M]) checkCandidate(ctx context.Context, scope db.Queryer, namespace auth.CredentialDomain, actor auth.Principal, operation ProfileRelationOperation, candidate *creationCandidate[M], encoded bool) error {
	binding := state.store.state.binding
	role := binding.state.role
	if err := checkProfileCollections(ctx, scope, actor, ProfileRelationOrigin{operation: operation}, state.collections, candidate.profile); err != nil {
		return err
	}
	if len(state.relations) != 0 {
		values, err := binding.state.objects.ModelValues(candidate.row)
		if err != nil {
			return err
		}
		if err := checkProfileRelations(ctx, scope, actor, ProfileRelationOrigin{operation: operation}, state.relations, values); err != nil {
			return err
		}
	}
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
	excluded := slices.Clone(candidate.pendingFiles)
	if !encoded {
		excluded = append(excluded, role.Password)
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
		selected := binding.state.objects.Using(scope).Filter(state.store.state.login.IExact(login))
		if candidate.stored {
			key, present, err := binding.state.objects.PrimaryKey(candidate.row)
			id, integer := key.Integer()
			if err != nil || !present || !integer {
				return invalidAccountCredential()
			}
			selected = selected.Filter(orm.Not(state.store.state.key.Exact(id)))
		}
		exists, err := selected.Exists(ctx)
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
	// Before publication only actual scalar/choice/grant authority and partial
	// uniqueness are meaningful. Validators receive the actual storage names
	// in the next read snapshot, still before hashing, and again at commit.
	if len(candidate.pendingFiles) != 0 {
		return validateAuthorityScope(ctx, validator)
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
		selection := AccountMemberships{state: &accountMembershipState{groups: slices.Clone(candidate.groups), permissions: slices.Clone(candidate.permissions), profile: candidate.profile}}
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
		if err := passwordPolicyDiagnostics(diagnostics); err != nil {
			return credentialPasswordRejection(err)
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
	return encodeUsablePassword(ctx, state.hasher, raw)
}
