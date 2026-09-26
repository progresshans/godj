package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// CredentialStore supplies a coherent current identity, role, permission and
// password observation. Absence is false without error. Implementations must
// finish their read scope before returning and must not retain account caches.
type CredentialStore interface {
	CredentialByUsername(context.Context, string) (Credential, bool, error)
	CredentialByID(context.Context, string) (Credential, bool, error)
}

// StoredAuthenticator verifies passwords outside the store's read scope and
// re-reads the same identity before publishing successful authentication. No
// password or user snapshot is cached; only one bounded dummy hash is retained.
type StoredAuthenticator struct{ state *storedAuthenticatorState }

type storedAuthenticatorState struct {
	store     CredentialStore
	hasher    PasswordHasher
	dummyHash string
}

var _ CredentialAuthenticator = (*StoredAuthenticator)(nil)

func (StoredAuthenticator) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("auth.StoredAuthenticator{redacted}"))
}

func (*StoredAuthenticator) String() string   { return "auth.StoredAuthenticator{redacted}" }
func (*StoredAuthenticator) GoString() string { return "auth.StoredAuthenticator{redacted}" }

func NewStoredAuthenticator(ctx context.Context, store CredentialStore, hasher PasswordHasher) (*StoredAuthenticator, error) {
	if err := authContext(ctx); err != nil {
		return nil, err
	}
	if nilAuthValue(store) || nilAuthValue(hasher) {
		return nil, &Error{Code: CodeInvalidConfig, Detail: "credential store or password hasher is nil"}
	}
	dummy, err := makeDummyHash(ctx, hasher)
	if err != nil {
		return nil, err
	}
	return &StoredAuthenticator{state: &storedAuthenticatorState{store: store, hasher: hasher, dummyHash: dummy}}, nil
}

func (a *StoredAuthenticator) Authenticate(ctx context.Context, username, password string) (Credential, error) {
	if err := a.validCall(ctx); err != nil {
		return Credential{}, err
	}
	var observed Credential
	var found bool
	var err error
	if validUsername(username) {
		observed, found, err = a.state.store.CredentialByUsername(ctx, username)
		if err := errors.Join(err, ctx.Err()); err != nil {
			return Credential{}, storedCredentialFailure(err)
		}
		if found {
			if observed.value().username != username {
				return Credential{}, storedCredentialFailure(nil)
			}
			if err := a.validateObserved(ctx, observed); err != nil {
				return Credential{}, err
			}
		}
	}
	if err := verifyCredential(ctx, a.state.hasher, a.state.dummyHash, observed, found, username, password); err != nil {
		return Credential{}, err
	}
	// The first password observation cannot authenticate a changed credential.
	// Permission/role changes may be returned from this fresh coherent read.
	current, present, err := a.state.store.CredentialByID(ctx, observed.value().principal.ID())
	if err := errors.Join(err, ctx.Err()); err != nil {
		return Credential{}, storedCredentialFailure(err)
	}
	if !present {
		return Credential{}, ErrInvalidCredentials
	}
	if err := a.validateObserved(ctx, current); err != nil {
		return Credential{}, err
	}
	if !current.value().principal.Active() || current.value().principal.ID() != observed.value().principal.ID() || current.value().username != username || !current.MatchesSessionStamp(observed.SessionStamp()) {
		return Credential{}, ErrInvalidCredentials
	}
	return current, nil
}

func (a *StoredAuthenticator) Resolve(ctx context.Context, principalID string) (Credential, error) {
	if err := a.validCall(ctx); err != nil {
		return Credential{}, err
	}
	if !validIdentity(principalID) {
		return Credential{}, ErrInvalidCredentials
	}
	current, found, err := a.state.store.CredentialByID(ctx, principalID)
	if err := errors.Join(err, ctx.Err()); err != nil {
		return Credential{}, storedCredentialFailure(err)
	}
	if !found {
		return Credential{}, ErrInvalidCredentials
	}
	if current.value().principal.ID() != principalID {
		return Credential{}, storedCredentialFailure(nil)
	}
	if err := a.validateObserved(ctx, current); err != nil {
		return Credential{}, err
	}
	if !current.value().principal.Active() {
		return Credential{}, ErrInvalidCredentials
	}
	return current, nil
}

func (a *StoredAuthenticator) validateObserved(ctx context.Context, credential Credential) error {
	if credential.value().principal.ID() == "" || !validUsername(credential.value().username) {
		return storedCredentialFailure(nil)
	}
	if err := errors.Join(a.state.hasher.ValidateEncoded(credential.value().hash), ctx.Err()); err != nil {
		return storedCredentialFailure(err)
	}
	return nil
}

func (a *StoredAuthenticator) validCall(ctx context.Context) error {
	if err := authContext(ctx); err != nil {
		return err
	}
	if a == nil || a.state == nil || a.state.store == nil || a.state.hasher == nil || a.state.dummyHash == "" {
		return &Error{Code: CodeInvalidConfig, Detail: "stored credential authenticator is uninitialized"}
	}
	return nil
}

func authContext(ctx context.Context) error {
	if ctx == nil {
		return &Error{Code: CodeInvalidInput, Field: "context", Detail: "context is nil"}
	}
	return ctx.Err()
}

func storedCredentialFailure(cause error) error {
	return &Error{Code: CodeCredential, Detail: "stored credential observation failed", Cause: cause}
}

func nilAuthValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}
