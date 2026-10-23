package identity

import (
	"context"

	"github.com/progresshans/godj/auth"
)

// NewAuthenticator connects current stored accounts to the credential/session
// boundary. Password work happens after Directory has ended its DB snapshot.
func NewAuthenticator(ctx context.Context, directory *Directory, hasher auth.PasswordHasher) (*auth.StoredAuthenticator, error) {
	if directory == nil || directory.state == nil || directory.state.backend == nil {
		return nil, &auth.Error{Code: auth.CodeInvalidConfig, Field: "identity_backend", Detail: "identity directory is uninitialized"}
	}
	return auth.NewStoredAuthenticator(ctx, credentialDirectory{directory}, hasher)
}

type credentialDirectory struct{ directory *Directory }

func (store credentialDirectory) CredentialByUsername(ctx context.Context, username string) (auth.Credential, bool, error) {
	account, found, err := store.directory.ByUsername(ctx, username)
	return account.value().credential, found, err
}

func (store credentialDirectory) CredentialByID(ctx context.Context, principalID string) (auth.Credential, bool, error) {
	account, found, err := store.directory.ByPrincipalID(ctx, principalID)
	return account.value().credential, found, err
}
