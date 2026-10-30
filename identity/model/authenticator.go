package model

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/schema/ir"
)

// Authenticator applies the selected input normalization before the shared
// stored verifier. Password work, dummy verification and the final current
// credential/permission recheck remain owned by auth.StoredAuthenticator.
type Authenticator[M any] struct {
	inner         *auth.StoredAuthenticator
	normalization ir.AccountNormalization
	credentials   *CredentialStore[M]
	hasher        auth.PasswordHasher
}

func (Authenticator[M]) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("identity.model.Authenticator{redacted}"))
}

func NewAuthenticator[M any](ctx context.Context, store *CredentialStore[M], hasher auth.PasswordHasher) (*Authenticator[M], error) {
	if err := store.valid(); err != nil {
		return nil, err
	}
	inner, err := auth.NewStoredAuthenticator(ctx, store, hasher)
	if err != nil {
		return nil, err
	}
	return &Authenticator[M]{inner: inner, normalization: store.state.binding.state.role.AuthenticateNormalization, credentials: store, hasher: hasher}, nil
}

func (authenticator *Authenticator[M]) Authenticate(ctx context.Context, login, password string) (auth.Credential, error) {
	if authenticator == nil || authenticator.inner == nil {
		return auth.Credential{}, invalidCredentialStore()
	}
	// Bound work before normalization. Invalid input still reaches the shared
	// verifier's bounded dummy-password path, without a database lookup.
	if authenticator.normalization == ir.AccountUnicode16NFKC && auth.ValidateUsername(login) == nil {
		login = unicode16.NFKC(login)
	}
	return authenticator.inner.Authenticate(ctx, login, password)
}

func (authenticator *Authenticator[M]) ConfirmPassword(ctx context.Context, principalID, password string) (auth.Credential, error) {
	if authenticator == nil || authenticator.inner == nil {
		return auth.Credential{}, invalidCredentialStore()
	}
	return authenticator.inner.ConfirmPassword(ctx, principalID, password)
}

func (authenticator *Authenticator[M]) Resolve(ctx context.Context, principalID string) (auth.Credential, error) {
	if authenticator == nil || authenticator.inner == nil {
		return auth.Credential{}, invalidCredentialStore()
	}
	return authenticator.inner.Resolve(ctx, principalID)
}
