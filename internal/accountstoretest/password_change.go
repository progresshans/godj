package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/validation"
)

type passwordScopeOwner struct {
	Backend
	scopes, writes atomic.Int64
}

func (owner *passwordScopeOwner) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	owner.scopes.Add(1)
	defer owner.scopes.Add(-1)
	return owner.Backend.ReadSnapshot(ctx, read)
}
func (owner *passwordScopeOwner) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	owner.writes.Add(1)
	owner.scopes.Add(1)
	defer owner.scopes.Add(-1)
	return owner.Backend.CoordinatedAtomic(ctx, write)
}

type passwordChangeHasher struct {
	auth.PasswordHasher
	scopes           *atomic.Int64
	hashes, verifies atomic.Int64
	beforeHash       func(context.Context) error
	afterVerify      func(context.Context) error
	encode           func(context.Context, string) (string, error)
}

func (hasher *passwordChangeHasher) Hash(ctx context.Context, raw string) (string, error) {
	if hasher.scopes != nil && hasher.scopes.Load() != 0 {
		return "", errors.New("password hash retained native scope")
	}
	hasher.hashes.Add(1)
	if hasher.beforeHash != nil {
		if err := hasher.beforeHash(ctx); err != nil {
			return "", err
		}
	}
	if hasher.encode != nil {
		return hasher.encode(ctx, raw)
	}
	return hasher.PasswordHasher.Hash(ctx, raw)
}
func (hasher *passwordChangeHasher) Verify(ctx context.Context, raw, encoded string) (bool, error) {
	if hasher.scopes != nil && hasher.scopes.Load() != 0 {
		return false, errors.New("password verification retained native scope")
	}
	hasher.verifies.Add(1)
	valid, err := hasher.PasswordHasher.Verify(ctx, raw, encoded)
	if err == nil && valid && hasher.afterVerify != nil {
		err = hasher.afterVerify(ctx)
	}
	return valid, err
}
func (hasher *passwordChangeHasher) ValidateEncoded(encoded string) error {
	if hasher.scopes != nil && hasher.scopes.Load() != 0 {
		return errors.New("password encoding check retained native scope")
	}
	return hasher.PasswordHasher.ValidateEncoded(encoded)
}

type passwordFixture struct {
	store         *accountmodel.SessionStore[models.User]
	manager       *sessions.Manager
	authenticator *accountmodel.Authenticator[models.User]
	owner         auth.PasswordChangePersistence
}

func passwordComposition(t *testing.T, backend Backend, now *time.Time, hasher auth.PasswordHasher, config accountmodel.PasswordChangeConfig[models.User]) passwordFixture {
	t.Helper()
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := store.Authenticator(t.Context(), hasher)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.PasswordChangePersistence(manager, authenticator, config)
	if err != nil || owner.Sessions() != manager {
		t.Fatal("password exact manager owner", err)
	}
	return passwordFixture{store, manager, authenticator, owner}
}

func passwordSession(t *testing.T, manager *sessions.Manager, credential auth.Credential, marker string) sessions.Record {
	t.Helper()
	record, err := manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: credential.Principal().ID(), auth.SessionCredentialStampKey: credential.SessionStamp(), "csrf": "synthetic-csrf", "profile-marker": marker})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func passwordChangeLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	credential := profileActor(t, f)
	now := joined
	tracked := &passwordScopeOwner{Backend: backend}
	hasher := &passwordChangeHasher{PasswordHasher: f.hasher, scopes: &tracked.scopes}
	policyCalls := 0
	configured := passwordComposition(t, tracked, &now, hasher, accountmodel.PasswordChangeConfig[models.User]{ValidatePassword: func(_ context.Context, raw string, row models.User) (validation.Errors, error) {
		policyCalls++
		if row.ID != f.user.ID || row.PrincipalID != f.user.PrincipalID || row.EncodedPassword != "" || row.FirstName != f.user.FirstName {
			return validation.Errors{}, errors.New("typed password policy lost current profile or leaked encoded password")
		}
		if len(raw) < 12 {
			return validation.NewErrors(validation.New("password", "too_short")), nil
		}
		return validation.Errors{}, nil
	}})
	current := passwordSession(t, configured.manager, credential, "current")
	anonymous, err := configured.manager.Create(t.Context(), map[string]string{"anonymous": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := configured.manager.Create(t.Context(), map[string]string{auth.SessionPrincipalIDKey: "other-principal", "other": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sessiondb.New(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	history, err := database.Store(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if created, err := history.Create(t.Context(), current); !created || err != nil {
		t.Fatal("historical session fixture", err)
	}
	historical := current
	oldPassword := password
	newPassword := password + "-changed"
	for _, name := range []string{"new_password", "same_password_new_salt"} {
		t.Run(name, func(t *testing.T) {
			previous := current
			sibling := passwordSession(t, configured.manager, credential, "revoke")
			before := profileRow(t, backend, f.user.ID)
			hasher.hashes.Store(0)
			hasher.verifies.Store(0)
			tracked.writes.Store(0)
			policyCalls = 0
			now = now.Add(time.Minute)
			result, err := configured.owner.ChangePassword(t.Context(), previous.ID(), oldPassword, newPassword)
			if err != nil || !result.Record.ID().Valid() || result.Record.ID() == previous.ID() || result.Credential.MatchesSessionStamp(credential.SessionStamp()) {
				t.Fatal("selected password change", err)
			}
			if hasher.hashes.Load() != 1 || hasher.verifies.Load() != 1 || tracked.writes.Load() != 1 || policyCalls != 2 {
				t.Fatal("password work/fence/policy count", hasher.hashes.Load(), hasher.verifies.Load(), tracked.writes.Load(), policyCalls)
			}
			accountSessionRecord(t, configured.store, previous, false)
			accountSessionRecord(t, configured.store, sibling, false)
			accountSessionRecord(t, configured.store, result.Record, true)
			accountSessionRecord(t, configured.store, anonymous, true)
			accountSessionRecord(t, configured.store, other, true)
			if got, present, err := history.Load(t.Context(), historical.ID()); err != nil || !present || !reflect.DeepEqual(got.Snapshot(), historical.Snapshot()) {
				t.Fatal("password revocation changed historical namespace", err)
			}
			expectedValues := previous.Values()
			expectedValues[auth.SessionCredentialStampKey] = result.Credential.SessionStamp()
			if !reflect.DeepEqual(result.Record.Values(), expectedValues) || !result.Record.CreatedAt().Equal(previous.CreatedAt()) || !result.Record.AbsoluteExpiresAt().Equal(previous.AbsoluteExpiresAt()) {
				t.Fatal("rotation lost authoritative payload or absolute lifetime")
			}
			after := profileRow(t, reopen(t), f.user.ID)
			if after.EncodedPassword == before.EncodedPassword || after.Revision != before.Revision+1 {
				t.Fatal("password and revision did not commit")
			}
			preserved := after
			preserved.EncodedPassword = before.EncodedPassword
			preserved.Revision = before.Revision
			if !reflect.DeepEqual(preserved, before) {
				t.Fatal("password change overwrote profile, grants or observational fields")
			}
			entries := profileHistory(t, reopen(t), result.Credential.Principal(), f.user.ID)
			if len(entries) != int(after.Revision-1) || !reflect.DeepEqual(entries[len(entries)-1].ChangedFields(), []string{"encoded_password"}) || entries[len(entries)-1].ActorID() != f.user.PrincipalID || !entries[len(entries)-1].At().Equal(now) {
				t.Fatal("self-authored password audit missing or wrong")
			}
			if name == "new_password" {
				if _, err := configured.authenticator.Authenticate(t.Context(), f.user.Username, oldPassword); err != auth.ErrInvalidCredentials {
					t.Fatal("old password still authenticates", err)
				}
			}
			reopened, _, _ := accountSessions(t, reopen(t), &now)
			accountSessionRecord(t, reopened, result.Record, true)
			verified, err := configured.authenticator.Authenticate(t.Context(), f.user.Username, newPassword)
			if err != nil || !verified.MatchesSessionStamp(result.Credential.SessionStamp()) {
				t.Fatal("changed credential cannot authenticate", err)
			}
			for _, text := range []string{fmt.Sprintf("%+v", result), fmt.Sprintf("%#v", configured.owner)} {
				if strings.Contains(text, newPassword) || strings.Contains(text, after.EncodedPassword) {
					t.Fatal("password result diagnostics leaked")
				}
			}
			current, credential, oldPassword = result.Record, result.Credential, newPassword
		})
	}
}

// Compare exact stored account, audit and session payloads after every refused
// operation. Counts alone would miss an overwritten bearer, payload or hash.
type passwordDurableState struct {
	users    []models.User
	sessions []sessionmodels.Session
	audits   []auditmodels.Event
}

func passwordState(t *testing.T, backend Backend) passwordDurableState {
	t.Helper()
	users, err := models.UserObjects.Using(backend).OrderBy(models.UserFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	records, err := sessionmodels.SessionObjects.Using(backend).OrderBy(sessionmodels.SessionFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	audits, err := auditmodels.EventObjects.Using(backend).OrderBy(auditmodels.EventFields.ID.Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return passwordDurableState{users, records, audits}
}

func passwordUnchanged(t *testing.T, backend Backend, before passwordDurableState) {
	t.Helper()
	if !reflect.DeepEqual(before, passwordState(t, backend)) {
		t.Fatal("refused password change changed durable account, session or audit bytes")
	}
}

func passwordCredential(t *testing.T, f *fixture, row models.User) auth.Credential {
	t.Helper()
	credential, present, err := f.store.CredentialByID(t.Context(), row.PrincipalID)
	if err != nil || !present || !credential.Principal().Authenticated() {
		t.Fatal("password fixture credential", err)
	}
	return credential
}

func passwordNoResult(t *testing.T, result auth.PasswordChangeResult, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(result, auth.PasswordChangeResult{}) {
		t.Fatal("failed password change published authority", err)
	}
}
