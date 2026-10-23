package consumer

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
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
	var sources []definition.Source
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

// This consumer adapter exercises the typed credential projection inside real
// DB snapshots. Its manually changed generation is a test input, not a durable
// account-domain registry or the completed identity management runtime.
type storedAccount[M any] struct {
	backend     db.SnapshotReader
	binding     accountmodel.Binding[M]
	byLogin     func(string) orm.Predicate[M]
	byPrincipal func(string) orm.Predicate[M]
	order       orm.Ordering[M]
	generation  [sha256.Size]byte
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
		row, present, err := objects.Using(reader).Filter(predicate).OrderBy(store.order).First(ctx)
		if err != nil || !present {
			return err
		}
		result, err = store.binding.PrepareCredential(store.generation, row, nil)
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
	store.generation = [sha256.Size]byte{1}
	authenticator, err := auth.NewStoredAuthenticator(t.Context(), store, outsideSnapshotHasher{hasher, &store.reading})
	if err != nil {
		t.Fatal(err)
	}
	before, err := authenticator.Authenticate(t.Context(), login, accountPasswordText)
	if err != nil || before.Principal().ID() != principalID || !before.Principal().Authenticated() {
		t.Fatal("selected account did not authenticate through the shared stored authenticator", err)
	}
	if rejected, err := authenticator.Authenticate(t.Context(), login, "wrong-password"); !errors.Is(err, auth.ErrInvalidCredentials) || rejected.SessionStamp() != "" {
		t.Fatal("wrong password supplied an account credential", err)
	}
	store.generation = [sha256.Size]byte{2}
	after, err := authenticator.Resolve(t.Context(), principalID)
	if err != nil || after.Principal().ID() != principalID || after.MatchesSessionStamp(before.SessionStamp()) {
		t.Fatal("new account generation inherited authentication from identical stored fields", err)
	}
	fresh, err := authenticator.Authenticate(t.Context(), login, accountPasswordText)
	if err != nil || !fresh.MatchesSessionStamp(after.SessionStamp()) {
		t.Fatal("new ownership generation could not authenticate independently", err)
	}
	store.generation = [sha256.Size]byte{}
	if rejected, err := authenticator.Resolve(t.Context(), principalID); err == nil || rejected.SessionStamp() != "" {
		t.Fatal("missing ownership generation fell back to an unscoped credential")
	}
	return before
}
