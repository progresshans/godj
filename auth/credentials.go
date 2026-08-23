package auth

import (
	"context"
	"strings"
	"unicode/utf8"
)

const (
	maxUsernameBytes   = 256
	maxCredentialCount = 4096
	maxStoredHashBytes = hardMaxEncodedBytes
)

// CredentialAuthenticator verifies credentials and resolves an active
// principal from a process session identifier. Unknown and inactive identities
// use the same ErrInvalidCredentials surface.
type CredentialAuthenticator interface {
	Authenticate(context.Context, string, string) (Principal, error)
	Resolve(context.Context, string) (Principal, error)
}

// Credential is an opaque immutable startup credential. Formatting is
// redacted so an encoded password cannot enter a diagnostic accidentally.
type Credential struct {
	username  string
	hash      string
	principal Principal
}

func NewCredential(username, encodedHash string, principal Principal) (Credential, error) {
	if !validUsername(username) {
		return Credential{}, &Error{Code: CodeInvalidInput, Field: "username", Detail: "username is malformed or too large"}
	}
	if encodedHash == "" || len(encodedHash) > maxStoredHashBytes || strings.ContainsAny(encodedHash, "\r\n\x00") {
		return Credential{}, &Error{Code: CodeInvalidInput, Field: "encoded_password", Detail: "encoded password is malformed or too large"}
	}
	if principal.id == "" {
		return Credential{}, &Error{Code: CodeInvalidInput, Field: "principal", Detail: "credential principal is invalid"}
	}
	return Credential{username: username, hash: encodedHash, principal: principal.clone()}, nil
}

func (Credential) String() string   { return "auth.Credential{redacted}" }
func (Credential) GoString() string { return "auth.Credential{redacted}" }

type MemoryAuthenticator struct {
	byUsername map[string]Credential
	byID       map[string]Credential
	hasher     PasswordHasher
	dummyHash  string
}

func (*MemoryAuthenticator) String() string   { return "auth.MemoryAuthenticator{redacted}" }
func (*MemoryAuthenticator) GoString() string { return "auth.MemoryAuthenticator{redacted}" }

func NewMemoryAuthenticator(credentials []Credential, hasher PasswordHasher) (*MemoryAuthenticator, error) {
	if hasher == nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "password hasher is nil"}
	}
	if len(credentials) > maxCredentialCount {
		return nil, &Error{Code: CodeInvalidConfig, Field: "credentials", Detail: "credential count exceeds the supported limit"}
	}
	result := &MemoryAuthenticator{
		byUsername: make(map[string]Credential, len(credentials)),
		byID:       make(map[string]Credential, len(credentials)),
		hasher:     hasher,
	}
	for _, credential := range credentials {
		if !validUsername(credential.username) || credential.hash == "" || credential.principal.id == "" {
			return nil, &Error{Code: CodeInvalidConfig, Field: "credentials", Detail: "credential is invalid"}
		}
		if _, duplicate := result.byUsername[credential.username]; duplicate {
			return nil, &Error{Code: CodeInvalidConfig, Field: "credentials", Detail: "username is duplicated"}
		}
		if _, duplicate := result.byID[credential.principal.id]; duplicate {
			return nil, &Error{Code: CodeInvalidConfig, Field: "credentials", Detail: "principal identifier is duplicated"}
		}
		if err := hasher.ValidateEncoded(credential.hash); err != nil {
			return nil, &Error{Code: CodeInvalidConfig, Field: "credentials", Detail: "credential contains an invalid encoded password", Cause: err}
		}
		clone := Credential{username: credential.username, hash: credential.hash, principal: credential.principal.clone()}
		result.byUsername[clone.username] = clone
		result.byID[clone.principal.id] = clone
	}
	dummyHash, err := hasher.Hash(context.Background(), "godj-unmatchable-dummy-password")
	if err != nil {
		return nil, passwordFailure(err)
	}
	if err := hasher.ValidateEncoded(dummyHash); err != nil {
		return nil, &Error{Code: CodeInvalidConfig, Field: "password_hasher", Detail: "dummy password does not use the current bounded work profile", Cause: err}
	}
	result.dummyHash = dummyHash
	return result, nil
}

func (a *MemoryAuthenticator) Authenticate(ctx context.Context, username, password string) (Principal, error) {
	if err := validAuthCall(ctx, a); err != nil {
		return Principal{}, err
	}
	credential, found := a.byUsername[username]
	encoded := a.dummyHash
	if found {
		encoded = credential.hash
	}
	verified, err := a.hasher.Verify(ctx, password, encoded)
	if err != nil {
		return Principal{}, passwordFailure(err)
	}
	if !found || !credential.principal.Active() || !verified || !validUsername(username) {
		return Principal{}, ErrInvalidCredentials
	}
	return credential.principal.clone(), nil
}

func (a *MemoryAuthenticator) Resolve(ctx context.Context, principalID string) (Principal, error) {
	if err := validAuthCall(ctx, a); err != nil {
		return Principal{}, err
	}
	credential, found := a.byID[principalID]
	if !found || !credential.principal.Active() || !validIdentity(principalID) {
		return Principal{}, ErrInvalidCredentials
	}
	return credential.principal.clone(), nil
}

func validAuthCall(ctx context.Context, authenticator *MemoryAuthenticator) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if authenticator == nil || authenticator.hasher == nil || authenticator.dummyHash == "" {
		return &Error{Code: CodeInvalidConfig, Detail: "credential authenticator is nil or uninitialized"}
	}
	return nil
}

func validUsername(username string) bool {
	return username != "" && len(username) <= maxUsernameBytes && utf8.ValidString(username) &&
		!strings.ContainsRune(username, '\x00') && strings.TrimSpace(username) == username
}

func (p Principal) clone() Principal {
	clone := p
	clone.permissions = append([]Permission(nil), p.permissions...)
	clone.permission = make(map[Permission]struct{}, len(p.permission))
	for permission := range p.permission {
		clone.permission[permission] = struct{}{}
	}
	return clone
}
