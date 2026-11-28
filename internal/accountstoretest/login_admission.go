package accountstoretest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
)

// Host errors can have noncomparable dynamic values. Error provenance must
// not depend on comparing arbitrary error interfaces with one another.
type loginOpaqueError []string

func (loginOpaqueError) Error() string { return "private selected login admission" }

func loginPrevious(t *testing.T, f *loginFixture, operation string) sessions.Record {
	t.Helper()
	if operation == "create" {
		return sessions.Record{}
	}
	values := map[string]string{"payload": "private previous login"}
	if operation == "replace" {
		values[auth.SessionPrincipalIDKey], values[auth.SessionCredentialStampKey] = f.actor.Principal().ID(), f.actor.SessionStamp()
	}
	previous, err := f.runtime.Sessions().Create(t.Context(), values)
	if err != nil {
		t.Fatal(err)
	}
	return previous
}

func runLoginAdmission(add registerCase, open Open) {
	add("login_admission", func(t *testing.T) {
		for _, operation := range []string{"create", "rotate", "replace"} {
			t.Run(operation, func(t *testing.T) {
				for _, mode := range []string{"denied", "joined_denial", "joined_unknown", "entropy", "capacity", "canceled", "opaque", "denied_cleanup", "swallowed_denial"} {
					t.Run(mode, func(t *testing.T) {
						backend, reopen := open(t)
						f := provisionLogin(t, backend, true)
						previous := loginPrevious(t, f, operation)
						fault := errors.New("private selected login admission")
						failure := error(auth.ErrInvalidCredentials)
						switch mode {
						case "joined_denial":
							failure = errors.Join(auth.ErrInvalidCredentials, fault)
						case "joined_unknown":
							failure = errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
						case "entropy":
							failure = &sessions.Error{Code: sessions.CodeEntropy}
						case "capacity":
							failure = &sessions.Error{Code: sessions.CodeStoreFull}
						case "canceled":
							failure = context.Canceled
						case "opaque":
							failure = loginOpaqueError{"preserved noncomparable cause"}
						}
						boundary := &loginFailureBackend{Backend: backend, mode: mode, fault: fault}
						owner := newUserReferenceOwner(t, boundary, f.userReferenceFixture, f.config(t))
						before := userReferenceSnapshot(t, reopen(t))
						entropy, admissions := f.entropy.count(), 0
						result, err := owner.runtime.LoginPersistence().Login(t.Context(), auth.SessionLogin{Credential: f.credential, Previous: previous, At: f.clock(), Admit: func(context.Context, auth.Principal) error { admissions++; return failure }})
						loginNoResult(t, result, err)
						candidate := loginExpectedRecord(t, f.credential, previous, operation, f.entropy.one(t, entropy), f.clock())
						if boundary.calls != 1 || admissions != 1 || len(boundary.effects) != 0 || owner.hasher.hashes.Load() != 0 || owner.hasher.verifies.Load() != 0 || owner.policies.Load() != 0 {
							t.Fatal("admission error was retried or crossed a write/password boundary")
						}
						if (err == auth.ErrInvalidCredentials) != (mode == "denied") {
							t.Fatal("execution failure became ordinary credential denial", err)
						}
						if mode == "opaque" {
							var opaque loginOpaqueError
							if !errors.As(err, &opaque) || !slices.Equal(opaque, []string{"preserved noncomparable cause"}) {
								t.Fatal("noncomparable admission cause was lost")
							}
						} else if !errors.Is(err, failure) {
							t.Fatal("admission cause was lost", err)
						}
						if mode == "denied_cleanup" && !errors.Is(err, fault) {
							t.Fatal("admission cleanup cause was lost", err)
						}
						if mode == "joined_unknown" || mode == "swallowed_denial" {
							if !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown}) {
								t.Fatal("uncertain admission was downgraded", err)
							}
						}
						if mode != "denied" && (!errors.Is(err, &sessions.Error{Code: sessions.CodeStoreFailure}) || strings.Contains(fmt.Sprintf("%+v", err), fault.Error())) {
							t.Fatal("private admission failure lost its execution boundary")
						}
						assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
						accountSessionRecord(t, f.runtime.SessionStore(), candidate, false)
						if previous.ID().Valid() {
							accountSessionRecord(t, f.runtime.SessionStore(), previous, true)
						}
					})
				}
			})
		}
	})
}

func runLoginExpired(add registerCase, open Open) {
	add("login_expired", func(t *testing.T) {
		for _, mode := range []string{"idle", "absolute"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionLogin(t, backend, true)
				config := f.config(t)
				config.Sessions.IdleTimeout, config.Sessions.AbsoluteLifetime = time.Second, time.Minute
				boundary := &loginFailureBackend{Backend: backend}
				owner := newUserReferenceOwner(t, boundary, f.userReferenceFixture, config)
				previous, err := owner.runtime.Sessions().Create(t.Context(), map[string]string{"private": "retain expired"})
				if err != nil {
					t.Fatal(err)
				}
				boundary.calls, boundary.effects = 0, nil
				elapsed := 2 * time.Second
				if mode == "absolute" {
					elapsed += time.Minute
				}
				f.instant.Store(nativeLoginInstant.Add(elapsed).UnixNano())
				before := userReferenceSnapshot(t, reopen(t))
				entropy := f.entropy.count()
				result, err := owner.runtime.LoginPersistence().Login(t.Context(), auth.SessionLogin{Credential: f.credential, Previous: previous, At: f.clock(), Admit: loginAdmission})
				loginNoResult(t, result, err)
				if err == auth.ErrInvalidCredentials || !errors.Is(err, &sessions.Error{Code: sessions.CodeNotFound}) {
					t.Fatal("expired snapshot became an admission denial", err)
				}
				if mode == "absolute" {
					if boundary.calls != 0 || len(boundary.effects) != 0 || f.entropy.count() != entropy {
						t.Fatal("absolute expiry attempted standalone cleanup or entropy")
					}
				} else {
					f.entropy.one(t, entropy)
					if boundary.calls != 1 || !slices.Equal(boundary.effects, []string{"delete"}) {
						t.Fatal("idle expiry did not roll back its actual delete prefix")
					}
				}
				assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				accountSessionRecord(t, f.runtime.SessionStore(), previous, true)
			})
		}
	})
}

func runLoginCollision(add registerCase, open Open) {
	add("login_collision", func(t *testing.T) {
		for _, operation := range []string{"create", "rotate", "replace"} {
			t.Run(operation, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionLogin(t, backend, true)
				previous := loginPrevious(t, f, operation)
				collision, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"collision": "preserved"})
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := base64.RawURLEncoding.DecodeString(collision.ID().Encoded())
				if err != nil {
					t.Fatal(err)
				}
				first := bytes.NewReader(encoded)
				config := f.config(t)
				config.Sessions.Random = io.MultiReader(first, &f.entropy)
				boundary := &loginFailureBackend{Backend: backend}
				owner := newUserReferenceOwner(t, boundary, f.userReferenceFixture, config)
				f.instant.Store(nativeLoginInstant.Add(time.Second).UnixNano())
				before := userReferenceSnapshot(t, reopen(t))
				entropy, admissions := f.entropy.count(), 0
				result, err := owner.runtime.LoginPersistence().Login(t.Context(), auth.SessionLogin{Credential: f.credential, Previous: previous, At: f.clock(), Admit: func(ctx context.Context, principal auth.Principal) error {
					admissions++
					return loginAdmission(ctx, principal)
				}})
				expected := loginExpectedRecord(t, f.credential, previous, operation, f.entropy.one(t, entropy), f.clock())
				if err != nil || first.Len() != 0 || boundary.calls != 2 || admissions != 2 || !reflect.DeepEqual(result.Record.Snapshot(), expected.Snapshot()) || !result.Credential.MatchesSessionStamp(f.credential.SessionStamp()) {
					t.Fatal("confirmed session collision did not admit one fresh entropy retry", err)
				}
				want := []string{"insert", "last_login"}
				if operation != "create" {
					want = append([]string{"delete"}, want...)
				}
				if !slices.Equal(boundary.effects, want) {
					t.Fatal("collision attempt changed state before its confirmed replacement", boundary.effects)
				}
				after := userReferenceSnapshot(t, reopen(t))
				assertUserReferenceState(t, loginExpectedState(t, before, after, f.credential, f.target, previous, expected, f.clock()), after, f.actor.Principal())
				accountSessionRecord(t, f.runtime.SessionStore(), collision, true)
				accountSessionRecord(t, f.runtime.SessionStore(), expected, true)
				if previous.ID().Valid() {
					accountSessionRecord(t, f.runtime.SessionStore(), previous, false)
				}
			})
		}
	})
}
