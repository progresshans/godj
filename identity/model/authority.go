package model

import (
	"context"
	"errors"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

// AuthorizeIn rechecks an already authenticated actor's account domain,
// current active/role/grant state and explicit required permissions in the
// caller's live stable read snapshot or coordinated write scope. The authorizer
// is a deny overlay over current stored grants, never a source of extra grants.
//
// This method does not authenticate the caller, start or commit a transaction,
// run password work, cache authority, or authorize a later independent scope.
// Related reads, mutations, revision checks, session effects and audit must use
// the SAME scope, and the owner must propagate errors and confirm cleanup or
// commit before publishing any result. The overlay must not re-enter the DB
// owner or mutate this scope. Its Principal is current, detached and bound to
// the stored domain. It remains provisional until the scope owner finishes.
//
// A definite denial returns an anonymous Principal, false and nil. A storage,
// selected-role, scope, cancellation or overlay failure returns an error and no
// Principal; callers must not treat it as denial and try an alternative grant.
func (store *CredentialStore[M]) AuthorizeIn(ctx context.Context, scope db.Queryer, actor auth.Principal, overlay auth.Authorizer, first auth.Permission, additional ...auth.Permission) (auth.Principal, bool, error) {
	if err := store.valid(); err != nil {
		return auth.Principal{}, false, err
	}
	if nilModelValue(ctx) || nilModelValue(scope) || nilModelValue(overlay) {
		return auth.Principal{}, false, invalidAuthorityScope()
	}
	validator, borrowed := scope.(db.SessionValidator)
	if !borrowed || nilModelValue(validator) {
		return auth.Principal{}, false, invalidAuthorityScope()
	}
	if err := validateAuthorityScope(ctx, validator); err != nil {
		return auth.Principal{}, false, accountReadFailure(err)
	}
	required, err := auth.RequiredPermissions(first, additional...)
	if err != nil {
		return auth.Principal{}, false, err
	}
	deny := func() (auth.Principal, bool, error) { return finishAuthority(ctx, validator, auth.Principal{}, false) }
	if !actor.Authenticated() {
		return deny()
	}
	binding := store.state.binding
	ownership, err := binding.RequireDomainIn(ctx, scope)
	if err != nil {
		return auth.Principal{}, false, accountReadFailure(err)
	}
	namespace, err := binding.CredentialDomain(ownership.Generation())
	if err != nil {
		return auth.Principal{}, false, err
	}
	// Reject an unbound or old-domain actor before its coincident ID can resolve
	// to the new account. The ID alone is never the management identity.
	if !namespace.MatchesPrincipal(actor) {
		return deny()
	}
	credential, found, err := store.credentialInDomain(ctx, scope, store.state.principal.Exact(actor.ID()), ownership.Generation())
	if err != nil {
		return auth.Principal{}, false, accountReadFailure(err)
	}
	if !found {
		return deny()
	}
	current := credential.Principal()
	for _, permission := range required {
		if !current.Has(permission) {
			return deny()
		}
	}
	for _, permission := range required {
		allowed, err := overlay.Allowed(ctx, current, permission)
		if err = errors.Join(err, validateAuthorityScope(ctx, validator)); err != nil {
			return auth.Principal{}, false, accountReadFailure(err)
		}
		if !allowed {
			return deny()
		}
	}
	return finishAuthority(ctx, validator, current, true)
}

func validateAuthorityScope(ctx context.Context, scope db.SessionValidator) error {
	return errors.Join(ctx.Err(), scope.ValidateSession(ctx))
}
func finishAuthority(ctx context.Context, scope db.SessionValidator, current auth.Principal, allowed bool) (auth.Principal, bool, error) {
	if err := validateAuthorityScope(ctx, scope); err != nil {
		return auth.Principal{}, false, accountReadFailure(err)
	}
	return current, allowed, nil
}
func invalidAuthorityScope() error {
	return &auth.Error{Code: auth.CodeInvalidConfig, Detail: "account authorization requires a live borrowed database scope and an authorizer"}
}
