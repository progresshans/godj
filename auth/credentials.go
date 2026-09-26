package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	maxUsernameBytes   = 256
	maxCredentialCount = 4096
	maxStoredHashBytes = hardMaxEncodedBytes
)

// CredentialAuthenticator returns one immutable credential and authorization
// snapshot. Resolve must read the current credential, not restore the snapshot
// saved at login. Unknown and inactive identities use ErrInvalidCredentials.
type CredentialAuthenticator interface {
	Authenticate(context.Context, string, string) (Credential, error)
	Resolve(context.Context, string) (Credential, error)
}

// Credential is an opaque immutable credential snapshot. Formatting is
// redacted so an encoded password cannot enter a diagnostic accidentally.
type Credential struct {
	username  string
	hash      string
	principal Principal
}

func NewCredential(username, encodedHash string, principal Principal) (Credential, error) {
	if err := ValidateUsername(username); err != nil {
		return Credential{}, err
	}
	if encodedHash == "" || len(encodedHash) > maxStoredHashBytes || strings.ContainsAny(encodedHash, "\r\n\x00") {
		return Credential{}, &Error{Code: CodeInvalidInput, Field: "encoded_password", Detail: "encoded password is malformed or too large"}
	}
	if principal.id == "" {
		return Credential{}, &Error{Code: CodeInvalidInput, Field: "principal", Detail: "credential principal is invalid"}
	}
	return Credential{username: username, hash: encodedHash, principal: principal}, nil
}

// ValidateUsername applies the credential identity's exact UTF-8/byte/NUL
// policy without I/O or password material. It does not normalize or trim input.
func ValidateUsername(username string) error {
	if !validUsername(username) {
		return &Error{Code: CodeInvalidInput, Field: "username", Detail: "username is malformed or too large"}
	}
	return nil
}

func (Credential) String() string   { return "auth.Credential{redacted}" }
func (Credential) GoString() string { return "auth.Credential{redacted}" }

func (c Credential) Principal() Principal { return c.principal }

// SessionStamp binds server-side session data to this principal and encoded
// password. A password replacement or rehash invalidates the previous stamp;
// username and permission changes do not. This is not a bearer token, password
// verifier or client cookie. Store it only in the trusted server session.
func (c Credential) SessionStamp() string {
	if c.principal.id == "" || c.hash == "" {
		return ""
	}
	// Both fields exclude NUL, so the framing is unambiguous. The input is an
	// already salted encoded password, never the raw password.
	digest := sha256.Sum256([]byte("godj.session-credential.v1\x00" + c.principal.id + "\x00" + c.hash))
	return hex.EncodeToString(digest[:])
}

// MatchesSessionStamp rejects absent or stale authentication state. Comparison
// does not expose a matching prefix of the server-side credential stamp.
func (c Credential) MatchesSessionStamp(stamp string) bool {
	expected := c.SessionStamp()
	return expected != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(stamp)) == 1
}

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
		result.byUsername[credential.username] = credential
		result.byID[credential.principal.id] = credential
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

func (a *MemoryAuthenticator) Authenticate(ctx context.Context, username, password string) (Credential, error) {
	if err := validAuthCall(ctx, a); err != nil {
		return Credential{}, err
	}
	credential, found := a.byUsername[username]
	encoded := a.dummyHash
	if found {
		encoded = credential.hash
	}
	verified, err := a.hasher.Verify(ctx, password, encoded)
	if err != nil {
		var authError *Error
		if errors.As(err, &authError) && authError.Code == CodeInvalidInput && authError.Field == "password" {
			return Credential{}, ErrInvalidCredentials
		}
		return Credential{}, passwordFailure(err)
	}
	if !found || !credential.principal.Active() || !verified || !validUsername(username) {
		return Credential{}, ErrInvalidCredentials
	}
	return credential, nil
}

func (a *MemoryAuthenticator) Resolve(ctx context.Context, principalID string) (Credential, error) {
	if err := validAuthCall(ctx, a); err != nil {
		return Credential{}, err
	}
	credential, found := a.byID[principalID]
	if !found || !credential.principal.Active() || !validIdentity(principalID) {
		return Credential{}, ErrInvalidCredentials
	}
	return credential, nil
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
