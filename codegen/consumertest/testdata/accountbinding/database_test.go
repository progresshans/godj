package consumer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	accountaudit "github.com/progresshans/godj/account/audit"
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	postgresdb "github.com/progresshans/godj/db/postgres"
	sqlitedb "github.com/progresshans/godj/db/sqlite"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
	"github.com/progresshans/godj/settings/database"
)

func accountDatabase(t *testing.T, schemas ...ir.Schema) database.Backend {
	t.Helper()
	backend, _ := accountDatabaseWithConfig(t, schemas...)
	return backend
}

func accountDatabaseWithConfig(t *testing.T, schemas ...ir.Schema) (database.Backend, database.Config) {
	t.Helper()
	selected, vendor := accountDatabaseConfig(t)
	backend, err := database.Open(t.Context(), selected)
	if err != nil {
		t.Fatal(err)
	}
	if vendor == "postgres" {
		if _, actual := backend.(*postgresdb.Backend); !actual {
			_ = backend.Close()
			t.Fatal("PostgreSQL account consumer did not open a native PostgreSQL backend")
		}
	} else {
		if _, actual := backend.(*sqlitedb.Backend); !actual {
			_ = backend.Close()
			t.Fatal("SQLite account consumer did not open a native SQLite backend")
		}
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	migrateAccountDatabase(t, backend, schemas...)
	return backend, selected
}

var accountSchemaSequence atomic.Uint64

func accountDatabaseConfig(t *testing.T) (database.Config, string) {
	t.Helper()
	vendor := os.Getenv("GODJ_ACCOUNT_CONSUMER_DATABASE")
	if vendor == "" {
		vendor = "sqlite"
	}
	if vendor == "sqlite" {
		selected, err := database.SQLite(filepath.Join(t.TempDir(), "accounts.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		return selected, vendor
	}
	if vendor != "postgres" {
		t.Fatal("unknown selected account consumer database")
	}
	url := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if url == "" {
		t.Fatal("selected PostgreSQL account consumer requires GODJ_TEST_POSTGRES_URL")
	}
	schema := fmt.Sprintf("godj_account_consumer_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), accountSchemaSequence.Add(1))
	selected, err := database.PostgreSQL(url, schema)
	if err != nil {
		t.Fatal("invalid selected PostgreSQL configuration")
	}
	connection, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal("connect selected PostgreSQL schema owner")
	}
	quoted := pgx.Identifier{schema}.Sanitize()
	created := false
	// Register the schema owner before native backends so LIFO cleanup closes
	// every connection/reconnect before dropping only this fixture's schema.
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if created {
			if _, err := connection.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error("drop selected PostgreSQL consumer schema")
			}
		}
		if err := connection.Close(cleanup); err != nil {
			t.Error("close selected PostgreSQL schema owner")
		}
	})
	if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal("create isolated PostgreSQL consumer schema")
	}
	created = true
	return selected, vendor
}

func migrateAccountDatabase(t *testing.T, backend database.Backend, schemas ...ir.Schema) {
	t.Helper()
	sources := domain.MigrationSources()
	sources = append(sources, accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
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

// Track the lifetime of the real native snapshot without replacing any account
// or permission loading. The generated factory supplies the production store.
type trackedSnapshots struct {
	backend db.SnapshotReader
	reading bool
}

func (scope *trackedSnapshots) ReadSnapshot(ctx context.Context, observe func(db.Queryer) error) error {
	return scope.backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		if scope.reading {
			return errors.New("overlapping consumer snapshot")
		}
		scope.reading = true
		defer func() { scope.reading = false }()
		return observe(reader)
	})
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

func assertStoredAccount[M any](t *testing.T, backend database.Backend, binding accountmodel.Binding[M], authenticator *accountmodel.Authenticator[M], login, principalID, currentPassword string) auth.Credential {
	t.Helper()
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("account binding consumer domain declaration"))
	ownership, err := domain.Read(t.Context(), backend, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	before, err := authenticator.Authenticate(t.Context(), login, currentPassword)
	if err != nil || before.Principal().ID() != principalID || !before.Principal().Authenticated() {
		t.Fatal("selected account did not authenticate through the shared stored authenticator", err)
	}
	unscoped, err := auth.NewPrincipal(auth.PrincipalConfig{ID: principalID, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := binding.RequirePrincipalDomainIn(t.Context(), transaction, unscoped)
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
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := binding.RequirePrincipalDomainIn(t.Context(), transaction, before.Principal())
		if err != nil {
			return err
		}
		ownership, err = domain.TransitionIn(t.Context(), transaction, ownership, change, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := binding.RequirePrincipalDomainIn(t.Context(), transaction, before.Principal())
		return err
	}); !errors.Is(err, &auth.Error{Code: auth.CodeCredential}) {
		t.Fatal("old-domain actor resolved by the same account ID", err)
	}
	after, err := authenticator.Resolve(t.Context(), principalID)
	if err != nil || after.Principal().ID() != principalID || after.MatchesSessionStamp(before.SessionStamp()) {
		t.Fatal("new account generation inherited authentication from identical stored fields", err)
	}
	fresh, err := authenticator.Authenticate(t.Context(), login, currentPassword)
	if err != nil || !fresh.MatchesSessionStamp(after.SessionStamp()) {
		t.Fatal("new ownership generation could not authenticate independently", err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
		_, err := binding.RequirePrincipalDomainIn(t.Context(), transaction, fresh.Principal())
		return err
	}); err != nil {
		t.Fatal("current-domain actor could not be rechecked", err)
	}
	foreign := sha256.Sum256([]byte("different account selection"))
	change, err = domain.Prepare(t.Context(), hex.EncodeToString(foreign[:]), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CoordinatedAtomic(t.Context(), func(transaction db.Session) error {
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
