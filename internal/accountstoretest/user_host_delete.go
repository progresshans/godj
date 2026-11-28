package accountstoretest

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	hostproject "github.com/progresshans/godj/conformance/identityfixture/project"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
)

func runUserHostDeletion(add registerCase, open Open) {
	add("user_host_delete", func(t *testing.T) {
		backend, reopen := open(t)
		userHostDeletion(t, backend, reopen)
	})
}

func userDeletedGraph(before userReferenceState, id, noteID int64, revoked map[int64]bool) userReferenceState {
	before.account.users = slices.DeleteFunc(slices.Clone(before.account.users), func(row models.User) bool { return row.ID == id })
	before.account.memberships = slices.DeleteFunc(slices.Clone(before.account.memberships), func(row models.UserGroupsLink) bool { return row.SourceID == id })
	before.account.direct = slices.DeleteFunc(slices.Clone(before.account.direct), func(row models.UserPermissionsLink) bool { return row.SourceID == id })
	before.account.sessions = slices.DeleteFunc(slices.Clone(before.account.sessions), func(row sessionmodels.Session) bool { return revoked[row.ID] })
	before.notes = slices.DeleteFunc(slices.Clone(before.notes), func(row hostmodels.Note) bool { return row.ID == noteID })
	return before
}

func userHostDeletion(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionUserReference(t, backend)
	actor := f.actor.Principal()
	target := roleTarget(t, backend, f.base, "host-deletion-target")
	if _, err := models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(target.ID, f.base.group.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(target.ID, f.base.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	note, err := hostmodels.NoteObjects.Create(t.Context(), backend, hostmodels.NewNoteCreate("target cascade", target.ID))
	if err != nil {
		t.Fatal(err)
	}
	guard, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(target.ID))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := f.runtime.Authenticator().Resolve(t.Context(), target.PrincipalID)
	if err != nil {
		t.Fatal(err)
	}
	passwordSession(t, f.runtime.Sessions(), f.actor, "unrelated actor")
	if _, err := f.runtime.Sessions().Create(t.Context(), map[string]string{"anonymous": "preserved"}); err != nil {
		t.Fatal(err)
	}
	revoked := map[int64]bool{}
	var records []sessions.Record
	for _, marker := range []string{"first", "second", "third"} {
		before := userReferenceSnapshot(t, reopen(t))
		record := passwordSession(t, f.runtime.Sessions(), credential, marker)
		key := userReferenceSession(t, f.runtime, credential, record.ID(), before, userReferenceSnapshot(t, reopen(t)), actor)
		revoked[key] = true
		records = append(records, record)
	}
	if !t.Run("protect", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		result, err := f.runtime.Accounts().Deleter().Delete(t.Context(), actor, target.ID, 1)
		if result != (accountmodel.AccountDeletion{}) {
			t.Fatal("protected deletion published metadata")
		}
		requireProtectedDeletionInput(t, err, 1)
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
	}) {
		return
	}
	if _, err := hostmodels.GuardObjects.Delete(t.Context(), backend, &guard); err != nil {
		t.Fatal(err)
	}
	if !t.Run("audit_rollback", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		fault := errors.New("private selected host deletion audit failure")
		owner := &profileFaultBackend{Backend: backend, mode: "host_audit_failure", fault: fault}
		effects := map[string]int{}
		owner.decorate = func(scope db.Session) db.Session {
			return newRoleFaultScope(t, scope, "audit_failure", fault, effects)
		}
		runtime := userReferenceRuntime(t, owner, f.base.hasher)
		result, err := runtime.Accounts().Deleter().Delete(t.Context(), actor, target.ID, 1)
		if result != (accountmodel.AccountDeletion{}) || !errors.Is(err, fault) || owner.calls != 1 || effects["godj_sessions_session"] == 0 || effects["godj_account_audit_event"] != 1 {
			t.Fatal("host deletion failure missed its actual session/audit writes", err)
		}
		requireProfileCode(t, err, accountmodel.CodePersistence)
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
	}) {
		return
	}
	policy, err := hostproject.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	var held db.RelationSession
	if !t.Run("borrowed_rollback", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		modelBefore := target
		abort := errors.New("outer host deletion owner aborted")
		visits := 0
		err := backend.CoordinatedAtomicRelation(t.Context(), func(scope db.RelationSession) error {
			visits++
			held = scope
			count, err := policy.AccountsUser.DeleteInSession(t.Context(), scope, target)
			if err != nil {
				return err
			}
			if count != 4 {
				return errors.New("borrowed deletion omitted or added host graph rows")
			}
			// Inspect the real deletion before the outer owner rejects it.
			// Plain borrowed ORM deletion has no account/session/audit authority.
			assertUserReferenceState(t, userDeletedGraph(before, target.ID, note.ID, nil), userReferenceRows(t, scope), actor)
			return abort
		})
		if !errors.Is(err, abort) || visits != 1 || !reflect.DeepEqual(target, modelBefore) {
			t.Fatal("borrowed deletion escaped outer ownership", err)
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
	}) {
		return
	}
	if !t.Run("expired_scope", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		count, err := policy.AccountsUser.DeleteInSession(t.Context(), held, target)
		if count != 0 || !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
			t.Fatal("expired borrowed deletion was usable", err)
		}
		assertUserReferenceState(t, before, userReferenceSnapshot(t, reopen(t)), actor)
	}) {
		return
	}
	if !t.Run("unknown_commit", func(t *testing.T) {
		before := userReferenceSnapshot(t, reopen(t))
		owner := &profileFaultBackend{Backend: backend, mode: "unknown_commit"}
		runtime := userReferenceRuntime(t, owner, f.base.hasher)
		result, err := runtime.Accounts().Deleter().Delete(t.Context(), actor, target.ID, 1)
		if result != (accountmodel.AccountDeletion{}) || owner.calls != 1 || owner.nativeError != nil || !errors.Is(err, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
			t.Fatal("uncertain host deletion published, retried or did not commit", err)
		}
		requireProfileCode(t, err, accountmodel.CodeOutcomeUnknown)
		assertUserReferenceState(t, userDeletedGraph(before, target.ID, note.ID, revoked), userReferenceSnapshot(t, reopen(t)), actor, catalogExpectedEvent{"user", target.ID, admin.ActionDelete, nil})
		for _, record := range records {
			accountSessionRecord(t, f.runtime.SessionStore(), record, false)
		}
		if _, err := f.runtime.Authenticator().Resolve(t.Context(), target.PrincipalID); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatal("uncertain committed deletion still authenticates", err)
		}
	}) {
		return
	}
}
