package accountstoretest

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/account/domain"
	domainmodels "github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/auth"
	deletefixture "github.com/progresshans/godj/conformance/accountdeletefixture"
	"github.com/progresshans/godj/conformance/accountdeletefixture/access"
	"github.com/progresshans/godj/conformance/accountdeletefixture/links"
	"github.com/progresshans/godj/conformance/accountdeletefixture/people"
	"github.com/progresshans/godj/conformance/accountdeletefixture/project"
	"github.com/progresshans/godj/conformance/accountdeletefixture/tickets"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/internal/migrationautodetect"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

func runAccountDeletion(t *testing.T, open Open, storedKey StoredKeyFixture) {
	t.Helper()
	for _, test := range []struct {
		name string
		run  func(*testing.T, Backend, func(*testing.T) Backend)
	}{
		{"delete_lifecycle", accountDeletionLifecycle},
		{"delete_catalog_lifecycle", accountCatalogDeletion},
		{"delete_authority", accountDeletionAuthority},
		{"delete_failures", accountDeletionFailures},
		{"delete_concurrent_revision", accountDeletionRevisionRace},
		{"delete_concurrent_login", accountDeletionLoginRace},
		{"delete_signed_keys", func(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
			accountDeletionSignedKeys(t, backend, reopen, storedKey)
		}},
		{"delete_membership_copies", accountDeletionMembershipCopies},
		{"delete_batches", accountDeletionBatches},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, reopen := open(t)
			migrateDeletion(t, backend)
			test.run(t, backend, reopen)
		})
	}
}

func migrateDeletion(t *testing.T, backend Backend) {
	t.Helper()
	spec, err := deletefixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var schemas []ir.Schema
	for _, app := range spec.Apps {
		schemas = append(schemas, app.Schema)
	}
	desired, err := migrations.NewProjectState(schemas...)
	if err != nil {
		t.Fatal(err)
	}
	empty, _, err := definition.Load()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := migrationautodetect.Detect(migrationautodetect.Request{Definitions: empty, Desired: desired, ManagedApps: desired.Apps()})
	if err != nil {
		t.Fatal(err)
	}
	sources := append(domain.MigrationSources(), accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
	for _, migration := range plan.Migrations() {
		wire, err := definition.Encode(definition.Producer{Name: "account-delete-product", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.App + "/" + migration.Name, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
}

type deletionFixture struct {
	binding     accountmodel.Binding[people.Member]
	store       *accountmodel.SessionStore[people.Member]
	credentials *accountmodel.CredentialStore[people.Member]
	manager     *sessions.Manager
	actor       auth.Principal
	actorRow    people.Member
	secret      string
	owner       domain.Receipt
}

func deletionConfig() accountmodel.AccountDeleteConfig {
	return accountmodel.AccountDeleteConfig{
		Account:    accountmodel.DeletionPermissions{Delete: "account.delete", Change: "account.change"},
		Group:      accountmodel.DeletionPermissions{Delete: "catalog.delete_group", Change: "catalog.change_group"},
		Permission: accountmodel.DeletionPermissions{Delete: "catalog.delete_permission", Change: "catalog.change_permission"},
		Now:        func() time.Time { return joined },
	}
}

func provisionDeletion(t *testing.T, backend Backend) *deletionFixture {
	t.Helper()
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := project.NewAccountCredentialStore(backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(store, sessions.Config{Clock: func() time.Time { return joined }})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := hasher.Hash(t.Context(), password)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("native account deletion ownership"))
	change, err := domain.Prepare(t.Context(), fingerprint, hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	f := &deletionFixture{binding: binding, store: store, credentials: credentials, manager: manager, secret: secret}
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		var err error
		f.owner, err = domain.InitializeIn(t.Context(), scope, change, joined)
		if err != nil {
			return err
		}
		f.actorRow, err = people.MemberObjects.Create(t.Context(), scope, people.NewMemberCreate("operator", "operator@example.test", secret, "KR").WithRoot(true))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	credential, present, err := credentials.CredentialByID(t.Context(), f.actorRow.Subject)
	if err != nil || !present {
		t.Fatal("deletion actor credential", err)
	}
	f.actor = credential.Principal()
	return f
}

func deletionMember(t *testing.T, backend db.Session, f *deletionFixture, name string, edit func(people.MemberCreate) people.MemberCreate) people.Member {
	t.Helper()
	input := people.NewMemberCreate(name, name+"@example.test", f.secret, "KR")
	if edit != nil {
		input = edit(input)
	}
	row, err := people.MemberObjects.Create(t.Context(), backend, input)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func deletionOwner(t *testing.T, backend Backend) *accountmodel.AccountDeleter[people.Member] {
	t.Helper()
	store, err := project.NewAccountSessionStore(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := project.NewAccountDeleter(store, auth.PrincipalAuthorizer{}, deletionConfig())
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func deletionCatalog(t *testing.T, store *accountmodel.SessionStore[people.Member]) *accountmodel.Catalog[access.Team, access.Grant] {
	t.Helper()
	config := accountmodel.CatalogConfig[access.Team, access.Grant]{
		Group:              accountmodel.CatalogModelConfig[access.Team]{ReadFields: []string{"title", "grants"}, WriteFields: []string{"title", "grants"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_group", Add: "catalog.add_group", Change: "catalog.change_group", Delete: "catalog.delete_group"}},
		Permission:         accountmodel.CatalogModelConfig[access.Grant]{ReadFields: []string{"key", "title"}, WriteFields: []string{"key", "title"}, Permissions: accountmodel.CatalogPermissions{View: "catalog.view_permission", Add: "catalog.add_permission", Change: "catalog.change_permission", Delete: "catalog.delete_permission"}},
		AccountPermissions: deletionConfig().Account, Now: func() time.Time { return joined },
	}
	catalog, err := project.NewAccountCatalog(store, auth.PrincipalAuthorizer{}, config)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func deletionSession(t *testing.T, manager *sessions.Manager, subject string) sessions.Record {
	t.Helper()
	payload := map[string]string{"preserve": "exact session bytes"}
	if subject != "" {
		payload[auth.SessionPrincipalIDKey] = subject
	}
	record, err := manager.Create(t.Context(), payload)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func deletionRows[M any](t *testing.T, scope db.Queryer, objects orm.Manager[M]) []M {
	t.Helper()
	metadata, err := objects.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	var key ir.Field
	for _, field := range metadata.Fields {
		if field.PrimaryKey {
			key = field
		}
	}
	rows, err := objects.Using(scope).OrderBy(orm.NewAutoField[M](key).Asc()).All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func deletionSnapshot(t *testing.T, backend Backend) map[string]any {
	t.Helper()
	var snapshot map[string]any
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		snapshot = map[string]any{
			"member": deletionRows(t, scope, people.MemberObjects), "team": deletionRows(t, scope, access.TeamObjects), "grant": deletionRows(t, scope, access.GrantObjects),
			"membership": deletionRows(t, scope, links.MembershipObjects), "grant_link": deletionRows(t, scope, links.GrantLinkObjects), "direct": deletionRows(t, scope, people.MemberGrantsLinkObjects),
			"ticket": deletionRows(t, scope, tickets.TicketObjects), "session": deletionRows(t, scope, sessionmodels.SessionObjects),
			"audit": deletionRows(t, scope, auditmodels.EventObjects), "domain": deletionRows(t, scope, domainmodels.TransitionObjects),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func deletionModelCount(snapshot map[string]any) int64 {
	var count int64
	for _, name := range []string{"member", "team", "grant", "membership", "grant_link", "direct", "ticket"} {
		count += int64(reflect.ValueOf(snapshot[name]).Len())
	}
	return count
}

func deletionHistory(t *testing.T, backend Backend, actor auth.Principal, model string, id int64) []accountaudit.Entry {
	t.Helper()
	namespace, _ := actor.CredentialDomain()
	var history []accountaudit.Entry
	if err := backend.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
		var err error
		history, err = accountaudit.HistoryIn(t.Context(), scope, namespace, model, id, 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return history
}

// A complete before/after snapshot is kept only by tests and never printed:
// failures must not disclose credential hashes or session payloads.
func requireDeletionUnchanged(t *testing.T, backend Backend, before map[string]any) {
	t.Helper()
	after := deletionSnapshot(t, backend)
	for name, rows := range before {
		if !reflect.DeepEqual(rows, after[name]) {
			t.Fatalf("failed deletion changed durable %s", name)
		}
	}
}
