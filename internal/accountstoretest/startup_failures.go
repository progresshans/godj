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
	"github.com/progresshans/godj/account/domain"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	sessiondb "github.com/progresshans/godj/sessions/database"
)

func startupReadFailures(t *testing.T, native Backend, _ func(*testing.T) Backend) {
	provision(t, native, ir.AccountExact)
	before := startupCounts(t, native)
	for _, mode := range []string{"skipped", "nil", "typed_nil", "root", "repeated", "swallowed", "cleanup", "cancel", "expired", "pre_cancel", "nil_context"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := errors.New("private startup snapshot failure")
			owner := &startupOwner{Backend: native}
			owner.read = func(ctx context.Context, observe func(db.Queryer) error) error {
				switch mode {
				case "skipped":
					return nil
				case "nil":
					return observe(nil)
				case "typed_nil":
					return observe((*profileReadProbe)(nil))
				case "root":
					return observe(native)
				}
				var retained db.Queryer
				err := native.ReadSnapshot(ctx, func(scope db.Queryer) error {
					retained = scope
					if mode == "expired" {
						return nil
					}
					probe := &profileReadProbe{Queryer: scope, validator: scope.(db.SessionValidator)}
					if mode == "swallowed" {
						probe.fault = fault
					}
					err := observe(probe)
					if mode == "swallowed" {
						return nil
					}
					if mode == "repeated" {
						return errors.Join(err, observe(probe))
					}
					return err
				})
				if mode == "expired" && err == nil {
					return observe(retained)
				}
				if mode == "cleanup" {
					return errors.Join(err, fault)
				}
				if mode == "cancel" {
					cancel()
				}
				return err
			}
			var work context.Context = ctx
			if mode == "pre_cancel" {
				cancel()
			}
			if mode == "nil_context" {
				work = nil
			}
			got, err := startupStore(t, owner).InspectStartup(work)
			noStartupResult(t, got, err)
			if mode == "cleanup" || mode == "swallowed" {
				if !errors.Is(err, fault) {
					t.Fatal("snapshot failure cause lost", err)
				}
			}
			if mode == "cancel" || mode == "pre_cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("snapshot cancellation lost", err)
				}
			}
			if strings.Contains(fmt.Sprintf("%+v", err), fault.Error()) {
				t.Fatal("startup diagnostics exposed private failure")
			}
			want := 1
			if mode == "pre_cancel" || mode == "nil_context" {
				want = 0
			}
			if owner.histories != want || owner.reads != want || owner.writes != 0 {
				t.Fatal("inspection retried, wrote or entered invalid input")
			}
			if !reflect.DeepEqual(before, startupCounts(t, native)) {
				t.Fatal("failed startup changed state")
			}
		})
	}
	// Exact table/projection failure coverage: even empty catalogs and link
	// tables must be queried; none can be hidden by an empty account shortcut.
	for _, stage := range []struct {
		name, table string
		skip        int
	}{
		{"domain_chain", "godj_account_domain_transition", 0},
		{"domain_operations", "godj_account_domain_transition", 1},
		{"domain_generations", "godj_account_domain_transition", 2},
		{"account", "godj_identity_user", 0}, {"group", "godj_identity_group", 0}, {"permission", "godj_identity_permission", 0},
		{"account_groups", "godj_identity_user_groups", 0}, {"account_permissions", "godj_identity_user_permissions", 0}, {"group_permissions", "godj_identity_group_permissions", 0},
		{"audit", "godj_account_audit_event", 0}, {"session_inventory", "godj_sessions_session", 0}, {"session_payload", "godj_sessions_session", 1},
	} {
		t.Run(stage.name, func(t *testing.T) {
			for _, mode := range []string{"query", "close"} {
				t.Run(mode, func(t *testing.T) {
					fault := errors.New("private stage read failure")
					owner := &startupOwner{Backend: native}
					var probe *catalogReadProbe
					owner.read = func(ctx context.Context, observe func(db.Queryer) error) error {
						return native.ReadSnapshot(ctx, func(scope db.Queryer) error {
							probe = &catalogReadProbe{Queryer: scope, validator: scope.(db.SessionValidator), table: stage.table, skip: stage.skip, fault: fault, closeFail: mode == "close"}
							return observe(probe)
						})
					}
					got, err := startupStore(t, owner).InspectStartup(t.Context())
					noStartupResult(t, got, err)
					if !errors.Is(err, fault) || probe == nil || probe.matches != stage.skip+1 {
						t.Fatal("startup omitted required read/close", err)
					}
					if owner.histories != 1 || owner.reads != 1 || owner.writes != 0 {
						t.Fatal("startup scope ownership changed")
					}
				})
			}
		})
	}
}

type inspectionFailureReader struct {
	db.Queryer
	mode   string
	fault  error
	closed *int
}

func (reader *inspectionFailureReader) ValidateSession(ctx context.Context) error {
	return reader.Queryer.(db.SessionValidator).ValidateSession(ctx)
}
func (reader *inspectionFailureReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	rows, err := reader.Queryer.Query(ctx, plan)
	if err != nil || rows == nil {
		return rows, err
	}
	return &inspectionFailureRows{Rows: rows, mode: reader.mode, fault: reader.fault, closed: reader.closed}, nil
}

type inspectionFailureRows struct {
	db.Rows
	mode   string
	fault  error
	closed *int
}

func (rows *inspectionFailureRows) Scan(dest ...any) error {
	if rows.mode == "scan" {
		return rows.fault
	}
	return rows.Rows.Scan(dest...)
}
func (rows *inspectionFailureRows) Err() error {
	if rows.mode == "iteration" {
		return errors.Join(rows.Rows.Err(), rows.fault)
	}
	return rows.Rows.Err()
}
func (rows *inspectionFailureRows) Close() error { *rows.closed++; return rows.Rows.Close() }

func startupPublicFailures(t *testing.T, native Backend, _ func(*testing.T) Backend) {
	f := provision(t, native, ir.AccountExact)
	credential, err := f.binding.PrepareCredential(f.ownership.Generation(), f.user, nil)
	if err != nil {
		t.Fatal(err)
	}
	event, err := accountaudit.Prepare(credential.Principal(), "godj_identity.user", f.user.ID, admin.ActionChange, []string{"first_name"}, joined)
	if err != nil {
		t.Fatal(err)
	}
	if err := native.CoordinatedAtomic(t.Context(), func(scope db.Session) error { _, err := accountaudit.AppendIn(t.Context(), scope, event); return err }); err != nil {
		t.Fatal(err)
	}
	now := joined
	_, manager, _ := accountSessions(t, native, &now)
	if _, err := manager.Create(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	database, err := sessiondb.New(native, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"domain", "audit", "sessions"} {
		t.Run(operation, func(t *testing.T) {
			for _, mode := range []string{"scan", "iteration"} {
				t.Run(mode, func(t *testing.T) {
					fault := errors.New("private database conversion value")
					closed := 0
					err := native.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
						reader := &inspectionFailureReader{Queryer: scope, mode: mode, fault: fault, closed: &closed}
						switch operation {
						case "domain":
							got, err := domain.InspectIn(t.Context(), reader)
							if got.Sequence() != 0 {
								t.Fatal("failed domain inspection published receipt")
							}
							return err
						case "audit":
							got, err := accountaudit.InspectIn(t.Context(), reader)
							if got != 0 {
								t.Fatal("failed audit inspection published count")
							}
							return err
						default:
							got, err := database.InspectIn(t.Context(), reader)
							if got != 0 {
								t.Fatal("failed session inspection published count")
							}
							return err
						}
					})
					if !errors.Is(err, fault) || closed != 1 {
						t.Fatal("inspection lost private cause or row cleanup", closed, err)
					}
					if strings.Contains(fmt.Sprintf("%v %+v %#v", err, err, err), fault.Error()) {
						t.Fatal("public inspector exposed private database detail")
					}
				})
			}
		})
	}
}

func startupSnapshot(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provision(t, backend, ir.AccountExact)
	owner := &sessionSnapshotOwner{Backend: backend, observed: make(chan struct{}), release: make(chan struct{})}
	store := startupStore(t, &startupOwner{Backend: backend, read: owner.ReadSnapshot})
	type observation struct {
		value accountmodel.StartupInspection
		err   error
	}
	finished := make(chan observation, 1)
	go func() { value, err := store.InspectStartup(t.Context()); finished <- observation{value, err} }()
	released := false
	defer func() {
		if !released {
			close(owner.release)
		}
	}()
	select {
	case <-owner.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not observe ownership")
	}
	writer := reopen(t)
	fingerprint, err := f.binding.RoleFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	change, err := domain.Prepare(t.Context(), fingerprint, strings.Repeat("e", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
		if _, err := domain.TransitionIn(t.Context(), scope, f.ownership, change, joined); err != nil {
			return err
		}
		credential, err := f.binding.PrepareCredential(change.Generation(), f.user, nil)
		if err != nil {
			return err
		}
		event, err := accountaudit.Prepare(credential.Principal(), "godj_identity.user", f.user.ID, admin.ActionChange, []string{"first_name"}, joined)
		if err != nil {
			return err
		}
		_, err = accountaudit.AppendIn(t.Context(), scope, event)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	now := joined
	_, manager, _ := accountSessions(t, writer, &now)
	if _, err := manager.Create(t.Context(), map[string]string{"generation": "new"}); err != nil {
		t.Fatal(err)
	}
	close(owner.release)
	released = true
	select {
	case observed := <-finished:
		if observed.err != nil || !observed.value.Ready() || observed.value.Ownership().Sequence() != 1 || observed.value.AuditEvents() != 0 || observed.value.SessionRecords() != 0 {
			t.Fatal("startup mixed snapshots", observed.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup snapshot did not finish")
	}
	current, err := startupStore(t, reopen(t)).InspectStartup(t.Context())
	if err != nil || !current.Ready() || current.Ownership().Sequence() != 2 || current.AuditEvents() != 1 || current.SessionRecords() != 1 {
		t.Fatal("startup reused cached receipt/counts", err)
	}
}

// This narrower native facade intentionally does not advertise migration
// history. Startup must reject it before opening a data snapshot.
type startupNoHistory struct {
	db.SnapshotReader
	writer db.CoordinatedAtomic
}

func (owner *startupNoHistory) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	return owner.writer.CoordinatedAtomic(ctx, write)
}

func startupHistoryCapability(t *testing.T, native Backend, _ func(*testing.T) Backend) {
	owner := &startupOwner{Backend: native}
	narrow := &startupNoHistory{SnapshotReader: owner, writer: owner}
	store, err := project.NewAccountSessionStore(narrow, sessiondb.Config{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeInvalidConfig}) || owner.reads+owner.histories+owner.writes != 0 {
		t.Fatal("startup accepted missing history capability", err)
	}
	// History cancellation/failure is already covered above; a nil transport
	// row set is an incomplete inventory, not implicitly installed storage.
	owner.history = func(context.Context) ([]backend.AppliedMigration, error) { return nil, nil }
	got, err = startupStore(t, owner).InspectStartup(t.Context())
	noStartupResult(t, got, err)
	if owner.reads != 0 || !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeSchemaUnavailable}) {
		t.Fatal("empty history passed", err)
	}
}
