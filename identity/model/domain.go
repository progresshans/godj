package model

import (
	"context"

	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
)

// RequireDomainIn verifies this generated binding's exact role in the caller's
// stable read snapshot or coordinated write scope. Account and permission
// reads must use the same handle. Its receipt is not cacheable authority.
func (binding Binding[M]) RequireDomainIn(ctx context.Context, reader db.Queryer) (domain.Receipt, error) {
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		return domain.Receipt{}, err
	}
	return domain.RequireIn(ctx, reader, fingerprint)
}

// RequirePrincipalDomainIn rejects an actor from a different ownership before
// resolving its ID against this model. A successful match does not grant any
// permission: the caller must still read current active/role/grant state and
// apply its authorizer in this same scope.
func (binding Binding[M]) RequirePrincipalDomainIn(ctx context.Context, reader db.Queryer, principal auth.Principal) (domain.Receipt, error) {
	receipt, err := binding.RequireDomainIn(ctx, reader)
	if err != nil {
		return domain.Receipt{}, err
	}
	credentialDomain, err := binding.CredentialDomain(receipt.Generation())
	if err != nil {
		return domain.Receipt{}, err
	}
	if !credentialDomain.MatchesPrincipal(principal) {
		return domain.Receipt{}, invalidAccountCredential()
	}
	return receipt, nil
}
