package consumer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/domain"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/settings/database"
)

func accountDatabase(t *testing.T, schemas ...ir.Schema) database.Backend {
	t.Helper()
	selected, err := database.SQLite(filepath.Join(t.TempDir(), "accounts.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	backend, err := database.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	sources := domain.MigrationSources()
	var previous *migrations.MigrationKey
	for _, app := range schemas {
		migration := migrations.Migration{App: app.AppLabel, Name: "0001_initial"}
		if previous != nil {
			migration.Dependencies = []migrations.MigrationKey{*previous}
		}
		for _, model := range app.Models {
			migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: app.AppLabel, Model: model})
		}
		encoded, err := definition.Encode(definition.Producer{Name: "account-binding-consumer", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: app.AppLabel + "/0001_initial", Document: encoded})
		key := migrations.MigrationKey{App: app.AppLabel, Name: "0001_initial"}
		previous = &key
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	return backend
}

// The consumer composes the real domain and typed model writes in one native
// transaction. A failure after both writes must preserve the empty database.
func createBoundAccount[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], input orm.CreateInput[M]) M {
	t.Helper()
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("first account declaration"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	objects, err := binding.Objects()
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("first account consumer failure after writes")
	apply := func(fail bool) (M, error) {
		var candidate M
		err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
			if _, err := domain.InitializeIn(t.Context(), transaction, change, time.Now()); err != nil {
				return err
			}
			var err error
			candidate, err = objects.Create(t.Context(), transaction, input)
			if err != nil {
				return err
			}
			if fail {
				return fault
			}
			return nil
		})
		if err != nil {
			var zero M
			return zero, err
		}
		return candidate, nil
	}
	if _, err := apply(true); !errors.Is(err, fault) {
		t.Fatal("expected provisioning rollback", err)
	}
	if count, err := objects.Using(backend).Count(t.Context()); err != nil || count != 0 {
		t.Fatal("failed provisioning retained an account", count, err)
	}
	if got, err := domain.Lookup(t.Context(), backend, change.OperationID()); !errors.Is(err, domain.ErrUninitialized) || got.Sequence() != 0 {
		t.Fatal("failed provisioning retained domain history", err)
	}
	row, err := apply(false)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

const accountPasswordText = "account-binding-consumer-password"

func accountPassword(t *testing.T) (*auth.PBKDF2, string) {
	t.Helper()
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(t.Context(), accountPasswordText)
	if err != nil {
		t.Fatal(err)
	}
	return hasher, encoded
}

// This consumer adapter exercises the generated account binding and durable
// domain in one real snapshot. It does not implement the management runtime or
// permission/catalog loading; these rows have no effective permissions.
type storedAccount[M any] struct {
	backend     database.Backend
	binding     accountmodel.Binding[M]
	byLogin     func(string) orm.Predicate[M]
	byPrincipal func(string) orm.Predicate[M]
	order       orm.Ordering[M]
	reading     bool
}

func (store *storedAccount[M]) CredentialByUsername(ctx context.Context, value string) (auth.Credential, bool, error) {
	return store.read(ctx, store.byLogin(value))
}

func (store *storedAccount[M]) CredentialByID(ctx context.Context, value string) (auth.Credential, bool, error) {
	return store.read(ctx, store.byPrincipal(value))
}

func (store *storedAccount[M]) read(ctx context.Context, predicate orm.Predicate[M]) (auth.Credential, bool, error) {
	objects, err := store.binding.Objects()
	if err != nil {
		return auth.Credential{}, false, err
	}
	var result auth.Credential
	var found bool
	calls := 0
	err = store.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		calls++
		if calls != 1 || store.reading {
			return errors.New("account consumer violated its read scope")
		}
		store.reading = true
		defer func() { store.reading = false }()
		ownership, err := store.binding.RequireDomainIn(ctx, reader)
		if err != nil {
			return err
		}
		row, present, err := objects.Using(reader).Filter(predicate).OrderBy(store.order).First(ctx)
		if err != nil || !present {
			return err
		}
		result, err = store.binding.PrepareCredential(ownership.Generation(), row, nil)
		found = err == nil
		return err
	})
	if err != nil || calls != 1 {
		return auth.Credential{}, false, errors.Join(err, errors.New("account credential snapshot did not complete"))
	}
	return result, found, nil
}

type outsideSnapshotHasher struct {
	inner   auth.PasswordHasher
	reading *bool
}

func (hasher outsideSnapshotHasher) Hash(ctx context.Context, raw string) (string, error) {
	if *hasher.reading {
		return "", errors.New("hash work retained a DB snapshot")
	}
	return hasher.inner.Hash(ctx, raw)
}

func (hasher outsideSnapshotHasher) Verify(ctx context.Context, raw, encoded string) (bool, error) {
	if *hasher.reading {
		return false, errors.New("password verification retained a DB snapshot")
	}
	return hasher.inner.Verify(ctx, raw, encoded)
}

func (hasher outsideSnapshotHasher) ValidateEncoded(encoded string) error {
	if *hasher.reading {
		return errors.New("hash profile validation retained a DB snapshot")
	}
	return hasher.inner.ValidateEncoded(encoded)
}

func assertStoredAccount[M any](t *testing.T, store *storedAccount[M], hasher auth.PasswordHasher, login, principalID string) auth.Credential {
	t.Helper()
	fingerprint, err := store.binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("account binding consumer domain declaration"))
	ownership, err := domain.Read(t.Context(), store.backend, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewStoredAuthenticator(t.Context(), store, outsideSnapshotHasher{hasher, &store.reading})
	if err != nil {
		t.Fatal(err)
	}
	before, err := authenticator.Authenticate(t.Context(), login, accountPasswordText)
	if err != nil || before.Principal().ID() != principalID || !before.Principal().Authenticated() {
		t.Fatal("selected account did not authenticate through the shared stored authenticator", err)
	}
	unscoped, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := store.binding.RequirePrincipalDomainIn(t.Context(), transaction, unscoped)
		return err
	}); !errors.Is(err, &auth.Error{Code: auth.CodeCredential}) {
		t.Fatal("unscoped actor inherited a stored account by ID", err)
	}
	if rejected, err := authenticator.Authenticate(t.Context(), login, "wrong-password"); !errors.Is(err, auth.ErrInvalidCredentials) || rejected.SessionStamp() != "" {
		t.Fatal("wrong password supplied an account credential", err)
	}
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := store.binding.RequirePrincipalDomainIn(t.Context(), transaction, before.Principal())
		if err != nil {
			return err
		}
		ownership, err = domain.TransitionIn(t.Context(), transaction, ownership, change, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := store.binding.RequirePrincipalDomainIn(t.Context(), transaction, before.Principal())
		return err
	}); !errors.Is(err, &auth.Error{Code: auth.CodeCredential}) {
		t.Fatal("old-domain actor resolved by the same account ID", err)
	}
	after, err := authenticator.Resolve(t.Context(), principalID)
	if err != nil || after.Principal().ID() != principalID || after.MatchesSessionStamp(before.SessionStamp()) {
		t.Fatal("new account generation inherited authentication from identical stored fields", err)
	}
	fresh, err := authenticator.Authenticate(t.Context(), login, accountPasswordText)
	if err != nil || !fresh.MatchesSessionStamp(after.SessionStamp()) {
		t.Fatal("new ownership generation could not authenticate independently", err)
	}
	if err := store.backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := store.binding.RequirePrincipalDomainIn(t.Context(), transaction, fresh.Principal())
		return err
	}); err != nil {
		t.Fatal("current-domain actor could not be rechecked", err)
	}
	foreign := sha256.Sum256([]byte("different account selection"))
	change, err = domain.Prepare(t.Context(), hex.EncodeToString(foreign[:]), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := domain.TransitionIn(t.Context(), transaction, ownership, change, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if rejected, err := authenticator.Resolve(t.Context(), principalID); !errors.Is(err, domain.ErrMismatch) || rejected.SessionStamp() != "" {
		t.Fatal("stored role mismatch fell back to the previous account model", err)
	}
	return before
}
