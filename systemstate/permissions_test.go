package systemstate

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/query"
)

func permissionFixture(t *testing.T) (context.Context, *sqlite.Backend, CredentialPolicy, *Runtime, string) {
	t.Helper()
	ctx := context.Background()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "permissions.sqlite3")) + "?mode=rwc&_busy_timeout=5000"
	backend := openSessionStoreBackend(t, ctx, dsn)
	t.Cleanup(func() { _ = backend.Close() })
	explicitlyMigrateSystemState(t, ctx, backend)
	policy := operatorTestPolicy(t, newOperatorHasherSpy(t, 10_000), "operator", true)
	if err := ProvisionOperator(ctx, backend, ProvisionOperatorConfig{Username: "admin", Password: "permission-change-password", CredentialPolicy: policy}); err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenExisting(ctx, backend, operatorRuntimeConfig(policy))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, backend, policy, runtime, dsn
}

func permissionPolicy(t *testing.T, before CredentialPolicy, permissions []auth.Permission) CredentialPolicy {
	t.Helper()
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: before.Principal.ID(), Active: before.Principal.Active(), Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	return CredentialPolicy{Principal: principal, PasswordHasher: before.PasswordHasher}
}

func TestPermissionMaintenancePreservesCredentialAndRevokesSessionsAndStaleRuntimes(t *testing.T) {
	ctx, database, before, stale, dsn := permissionFixture(t)
	secondBackend := openSessionStoreBackend(t, ctx, dsn)
	t.Cleanup(func() { _ = secondBackend.Close() })
	second, err := OpenExisting(ctx, secondBackend, operatorRuntimeConfig(before))
	if err != nil {
		t.Fatal(err)
	}
	id := mustCodecSessionID(t, "A")
	now := time.Now().UTC()
	record := mustSessionStoreRecord(t, id, map[string]string{"principal_id": "operator"}, now, now, now.Add(time.Hour), now.Add(time.Minute))
	if created, err := stale.SessionStore().Create(ctx, record); err != nil || !created {
		t.Fatalf("session setup: %v", err)
	}
	original, err := readCredentialRows(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	nextPermissions := []auth.Permission{"admin.site.access", "helpdesk.ticket.view", "helpdesk.ticket.change"}
	after := permissionPolicy(t, before, nextPermissions)
	observed := &observedRuntimeBackend{Backend: database}
	if err := UpdateOperatorPermissions(ctx, observed, before, nextPermissions); err != nil {
		t.Fatal(err)
	}
	if observed.atomicCalls.Load() != 1 || observed.updateCalls.Load() != 1 || observed.deleteCalls.Load() != 1 || observed.insertCalls.Load() != 0 {
		t.Fatalf("maintenance must use one update/revocation transaction: %+v", observed)
	}
	updated, err := readCredentialRows(ctx, database)
	if err != nil || len(updated) != 1 {
		t.Fatalf("credential: %v", err)
	}
	updated[0].permissions = original[0].permissions
	if updated[0] != original[0] {
		t.Fatal("maintenance changed credential identity, password, or schema binding")
	}
	if _, found, err := stale.SessionStore().Load(ctx, id); err != nil || found {
		t.Fatalf("old session survived: %v %v", found, err)
	}
	for _, runtime := range []*Runtime{stale, second} {
		if principal, err := runtime.Authenticator().Resolve(ctx, "operator"); principal.Authenticated() || !errors.Is(err, &Error{Code: CodeCredentialPolicyMismatch}) {
			t.Fatalf("stale resolver: %v %v", principal, err)
		}
		if principal, err := runtime.Authenticator().Authenticate(ctx, "admin", "permission-change-password"); principal.Authenticated() || !errors.Is(err, &Error{Code: CodeCredentialPolicyMismatch}) {
			t.Fatalf("stale login: %v %v", principal, err)
		}
	}
	fresh, err := OpenExisting(ctx, secondBackend, operatorRuntimeConfig(after))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := fresh.Authenticator().Authenticate(ctx, "admin", "permission-change-password")
	if err != nil || !principal.Has("helpdesk.ticket.change") || principal.Has("article.article.delete") {
		t.Fatalf("new permissions: %v %v", principal, err)
	}
	observed.resetObservation()
	if err := UpdateOperatorPermissions(ctx, observed, before, []auth.Permission{"unexpected.grant"}); !errors.Is(err, &Error{Code: CodeCredentialPolicyMismatch}) {
		t.Fatalf("stale CAS accepted: %v", err)
	}
	if observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatal("stale expected policy mutated durable state")
	}
	if created, err := fresh.SessionStore().Create(ctx, record); err != nil || !created {
		t.Fatalf("new session: %v", err)
	}
	observed.resetObservation()
	if err := UpdateOperatorPermissions(ctx, observed, after, nextPermissions); err != nil {
		t.Fatal(err)
	}
	if observed.updateCalls.Load() != 0 || observed.deleteCalls.Load() != 0 {
		t.Fatal("unchanged policy unnecessarily revoked sessions")
	}
	if _, found, err := fresh.SessionStore().Load(ctx, id); err != nil || !found {
		t.Fatal("no-op removed session")
	}
}

func TestPermissionMaintenanceRollbackAndUnknownCommitNeverRetry(t *testing.T) {
	for _, mode := range []string{"delete failure", "unknown commit"} {
		t.Run(mode, func(t *testing.T) {
			ctx, database, before, old, _ := permissionFixture(t)
			id := mustCodecSessionID(t, "A")
			now := time.Now().UTC()
			record := mustSessionStoreRecord(t, id, map[string]string{"principal_id": "operator"}, now, now, now.Add(time.Hour), now.Add(time.Minute))
			if _, err := old.SessionStore().Create(ctx, record); err != nil {
				t.Fatal(err)
			}
			fault := &permissionFaultBackend{Backend: database, mode: mode}
			permissions := []auth.Permission{"helpdesk.ticket.view"}
			err := UpdateOperatorPermissions(ctx, fault, before, permissions)
			if err == nil || fault.calls != 1 || strings.Contains(err.Error(), "sensitive-database-marker") {
				t.Fatalf("maintenance fault: calls=%d err=%v", fault.calls, err)
			}
			if mode == "delete failure" {
				if _, err := OpenExisting(ctx, database, operatorRuntimeConfig(before)); err != nil {
					t.Fatalf("permission update survived rollback: %v", err)
				}
				if _, found, err := old.SessionStore().Load(ctx, id); err != nil || !found {
					t.Fatal("session revocation survived rollback")
				}
			} else {
				var outcome *query.Error
				if !errors.As(err, &outcome) || outcome.Code != query.CodeCommitOutcomeUnknown {
					t.Fatalf("unknown commit marker lost: %v", err)
				}
				if _, err := OpenExisting(ctx, database, operatorRuntimeConfig(permissionPolicy(t, before, permissions))); err != nil {
					t.Fatalf("durable reconciliation: %v", err)
				}
				if _, found, err := old.SessionStore().Load(ctx, id); err != nil || found {
					t.Fatal("committed revocation missing")
				}
			}
		})
	}
}

func TestConcurrentPermissionMaintenanceHasOneExpectedPolicyWinner(t *testing.T) {
	ctx, first, before, _, dsn := permissionFixture(t)
	second := openSessionStoreBackend(t, ctx, dsn)
	t.Cleanup(func() { _ = second.Close() })
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, backend := range []*sqlite.Backend{first, second} {
		wait.Add(1)
		go func(index int, backend *sqlite.Backend) {
			defer wait.Done()
			<-start
			permissions := []auth.Permission{"helpdesk.ticket.view"}
			if index == 1 {
				permissions = []auth.Permission{"helpdesk.category.view"}
			}
			results <- UpdateOperatorPermissions(ctx, backend, before, permissions)
		}(index, backend)
	}
	close(start)
	wait.Wait()
	close(results)
	wins, stale := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, &Error{Code: CodeCredentialPolicyMismatch}) {
			stale++
		} else {
			t.Fatalf("unexpected concurrent failure: %v", err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("winners=%d stale=%d", wins, stale)
	}
}

type permissionFaultBackend struct {
	*sqlite.Backend
	mode  string
	calls int
}

func (b *permissionFaultBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	b.calls++
	err := b.Backend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if b.mode == "delete failure" {
			return callback(permissionDeleteFailure{Session: session})
		}
		return callback(session)
	})
	if err == nil && b.mode == "unknown commit" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "sensitive-database-marker"}
	}
	return err
}

type permissionDeleteFailure struct{ db.Session }

func (s permissionDeleteFailure) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	_, err := s.Session.Delete(ctx, plan)
	if err != nil {
		return 0, err
	}
	return 0, errors.New("sensitive-database-marker")
}
