package accountstoretest

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/query"
)

func runLoginConcurrentEdits(add registerCase, open Open) {
	add("login_concurrent_edits", func(t *testing.T) {
		for _, mode := range []string{"profile", "password"} {
			t.Run(mode, func(t *testing.T) {
				backend, reopen := open(t)
				f := provisionLogin(t, backend, true)
				previous := loginPrevious(t, f, "rotate")
				f.instant.Store(nativeLoginInstant.Add(time.Second).UnixNano())
				ready, start := make(chan struct{}, 2), make(chan struct{})
				login := newUserReferenceOwner(t, &passwordWriteGate{backend, ready, start}, f.userReferenceFixture, f.config(t))
				writer := newUserReferenceOwner(t, &passwordWriteGate{reopen(t), ready, start}, f.userReferenceFixture, f.config(t))
				before := userReferenceSnapshot(t, reopen(t))
				entropy := f.entropy.count()
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				var once sync.Once
				unblock := func() { once.Do(func() { close(start) }) }
				var workers sync.WaitGroup
				defer func() { cancel(); unblock(); workers.Wait() }()
				type outcome struct {
					login    bool
					session  auth.SessionLoginResult
					profile  accountmodel.AccountChange
					password accountmodel.PasswordEditResult
					err      error
				}
				done := make(chan outcome, 2)
				input := profileInput(t, map[string]query.Value{"first_name": query.String("Concurrent")})
				workers.Add(2)
				go func() {
					defer workers.Done()
					result, err := login.runtime.LoginPersistence().Login(ctx, auth.SessionLogin{Credential: f.credential, Previous: previous, At: f.clock(), Admit: loginAdmission})
					done <- outcome{login: true, session: result, err: err}
				}()
				go func() {
					defer workers.Done()
					var result outcome
					if mode == "profile" {
						result.profile, result.err = writer.runtime.Accounts().Editor().Update(ctx, f.actor.Principal(), f.target.ID, 1, input)
					} else {
						result.password, result.err = writer.runtime.Accounts().Passwords().SetPassword(ctx, f.actor.Principal(), f.target.ID, 1, userReferencePassword)
					}
					done <- result
				}()
				for range 2 {
					select {
					case <-ready:
					case early := <-done:
						t.Fatal("login/edit did not reach both independent native fences", early.err)
					case <-ctx.Done():
						t.Fatal("login/edit preparation retained a scope or timed out", ctx.Err())
					}
				}
				unblock()
				var logged, edited outcome
				for range 2 {
					select {
					case result := <-done:
						if result.login {
							logged = result
						} else {
							edited = result
						}
					case <-ctx.Done():
						t.Fatal("login/edit did not finish within its deadline", ctx.Err())
					}
				}
				workers.Wait()
				if edited.err != nil || login.boundary.calls != 1 || writer.boundary.calls != 1 || login.hasher.hashes.Load() != 0 || login.hasher.verifies.Load() != 0 || login.policies.Load() != 0 {
					t.Fatal("observational login blocked or replayed the concurrent edit", edited.err)
				}
				candidate := loginExpectedRecord(t, f.credential, previous, "rotate", f.entropy.one(t, entropy), f.clock())
				after := userReferenceSnapshot(t, reopen(t))
				expected, row := before, f.target
				row.Revision = 2
				if mode == "profile" {
					if logged.err != nil || !reflect.DeepEqual(logged.session.Record.Snapshot(), candidate.Snapshot()) || !logged.session.Credential.MatchesSessionStamp(f.credential.SessionStamp()) {
						t.Fatal("profile edit caused a spurious login failure", logged.err)
					}
					if edited.profile.ID != row.ID || edited.profile.Revision != 2 || edited.profile.RevokedSessions != 0 || !slices.Equal(edited.profile.ChangedFields, []string{"first_name"}) || writer.hasher.hashes.Load() != 0 || writer.policies.Load() != 0 {
						t.Fatal("concurrent profile edit lost its exact result")
					}
					row.FirstName = "Concurrent"
					expected = loginExpectedState(t, before, after, f.credential, row, previous, candidate, f.clock())
					assertUserReferenceState(t, expected, after, f.actor.Principal(), catalogExpectedEvent{"user", row.ID, admin.ActionChange, []string{"first_name"}})
					accountSessionRecord(t, f.runtime.SessionStore(), previous, false)
					accountSessionRecord(t, f.runtime.SessionStore(), candidate, true)
				} else {
					if logged.err != nil && logged.err != auth.ErrInvalidCredentials {
						t.Fatal("password edit changed stale-login rejection", logged.err)
					}
					wantRevoked := len(f.targetSessions)
					if logged.err == nil {
						wantRevoked++
						if !reflect.DeepEqual(logged.session.Record.Snapshot(), candidate.Snapshot()) || !logged.session.Credential.MatchesSessionStamp(f.credential.SessionStamp()) {
							t.Fatal("transient login did not use the verified earlier credential")
						}
						instant := f.clock().Truncate(time.Microsecond)
						row.LastLogin = &instant
						loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), previous.ID())
					} else {
						loginNoResult(t, logged.session, logged.err)
					}
					if edited.password.ID != row.ID || edited.password.Revision != 2 || edited.password.RevokedSessions != wantRevoked || writer.hasher.hashes.Load() != 1 || writer.policies.Load() != 2 {
						t.Fatal("concurrent password edit lost its exact revocation or repeated hashing")
					}
					row.EncodedPassword = writer.encoded.Load().(string)
					userReferenceReplace(&expected.account, row)
					for _, record := range f.targetSessions {
						loginRemoveRecord(t, &expected.account, loginNamespace(t, f.credential), record.ID())
					}
					assertUserReferenceState(t, expected, after, f.actor.Principal(), catalogExpectedEvent{"user", row.ID, admin.ActionChange, []string{"encoded_password"}})
					accountSessionRecord(t, f.runtime.SessionStore(), previous, logged.err != nil)
					accountSessionRecord(t, f.runtime.SessionStore(), candidate, false)
					for _, record := range f.targetSessions {
						accountSessionRecord(t, f.runtime.SessionStore(), record, false)
					}
					current, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", userReferencePassword)
					if err != nil || current.MatchesSessionStamp(f.credential.SessionStamp()) {
						t.Fatal("password race winner lost its new credential", err)
					}
					assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
				}
			})
		}
	})
}
