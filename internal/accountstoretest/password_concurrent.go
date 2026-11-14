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

	"github.com/progresshans/godj/admin"
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
	f := provisionSelfPassword(t, backend)
	f.instant.Store(nativeLoginInstant.Add(time.Minute).UnixNano())
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	ready, start := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(start) }) }
	var workers sync.WaitGroup
	defer func() { cancel(); unblock(); workers.Wait() }()
	owners := make([]*userReferenceOwner, 2)
	entropy := make([]loginEntropy, 2)
	previous := f.targetSessions[:2]
	for i, connection := range []Backend{backend, reopen(t)} {
		config := f.config(t)
		config.Sessions.Random = &entropy[i]
		owners[i] = newUserReferenceOwner(t, connection, f.userReferenceFixture, config)
		owners[i].hasher.beforeHash = func(ctx context.Context) error {
			select {
			case ready <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-start:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	before := userReferenceSnapshot(t, reopen(t))
	type outcome struct {
		index  int
		result auth.PasswordChangeResult
		err    error
	}
	done := make(chan outcome, 2)
	for i := range owners {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			result, err := owners[i].runtime.PasswordChangePersistence().ChangePassword(ctx, previous[i].ID(), password, fmt.Sprintf("concurrent-new-password-%d", i))
			done <- outcome{i, result, err}
		}(i)
	}
	for range 2 {
		select {
		case <-ready:
		case result := <-done:
			t.Fatal("password writer finished before both hash barriers", result.err)
		case <-ctx.Done():
			t.Fatal("both password requests did not reach the outside-scope hash barrier", ctx.Err())
		}
	}
	unblock()
	winner := -1
	var won auth.PasswordChangeResult
	for range 2 {
		select {
		case result := <-done:
			i := result.index
			if owners[i].hasher.hashes.Load() != 1 || owners[i].hasher.verifies.Load() != 1 || owners[i].boundary.calls != 1 {
				t.Fatal("concurrent password request repeated hash, verification or native write")
			}
			if result.err == nil {
				if winner != -1 || !result.result.Record.ID().Valid() || owners[i].policies.Load() != 2 {
					t.Fatal("concurrent password writes published more than one authority or skipped the final policy")
				}
				winner, won = i, result.result
			} else {
				passwordNoResult(t, result.result, result.err)
				if result.err != auth.ErrInvalidCredentials {
					t.Fatal("concurrent password loser was not a confirmed stale credential", result.err)
				}
			}
		case <-ctx.Done():
			t.Fatal("concurrent password writes did not finish", ctx.Err())
		}
	}
	workers.Wait()
	if winner < 0 {
		t.Fatal("no password writer won")
	}
	for i := range owners {
		id := entropy[i].one(t, 0)
		if i != winner {
			if _, found, err := f.runtime.SessionStore().Load(t.Context(), id); err != nil || found {
				t.Fatal("losing password request persisted its candidate session", err)
			}
		}
	}
	after := userReferenceSnapshot(t, reopen(t))
	expected, envelope := selfPasswordExpected(t, f, owners[winner], before, after, previous[winner], entropy[winner].one(t, 0), f.clock())
	assertSelfPasswordResult(t, won, envelope)
	assertUserReferenceStateAt(t, expected, after, f.credential.Principal(), f.clock(), catalogExpectedEvent{"user", f.target.ID, admin.ActionChange, []string{"encoded_password"}})
	for _, record := range f.targetSessions {
		accountSessionRecord(t, f.runtime.SessionStore(), record, false)
	}
	accountSessionRecord(t, f.runtime.SessionStore(), envelope.Record, true)
	for i := range owners {
		_, err := owners[i].runtime.Authenticator().Authenticate(t.Context(), f.target.Username, fmt.Sprintf("concurrent-new-password-%d", i))
		if i == winner && err != nil || i != winner && err != auth.ErrInvalidCredentials {
			t.Fatal("durable password differs from winning request", err)
		}
	}
	assertUserReferenceState(t, after, userReferenceSnapshot(t, reopen(t)), f.actor.Principal())
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
			credential := passwordCredential(t, f, target)
			current := passwordSession(t, manager, credential, "current")
			oldBytes, err := base64.RawURLEncoding.DecodeString(current.ID().Encoded())
			if err != nil {
				t.Fatal(err)
			}
			freshBytes := bytes.Repeat([]byte{41}, 32)
			freshID, err := sessions.ParseID(base64.RawURLEncoding.EncodeToString(freshBytes))
			if err != nil {
				t.Fatal(err)
			}
			data := append(append(append([]byte{}, oldBytes...), collisionBytes...), freshBytes...)
			if mode == "unknown_collision" {
				data = append(append([]byte{}, collisionBytes...), freshBytes...)
			}
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
			var encoded string
			hasher.encode = func(ctx context.Context, raw string) (string, error) {
				value, err := f.hasher.Hash(ctx, raw)
				if err == nil {
					encoded = value
				}
				return value, err
			}
			before := passwordState(t, backend)
			result, err := passwordOwner.ChangePassword(t.Context(), current.ID(), password, password+"-new")
			if hasher.hashes.Load() != 1 {
				t.Fatal("session entropy retried password hash")
			}
			if mode == "confirmed_retry" {
				if err != nil || owner.calls != 2 || random.Len() != 0 || result.Record.ID() != freshID {
					t.Fatal("confirmed collision did not retry only rotation", err)
				}
				domain, _ := credential.Principal().CredentialDomain()
				expectedCredential, err := auth.NewCredentialForDomain(domain, target.Username, encoded, credential.Principal())
				if err != nil {
					t.Fatal(err)
				}
				snapshot := current.Snapshot()
				snapshot.ID, snapshot.Values[auth.SessionCredentialStampKey] = freshID, expectedCredential.SessionStamp()
				expectedRecord, err := sessions.RestoreRecord(snapshot, sessions.Limits{})
				if err != nil {
					t.Fatal(err)
				}
				assertSelfPasswordResult(t, result, auth.PasswordChangeResult{Credential: expectedCredential, Record: expectedRecord})
				after := accountNativeSnapshot(t, reopen(t))
				expected := before.accountNativeState
				row := target
				row.EncodedPassword, row.Revision = encoded, row.Revision+1
				userReferenceReplace(&expected, row)
				loginRemoveRecord(t, &expected, loginNamespace(t, credential), current.ID())
				loginPutRecord(t, &expected, after, loginNamespace(t, credential), expectedRecord, false)
				assertCatalogRowsAt(t, expected, after, credential.Principal(), now, catalogExpectedEvent{"user", target.ID, admin.ActionChange, []string{"encoded_password"}})
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
	if err := owner.wait(ctx); err != nil {
		return err
	}
	return owner.Backend.CoordinatedAtomic(ctx, write)
}

func (owner *passwordWriteGate) CoordinatedAtomicRelation(ctx context.Context, write func(db.RelationSession) error) error {
	if err := owner.wait(ctx); err != nil {
		return err
	}
	return owner.Backend.CoordinatedAtomicRelation(ctx, write)
}

func (owner *passwordWriteGate) wait(ctx context.Context) error {
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
	return nil
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
