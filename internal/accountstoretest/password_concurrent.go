package accountstoretest

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func passwordConcurrent(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	now := joined
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ready, start := make(chan struct{}, 2), make(chan struct{})
	owners := make([]passwordFixture, 2)
	hashers := make([]*passwordChangeHasher, 2)
	previous := make([]sessions.Record, 2)
	for i, connection := range []Backend{backend, reopen(t)} {
		hashers[i] = &passwordChangeHasher{PasswordHasher: f.hasher}
		owners[i] = passwordComposition(t, connection, &now, hashers[i], accountmodel.PasswordChangeConfig[models.User]{})
		previous[i] = passwordSession(t, owners[i].manager, credential, fmt.Sprintf("writer-%d", i))
		hashers[i].hashes.Store(0)
		hashers[i].beforeHash = func(ctx context.Context) error {
			ready <- struct{}{}
			select {
			case <-start:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	var wait sync.WaitGroup
	results, failures := make([]auth.PasswordChangeResult, 2), make([]error, 2)
	for i := range owners {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			results[i], failures[i] = owners[i].owner.ChangePassword(ctx, previous[i].ID(), password, fmt.Sprintf("concurrent-new-password-%d", i))
		}(i)
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("both password requests did not reach the hash barrier")
		}
	}
	close(start)
	wait.Wait()
	winner := -1
	for i, err := range failures {
		if hashers[i].hashes.Load() != 1 {
			t.Fatal("concurrent password request rehashed")
		}
		if err == nil {
			if winner != -1 || !results[i].Record.ID().Valid() {
				t.Fatal("concurrent password writes published more than one session")
			}
			winner = i
		} else {
			passwordNoResult(t, results[i], err)
			if err != auth.ErrInvalidCredentials {
				t.Fatal("concurrent password loser was not a confirmed stale credential", err)
			}
		}
	}
	if winner < 0 {
		t.Fatal("no password writer won")
	}
	for _, record := range previous {
		accountSessionRecord(t, owners[0].store, record, false)
	}
	accountSessionRecord(t, owners[1].store, results[winner].Record, true)
	row := profileRow(t, reopen(t), f.user.ID)
	if row.Revision != 2 || accountSessionCount(t, backend) != 1 || len(profileHistory(t, backend, credential.Principal(), f.user.ID)) != 1 {
		t.Fatal("concurrent password commit was not one account/session/audit transition")
	}
	for i := range owners {
		_, err := owners[i].authenticator.Authenticate(t.Context(), row.Username, fmt.Sprintf("concurrent-new-password-%d", i))
		if i == winner && err != nil || i != winner && err != auth.ErrInvalidCredentials {
			t.Fatal("durable password differs from winning request", err)
		}
	}
}

func passwordEntropy(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	base, manager, _ := accountSessions(t, backend, &now)
	collision := passwordSession(t, manager, profileActor(t, f), "collision-control")
	collisionBytes, err := base64.RawURLEncoding.DecodeString(collision.ID().Encoded())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"confirmed_retry", "exhausted", "unknown_collision", "entropy_failure"} {
		t.Run(mode, func(t *testing.T) {
			target := roleTarget(t, backend, f, "password-entropy-"+mode)
			current := passwordSession(t, manager, passwordCredential(t, f, target), "current")
			data := append(append([]byte{}, collisionBytes...), bytes.Repeat([]byte{41}, 32)...)
			if mode == "exhausted" {
				data = bytes.Repeat(collisionBytes, 4)
			}
			if mode == "entropy_failure" {
				data = nil
			}
			random := bytes.NewReader(data)
			owner := &profileFaultBackend{Backend: backend}
			if mode == "unknown_collision" {
				owner.mode, owner.fault = "cleanup_failure", &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
			}
			store, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
			if err != nil {
				t.Fatal(err)
			}
			controlled, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return now }, Random: random})
			if err != nil {
				t.Fatal(err)
			}
			hasher := &passwordChangeHasher{PasswordHasher: f.hasher}
			verifier, err := store.Authenticator(t.Context(), hasher)
			if err != nil {
				t.Fatal(err)
			}
			passwordOwner, err := store.PasswordChangePersistence(controlled, verifier, accountmodel.PasswordChangeConfig[models.User]{})
			if err != nil {
				t.Fatal(err)
			}
			hasher.hashes.Store(0)
			before := passwordState(t, backend)
			result, err := passwordOwner.ChangePassword(t.Context(), current.ID(), password, password+"-new")
			if hasher.hashes.Load() != 1 {
				t.Fatal("session entropy retried password hash")
			}
			if mode == "confirmed_retry" {
				if err != nil || owner.calls != 2 || random.Len() != 0 || profileRow(t, backend, target.ID).Revision != 2 || len(profileHistory(t, backend, result.Credential.Principal(), target.ID)) != 1 {
					t.Fatal("confirmed collision did not retry only rotation", err)
				}
				accountSessionRecord(t, base, current, false)
				accountSessionRecord(t, base, result.Record, true)
			} else {
				passwordNoResult(t, result, err)
				if mode == "unknown_collision" {
					requireProfileCode(t, err, accountmodel.CodeOutcomeUnknown)
					if owner.calls != 1 || random.Len() != 32 || !errors.Is(err, owner.fault) {
						t.Fatal("uncertain collision retried or lost native cause", err)
					}
				} else if mode == "exhausted" && (owner.calls != 4 || random.Len() != 0) || mode == "entropy_failure" && owner.calls != 0 {
					t.Fatal("entropy work exceeded or missed its bounded attempts", owner.calls)
				}
				passwordUnchanged(t, reopen(t), before)
			}
			accountSessionRecord(t, base, collision, true)
		})
	}
}

func passwordNonpositiveKeys(t *testing.T, backend Backend, storedKey StoredKeyFixture) {
	f := provision(t, backend, ir.AccountExact)
	now := joined
	configured := passwordComposition(t, backend, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	for _, key := range []int64{0, -17} {
		t.Run(fmt.Sprintf("key_%d", key), func(t *testing.T) {
			row := roleTarget(t, backend, f, fmt.Sprintf("password-key-%d", key))
			storedKey(t, backend, "user", row.ID, key)
			(models.UserDescriptor{}).SetPrimaryKey(&row, key)
			credential := passwordCredential(t, f, row)
			previous := passwordSession(t, configured.manager, credential, "signed-key")
			result, err := configured.owner.ChangePassword(t.Context(), previous.ID(), password, password+"-new")
			if err != nil || result.Credential.Principal().ID() != row.PrincipalID {
				t.Fatal("present nonpositive account key lost during password change", err)
			}
			stored := profileRow(t, backend, key)
			entries := profileHistory(t, backend, result.Credential.Principal(), key)
			if stored.Revision != 2 || stored.EncodedPassword == row.EncodedPassword || len(entries) != 1 || entries[0].ObjectID() != key {
				t.Fatal("nonpositive password row/audit mismatch")
			}
			accountSessionRecord(t, configured.store, previous, false)
			accountSessionRecord(t, configured.store, result.Record, true)
		})
	}
}

type passwordWriteGate struct {
	Backend
	ready chan<- struct{}
	start <-chan struct{}
}

func (owner *passwordWriteGate) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	select {
	case owner.ready <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-owner.start:
	case <-ctx.Done():
		return ctx.Err()
	}
	return owner.Backend.CoordinatedAtomic(ctx, write)
}

func passwordVersusLogin(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	now := joined
	base, manager, _ := accountSessions(t, backend, &now)
	previous := passwordSession(t, manager, credential, "password-writer")
	anonymous, err := manager.Create(t.Context(), map[string]string{"anonymous": "pending-login"})
	if err != nil {
		t.Fatal(err)
	}
	ready, start := make(chan struct{}, 2), make(chan struct{})
	configured := passwordComposition(t, &passwordWriteGate{backend, ready, start}, &now, f.hasher, accountmodel.PasswordChangeConfig[models.User]{})
	_, _, login := accountSessions(t, &passwordWriteGate{reopen(t), ready, start}, &now)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	var changed auth.PasswordChangeResult
	var loggedIn auth.SessionLoginResult
	var changeErr, loginErr error
	wait.Add(2)
	go func() {
		defer wait.Done()
		changed, changeErr = configured.owner.ChangePassword(ctx, previous.ID(), password, password+"-new")
	}()
	go func() {
		defer wait.Done()
		loggedIn, loginErr = login.Login(ctx, selectedSessionLogin(credential, anonymous, now))
	}()
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("password and login did not reach independent write fences")
		}
	}
	close(start)
	wait.Wait()
	if changeErr != nil || !changed.Record.ID().Valid() {
		t.Fatal("concurrent login blocked valid password change", changeErr)
	}
	if loginErr != nil && loginErr != auth.ErrInvalidCredentials {
		t.Fatal("concurrent login lost stale-credential denial", loginErr)
	}
	accountSessionRecord(t, base, previous, false)
	accountSessionRecord(t, base, changed.Record, true)
	expected := int64(1)
	if loginErr != nil {
		expected++
		if loggedIn.Record.ID().Valid() {
			t.Fatal("denied old credential published login")
		}
		accountSessionRecord(t, base, anonymous, true)
	} else {
		accountSessionRecord(t, base, anonymous, false)
		accountSessionRecord(t, base, loggedIn.Record, false)
	}
	if count := accountSessionCount(t, reopen(t)); count != expected || profileRow(t, backend, f.user.ID).Revision != 2 || len(profileHistory(t, backend, credential.Principal(), f.user.ID)) != 1 {
		t.Fatal("login left an old authenticated session after password commit", count, expected)
	}
}
