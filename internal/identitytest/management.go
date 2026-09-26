package identitytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
)

const managementOldPassword = "original managed-user password"
const managementNewPassword = "replacement managed-user password"

type managementFixture struct {
	backend TransitionBackend
	runtime *systemstate.Runtime
	hasher  *managementHasher
	actor   auth.Principal
	user    models.User
	root    models.User
	ids     []sessions.ID
}

type managementHasher struct {
	auth.PasswordHasher
	calls atomic.Int64
	hook  func(context.Context) error
	fixed string
}

func (h *managementHasher) Hash(ctx context.Context, password string) (string, error) {
	h.calls.Add(1)
	if h.hook != nil {
		if err := h.hook(ctx); err != nil {
			return "", err
		}
	}
	if h.fixed != "" {
		return h.fixed, nil
	}
	return h.PasswordHasher.Hash(ctx, password)
}

func newManagementFixture(t *testing.T, backend TransitionBackend, count int) *managementFixture {
	t.Helper()
	ctx := t.Context()
	applyIdentitySources(t, backend, systemstate.IdentityMigrationSources()...)
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "manager", Active: true, Staff: true, Superuser: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := systemstate.ProvisionIdentity(ctx, backend, systemstate.ProvisionIdentityConfig{Principal: actor, Username: "manager", Password: managementOldPassword, PasswordHasher: hasher}); err != nil {
		t.Fatal(err)
	}
	runtime, err := systemstate.OpenIdentity(ctx, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := hasher.Hash(ctx, managementOldPassword)
	if err != nil {
		t.Fatal(err)
	}
	user, err := models.UserObjects.Create(ctx, backend, models.NewUserCreate("managed-member", "member", encoded, time.Now().UTC()).WithStaff(true).WithFirstName("Original").WithEmail("member@example.test"))
	if err != nil {
		t.Fatal(err)
	}
	permission, err := models.PermissionObjects.Create(ctx, backend, models.NewPermissionCreate("helpdesk.ticket.view", "View ticket"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(ctx, backend, models.NewUserPermissionsLinkCreate(user.ID, permission.ID)); err != nil {
		t.Fatal(err)
	}
	root, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact(actor.ID())).OrderBy(models.UserFields.ID.Asc()).First(ctx)
	if err != nil || !found {
		t.Fatal("root missing", err)
	}
	f := &managementFixture{backend: backend, runtime: runtime, hasher: &managementHasher{PasswordHasher: hasher}, actor: actor, user: user, root: root}
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := runtime.Authenticator().Resolve(ctx, user.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	rootCredential, err := runtime.Authenticator().Resolve(ctx, actor.ID())
	if err != nil {
		t.Fatal(err)
	}
	for index := range count {
		values := map[string]string{"unrelated_application_value": "preserve"}
		if index%3 == 0 {
			values[auth.SessionPrincipalIDKey] = user.PrincipalID
			values[auth.SessionCredentialStampKey] = credential.SessionStamp()
		} else if index%3 == 1 {
			values[auth.SessionPrincipalIDKey] = actor.ID()
			values[auth.SessionCredentialStampKey] = rootCredential.SessionStamp()
		}
		record, err := manager.Create(ctx, values)
		if err != nil {
			t.Fatal(err)
		}
		f.ids = append(f.ids, record.ID())
	}
	return f
}

func (f *managementFixture) manager(t *testing.T, backend identity.ManagementBackend) *identity.Manager {
	t.Helper()
	manager, err := identity.NewManager(backend, f.hasher, auth.PrincipalAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func (f *managementFixture) stored(t *testing.T) models.User {
	t.Helper()
	value, present, err := models.UserObjects.Using(f.backend).Filter(models.UserFields.ID.Exact(f.user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !present {
		t.Fatal("managed user missing", err)
	}
	return value
}

func (f *managementFixture) assertOutcome(t *testing.T, changed bool) {
	t.Helper()
	stored := f.stored(t)
	wantRevision, password := int64(1), managementOldPassword
	if changed {
		wantRevision, password = 2, managementNewPassword
	}
	verified, err := f.hasher.Verify(t.Context(), password, stored.EncodedPassword)
	if err != nil || !verified || stored.Revision != wantRevision {
		t.Fatal("password/revision is not atomic", stored.Revision, err)
	}
	for index, id := range f.ids {
		_, found, err := f.runtime.SessionStore().Load(t.Context(), id)
		if err != nil || found != (!changed || index%3 != 0) {
			t.Fatal("wrong session revoked or rollback lost session", index, found, err)
		}
	}
	history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantHistory := 0
	if changed {
		wantHistory = 1
	}
	if len(history) != wantHistory {
		t.Fatal("password change audit was not atomic", len(history))
	}
	if changed && (history[0].ActorID != f.actor.ID() || history[0].Action != admin.ActionChange || !reflect.DeepEqual(history[0].ChangedFields, []string{"password"}) || history[0].DisplayLabel != "") {
		t.Fatal("password audit leaked values or lost semantics")
	}
}

// RunPasswordManagement executes the same service, ORM, durable sessions and
// native transaction owner on each database. Boundary faults are real rollbacks.
func RunPasswordManagement(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	t.Run("target_only_revocation_across_batches_and_reopen", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 260)
		beforeRows, _ := snapshotIdentitySystemRows(t, backend)
		old, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := f.manager(t, f.runtime).SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
		if err != nil || profile.ID != f.user.ID || profile.Revision != 2 || profile.Email != f.user.Email || profile.FirstName != f.user.FirstName {
			t.Fatal("password change lost profile", err)
		}
		if f.hasher.calls.Load() != 1 {
			t.Fatal("password hashing retried")
		}
		f.assertOutcome(t, true)
		afterRows, _ := snapshotIdentitySystemRows(t, backend)
		// The surviving rows must still be exact encoded originals.
		for _, row := range afterRows {
			if !containsStoredRow(beforeRows, row) {
				t.Fatal("foreign/anonymous session bytes changed")
			}
		}
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		current, err := reopened.Authenticator().Authenticate(t.Context(), "member", managementNewPassword)
		if err != nil || current.MatchesSessionStamp(old.SessionStamp()) {
			t.Fatal("reopened credential did not change", err)
		}
		if _, err := reopened.Authenticator().Authenticate(t.Context(), "member", managementOldPassword); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatal("old password remains usable", err)
		}
		verifyManagedPasswordHTTP(t, reopened, f, profile.Revision)
		encoded, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{managementNewPassword, f.stored(t).EncodedPassword, old.SessionStamp(), current.SessionStamp()} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("password material in profile")
			}
		}
	})
	t.Run("preflight_refusal_does_not_hash", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		fault := &managementBoundary{ManagementBackend: f.runtime}
		manager := f.manager(t, fault)
		for _, target := range []struct {
			actor        auth.Principal
			id, revision int64
			want         identity.ErrorCode
		}{
			{f.actor, f.user.ID, 2, identity.CodeConflict}, {f.actor, 9999, 1, identity.CodeNotFound}, {auth.Anonymous(), f.user.ID, 1, identity.CodePermission},
		} {
			profile, err := manager.SetPassword(t.Context(), target.actor, target.id, target.revision, managementNewPassword)
			if !errors.Is(err, &identity.Error{Code: target.want}) || profile.ID != 0 {
				t.Fatal("preflight classification", err)
			}
		}
		if f.hasher.calls.Load() != 0 || fault.calls != 0 {
			t.Fatal("refused request performed password/write work")
		}
		f.assertOutcome(t, false)
	})
	for _, change := range []string{"actor_deactivated", "group_grant_removed", "target_revision", "target_hash_without_revision"} {
		t.Run("change_during_hash_"+change, func(t *testing.T) {
			backend, writer := open(t)
			f := newManagementFixture(t, backend, 3)
			_, link := managementGroupActor(t, f)
			// The stale argument grants everything. The DB grants only change_user.
			f.hasher.hook = func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				owner := writer
				if change == "actor_deactivated" {
					owner = backend
				}
				return owner.CoordinatedAtomic(ctx, func(session db.Session) error {
					switch change {
					case "actor_deactivated":
						_, err := models.UserObjects.Update(ctx, session, f.root, models.UserPatch{}.WithActive(false).WithRevision(3))
						return err
					case "group_grant_removed":
						_, err := models.GroupPermissionsLinkObjects.Delete(ctx, session, &link)
						return err
					case "target_revision":
						_, err := models.UserObjects.Update(ctx, session, f.user, models.UserPatch{}.WithRevision(2))
						return err
					default:
						_, err := models.UserObjects.Update(ctx, session, f.user, models.UserPatch{}.WithEncodedPassword("independently-replaced"))
						return err
					}
				})
			}
			fault := &managementBoundary{ManagementBackend: f.runtime}
			profile, err := f.manager(t, fault).SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
			want := identity.CodePermission
			if strings.HasPrefix(change, "target_") {
				want = identity.CodeConflict
			}
			if !errors.Is(err, &identity.Error{Code: want}) || profile.ID != 0 || fault.calls != 1 || f.hasher.calls.Load() != 1 {
				t.Fatal("stale authority/target was changed or retried", err)
			}
			if history, err := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10); err != nil || len(history) != 0 {
				t.Fatal("rejected change was audited", err)
			}
			for _, id := range f.ids {
				if _, found, err := f.runtime.SessionStore().Load(t.Context(), id); err != nil || !found {
					t.Fatal("rejected change revoked sessions", err)
				}
			}
		})
	}
	t.Run("current_group_grant_and_deny_overlay", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		managementGroupActor(t, f)
		denied, err := identity.NewManager(f.runtime, f.hasher, denyIdentityAuthorization{})
		if err != nil {
			t.Fatal(err)
		}
		if result, err := denied.SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword); !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || result.ID != 0 || f.hasher.calls.Load() != 0 {
			t.Fatal("deny overlay escaped", err)
		}
		actorWithoutGrants, _ := auth.NewPrincipal(auth.PrincipalConfig{ID: f.actor.ID(), Active: true})
		if _, err := f.manager(t, f.runtime).SetPassword(t.Context(), actorWithoutGrants, f.user.ID, 1, managementNewPassword); err != nil {
			t.Fatal("current group grant not used", err)
		}
		f.assertOutcome(t, true)
	})
	for _, mode := range []string{"update_failure", "second_delete_failure", "audit_failure", "callback_cancel", "unknown_rollback", "unknown_commit", "late_cancel"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 6)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := &managementBoundary{ManagementBackend: f.runtime, mode: mode, afterCommit: cancel}
			profile, err := f.manager(t, fault).SetPassword(ctx, f.actor, f.user.ID, 1, managementNewPassword)
			committed := mode == "unknown_commit" || mode == "late_cancel"
			if mode == "late_cancel" {
				if err != nil || profile.ID == 0 || ctx.Err() == nil {
					t.Fatal("known commit lost to late cancellation", err)
				}
			} else {
				if err == nil || profile.ID != 0 {
					t.Fatal("failed/unknown operation published success")
				}
				if mode == "unknown_commit" && (!errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) || !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown})) {
					t.Fatal("unknown commit classification lost", err)
				}
				if mode == "unknown_rollback" && (!errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) || !errors.Is(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown})) {
					t.Fatal("unknown rollback downgraded to callback cancellation", err)
				}
				if mode == "callback_cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("callback cancellation lost", err)
				}
				for _, format := range []string{"%v", "%+v", "%#v", "%d", "%f", "%p", "%w"} {
					if strings.Contains(fmt.Sprintf(format, err), "private-maintenance-fault") {
						t.Fatal("private failure rendered")
					}
				}
			}
			if fault.calls != 1 || f.hasher.calls.Load() != 1 {
				t.Fatal("automatic retry or missing write")
			}
			if (mode == "update_failure" || mode == "second_delete_failure" || mode == "audit_failure") && fault.faults != 1 {
				t.Fatal("fault did not reach its intended write boundary")
			}
			f.assertOutcome(t, committed)
		})
	}
	t.Run("corrupt_later_session_rolls_back_earlier_deletes", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 260)
		rows, _ := snapshotIdentitySystemRows(t, backend)
		id, err := strconv.ParseInt(rows[len(rows)-1][0], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Update(t.Context(), query.NewUpdatePlan("godj_system_session", []query.Assignment{query.NewAssignment(query.NewFieldRef("payload", "payload", query.FieldString, false), query.String("corrupt-private-maintenance-fault"))}, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(id))); err != nil {
			t.Fatal(err)
		}
		beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
		if result, err := f.manager(t, f.runtime).SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword); err == nil || result.ID != 0 {
			t.Fatal("corrupt session scan published password change")
		}
		afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
		stored := f.stored(t)
		if stored.EncodedPassword != f.user.EncodedPassword || stored.Revision != 1 || !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
			t.Fatal("later session corruption retained earlier partial writes")
		}
	})
	for _, mode := range []string{"read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure", "write_zero", "write_nil", "write_twice", "write_swallowed_failure"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			fault := &managementBoundary{ManagementBackend: f.runtime, mode: mode}
			profile, err := f.manager(t, fault).SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
			if err == nil || profile.ID != 0 {
				t.Fatal("broken backend contract published success")
			}
			if strings.HasPrefix(mode, "read_") && (f.hasher.calls.Load() != 0 || fault.calls != 0) {
				t.Fatal("failed snapshot admitted password/write work")
			}
			f.assertOutcome(t, false)
		})
	}
	for _, mode := range []string{"hash_failure", "malformed_hash", "unchanged_hash"} {
		t.Run(mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			switch mode {
			case "hash_failure":
				f.hasher.hook = func(context.Context) error {
					return errors.Join(context.Canceled, errors.New("private-maintenance-fault"))
				}
			case "malformed_hash":
				f.hasher.fixed = "malformed-private-maintenance-fault"
			case "unchanged_hash":
				f.hasher.fixed = f.user.EncodedPassword
			}
			fault := &managementBoundary{ManagementBackend: f.runtime}
			profile, err := f.manager(t, fault).SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
			if err == nil || profile.ID != 0 || fault.calls != 0 || f.hasher.calls.Load() != 1 {
				t.Fatal("invalid password work reached write/published", err)
			}
			if mode == "hash_failure" && !errors.Is(err, context.Canceled) {
				t.Fatal("hasher cancellation cause lost")
			}
			f.assertOutcome(t, false)
		})
	}
	t.Run("competing_revisions", func(t *testing.T) {
		first, second := open(t)
		f := newManagementFixture(t, first, 6)
		other, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		arrived := make(chan struct{}, 2)
		release := make(chan struct{})
		f.hasher.hook = func(ctx context.Context) error {
			arrived <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		results := make(chan error, 2)
		for _, backend := range []identity.ManagementBackend{f.runtime, other} {
			manager := f.manager(t, backend)
			go func() {
				_, err := manager.SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
				results <- err
			}()
		}
		for range 2 {
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("hashing retained database scope or failed preflight")
			}
		}
		close(release)
		succeeded, conflicted := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				succeeded++
			} else if errors.Is(err, &identity.Error{Code: identity.CodeConflict}) {
				conflicted++
			} else {
				t.Fatal("competition failed outside revision fence", err)
			}
		}
		if succeeded != 1 || conflicted != 1 || f.hasher.calls.Load() != 2 {
			t.Fatal("competing writes both committed or retried")
		}
		f.assertOutcome(t, true)
	})
}

func containsStoredRow(rows [][]string, wanted []string) bool {
	for _, row := range rows {
		if reflect.DeepEqual(row, wanted) {
			return true
		}
	}
	return false
}

func managementGroupActor(t *testing.T, f *managementFixture) (models.Group, models.GroupPermissionsLink) {
	t.Helper()
	ctx := t.Context()
	group, err := models.GroupObjects.Create(ctx, f.backend, models.NewGroupCreate("Credential managers"))
	if err != nil {
		t.Fatal(err)
	}
	permission, err := models.PermissionObjects.Create(ctx, f.backend, models.NewPermissionCreate(string(identity.ChangeUser), "Change user"))
	if err != nil {
		t.Fatal(err)
	}
	link, err := models.GroupPermissionsLinkObjects.Create(ctx, f.backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserGroupsLinkObjects.Create(ctx, f.backend, models.NewUserGroupsLinkCreate(f.root.ID, group.ID)); err != nil {
		t.Fatal(err)
	}
	f.root, err = models.UserObjects.Update(ctx, f.backend, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
	if err != nil {
		t.Fatal(err)
	}
	return group, link
}

type managementBoundary struct {
	identity.ManagementBackend
	mode        string
	calls       int
	afterCommit func()
	faults      int
}

func (b *managementBoundary) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	if b.mode == "read_zero" {
		return nil
	}
	err := b.ManagementBackend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		switch b.mode {
		case "read_nil":
			return callback(nil)
		case "read_twice":
			if err := callback(reader); err != nil {
				return err
			}
			return callback(reader)
		case "read_swallowed_failure":
			_ = callback(managementReadFailure{})
			return nil
		default:
			return callback(reader)
		}
	})
	if err == nil && b.mode == "read_end_failure" {
		return errors.New("private-maintenance-fault")
	}
	return err
}

func (b *managementBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	b.calls++
	if b.mode == "write_zero" {
		return nil
	}
	err := b.ManagementBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if b.mode == "write_nil" {
			return callback(nil)
		}
		if b.mode == "write_swallowed_failure" {
			_ = callback(&managementSession{Session: session, mode: "read_failure"})
			return nil
		}
		if err := callback(&managementSession{Session: session, mode: b.mode, fault: func() { b.faults++ }}); err != nil {
			return err
		}
		if b.mode == "write_twice" {
			return callback(session)
		}
		if b.mode == "callback_cancel" || b.mode == "unknown_rollback" {
			return context.Canceled
		}
		return nil
	})
	if err != nil {
		if b.mode == "unknown_rollback" {
			return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown, Detail: "private-maintenance-fault"})
		}
		return err
	}
	if b.mode == "late_cancel" {
		b.afterCommit()
	}
	if b.mode == "unknown_commit" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-maintenance-fault"}
	}
	return nil
}

func (b *managementBoundary) AppendAudit(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
	if b.mode == "audit_failure" {
		b.faults++
		return errors.New("private-maintenance-fault")
	}
	return b.ManagementBackend.AppendAudit(ctx, session, event)
}

type managementSession struct {
	db.Session
	mode    string
	deletes int
	fault   func()
}

func (s *managementSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if s.mode == "read_failure" {
		return nil, errors.New("private-maintenance-fault")
	}
	return s.Session.Query(ctx, plan)
}
func (s *managementSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if s.mode == "update_failure" {
		s.fault()
		return 0, errors.New("private-maintenance-fault")
	}
	return s.Session.Update(ctx, plan)
}
func (s *managementSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	s.deletes++
	if s.mode == "second_delete_failure" && s.deletes == 2 {
		s.fault()
		return 0, errors.New("private-maintenance-fault")
	}
	return s.Session.Delete(ctx, plan)
}

type managementReadFailure struct{}

func (managementReadFailure) Query(context.Context, query.Plan) (db.Rows, error) {
	return nil, errors.New("private-maintenance-fault")
}
