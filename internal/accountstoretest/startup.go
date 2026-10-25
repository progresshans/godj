package accountstoretest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	accountaudit "github.com/progresshans/godj/account/audit"
	auditmodels "github.com/progresshans/godj/account/audit/models"
	"github.com/progresshans/godj/account/domain"
	domainmodels "github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

type startupOwner struct {
	Backend
	reads, histories, writes int
	read                     snapshotFunc
	history                  func(context.Context) ([]mb.AppliedMigration, error)
}

func (owner *startupOwner) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	owner.reads++
	if owner.read != nil {
		return owner.read(ctx, read)
	}
	return owner.Backend.ReadSnapshot(ctx, read)
}
func (owner *startupOwner) ReadAppliedMigrations(ctx context.Context) ([]mb.AppliedMigration, error) {
	owner.histories++
	if owner.history != nil {
		return owner.history(ctx)
	}
	return owner.Backend.(mb.AppliedMigrationReader).ReadAppliedMigrations(ctx)
}
func (owner *startupOwner) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	owner.writes++
	return errors.New("startup attempted a write scope")
}
func (owner *startupOwner) CoordinatedAtomicRelation(context.Context, func(db.RelationSession) error) error {
	owner.writes++
	return errors.New("startup attempted a relation write scope")
}

func startupStore(t *testing.T, owner sessiondb.Backend) *accountmodel.SessionStore[models.User] {
	t.Helper()
	store, err := project.NewAccountSessionStore(owner, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
func noStartupResult(t *testing.T, got accountmodel.StartupInspection, err error) {
	t.Helper()
	if err == nil || got.Ready() || got.Fresh() || got.Ownership().Sequence() != 0 || got.SessionRecords() != 0 || got.AuditEvents() != 0 || !reflect.DeepEqual(got, accountmodel.StartupInspection{}) {
		t.Fatal("failed startup inspection published state", err)
	}
}
func startupCounts(t *testing.T, backend Backend) []int64 {
	t.Helper()
	return append(firstAccountCounts(t, backend), accountSessionCount(t, backend))
}

func startupLifecycle(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	owner := &startupOwner{Backend: backend}
	store := startupStore(t, owner)
	if owner.reads+owner.histories+owner.writes != 0 {
		t.Fatal("startup construction did I/O")
	}
	// A session namespace belonging to another feature is legitimate even
	// before account provisioning, and inspection never prunes expired data.
	database, err := sessiondb.New(backend, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := database.Store("other-feature")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := sessions.NewManager(foreign, sessions.Config{Clock: func() time.Time { return joined.Add(-48 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Create(t.Context(), map[string]string{"retained": "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	before := startupCounts(t, backend)
	fresh, err := store.InspectStartup(t.Context())
	if err != nil || !fresh.Fresh() || fresh.Ready() || fresh.Ownership().Sequence() != 0 || fresh.SessionRecords() != 1 || fresh.AuditEvents() != 0 || owner.reads != 1 || owner.histories != 1 || owner.writes != 0 {
		t.Fatal("fresh startup did not inspect the exact read-only store", err)
	}
	if !reflect.DeepEqual(before, startupCounts(t, reopen(t))) {
		t.Fatal("fresh inspection changed durable state")
	}
	accountSessionRecord(t, foreign, record, true)
	first := firstAccountOwner(t, backend, firstAccountHasher(t), firstAccountConfig())
	pending, err := first.Prepare(t.Context(), creationInput(t, "startup-admin"), password)
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.Commit(t.Context(), pending)
	if err != nil {
		t.Fatal(err)
	}
	before = startupCounts(t, backend)
	reopened := startupStore(t, reopen(t))
	ready, err := reopened.InspectStartup(t.Context())
	if err != nil || !ready.Ready() || ready.Fresh() || ready.Ownership().OperationID() != created.Ownership.OperationID() || ready.AuditEvents() != 1 || ready.SessionRecords() != 1 {
		t.Fatal("reopened startup lost current ownership/history", err)
	}
	if !reflect.DeepEqual(before, startupCounts(t, reopen(t))) {
		t.Fatal("ready inspection changed durable state")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", ready, ready), created.Ownership.OperationID()) {
		t.Fatal("startup diagnostic exposed metadata")
	}
	// Removing the last user does not make a previously initialized domain
	// fresh. Its immutable history and audit remain valid historical records.
	row := profileRow(t, backend, created.Account.ID)
	if count, err := models.UserObjects.Delete(t.Context(), backend, &row); err != nil || count != 1 {
		t.Fatal(err)
	}
	ready, err = reopened.InspectStartup(t.Context())
	if err != nil || !ready.Ready() || ready.Fresh() || ready.AuditEvents() != 1 {
		t.Fatal("empty initialized domain became fresh", err)
	}
	if _, err := first.Prepare(t.Context(), creationInput(t, "replacement"), password); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) {
		t.Fatal("inspection allowed implicit rebootstrap", err)
	}
}

func startupNonfresh(t *testing.T, backend Backend, _ func(*testing.T) Backend, storedKey StoredKeyFixture) {
	store := startupStore(t, backend)
	for _, key := range []int64{0, -13} {
		input := models.NewUserCreate(fmt.Sprintf("orphan-%d", key), fmt.Sprintf("orphan-%d", key), "!unusable", joined)
		row, err := models.UserObjects.Create(t.Context(), backend, input)
		if err != nil {
			t.Fatal(err)
		}
		storedKey(t, backend, "user", row.ID, key)
		got, err := store.InspectStartup(t.Context())
		noStartupResult(t, got, err)
		if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) {
			t.Fatal("orphan account key was skipped", err)
		}
		row = profileRow(t, backend, key)
		if count, err := models.UserObjects.Delete(t.Context(), backend, &row); err != nil || count != 1 {
			t.Fatal(err)
		}
	}
	// Audit without ownership cannot be silently reclassified as a new DB.
	if _, err := auditmodels.EventObjects.Create(t.Context(), backend, auditmodels.NewEventCreate(strings.Repeat("a", 64), "historical-actor", "people.member", -13, "change", "[]", joined)); err != nil {
		t.Fatal(err)
	}
	got, err := store.InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeNotFresh}) {
		t.Fatal("orphan audit was ignored", err)
	}
}

func startupMigrations(t *testing.T, backend Backend, _ func(*testing.T) Backend) {
	baseline, err := backend.(mb.AppliedMigrationReader).ReadAppliedMigrations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing_domain", "missing_audit", "missing_sessions", "unknown_owned", "duplicate_owned", "read_failure", "cancel", "foreign_app"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private startup history failure")
			owner := &startupOwner{Backend: backend}
			owner.history = func(context.Context) ([]mb.AppliedMigration, error) {
				rows := append([]mb.AppliedMigration(nil), baseline...)
				missing := map[string]string{"missing_domain": "godj_account_domain", "missing_audit": "godj_account_audit", "missing_sessions": "godj_sessions"}[mode]
				for i, row := range rows {
					if row.App == missing {
						rows = append(rows[:i], rows[i+1:]...)
						break
					}
				}
				switch mode {
				case "unknown_owned":
					rows = append(rows, mb.AppliedMigration{App: "godj_account_domain", Name: "future"})
				case "duplicate_owned":
					rows = append(rows, mb.AppliedMigration{App: "godj_account_domain", Name: "0001_initial"})
				case "foreign_app":
					rows = append(rows, mb.AppliedMigration{App: "unrelated", Name: "future"})
				case "read_failure":
					return rows, fault
				case "cancel":
					cancel()
				}
				return rows, nil
			}
			got, err := startupStore(t, owner).InspectStartup(ctx)
			if mode == "foreign_app" {
				if err != nil || !got.Fresh() || owner.reads != 1 {
					t.Fatal("foreign app migration was rejected", err)
				}
			} else {
				noStartupResult(t, got, err)
				if owner.reads != 0 {
					t.Fatal("failed migration preflight performed data I/O")
				}
				if mode == "read_failure" && !errors.Is(err, fault) || mode == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("preflight cause lost", err)
				}
				if mode != "read_failure" && mode != "cancel" && !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeSchemaUnavailable}) {
					t.Fatal("missing migration not classified", err)
				}
			}
			if owner.writes != 0 || owner.histories != 1 {
				t.Fatal("startup retried or wrote migrations")
			}
		})
	}
}

func runStartupSchemaRequired(t *testing.T, open Open) {
	t.Run("startup_schema_required", func(t *testing.T) {
		for _, mode := range []string{"empty", "framework_only", "project_only"} {
			t.Run(mode, func(t *testing.T) {
				backend, _ := open(t)
				var sources []definition.Source
				if mode == "framework_only" {
					sources = append(domain.MigrationSources(), accountaudit.MigrationSources()...)
					sources = append(sources, sessiondb.MigrationSources()...)
				}
				if mode == "project_only" {
					sources = identity.MigrationSources()
				}
				if len(sources) > 0 {
					loaded, _, err := definition.Load(sources...)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
						t.Fatal(err)
					}
				}
				owner := &startupOwner{Backend: backend}
				got, err := startupStore(t, owner).InspectStartup(t.Context())
				noStartupResult(t, got, err)
				if owner.writes != 0 {
					t.Fatal("startup installed schema")
				}
				if mode == "framework_only" && owner.reads != 1 || mode != "framework_only" && owner.reads != 0 {
					t.Fatal("schema check did not follow migration preflight")
				}
			})
		}
	})
}

func startupCurrentDomain(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	store := startupStore(t, backend)
	ready, err := store.InspectStartup(t.Context())
	if err != nil || !ready.Ready() {
		t.Fatal(err)
	}
	role, err := f.binding.Role()
	if err != nil {
		t.Fatal(err)
	}
	role.AuthenticateNormalization = ir.AccountUnicode16NFKC
	binding, err := accountmodel.Bind(role, models.UserDescriptor{}, models.GoDjRelationSchema())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	change, err := domain.Prepare(t.Context(), fingerprint, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := reopen(t).CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		_, err := domain.TransitionIn(t.Context(), scope, f.ownership, change, joined)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if !errors.Is(err, domain.ErrMismatch) {
		t.Fatal("startup cached an old ownership receipt", err)
	}
	if !ready.Ready() || ready.Ownership().Sequence() != 1 {
		t.Fatal("detached observation was mutated")
	}
}

func startupInventory(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	binding, err := project.BindAccount()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	var first, last domainmodels.Transition
	var event auditmodels.Event
	// More than one page; all domains and deleted actors/targets are valid
	// historical data. Startup must inspect a later corrupt row too.
	if err := backend.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		for index := int64(1); index <= 257; index++ {
			input := domainmodels.NewTransitionCreate(fmt.Sprintf("%064x", index), index, fingerprint, fmt.Sprintf("%064x", index+1000), strings.Repeat("e", 64), joined)
			if index > 1 {
				input = input.WithPreviousID(last.ID)
			}
			var err error
			last, err = domainmodels.TransitionObjects.Create(t.Context(), scope, input)
			if err != nil {
				return err
			}
			if index == 1 {
				first = last
			}
			event, err = auditmodels.EventObjects.Create(t.Context(), scope, auditmodels.NewEventCreate(fmt.Sprintf("%064x", index+2000), "deleted-actor", "people.member", -index, "change", "[]", joined))
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	store := startupStore(t, reopen(t))
	ready, err := store.InspectStartup(t.Context())
	if err != nil || !ready.Ready() || ready.Ownership().Sequence() != 257 || ready.AuditEvents() != 257 {
		t.Fatal("startup omitted historical pages", err)
	}
	// Current-domain reads need only the latest links; startup checks all of
	// the chain, including an old entry which no current lookup would touch.
	if _, err := domainmodels.TransitionObjects.Patch(t.Context(), backend, first, domainmodels.TransitionPatch{}.WithEvidenceSHA256("invalid")); err != nil {
		t.Fatal(err)
	}
	got, err := store.InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if !errors.Is(err, domain.ErrCorrupt) {
		t.Fatal("startup skipped old corrupt history", err)
	}
	if _, err := domainmodels.TransitionObjects.Patch(t.Context(), backend, first, domainmodels.TransitionPatch{}.WithEvidenceSHA256(first.EvidenceSHA256)); err != nil {
		t.Fatal(err)
	}
	if _, err := auditmodels.EventObjects.Patch(t.Context(), backend, event, auditmodels.EventPatch{}.WithChangedFields("null")); err != nil {
		t.Fatal(err)
	}
	got, err = store.InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if !errors.Is(err, accountaudit.ErrCorrupt) {
		t.Fatal("startup skipped late corrupt audit", err)
	}
	if _, err := auditmodels.EventObjects.Patch(t.Context(), backend, event, auditmodels.EventPatch{}.WithChangedFields(event.ChangedFields)); err != nil {
		t.Fatal(err)
	}
	if _, err := sessionmodels.SessionObjects.Create(t.Context(), backend, sessionmodels.NewSessionCreate("foreign-namespace", strings.Repeat("b", 64), "bad-payload")); err != nil {
		t.Fatal(err)
	}
	before := startupCounts(t, backend)
	got, err = store.InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if !reflect.DeepEqual(before, startupCounts(t, backend)) {
		t.Fatal("startup repaired or deleted corrupt state")
	}
}
