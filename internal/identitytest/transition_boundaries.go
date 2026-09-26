package identitytest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

// RunTransitionBoundaries owns fresh databases per case and uses two native
// connections where concurrency matters. Faults wrap real transactions, so a
// rejected response alone cannot stand in for rollback or durable ownership.
func RunTransitionBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	for _, table := range []string{"godj_identity_user", "godj_identity_permission", "godj_identity_user_permissions", "godj_system_identity_transition"} {
		t.Run("rollback_at_"+table, func(t *testing.T) {
			backend, _ := open(t)
			config := transitionLegacyFixture(t, backend)
			failure := errors.New("private transition insert failure")
			fault := &boundaryBackend{TransitionBackend: backend, insertTable: table, failure: failure}
			receipt, err := systemstate.AdoptOperator(t.Context(), fault, config)
			if err == nil || receipt.UserID() != 0 || fault.calls != 1 || fault.faults != 1 {
				t.Fatal("injected write failure was not reached or was published/retried", err)
			}
			assertTransitionCounts(t, backend, 0, 0)
			assertLegacyCredentialUsable(t, backend, config)
		})
	}
	for _, collision := range []string{"principal", "username"} {
		t.Run("collision_"+collision, func(t *testing.T) {
			backend, _ := open(t)
			config := transitionLegacyFixture(t, backend)
			id, username := "other-id", "other-name"
			if collision == "principal" {
				id = config.Expected.CredentialPolicy.Principal.ID()
			} else {
				username = "legacy"
			}
			existing, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate(id, username, "unusable", time.Now().UTC()))
			if err != nil {
				t.Fatal(err)
			}
			if receipt, err := systemstate.AdoptOperator(t.Context(), backend, config); err == nil || receipt.UserID() != 0 {
				t.Fatal("colliding identity was overwritten/adopted", err)
			}
			assertTransitionCounts(t, backend, 1, 0)
			stored, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(existing.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
			if err != nil || !found || stored.PrincipalID != id || stored.Username != username || stored.EncodedPassword != "unusable" {
				t.Fatal("collision changed target", err)
			}
			assertLegacyCredentialUsable(t, backend, config)
		})
	}
	for _, bootstrap := range []bool{false, true} {
		name := "adoption"
		if bootstrap {
			name = "bootstrap"
		}
		t.Run(name+"_competing_owners", func(t *testing.T) {
			first, second := open(t)
			adoption, initial := transitionFixture(t, first, bootstrap)
			start := make(chan struct{})
			results := make(chan error, 2)
			backends := []*boundaryBackend{{TransitionBackend: first}, {TransitionBackend: second}}
			for _, backend := range backends {
				go func() {
					<-start
					_, err := transitionAttempt(t.Context(), backend, bootstrap, adoption, initial)
					results <- err
				}()
			}
			close(start)
			success, rejected := 0, 0
			for range backends {
				err := <-results
				if err == nil {
					success++
				} else if errors.Is(err, &systemstate.Error{Code: systemstate.CodeIdentityAlreadyInitialized}) {
					rejected++
				} else {
					t.Fatal("competing owner failed outside the ownership fence", err)
				}
			}
			if success != 1 || rejected != 1 || backends[0].calls > 1 || backends[1].calls > 1 {
				t.Fatal("multiple owners or automatic retries", success, rejected)
			}
			assertTransitionCounts(t, first, 1, 1)
		})
		for _, outcome := range []string{"unknown_commit", "late_cancel", "request_cancel", "callback_cancel"} {
			t.Run(name+"_"+outcome, func(t *testing.T) {
				backend, _ := open(t)
				adoption, initial := transitionFixture(t, backend, bootstrap)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				fault := &boundaryBackend{TransitionBackend: backend}
				switch outcome {
				case "unknown_commit":
					fault.unknown = true
				case "late_cancel":
					fault.afterCommit = cancel
				case "request_cancel":
					fault.beforeCommit = cancel
				case "callback_cancel":
					fault.callbackCancel = true
				}
				receipt, err := transitionAttempt(ctx, fault, bootstrap, adoption, initial)
				if fault.calls != 1 {
					t.Fatal("attempt retried or skipped", fault.calls)
				}
				if outcome == "request_cancel" || outcome == "callback_cancel" {
					unknownRollback := errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})
					if (!errors.Is(err, context.Canceled) && !(outcome == "request_cancel" && unknownRollback)) || receipt.UserID() != 0 {
						t.Fatal("canceled write published", err)
					}
					assertTransitionCounts(t, backend, 0, 0)
					return
				}
				if outcome == "unknown_commit" {
					if !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) || receipt.UserID() != 0 {
						t.Fatal("unknown write reported success", err)
					}
				} else if err != nil || receipt.UserID() == 0 || ctx.Err() == nil {
					t.Fatal("confirmed commit lost to late cancellation", err)
				}
				assertTransitionCounts(t, backend, 1, 1)
				if durable, err := systemstate.InspectIdentityTransition(t.Context(), backend); err != nil || durable.UserID() == 0 {
					t.Fatal("durable outcome cannot be reconciled", err)
				}
			})
		}
	}
	t.Run("read_scope_failures_never_publish", func(t *testing.T) {
		backend, _ := open(t)
		_, initial := transitionFixture(t, backend, true)
		for _, mode := range []string{"end_failure", "missing_callback", "duplicate_callback", "nil_reader", "swallowed_callback_failure"} {
			t.Run(mode, func(t *testing.T) {
				fault := &boundaryBackend{TransitionBackend: backend, readMode: mode}
				if receipt, err := systemstate.ProvisionIdentity(t.Context(), fault, initial); err == nil || receipt.UserID() != 0 || fault.calls != 0 {
					t.Fatal("failed preflight hashed/wrote/published", err)
				}
			})
		}
		if _, err := systemstate.ProvisionIdentity(t.Context(), backend, initial); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"end_failure", "missing_callback", "duplicate_callback", "nil_reader", "swallowed_callback_failure"} {
			fault := &boundaryBackend{TransitionBackend: backend, readMode: mode}
			if runtime, err := systemstate.OpenIdentity(t.Context(), fault, systemstate.IdentityRuntimeConfig{PasswordHasher: initial.PasswordHasher}); err == nil || runtime != nil {
				t.Fatal("failed scope published runtime", mode, err)
			}
			if receipt, err := systemstate.InspectIdentityTransition(t.Context(), fault); err == nil || receipt.UserID() != 0 {
				t.Fatal("failed scope published receipt", mode, err)
			}
		}
	})
}

func transitionLegacyFixture(t *testing.T, backend TransitionBackend) systemstate.AdoptOperatorConfig {
	t.Helper()
	applyIdentitySources(t, backend, systemstate.InitialDefinitionSource())
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "legacy-id", Active: true, Permissions: []auth.Permission{"identity.user.view"}})
	if err != nil {
		t.Fatal(err)
	}
	policy := systemstate.CredentialPolicy{Principal: principal, PasswordHasher: hasher}
	if err := systemstate.ProvisionOperator(t.Context(), backend, systemstate.ProvisionOperatorConfig{Username: "legacy", Password: "boundary password", CredentialPolicy: policy}); err != nil {
		t.Fatal(err)
	}
	applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
	return systemstate.AdoptOperatorConfig{Expected: systemstate.RuntimeConfig{CredentialPolicy: policy}, Staff: true}
}

func transitionFixture(t *testing.T, backend TransitionBackend, bootstrap bool) (systemstate.AdoptOperatorConfig, systemstate.ProvisionIdentityConfig) {
	t.Helper()
	if !bootstrap {
		return transitionLegacyFixture(t, backend), systemstate.ProvisionIdentityConfig{}
	}
	applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "fresh-id", Active: true, Staff: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	return systemstate.AdoptOperatorConfig{}, systemstate.ProvisionIdentityConfig{Principal: principal, Username: "fresh", Password: "boundary password", PasswordHasher: hasher}
}

func transitionAttempt(ctx context.Context, backend systemstate.IdentityBackend, bootstrap bool, adoption systemstate.AdoptOperatorConfig, initial systemstate.ProvisionIdentityConfig) (systemstate.IdentityTransition, error) {
	if bootstrap {
		return systemstate.ProvisionIdentity(ctx, backend, initial)
	}
	return systemstate.AdoptOperator(ctx, backend, adoption)
}

func assertTransitionCounts(t *testing.T, backend TransitionBackend, users, receipts int64) {
	t.Helper()
	for table, want := range map[string]int64{"godj_identity_user": users, "godj_system_identity_transition": receipts} {
		got, err := transitionRowCount(t.Context(), backend, table)
		if err != nil || got != want {
			t.Fatal("transition table differs", table, got, want, err)
		}
	}
	if receipts == 0 {
		for _, table := range []string{"godj_identity_permission", "godj_identity_user_permissions"} {
			if got, err := transitionRowCount(t.Context(), backend, table); err != nil || got != 0 {
				t.Fatal("failed transition left grants", table, got, err)
			}
		}
	}
}

func assertLegacyCredentialUsable(t *testing.T, backend TransitionBackend, config systemstate.AdoptOperatorConfig) {
	t.Helper()
	// These are independent raw observations of the legacy source; the main
	// adoption consumer also exercises its already-running authenticator.
	fields := []query.FieldRef{query.NewFieldRef("username", "username", query.FieldString, false), query.NewFieldRef("encoded_password", "encoded_password", query.FieldString, false), query.NewFieldRef("active", "active", query.FieldBoolean, false)}
	rows, err := backend.Query(t.Context(), query.NewPlan("godj_system_credential", fields))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("legacy source disappeared")
	}
	var username, encoded string
	var active bool
	if err := rows.Scan(&username, &encoded, &active); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || errors.Join(rows.Err(), rows.Close()) != nil {
		t.Fatal("legacy source cardinality/read failed")
	}
	valid, err := config.Expected.CredentialPolicy.PasswordHasher.Verify(t.Context(), "boundary password", encoded)
	if err != nil || !valid || !active || username != "legacy" {
		t.Fatal("rollback changed legacy credential", err)
	}
}

type boundaryBackend struct {
	TransitionBackend
	insertTable, readMode     string
	failure                   error
	calls, faults             int
	unknown                   bool
	callbackCancel            bool
	beforeCommit, afterCommit func()
}

func (b *boundaryBackend) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	b.calls++
	err := b.TransitionBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		err := callback(boundarySession{Session: session, owner: b})
		if b.callbackCancel && err == nil {
			return context.Canceled
		}
		if b.beforeCommit != nil && err == nil {
			b.beforeCommit()
			return ctx.Err()
		}
		return err
	})
	if err == nil {
		if b.afterCommit != nil {
			b.afterCommit()
		}
		if b.unknown {
			return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Detail: "private uncertain commit"}
		}
	}
	return err
}

func (b *boundaryBackend) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	if b.readMode == "missing_callback" {
		return nil
	}
	if b.readMode == "nil_reader" {
		return callback(nil)
	}
	err := b.TransitionBackend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		if b.readMode == "swallowed_callback_failure" {
			_ = callback(nil)
			return nil
		}
		if err := callback(reader); err != nil {
			return err
		}
		if b.readMode == "duplicate_callback" {
			return callback(reader)
		}
		return nil
	})
	if err == nil && b.readMode == "end_failure" {
		return errors.New("private read scope end failure")
	}
	return err
}

type boundarySession struct {
	db.Session
	owner *boundaryBackend
}

func (s boundarySession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == s.owner.insertTable {
		s.owner.faults++
		return 0, s.owner.failure
	}
	return s.Session.Insert(ctx, plan)
}

func transitionRowCount(ctx context.Context, reader db.Queryer, table string) (int64, error) {
	field := query.NewFieldRef("id", "id", query.FieldInteger, false)
	plan, err := query.NewPlan(table, []query.FieldRef{field}).WithLimit(3)
	if err != nil {
		return 0, err
	}
	rows, err := reader.Query(ctx, plan)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var count int64
	for rows.Next() {
		count++
	}
	return count, errors.Join(rows.Err(), rows.Close())
}
