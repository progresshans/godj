package identitytest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostdef "github.com/progresshans/godj/conformance/identityfixture/modeldef"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/validation"
)

func managementHostSources(t *testing.T) []definition.Source {
	t.Helper()
	schema, err := hostdef.Schema()
	if err != nil {
		t.Fatal(err)
	}
	operations := make([]migrations.Operation, len(schema.Models))
	for index, model := range schema.Models {
		operations[index] = migrations.CreateModel{AppLabel: schema.AppLabel, Model: model}
	}
	document, err := definition.Encode(definition.Producer{Name: "identity-management-host", Version: "1"}, migrations.Migration{App: schema.AppLabel, Name: "0001_initial", Dependencies: []migrations.MigrationKey{identity.InitialMigrationKey()}, Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	return append(systemstate.IdentityMigrationSources(), definition.Source{SourceID: "management-host/0001_initial", Document: document})
}

func managementHost(t *testing.T, backend TransitionBackend) hostproject.RelationDeleters {
	t.Helper()
	applyIdentitySources(t, backend, managementHostSources(t)...)
	policy, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func managementUserSelections(t *testing.T, f *managementFixture) (models.Group, models.Permission, models.Permission) {
	t.Helper()
	view, found, err := models.PermissionObjects.Using(f.backend).Filter(models.PermissionFields.Code.Exact("helpdesk.ticket.view")).OrderBy(models.PermissionFields.ID.Asc()).First(t.Context())
	if err != nil || !found {
		t.Fatal(err)
	}
	change, err := models.PermissionObjects.Create(t.Context(), f.backend, models.NewPermissionCreate("helpdesk.ticket.change", "Change ticket"))
	if err != nil {
		t.Fatal(err)
	}
	group, err := models.GroupObjects.Create(t.Context(), f.backend, models.NewGroupCreate("Editors"))
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range []models.Permission{view, change} {
		if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), f.backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
			t.Fatal(err)
		}
	}
	return group, view, change
}

func RunUserManagement(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Helper()
	t.Run("host_protect_and_failed_delete_preserve_every_row", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		policy := managementHost(t, backend)
		guard, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(f.user.ID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("cascade candidate", f.user.ID)); err != nil {
			t.Fatal(err)
		}
		manager := f.manager(t, f.runtime)
		beforeSessions, beforeAudit := snapshotIdentitySystemRows(t, backend)
		if result, err := manager.DeleteUser(t.Context(), f.actor, f.user.ID, 1, policy.AccountsUser); !errors.Is(err, &query.Error{Code: query.CodeProtectedForeignKey}) || result.ID != 0 {
			t.Fatal("host PROTECT omitted", err)
		}
		f.assertOutcome(t, false)
		if _, err := hostmodels.GuardObjects.Delete(t.Context(), backend, &guard); err != nil {
			t.Fatal(err)
		}
		fault := &userManagementBoundary{ManagementBackend: f.runtime, mode: "audit_failure"}
		if result, err := f.manager(t, fault).DeleteUser(t.Context(), f.actor, f.user.ID, 1, policy.AccountsUser); err == nil || result.ID != 0 || fault.faults != 1 {
			t.Fatal("failed host delete published or missed audit", err)
		}
		f.assertOutcome(t, false)
		if count, err := hostmodels.NoteObjects.Using(backend).Count(t.Context()); err != nil || count != 1 {
			t.Fatal("outer rollback lost CASCADE child", err)
		}
		afterSessions, afterAudit := snapshotIdentitySystemRows(t, backend)
		if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeAudit, afterAudit) {
			t.Fatal("failed deletion changed session/audit bytes")
		}
	})
	t.Run("invalid_duplicate_and_missing_targets_do_not_hash_or_write", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 3)
		fault := &userManagementBoundary{ManagementBackend: f.runtime}
		manager := f.manager(t, fault)
		for _, input := range []identity.UserCreate{
			identity.NewUserCreate("other", f.user.Username), identity.NewUserCreate(f.user.PrincipalID, "unique"),
			identity.NewUserCreate("other", "unique").WithGroups(99999), identity.NewUserCreate("other", "unique").WithPermissions(99999),
			identity.NewUserCreate("other", "unique").WithGroups(-1), identity.NewUserCreate("other", " unique"),
		} {
			value, err := manager.CreateUser(t.Context(), f.actor, input, managementOldPassword)
			if fields, rejected := validation.Rejected(err); !rejected || fields.Empty() || value.ID != 0 {
				t.Fatal("invalid creation not classified", err)
			}
		}
		if f.hasher.calls.Load() != 0 || fault.calls != 0 {
			t.Fatal("invalid preflight hashed or wrote")
		}
		f.assertOutcome(t, false)
	})
	t.Run("creator_authority_revoked_during_hash", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		f.hasher.hook = func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return backend.CoordinatedAtomic(ctx, func(session db.Session) error {
				_, err := models.UserObjects.Patch(ctx, session, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(2))
				return err
			})
		}
		value, err := f.manager(t, f.runtime).CreateUser(t.Context(), f.actor, identity.NewUserCreate("new-member", "new-member"), managementOldPassword)
		if !errors.Is(err, &identity.Error{Code: identity.CodePermission}) || value.ID != 0 || f.hasher.calls.Load() != 1 {
			t.Fatal("stale creator admitted", err)
		}
		if count, err := models.UserObjects.Using(backend).Count(t.Context()); err != nil || count != 2 {
			t.Fatal("refused creator stored a user", err)
		}
	})
	t.Run("rejected_read_with_cleanup_failure_stays_execution_error", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		boundary := &managementBoundary{ManagementBackend: f.runtime, mode: "read_end_failure"}
		result, err := f.manager(t, boundary).CreateUser(t.Context(), f.actor, identity.NewUserCreate("duplicate", f.user.Username), managementOldPassword)
		if _, rejected := validation.Rejected(err); err == nil || rejected || result.ID != 0 || f.hasher.calls.Load() != 0 {
			t.Fatal("read cleanup failure became a validation result or password work", err)
		}
	})
	for _, mode := range []string{"update_failure", "membership_failure", "audit_failure", "callback_cancel", "unknown_commit", "late_cancel"} {
		t.Run("update_"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			group, _, change := managementUserSelections(t, f)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fault := &userManagementBoundary{ManagementBackend: f.runtime, mode: mode, afterCommit: cancel}
			value, err := f.manager(t, fault).UpdateUser(ctx, f.actor, f.user.ID, 1, identity.UserPatch{}.WithFirstName("changed").WithGroups(group.ID).WithPermissions(change.ID).WithActive(false))
			committed := mode == "unknown_commit" || mode == "late_cancel"
			if mode == "late_cancel" {
				if err != nil || value.ID == 0 || ctx.Err() == nil {
					t.Fatal("confirmed update lost", err)
				}
			} else if err == nil || value.ID != 0 {
				t.Fatal("failed/unknown update published")
			}
			if mode == "unknown_commit" && !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) {
				t.Fatal("unknown classification lost", err)
			}
			if fault.calls != 1 {
				t.Fatal("write retry or missing call")
			}
			if (mode == "update_failure" || mode == "membership_failure" || mode == "audit_failure") && fault.faults != 1 {
				t.Fatal("fault stage not reached", fault.faults, err)
			}
			stored := f.stored(t)
			if committed {
				if stored.Revision != 2 || stored.Active || stored.FirstName != "changed" {
					t.Fatal("committed result missing")
				}
			} else if stored.Revision != 1 || !stored.Active || stored.FirstName != f.user.FirstName {
				t.Fatal("failed update changed scalar")
			}
			for index, id := range f.ids {
				_, found, loadErr := f.runtime.SessionStore().Load(t.Context(), id)
				if loadErr != nil || found != (!committed || index%3 != 0) {
					t.Fatal("update sessions not atomic", loadErr)
				}
			}
			history, historyErr := f.runtime.AuditHistory(t.Context(), "godj_identity.user", f.user.ID, 10)
			if historyErr != nil || (len(history) == 1) != committed {
				t.Fatal("update audit not atomic", historyErr)
			}
			if stored.EncodedPassword != f.user.EncodedPassword || f.hasher.calls.Load() != 0 {
				t.Fatal("profile update changed password")
			}
		})
	}
	for _, operation := range []string{"create", "delete"} {
		for _, mode := range []string{"audit_failure", "unknown_commit"} {
			t.Run(operation+"_"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 3)
				policy := managementHost(t, backend)
				group, view, _ := managementUserSelections(t, f)
				fault := &userManagementBoundary{ManagementBackend: f.runtime, mode: mode}
				manager := f.manager(t, fault)
				var err error
				if operation == "create" {
					var result identity.UserDetails
					result, err = manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("uncertain-created", "uncertain-created").WithGroups(group.ID).WithPermissions(view.ID), managementOldPassword)
					if result.ID != 0 {
						t.Fatal("failed/unknown create published")
					}
				} else {
					var result identity.UserDeletion
					result, err = manager.DeleteUser(t.Context(), f.actor, f.user.ID, 1, policy.AccountsUser)
					if result.ID != 0 {
						t.Fatal("failed/unknown delete published")
					}
				}
				if err == nil || fault.calls != 1 {
					t.Fatal("failed/unknown operation succeeded or retried", err)
				}
				committed := mode == "unknown_commit"
				if committed && !errors.Is(err, &identity.Error{Code: identity.CodeOutcomeUnknown}) {
					t.Fatal("unknown classification lost", err)
				}
				if !committed && fault.faults != 1 {
					t.Fatal("audit stage not reached")
				}
				if operation == "create" {
					found, readErr := models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact("uncertain-created")).Exists(t.Context())
					if readErr != nil || found != committed {
						t.Fatal("create outcome not atomic", readErr)
					}
				} else {
					found, readErr := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(f.user.ID)).Exists(t.Context())
					if readErr != nil || found == committed {
						t.Fatal("delete outcome not atomic", readErr)
					}
					for index, id := range f.ids {
						_, found, readErr := f.runtime.SessionStore().Load(t.Context(), id)
						if readErr != nil || found != (!committed || index%3 != 0) {
							t.Fatal("delete session outcome", readErr)
						}
					}
				}
			})
		}
	}
	t.Run("effective_grant_union_limit_is_atomic", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		var group models.Group
		var extra models.Permission
		if err := backend.CoordinatedAtomic(t.Context(), func(session db.Session) error {
			var err error
			group, err = models.GroupObjects.Create(t.Context(), session, models.NewGroupCreate("At grant limit"))
			if err != nil {
				return err
			}
			for index := range auth.MaximumPermissions + 1 {
				permission, err := models.PermissionObjects.Create(t.Context(), session, models.NewPermissionCreate(fmt.Sprintf("bounded.permission_%03d", index), "Bounded"))
				if err != nil {
					return err
				}
				if index == auth.MaximumPermissions {
					extra = permission
					continue
				}
				if _, err := models.GroupPermissionsLinkObjects.Create(t.Context(), session, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		manager := f.manager(t, f.runtime)
		atLimit, err := manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("at-limit", "at-limit").WithGroups(group.ID), managementOldPassword)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := f.runtime.Authenticator().Resolve(t.Context(), atLimit.PrincipalID)
		if err != nil || len(credential.Principal().Permissions()) != auth.MaximumPermissions {
			t.Fatal("exact bound rejected or truncated", err)
		}
		beforeHash := f.hasher.calls.Load()
		for _, create := range []bool{true, false} {
			var result identity.UserDetails
			if create {
				result, err = manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("over-limit", "over-limit").WithGroups(group.ID).WithPermissions(extra.ID), managementOldPassword)
			} else {
				result, err = manager.UpdateUser(t.Context(), f.actor, atLimit.ID, 1, identity.UserPatch{}.WithPermissions(extra.ID).WithFirstName("must rollback"))
			}
			if fields, ok := validation.Rejected(err); !ok || fields.ByField("permissions").Empty() || result.ID != 0 {
				t.Fatal("grant overflow not rejected", err)
			}
		}
		if f.hasher.calls.Load() != beforeHash {
			t.Fatal("over-limit creation hashed")
		}
		unchanged, err := manager.User(t.Context(), f.actor, atLimit.ID)
		if err != nil || unchanged.Revision != 1 || unchanged.FirstName != "" || len(unchanged.PermissionIDs) != 0 {
			t.Fatal("overflow partially changed user", err)
		}
	})
	t.Run("borrowed_delete_rollback_and_lifetime", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		policy := managementHost(t, backend)
		if _, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("restore child", f.user.ID)); err != nil {
			t.Fatal(err)
		}
		before := f.user
		var held db.RelationSession
		abort := errors.New("outer owner aborted")
		err := f.runtime.CoordinatedAtomicRelation(t.Context(), func(session db.RelationSession) error {
			held = session
			count, err := policy.AccountsUser.DeleteInSession(t.Context(), session, f.user)
			if err != nil {
				return err
			}
			if count < 2 {
				return errors.New("host closure omitted child")
			}
			return abort
		})
		if !errors.Is(err, abort) || !reflect.DeepEqual(f.user, before) {
			t.Fatal("borrowed delete escaped outer ownership", err)
		}
		if f.stored(t).ID != f.user.ID {
			t.Fatal("outer rollback lost user")
		}
		if count, err := hostmodels.NoteObjects.Using(backend).Count(t.Context()); err != nil || count != 1 {
			t.Fatal("outer rollback lost child", err)
		}
		if count, err := policy.AccountsUser.DeleteInSession(t.Context(), held, f.user); err == nil || count != 0 {
			t.Fatal("expired borrowed delete executed")
		}
	})
	t.Run("read_scope_failure_never_publishes_user_or_page", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		for _, mode := range []string{"read_end_failure", "read_zero", "read_nil", "read_twice", "read_swallowed_failure"} {
			boundary := &managementBoundary{ManagementBackend: f.runtime, mode: mode}
			manager := f.manager(t, boundary)
			if value, err := manager.User(t.Context(), f.actor, f.user.ID); err == nil || value.ID != 0 {
				t.Fatal("failed snapshot published user", mode)
			}
			if value, err := manager.Users(t.Context(), f.actor, 0, 10); err == nil || len(value.Users) != 0 || value.Total != 0 {
				t.Fatal("failed snapshot published page", mode)
			}
		}
	})
	t.Run("competing_creators_and_editors", func(t *testing.T) {
		first, second := open(t)
		f := newManagementFixture(t, first, 0)
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
				_, err := manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("one-owner", "one-owner"), managementOldPassword)
				results <- err
			}()
		}
		for range 2 {
			select {
			case <-arrived:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("hash retained scope or did not finish preflight")
			}
		}
		close(release)
		success, rejected := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				success++
			} else if fields, ok := validation.Rejected(err); ok && !fields.Empty() {
				rejected++
			} else {
				t.Fatal("creation competition", err)
			}
		}
		if success != 1 || rejected != 1 || f.hasher.calls.Load() != 2 {
			t.Fatal("multiple owners or retry")
		}
		start := make(chan struct{})
		results = make(chan error, 2)
		for _, backend := range []identity.ManagementBackend{f.runtime, other} {
			manager := f.manager(t, backend)
			go func() {
				<-start
				_, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithFirstName("one edit"))
				results <- err
			}()
		}
		close(start)
		success, rejected = 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				success++
			} else if errors.Is(err, &identity.Error{Code: identity.CodeConflict}) {
				rejected++
			} else {
				t.Fatal("update competition", err)
			}
		}
		if success != 1 || rejected != 1 || f.stored(t).Revision != 2 {
			t.Fatal("both revisions committed")
		}
	})
}

type userManagementBoundary struct {
	identity.ManagementBackend
	mode          string
	calls, faults int
	afterCommit   func()
}

func (b *userManagementBoundary) CoordinatedAtomicRelation(ctx context.Context, callback func(db.RelationSession) error) error {
	b.calls++
	if b.mode == "write_zero" {
		return nil
	}
	owner, ok := b.ManagementBackend.(db.CoordinatedRelationAtomic)
	if !ok {
		return errors.New("missing native relation owner")
	}
	err := owner.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error {
		if b.mode == "write_nil" {
			return callback(nil)
		}
		if b.mode == "write_swallowed_failure" {
			_ = callback(&userManagementSession{RelationSession: session, owner: b})
			return nil
		}
		if err := callback(&userManagementSession{RelationSession: session, owner: b}); err != nil {
			return err
		}
		if b.mode == "write_twice" {
			return callback(session)
		}
		if b.mode == "callback_cancel" {
			return context.Canceled
		}
		if b.mode == "unknown_rollback" {
			return &query.Error{Code: query.CodeTransactionOutcomeUnknown, Detail: "private-user-management-fault"}
		}
		return nil
	})
	if err != nil {
		if b.mode == "rejected_cleanup" {
			b.faults++
			return errors.Join(err, errors.New("private-user-management-cleanup-fault"))
		}
		return err
	}
	if b.mode == "unknown_commit" {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-user-management-fault"}
	}
	if b.mode == "late_cancel" {
		b.afterCommit()
	}
	return nil
}
func (b *userManagementBoundary) AppendAudit(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
	if b.mode == "audit_failure" {
		b.faults++
		return errors.New("private-user-management-fault")
	}
	return b.ManagementBackend.AppendAudit(ctx, session, event)
}

type userManagementSession struct {
	db.RelationSession
	owner *userManagementBoundary
}

func (s *userManagementSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if s.owner.mode == "write_swallowed_failure" {
		return nil, errors.New("private-user-management-query-fault")
	}
	return s.RelationSession.Query(ctx, plan)
}

func (s *userManagementSession) ValidateSession(ctx context.Context) error {
	return s.RelationSession.(db.SessionValidator).ValidateSession(ctx)
}
func (s *userManagementSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if s.owner.mode == "update_failure" {
		s.owner.faults++
		return 0, errors.New("private-user-management-fault")
	}
	return s.RelationSession.Update(ctx, plan)
}
func (s *userManagementSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	if s.owner.mode == "membership_failure" {
		s.owner.faults++
		return db.ConflictInsertResult{}, errors.New("private-user-management-fault")
	}
	return s.RelationSession.(db.ConflictInserter).InsertOnConflict(ctx, plan)
}
