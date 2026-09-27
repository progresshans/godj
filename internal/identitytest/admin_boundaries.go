package identitytest

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type adminMutationBoundary struct {
	*systemstate.Runtime
	mode          string
	calls, faults int
	before        func(context.Context) error
}

func (b *adminMutationBoundary) after(err error) error {
	if err == nil && b.mode == "unknown_commit" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-admin-fault"}
	}
	return err
}
func (b *adminMutationBoundary) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.calls++
	if b.before != nil {
		if err := b.before(ctx); err != nil {
			return err
		}
	}
	return b.after(b.Runtime.CoordinatedAtomicRelation(ctx, callback))
}
func (b *adminMutationBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	b.calls++
	if b.before != nil {
		if err := b.before(ctx); err != nil {
			return err
		}
	}
	return b.after(b.Runtime.CoordinatedAtomic(ctx, callback))
}
func (b *adminMutationBoundary) AppendAudit(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
	if b.mode == "audit_failure" {
		b.faults++
		return errors.New("private-admin-fault")
	}
	return b.Runtime.AppendAudit(ctx, session, event)
}

func runAdminFailureBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	for _, operation := range []string{"create", "update", "delete", "password"} {
		for _, mode := range []string{"audit_failure", "unknown_commit"} {
			t.Run(operation+"_"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 3)
				boundary := &adminMutationBoundary{Runtime: f.runtime, mode: mode}
				h := newIdentityAdminHTTP(t, f, boundary)
				h.loginAdmin(t, h.client, "manager", managementOldPassword)
				beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
				want := 500
				if mode == "unknown_commit" {
					want = 503
				}
				var response adminHTTPResult
				switch operation {
				case "create":
					response = h.postForm(t, "/admin/users/add/", url.Values{"username": {"Candidate"}, "password1": {managementNewPassword}, "password2": {managementNewPassword}}, want)
				case "update":
					before, err := f.manager(t, f.runtime).User(t.Context(), f.actor, f.user.ID)
					if err != nil {
						t.Fatal(err)
					}
					data := adminUserData(before)
					data.Set("first_name", "Changed")
					response = h.postForm(t, adminObjectPath("users", "change", f.user.ID), data, want)
				case "delete":
					response = h.postForm(t, adminObjectPath("users", "delete", f.user.ID), url.Values{}, want)
				case "password":
					response = h.postForm(t, adminObjectPath("users", "command/password", f.user.ID), url.Values{"password1": {managementNewPassword}, "password2": {managementNewPassword}}, want)
				}
				if boundary.calls != 1 || mode == "audit_failure" && boundary.faults != 1 || response.header.Get("Location") != "" || response.header.Get("Retry-After") != "" {
					t.Fatal("failed/unknown operation retried, redirected or missed boundary")
				}
				afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
				if mode == "audit_failure" {
					if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
						t.Fatal("failed audit left side effects")
					}
					f.assertOutcome(t, false)
				} else {
					switch operation {
					case "create":
						if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 3 {
							t.Fatal("unknown create was not committed", err)
						}
					case "update":
						if row := f.stored(t); row.FirstName != "Changed" || row.Revision != 2 {
							t.Fatal("unknown update was not committed")
						}
					case "delete":
						if found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(f.user.ID)).Exists(t.Context()); err != nil || found {
							t.Fatal("unknown delete was not committed", err)
						}
					case "password":
						f.assertOutcome(t, true)
					}
				}
			})
		}
	}
	for _, kind := range []string{"revision", "authority"} {
		t.Run("write_fence_rechecks_"+kind, func(t *testing.T) {
			backend, second := open(t)
			f := newManagementFixture(t, backend, 0)
			boundary := &adminMutationBoundary{Runtime: f.runtime}
			boundary.before = func(ctx context.Context) error {
				return second.CoordinatedAtomic(ctx, func(session db.Session) error {
					if kind == "revision" {
						_, err := models.UserObjects.Update(ctx, session, f.user, models.UserPatch{}.WithFirstName("Competing winner").WithRevision(2))
						return err
					}
					_, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
					return err
				})
			}
			h := newIdentityAdminHTTP(t, f, boundary)
			h.loginAdmin(t, h.client, "manager", managementOldPassword)
			before, err := f.manager(t, f.runtime).User(t.Context(), f.actor, f.user.ID)
			if err != nil {
				t.Fatal(err)
			}
			data := adminUserData(before)
			data.Set("first_name", "Must not win")
			want := 409
			if kind == "authority" {
				want = 403
			}
			h.postForm(t, adminObjectPath("users", "change", f.user.ID), data, want)
			if boundary.calls != 1 || f.stored(t).FirstName == "Must not win" {
				t.Fatal("stale form escaped final fence")
			}
			history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
			if err != nil || len(history) != 0 {
				t.Fatal("rejected write left audit", err)
			}
		})
	}
}

type adminHistoryBoundary struct {
	*systemstate.Runtime
	hook   func(context.Context) error
	reader db.Queryer
	fault  error
}

func (b *adminHistoryBoundary) AuditHistoryInSnapshot(ctx context.Context, reader db.Queryer, model string, id int64, limit int) ([]admin.AuditEntry, error) {
	b.reader = reader
	if b.hook != nil {
		if err := b.hook(ctx); err != nil {
			return nil, err
		}
	}
	if b.fault != nil {
		return nil, b.fault
	}
	return b.Runtime.AuditHistoryInSnapshot(ctx, reader, model, id, limit)
}

func runAdminReadBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	t.Run("history_shares_authorization_snapshot_and_reader_expires", func(t *testing.T) {
		backend, second := open(t)
		f := newManagementFixture(t, backend, 0)
		boundary := &adminHistoryBoundary{Runtime: f.runtime}
		boundary.hook = func(ctx context.Context) error {
			return second.CoordinatedAtomic(ctx, func(session db.Session) error {
				if _, err := models.UserObjects.Update(ctx, session, f.user, models.UserPatch{}.WithFirstName("Concurrent").WithRevision(2)); err != nil {
					return err
				}
				if _, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2)); err != nil {
					return err
				}
				event, err := admin.PrepareEvent(f.actor.ID(), "godj_identity.user", f.user.ID, admin.ActionChange, []string{"first_name"}, "")
				if err != nil {
					return err
				}
				return f.runtime.AppendAudit(ctx, session, event)
			})
		}
		manager := f.manager(t, boundary)
		entries, err := manager.UserHistory(t.Context(), f.actor, f.user.ID, 10)
		if err != nil || len(entries) != 0 {
			t.Fatal("history escaped the authorization snapshot", err, entries)
		}
		if entries, err := manager.UserHistory(t.Context(), f.actor, f.user.ID, 10); !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || len(entries) != 0 {
			t.Fatal("history reused old authority", err)
		}
		if entries, err := f.runtime.AuditHistoryInSnapshot(t.Context(), boundary.reader, "godj_identity.user", f.user.ID, 10); err == nil || entries != nil {
			t.Fatal("expired history reader executed")
		}
		if entries, err := f.runtime.AuditHistoryInSnapshot(t.Context(), backend, "godj_identity.user", f.user.ID, 10); err == nil || entries != nil {
			t.Fatal("root backend impersonated a borrowed reader")
		}
	})
	t.Run("history_storage_failure_is_not_permission_denial", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		boundary := &adminHistoryBoundary{Runtime: f.runtime, fault: errors.Join(errors.New("private-admin-fault"), &identity.Error{Code: identity.CodePermission, Field: "actor"})}
		manager := f.manager(t, boundary)
		entries, err := manager.UserHistory(t.Context(), f.actor, f.user.ID, 10)
		failure, ok := err.(*identity.Error)
		if !ok || failure.Code != identity.CodePersistence || entries != nil {
			t.Fatal("history storage failure downgraded", err)
		}
		h := newIdentityAdminHTTP(t, f, boundary)
		h.loginAdmin(t, h.client, "manager", managementOldPassword)
		h.call(t, h.client, "GET", adminObjectPath("users", "history", f.user.ID), nil, 500)
	})
}
