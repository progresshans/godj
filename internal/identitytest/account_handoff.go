package identitytest

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
	domainmodels "github.com/progresshans/godj/account/domain/models"
	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	sessiondb "github.com/progresshans/godj/sessions/database"
	sessionmodels "github.com/progresshans/godj/sessions/database/models"
	"github.com/progresshans/godj/systemstate"
)

var handoffFailure = errors.New("private handoff boundary failure")
var handoffAt = time.Date(2026, 11, 6, 1, 2, 3, 456789000, time.UTC)

type handoffFixture struct {
	*managementFixture
	group      models.Group
	permission models.Permission
	grant      models.GroupPermissionsLink
	binding    accountmodel.Binding[models.User]
	config     systemstate.IdentityHandoffConfig
	owner      *systemstate.IdentityHandoff
}

func handoffMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newHandoffFixture(t *testing.T, backend TransitionBackend) *handoffFixture {
	t.Helper()
	f := newManagementFixture(t, backend, 3)
	sources := append(systemstate.IdentityMigrationSources(), domain.MigrationSources()...)
	sources = append(sources, accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
	applyIdentitySources(t, backend, sources...)
	binding, err := project.BindAccount()
	handoffMust(t, err)
	group, err := models.GroupObjects.Create(t.Context(), backend, models.NewGroupCreate("Handoff reviewers"))
	handoffMust(t, err)
	permission, err := models.PermissionObjects.Create(t.Context(), backend, models.NewPermissionCreate("helpdesk.ticket.change", "Change ticket"))
	handoffMust(t, err)
	grant, err := models.GroupPermissionsLinkObjects.Create(t.Context(), backend, models.NewGroupPermissionsLinkCreate(group.ID, permission.ID))
	handoffMust(t, err)
	_, err = models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(f.user.ID, group.ID))
	handoffMust(t, err)
	event, err := admin.PrepareEvent(f.actor.ID(), "tickets.ticket", 7, admin.ActionChange, []string{"title"}, "Original historical label")
	handoffMust(t, err)
	handoffMust(t, f.runtime.CoordinatedAtomic(t.Context(), func(scope db.Session) error { return f.runtime.AppendAudit(t.Context(), scope, event) }))
	config := systemstate.IdentityHandoffConfig{Permission: "accounts.handoff", Authorizer: auth.PrincipalAuthorizer{}, MaxAccounts: 32, MaxCatalogRows: 32, MaxSessions: 512, AuditCapacity: 512, Now: func() time.Time { return handoffAt }}
	owner, err := systemstate.NewIdentityHandoff(backend, binding, config)
	handoffMust(t, err)
	selected, err := project.NewAccountSessionStore(backend, sessiondb.Config{MaxRecords: 512})
	handoffMust(t, err)
	if inspection, err := selected.InspectStartup(t.Context()); !errors.Is(err, &accountmodel.Error{Code: accountmodel.CodeTransitionRequired}) || inspection.Ready() || inspection.Fresh() {
		t.Fatal("installing selected storage silently adopted historical identity", err)
	}
	return &handoffFixture{f, group, permission, grant, binding, config, owner}
}

func handoffState(t *testing.T, backend TransitionBackend) []any {
	t.Helper()
	result, err := db.ReadSnapshotResult(t.Context(), backend, func(_ context.Context, scope db.Queryer) ([]any, error) {
		sessionRows, audit := snapshotIdentitySystemRows(t, scope)
		return []any{
			catalogRows(t, models.UserObjects.Using(scope).OrderBy(models.UserFields.ID.Asc())), catalogRows(t, models.GroupObjects.Using(scope).OrderBy(models.GroupFields.ID.Asc())), catalogRows(t, models.PermissionObjects.Using(scope).OrderBy(models.PermissionFields.ID.Asc())),
			catalogRows(t, models.UserGroupsLinkObjects.Using(scope).OrderBy(models.UserGroupsLinkFields.ID.Asc())), catalogRows(t, models.UserPermissionsLinkObjects.Using(scope).OrderBy(models.UserPermissionsLinkFields.ID.Asc())), catalogRows(t, models.GroupPermissionsLinkObjects.Using(scope).OrderBy(models.GroupPermissionsLinkFields.ID.Asc())), sessionRows, audit,
		}, nil
	})
	handoffMust(t, err)
	return result
}

func handoffMarker(t *testing.T, backend db.Queryer) []any {
	t.Helper()
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	fields := []query.FieldRef{id}
	for _, name := range []string{"principal_id", "username", "encoded_password"} {
		fields = append(fields, query.NewFieldRef(name, name, query.FieldString, false))
	}
	fields = append(fields, query.NewFieldRef("active", "active", query.FieldBoolean, false), query.NewFieldRef("permissions", "permissions", query.FieldString, false), query.NewFieldRef("definition_digest", "definition_digest", query.FieldString, false))
	rows, err := backend.Query(t.Context(), query.NewPlan("godj_system_credential", fields))
	handoffMust(t, err)
	defer rows.Close()
	var key int64
	var principal, login, password, permissions, digest string
	var active bool
	if !rows.Next() {
		t.Fatal("legacy marker missing")
	}
	handoffMust(t, rows.Scan(&key, &principal, &login, &password, &active, &permissions, &digest))
	if rows.Next() {
		t.Fatal("legacy marker duplicated")
	}
	handoffMust(t, rows.Err())
	handoffMust(t, rows.Close())
	return []any{key, principal, login, password, active, permissions, digest}
}

type handoffBackend struct {
	TransitionBackend
	mode           string
	writes, faults int
	afterMarker    func(context.Context, db.Session) error
}

func (backend *handoffBackend) ReadSnapshot(ctx context.Context, read func(db.Queryer) error) error {
	if backend.mode == "read_missing" {
		return nil
	}
	err := backend.TransitionBackend.ReadSnapshot(ctx, read)
	if err == nil && backend.mode == "read_cleanup" {
		backend.faults++
		return handoffFailure
	}
	return err
}
func (backend *handoffBackend) CoordinatedAtomic(ctx context.Context, write func(db.Session) error) error {
	backend.writes++
	if backend.mode == "write_missing" {
		return nil
	}
	err := backend.TransitionBackend.CoordinatedAtomic(ctx, func(scope db.Session) error {
		wrapped := &handoffScope{Session: scope, owner: backend}
		if err := write(wrapped); err != nil {
			return err
		}
		if backend.mode == "write_twice" {
			return write(wrapped)
		}
		if backend.mode == "rollback" || backend.mode == "unknown_rollback" {
			backend.faults++
			return handoffFailure
		}
		return nil
	})
	if err == nil && backend.mode == "unknown_commit" || backend.mode == "unknown_rollback" && errors.Is(err, handoffFailure) {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown, Cause: handoffFailure}
	}
	return err
}

type handoffScope struct {
	db.Session
	owner *handoffBackend
}

func (scope *handoffScope) ValidateSession(ctx context.Context) error {
	return scope.Session.(db.SessionValidator).ValidateSession(ctx)
}
func (scope *handoffScope) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	key, err := scope.Session.Insert(ctx, plan)
	if err == nil && (scope.owner.mode == "header_insert" && plan.Table() == "godj_account_domain_transition" || scope.owner.mode == "mapping_insert" && plan.Table() == "godj_account_domain_mapping") {
		scope.owner.faults++
		return 0, handoffFailure
	}
	return key, err
}
func (scope *handoffScope) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	changed, err := scope.Session.Update(ctx, plan)
	if err == nil && plan.Table() == "godj_system_credential" {
		if scope.owner.mode == "marker_update" {
			scope.owner.faults++
			return 0, handoffFailure
		}
		if scope.owner.afterMarker != nil {
			scope.owner.faults++
			err = scope.owner.afterMarker(ctx, scope.Session)
		}
	}
	return changed, err
}

// Every refusal is exercised against native storage and compared with the
// independent complete identity/session/audit snapshot, not just an error code.
func RunAccountHandoff(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"lifecycle", "owned_manifest", "signed_keys", "foreign_namespace", "inactive_actor", "revoked_actor", "denied_overlay", "changed_account", "changed_grant", "changed_session", "changed_audit", "corrupt_session", "corrupt_audit", "account_limit", "session_limit", "audit_limit", "read_missing", "read_cleanup", "write_missing", "write_twice", "header_insert", "mapping_insert", "marker_update", "late_account", "late_archive", "late_marker", "late_cancel", "rollback", "unknown_commit", "unknown_rollback", "cancelled", "manifest_tamper"} {
		t.Run(mode, func(t *testing.T) {
			backend, second := open(t)
			f := newHandoffFixture(t, backend)
			boundary := &handoffBackend{TransitionBackend: backend, mode: mode}
			config := f.config
			if mode == "denied_overlay" {
				config.Authorizer = handoffDeny{}
			}
			if mode == "account_limit" {
				config.MaxAccounts = 1
			}
			if mode == "session_limit" {
				config.MaxSessions = 2
			}
			if mode == "audit_limit" {
				config.AuditCapacity = 1
				event, err := admin.PrepareEvent(f.actor.ID(), "tickets.ticket", 7, admin.ActionAdd, nil, "Second historical event")
				handoffMust(t, err)
				handoffMust(t, f.runtime.CoordinatedAtomic(t.Context(), func(scope db.Session) error { return f.runtime.AppendAudit(t.Context(), scope, event) }))
			}
			if mode == "signed_keys" {
				groupLinks, err := models.UserGroupsLinkObjects.Using(backend).Filter(models.UserGroupsLinkFields.SourceID.Exact(f.user.ID)).All(t.Context())
				handoffMust(t, err)
				for _, row := range groupLinks {
					count, err := models.UserGroupsLinkObjects.Delete(t.Context(), backend, &row)
					handoffMust(t, err)
					if count != 1 {
						t.Fatal("remove fixture group link")
					}
				}
				permissionLinks, err := models.UserPermissionsLinkObjects.Using(backend).Filter(models.UserPermissionsLinkFields.SourceID.Exact(f.user.ID)).All(t.Context())
				handoffMust(t, err)
				for _, row := range permissionLinks {
					count, err := models.UserPermissionsLinkObjects.Delete(t.Context(), backend, &row)
					handoffMust(t, err)
					if count != 1 {
						t.Fatal("remove fixture direct link")
					}
				}
				id := query.NewFieldRef("id", "id", query.FieldInteger, false)
				count, err := backend.Update(t.Context(), query.NewUpdatePlan("godj_identity_user", []query.Assignment{query.NewAssignment(id, query.Integer(0))}, id, query.Integer(f.user.ID)))
				handoffMust(t, err)
				if count != 1 {
					t.Fatal("signed key fixture")
				}
				f.user, err = models.UserObjects.Using(backend).Filter(models.UserFields.PrincipalID.Exact(f.user.PrincipalID)).Get(t.Context())
				handoffMust(t, err)
				_, err = models.UserGroupsLinkObjects.Create(t.Context(), backend, models.NewUserGroupsLinkCreate(0, f.group.ID))
				handoffMust(t, err)
				negative, err := models.UserObjects.Create(t.Context(), backend, models.NewUserCreate("negative-principal", "negative", "!unusable", handoffAt))
				handoffMust(t, err)
				count, err = backend.Update(t.Context(), query.NewUpdatePlan("godj_identity_user", []query.Assignment{query.NewAssignment(id, query.Integer(-7))}, id, query.Integer(negative.ID)))
				handoffMust(t, err)
				if count != 1 {
					t.Fatal("negative key fixture")
				}
			}
			if mode == "foreign_namespace" {
				database, err := sessiondb.New(backend, sessiondb.Config{MaxRecords: 512})
				handoffMust(t, err)
				store, err := database.Store("unrelated-application")
				handoffMust(t, err)
				manager, err := sessions.NewManager(store, sessions.Config{})
				handoffMust(t, err)
				_, err = manager.Create(t.Context(), map[string]string{"preserve": "foreign"})
				handoffMust(t, err)
			}
			if mode == "corrupt_session" || mode == "corrupt_audit" {
				table, field := "godj_system_session", "payload"
				if mode == "corrupt_audit" {
					table, field = "godj_system_audit", "changed_fields"
				}
				id := query.NewFieldRef("id", "id", query.FieldInteger, false)
				_, err := backend.Update(t.Context(), query.NewUpdatePlan(table, []query.Assignment{query.NewAssignment(query.NewFieldRef(field, field, query.FieldString, false), query.String("malformed"))}, id, query.Integer(1)))
				handoffMust(t, err)
			}
			owner, err := systemstate.NewIdentityHandoff(boundary, f.binding, config)
			handoffMust(t, err)
			before, marker := handoffState(t, backend), handoffMarker(t, backend)
			prepared, err := owner.Prepare(t.Context(), f.actor, strings.Repeat("a", 64))
			early := mode == "denied_overlay" || mode == "account_limit" || mode == "session_limit" || mode == "audit_limit" || mode == "corrupt_session" || mode == "corrupt_audit" || mode == "read_missing" || mode == "read_cleanup"
			if early {
				if err == nil || prepared.Manifest().OperationID != "" || boundary.writes != 0 || !reflect.DeepEqual(before, handoffState(t, backend)) || !reflect.DeepEqual(marker, handoffMarker(t, backend)) {
					t.Fatal("rejected preparation published or changed durable state", err)
				}
				return
			}
			handoffMust(t, err)
			manifest := prepared.Manifest()
			wantAccounts := 2
			if mode == "signed_keys" {
				wantAccounts = 3
			}
			if manifest.Origin.SessionRecords != 3 || manifest.Origin.AuditEvents != 1 || len(manifest.Accounts) != wantAccounts || len(manifest.Catalog.Groups) != 1 || len(manifest.Catalog.Permissions) != 2 {
				t.Fatal("handoff omitted a reviewed inventory")
			}
			if mode == "owned_manifest" {
				changed := prepared.Manifest()
				changed.Accounts[0].TargetID = 999
				changed.Catalog.Groups[0].TargetRevision = 99
				changed.Origin.OwnerSHA256 = "changed"
			}
			switch mode {
			case "inactive_actor":
				_, err = models.UserObjects.Patch(t.Context(), second, f.root, models.UserPatch{}.WithActive(false).WithRevision(f.root.Revision+1))
			case "revoked_actor":
				_, err = models.UserObjects.Patch(t.Context(), second, f.root, models.UserPatch{}.WithSuperuser(false).WithRevision(f.root.Revision+1))
			case "changed_account":
				_, err = models.UserObjects.Patch(t.Context(), second, f.user, models.UserPatch{}.WithFirstName("unreviewed"))
			case "changed_grant":
				_, err = models.GroupPermissionsLinkObjects.Delete(t.Context(), second, &f.grant)
			case "changed_session":
				err = f.runtime.SessionStore().Delete(t.Context(), f.ids[0])
			case "changed_audit":
				event, failure := admin.PrepareEvent(f.actor.ID(), "tickets.ticket", 7, admin.ActionDelete, nil, "unreviewed")
				handoffMust(t, failure)
				err = f.runtime.CoordinatedAtomic(t.Context(), func(scope db.Session) error { return f.runtime.AppendAudit(t.Context(), scope, event) })
			}
			handoffMust(t, err)
			before, marker = handoffState(t, backend), handoffMarker(t, backend)
			if mode == "late_account" {
				boundary.afterMarker = func(ctx context.Context, scope db.Session) error {
					_, err := models.UserObjects.Patch(ctx, scope, f.user, models.UserPatch{}.WithFirstName("unreviewed late value"))
					return err
				}
			}
			if mode == "late_archive" {
				boundary.afterMarker = func(ctx context.Context, scope db.Session) error {
					_, err := scope.Update(ctx, query.NewUpdatePlan("godj_system_audit", []query.Assignment{query.NewAssignment(query.NewFieldRef("display_label", "display_label", query.FieldString, false), query.String("unreviewed late label"))}, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(1)))
					return err
				}
			}
			if mode == "late_marker" {
				boundary.afterMarker = func(ctx context.Context, scope db.Session) error {
					_, err := scope.Update(ctx, query.NewUpdatePlan("godj_system_credential", []query.Assignment{query.NewAssignment(query.NewFieldRef("active", "active", query.FieldBoolean, false), query.Boolean(true))}, query.NewFieldRef("id", "id", query.FieldInteger, false), query.Integer(marker[0].(int64))))
					return err
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "late_cancel" {
				boundary.afterMarker = func(context.Context, db.Session) error { cancel(); return nil }
			}
			result, failure := owner.Commit(ctx, f.actor, prepared)
			success := mode == "lifecycle" || mode == "owned_manifest" || mode == "signed_keys" || mode == "foreign_namespace" || mode == "manifest_tamper"
			committed := success || mode == "unknown_commit"
			if success {
				handoffMust(t, failure)
			} else if failure == nil || result.Sequence() != 0 {
				t.Fatal("incomplete handoff published a receipt", failure)
			}
			if mode == "unknown_commit" || mode == "unknown_rollback" {
				if !errors.Is(failure, &query.Error{Code: query.CodeCommitOutcomeUnknown}) {
					t.Fatal("unknown result was reclassified", failure)
				}
			}
			if mode == "header_insert" || mode == "mapping_insert" || mode == "marker_update" || strings.HasPrefix(mode, "late_") {
				if boundary.faults != 1 {
					t.Fatal("post-write control was not reached", mode, boundary.faults, failure)
				}
			}
			if !reflect.DeepEqual(before, handoffState(t, backend)) {
				t.Fatal("handoff changed user/catalog/through or historical session/audit bytes")
			}
			if !committed {
				if !reflect.DeepEqual(marker, handoffMarker(t, backend)) {
					t.Fatal("failed handoff retained retirement")
				}
				count, err := domainmodels.TransitionObjects.Using(backend).Count(t.Context())
				handoffMust(t, err)
				payloads, err := domainmodels.MappingObjects.Using(backend).Count(t.Context())
				handoffMust(t, err)
				if count != 0 || payloads != 0 {
					t.Fatal("failed handoff retained a partial selected domain")
				}
				if mode == "unknown_rollback" {
					reopened, err := systemstate.NewIdentityHandoff(second, f.binding, f.config)
					handoffMust(t, err)
					if actual, found, err := reopened.Reconcile(t.Context(), manifest); err != nil || found || actual.Sequence() != 0 {
						t.Fatal("rolled-back unknown handoff acquired a durable receipt", err)
					}
				}
				return
			}
			reopened, err := systemstate.NewIdentityHandoff(second, f.binding, f.config)
			handoffMust(t, err)
			receipt, found, err := reopened.Reconcile(t.Context(), manifest)
			handoffMust(t, err)
			if !found || receipt.Sequence() != 1 || receipt.MappingCount() != wantAccounts || success && receipt.OperationID() != result.OperationID() {
				t.Fatal("durable handoff does not reconcile")
			}
			store, err := project.NewAccountSessionStore(second, sessiondb.Config{MaxRecords: 512})
			handoffMust(t, err)
			inspection, err := store.InspectStartup(t.Context())
			handoffMust(t, err)
			if !inspection.Ready() || inspection.Fresh() {
				t.Fatal("handoff was reopened as fresh")
			}
			for _, id := range f.ids {
				_, present, err := store.Load(t.Context(), id)
				handoffMust(t, err)
				if present {
					t.Fatal("historical session crossed the new domain")
				}
			}
			if _, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID); err == nil {
				t.Fatal("retained historical authenticator survived handoff")
			}
			if _, err := f.runtime.Authenticator().Authenticate(t.Context(), f.user.Username, managementOldPassword); err == nil {
				t.Fatal("retained historical login survived handoff")
			}
			if _, _, err := f.runtime.SessionStore().Load(t.Context(), f.ids[0]); err == nil {
				t.Fatal("retained historical session owner survived handoff")
			}
			called := false
			if err := f.runtime.CoordinatedAtomic(t.Context(), func(db.Session) error { called = true; return nil }); err == nil || called {
				t.Fatal("retired runtime admitted a writer")
			}
			event, err := admin.PrepareEvent(f.actor.ID(), "tickets.ticket", 7, admin.ActionChange, nil, "must not append")
			handoffMust(t, err)
			handoffMust(t, second.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
				if err := f.runtime.AppendAudit(t.Context(), scope, event); err == nil {
					t.Fatal("borrowed writer changed the retired archive")
				}
				if _, err := f.runtime.RevokePrincipalSessions(t.Context(), scope, f.user.PrincipalID); err == nil {
					t.Fatal("borrowed writer revoked retired sessions")
				}
				return nil
			}))
			if err := f.runtime.SessionStore().Delete(t.Context(), f.ids[0]); err == nil {
				t.Fatal("retired session owner deleted historical bytes")
			}
			if !reflect.DeepEqual(before, handoffState(t, backend)) {
				t.Fatal("retired writer changed the reviewed archive")
			}
			if _, err := systemstate.OpenIdentity(t.Context(), second, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher}); !errors.Is(err, &systemstate.Error{Code: systemstate.CodeIdentityTransitionRequired}) {
				t.Fatal("retired runtime reopened", err)
			}
			authenticator, err := project.NewAccountAuthenticator(t.Context(), second, f.hasher)
			handoffMust(t, err)
			credential, err := authenticator.Authenticate(t.Context(), f.user.Username, managementOldPassword)
			handoffMust(t, err)
			if _, bound := credential.Principal().CredentialDomain(); !bound {
				t.Fatal("adopted account has no selected domain")
			}
			if mode != "signed_keys" && (!credential.Principal().Has("helpdesk.ticket.view") || !credential.Principal().Has("helpdesk.ticket.change")) {
				t.Fatal("adopted credential lost direct or group grants")
			}
			handoffMust(t, second.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
				entries, err := reopened.AuditHistoryIn(t.Context(), scope, manifest, "tickets.ticket", 7, 10)
				if err == nil && (len(entries) != 1 || entries[0].DisplayLabel != "Original historical label" || entries[0].Sequence != 1) {
					t.Fatal("archive reader rewrote history")
				}
				return err
			}))
			if replay, err := owner.Commit(t.Context(), f.actor, prepared); err == nil || replay.Sequence() != 0 {
				t.Fatal("prepared handoff was reused")
			}
			if mode == "manifest_tamper" {
				changed := manifest
				changed.Origin.AuditSHA256 = strings.Repeat("b", 64)
				if value, found, err := reopened.Reconcile(t.Context(), changed); err == nil || found || value.Sequence() != 0 {
					t.Fatal("modified review evidence was accepted")
				}
			}
			if mode == "foreign_namespace" {
				count, err := sessionmodels.SessionObjects.Using(second).Count(t.Context())
				handoffMust(t, err)
				if count != 1 {
					t.Fatal("foreign session namespace was changed")
				}
			}
		})
	}
}

type handoffDeny struct{}

func (handoffDeny) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	return false, nil
}

func RunAccountHandoffConcurrency(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	first, second := open(t)
	f := newHandoffFixture(t, first)
	other, err := systemstate.NewIdentityHandoff(second, f.binding, f.config)
	handoffMust(t, err)
	owners := []*systemstate.IdentityHandoff{f.owner, other}
	prepared := make([]systemstate.PreparedIdentityHandoff, 2)
	for index, owner := range owners {
		prepared[index], err = owner.Prepare(t.Context(), f.actor, strings.Repeat(fmt.Sprint(index+1), 64))
		handoffMust(t, err)
	}
	before := handoffState(t, first)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, owner := range owners {
		go func() { <-start; _, err := owner.Commit(t.Context(), f.actor, prepared[index]); results <- err }()
	}
	close(start)
	succeeded := 0
	for range 2 {
		if err := <-results; err == nil {
			succeeded++
		} else if !errors.Is(err, &systemstate.Error{Code: systemstate.CodeHandoffConflict}) {
			t.Fatal("concurrent handoff failed outside its ownership fence", err)
		}
	}
	if succeeded != 1 || !reflect.DeepEqual(before, handoffState(t, second)) {
		t.Fatal("competing handoffs did not preserve one exact adoption")
	}
	found := 0
	for index, owner := range owners {
		_, present, err := owner.Reconcile(t.Context(), prepared[index].Manifest())
		handoffMust(t, err)
		if present {
			found++
		}
	}
	if found != 1 {
		t.Fatal("competing handoff receipts disagree")
	}
}

func RunOperatorAccountHandoff(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	backend, second := open(t)
	config := transitionLegacyFixture(t, backend)
	operator, err := systemstate.OpenExisting(t.Context(), backend, config.Expected)
	handoffMust(t, err)
	sources := append(systemstate.IdentityMigrationSources(), domain.MigrationSources()...)
	sources = append(sources, accountaudit.MigrationSources()...)
	sources = append(sources, sessiondb.MigrationSources()...)
	applyIdentitySources(t, backend, sources...)
	binding, err := project.BindAccount()
	handoffMust(t, err)
	owner, err := systemstate.NewIdentityHandoff(backend, binding, systemstate.IdentityHandoffConfig{Permission: "identity.user.view", Authorizer: auth.PrincipalAuthorizer{}, Now: func() time.Time { return handoffAt }})
	handoffMust(t, err)
	if prepared, err := owner.Prepare(t.Context(), config.Expected.CredentialPolicy.Principal, strings.Repeat("e", 64)); err == nil || prepared.Manifest().OperationID != "" {
		t.Fatal("raw operator was implicitly adopted")
	}
	adopted, err := systemstate.AdoptOperator(t.Context(), backend, config)
	handoffMust(t, err)
	legacy, err := systemstate.OpenIdentity(t.Context(), backend, systemstate.IdentityRuntimeConfig{PasswordHasher: config.Expected.CredentialPolicy.PasswordHasher})
	handoffMust(t, err)
	credential, err := legacy.Authenticator().Resolve(t.Context(), config.Expected.CredentialPolicy.Principal.ID())
	handoffMust(t, err)
	prepared, err := owner.Prepare(t.Context(), credential.Principal(), strings.Repeat("e", 64))
	handoffMust(t, err)
	if len(prepared.Manifest().Accounts) != 1 || prepared.Manifest().Accounts[0].SourceID != adopted.UserID() {
		t.Fatal("operator handoff lost the explicit native adoption")
	}
	before := handoffState(t, backend)
	_, err = owner.Commit(t.Context(), credential.Principal(), prepared)
	handoffMust(t, err)
	manager, err := sessions.NewManager(operator.SessionStore(), sessions.Config{})
	handoffMust(t, err)
	if _, err := manager.Create(t.Context(), map[string]string{"retired": "forbidden"}); err == nil {
		t.Fatal("retained operator session owner survived handoff")
	}
	if !reflect.DeepEqual(before, handoffState(t, second)) {
		t.Fatal("operator handoff rewrote account or historical state")
	}
	current, err := project.NewAccountAuthenticator(t.Context(), second, config.Expected.CredentialPolicy.PasswordHasher)
	handoffMust(t, err)
	selected, err := current.Authenticate(t.Context(), "legacy", "boundary password")
	handoffMust(t, err)
	if !selected.Principal().Has("identity.user.view") || selected.MatchesSessionStamp(credential.SessionStamp()) {
		t.Fatal("operator handoff lost grants or retained a historical stamp")
	}
}

func RunAccountHandoffArchive(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, mode := range []string{"pages", "edited_account", "later_domain", "expired_scope", "session_corruption", "audit_corruption", "marker_corruption", "origin_corruption"} {
		t.Run(mode, func(t *testing.T) {
			backend, second := open(t)
			f := newHandoffFixture(t, backend)
			if mode == "pages" {
				for range 257 {
					event, err := admin.PrepareEvent(f.actor.ID(), "tickets.ticket", 7, admin.ActionChange, []string{"title"}, "Paged history")
					handoffMust(t, err)
					handoffMust(t, f.runtime.CoordinatedAtomic(t.Context(), func(scope db.Session) error { return f.runtime.AppendAudit(t.Context(), scope, event) }))
				}
			}
			prepared, err := f.owner.Prepare(t.Context(), f.actor, strings.Repeat("f", 64))
			handoffMust(t, err)
			manifest := prepared.Manifest()
			if mode == "pages" && manifest.Origin.AuditEvents != 258 {
				t.Fatal("source archive was truncated to a page")
			}
			_, err = f.owner.Commit(t.Context(), f.actor, prepared)
			handoffMust(t, err)
			id := query.NewFieldRef("id", "id", query.FieldInteger, false)
			switch mode {
			case "edited_account":
				_, err = models.UserObjects.Patch(t.Context(), second, f.user, models.UserPatch{}.WithFirstName("Later profile").WithRevision(f.user.Revision+1))
			case "later_domain":
				change, failure := domain.PrepareOperation(t.Context(), strings.Repeat("d", 64), strings.Repeat("b", 64), strings.Repeat("c", 64))
				handoffMust(t, failure)
				err = second.CoordinatedAtomic(t.Context(), func(scope db.Session) error {
					previous, err := domain.InspectIn(t.Context(), scope)
					if err != nil {
						return err
					}
					_, err = domain.TransitionIn(t.Context(), scope, previous, change, handoffAt.Add(time.Second))
					return err
				})
			case "session_corruption":
				_, err = second.Update(t.Context(), query.NewUpdatePlan("godj_system_session", []query.Assignment{query.NewAssignment(query.NewFieldRef("payload", "payload", query.FieldString, false), query.String("malformed"))}, id, query.Integer(1)))
			case "audit_corruption":
				_, err = second.Update(t.Context(), query.NewUpdatePlan("godj_system_audit", []query.Assignment{query.NewAssignment(query.NewFieldRef("display_label", "display_label", query.FieldString, false), query.String("Changed historical label"))}, id, query.Integer(1)))
			case "marker_corruption":
				marker := handoffMarker(t, second)
				_, err = second.Update(t.Context(), query.NewUpdatePlan("godj_system_credential", []query.Assignment{query.NewAssignment(query.NewFieldRef("definition_digest", "definition_digest", query.FieldString, false), query.String("sha256:"+strings.Repeat("a", 64)))}, id, query.Integer(marker[0].(int64))))
			case "origin_corruption":
				rows, failure := domainmodels.MappingObjects.Using(second).All(t.Context())
				handoffMust(t, failure)
				if len(rows) != 1 {
					t.Fatal("origin payload missing")
				}
				_, err = second.Update(t.Context(), query.NewUpdatePlan("godj_account_domain_mapping", []query.Assignment{query.NewAssignment(query.NewFieldRef("payload", "payload", query.FieldString, false), query.String(rows[0].Payload+" "))}, id, query.Integer(rows[0].ID)))
			}
			handoffMust(t, err)
			owner, err := systemstate.NewIdentityHandoff(second, f.binding, f.config)
			handoffMust(t, err)
			receipt, found, err := owner.Reconcile(t.Context(), manifest)
			valid := mode == "pages" || mode == "edited_account" || mode == "later_domain" || mode == "expired_scope"
			if valid {
				handoffMust(t, err)
				if !found || receipt.Sequence() != 1 {
					t.Fatal("stable historical handoff could not reconcile")
				}
			} else if err == nil || found || receipt.Sequence() != 0 {
				t.Fatal("corrupted archive or origin was accepted")
			}
			var borrowed db.Queryer
			err = second.ReadSnapshot(t.Context(), func(scope db.Queryer) error {
				borrowed = scope
				entries, err := owner.AuditHistoryIn(t.Context(), scope, manifest, "tickets.ticket", 7, 10)
				if valid && (err != nil || len(entries) == 0) {
					t.Fatal("archive history missing", err)
				}
				if !valid && (err == nil || entries != nil) {
					t.Fatal("corrupted history was returned")
				}
				return nil
			})
			handoffMust(t, err)
			if mode == "marker_corruption" || mode == "origin_corruption" {
				store, failure := project.NewAccountSessionStore(second, sessiondb.Config{MaxRecords: 512})
				handoffMust(t, failure)
				if inspection, failure := store.InspectStartup(t.Context()); failure == nil || inspection.Ready() || inspection.Fresh() {
					t.Fatal("startup admitted corrupted handoff evidence")
				}
			}
			if mode == "expired_scope" {
				if entries, err := owner.AuditHistoryIn(t.Context(), borrowed, manifest, "tickets.ticket", 7, 10); err == nil || entries != nil {
					t.Fatal("expired archive scope published data")
				}
			}
		})
	}
}
