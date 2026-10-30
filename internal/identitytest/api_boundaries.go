package identitytest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	hostmodels "github.com/progresshans/godj/conformance/identityfixture/models"
	"github.com/progresshans/godj/identity"
	identityapi "github.com/progresshans/godj/identity/api"
	"github.com/progresshans/godj/identity/models"
)

func runManagementAPIPermissions(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, resource := range []struct {
		kind, singular            string
		view, add, change, remove auth.Permission
		body                      string
	}{
		{"users", "user", identity.ViewUser, identity.AddUser, identity.ChangeUser, identity.DeleteUser, `{"username":"created","password":"private new password"}`},
		{"groups", "group", identity.ViewGroup, identity.AddGroup, identity.ChangeGroup, identity.DeleteGroup, `{"name":"Created"}`},
		{"permissions", "permission", identity.ViewPermission, identity.AddPermission, identity.ChangePermission, identity.DeletePermission, `{"code":"fixture.created","name":"Created"}`},
	} {
		for _, role := range []string{"none", "view", "change", "add", "add_change", "delete"} {
			t.Run("admission_"+resource.kind+"_"+role, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 0)
				manager := f.manager(t, f.runtime)
				var target int64
				switch resource.kind {
				case "users":
					v, err := manager.CreateUser(t.Context(), f.actor, identity.NewUserCreate("target", "target"), managementOldPassword)
					if err != nil {
						t.Fatal(err)
					}
					target = v.ID
				case "groups":
					v, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Target"))
					if err != nil {
						t.Fatal(err)
					}
					target = v.ID
				case "permissions":
					v, err := manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("fixture.target", "Target"))
					if err != nil {
						t.Fatal(err)
					}
					target = v.ID
				}
				var grants []auth.Permission
				switch role {
				case "view":
					grants = []auth.Permission{resource.view}
				case "change":
					grants = []auth.Permission{resource.change}
				case "add":
					grants = []auth.Permission{resource.add}
				case "add_change":
					grants = []auth.Permission{resource.add, resource.change}
				case "delete":
					grants = []auth.Permission{resource.remove}
				}
				var keys []int64
				for _, grant := range grants {
					p, err := manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate(string(grant), "Test grant"))
					if err != nil {
						t.Fatal(err)
					}
					keys = append(keys, p.ID)
				}
				if _, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithPermissions(keys...).WithStaff(false)); err != nil {
					t.Fatal(err)
				}
				h, _ := newManagementHTTP(t, f, f.runtime)
				member := h.newClient(t)
				h.login(t, member, "member", managementOldPassword, 200)
				readStatus := http.StatusForbidden
				if role == "view" || role == "change" || role == "add_change" {
					readStatus = http.StatusOK
				}
				managementCall(t, h, member, "GET", identityapi.BasePath+resource.kind+"/", "", "", false, readStatus)
				managementCall(t, h, member, "GET", apiPath(resource.kind, target), "", "", false, readStatus)
				changeStatus := http.StatusForbidden
				if role == "change" || role == "add_change" {
					changeStatus = http.StatusOK
				}
				managementCall(t, h, member, "PATCH", apiPath(resource.kind, target), `{}`, "1", true, changeStatus)
				createStatus := http.StatusForbidden
				if role == "add_change" || role == "add" && resource.kind != "users" {
					createStatus = http.StatusCreated
				}
				managementCall(t, h, member, "POST", identityapi.BasePath+resource.kind+"/", resource.body, "", true, createStatus)
				deleteStatus := http.StatusForbidden
				if role == "delete" {
					deleteStatus = http.StatusNoContent
				}
				managementCall(t, h, member, "DELETE", apiPath(resource.kind, target), "", "1", true, deleteStatus)
				if role == "change" {
					// Revoke through the same manager; the existing cookie is not authority.
					if _, err := manager.UpdateUser(t.Context(), f.actor, f.user.ID, 2, identity.UserPatch{}.WithPermissions()); err != nil {
						t.Fatal(err)
					}
					managementCall(t, h, member, "GET", apiPath(resource.kind, target), "", "", false, 403)
				}
			})
		}
	}
}

func runManagementAPIFailures(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	for _, kind := range []string{"users", "groups", "permissions"} {
		for _, operation := range []string{"create", "update", "delete"} {
			for _, mode := range []string{"audit_failure", "unknown_rollback", "unknown_commit"} {
				t.Run("failure_"+kind+"_"+operation+"_"+mode, func(t *testing.T) {
					backend, _ := open(t)
					f := newManagementFixture(t, backend, 0)
					manager := f.manager(t, f.runtime)
					id := f.user.ID
					if kind == "groups" {
						p, err := manager.CreateGroup(t.Context(), f.actor, identity.NewGroupCreate("Target"))
						if err != nil {
							t.Fatal(err)
						}
						id = p.ID
					}
					if kind == "permissions" {
						p, err := manager.CreatePermission(t.Context(), f.actor, identity.NewPermissionCreate("fixture.target", "Target"))
						if err != nil {
							t.Fatal(err)
						}
						id = p.ID
					}
					boundary := &userManagementBoundary{ManagementBackend: f.runtime, mode: mode}
					h, _ := newManagementHTTP(t, f, boundary)
					before := catalogSnapshot(t, f)
					method, path, body, revision := "PATCH", apiPath(kind, id), `{"name":"Changed"}`, "1"
					if kind == "users" {
						body = `{"first_name":"Changed","active":false}`
					}
					if operation == "create" {
						method = "POST"
						path = identityapi.BasePath + kind + "/"
						revision = ""
						switch kind {
						case "users":
							body = `{"username":"created","password":"private new password"}`
						case "groups":
							body = `{"name":"Created"}`
						case "permissions":
							body = `{"code":"fixture.created","name":"Created"}`
						}
					}
					if operation == "delete" {
						method = "DELETE"
						body = ""
					}
					status := 500
					if strings.HasPrefix(mode, "unknown_") {
						status = 503
					}
					response, result := managementCall(t, h, h.client, method, path, body, revision, true, status)
					if boundary.calls != 1 {
						t.Fatal("HTTP write retried/not reached", boundary.calls)
					}
					if mode == "audit_failure" && boundary.faults != 1 {
						t.Fatal("audit fault not reached")
					}
					if response.Header.Get("Revision") != "" || response.Header.Get("Retry-After") != "" {
						t.Fatal("failed/unknown response published version or retry")
					}
					if mode != "audit_failure" && string(result["code"]) != `"outcome_unknown"` {
						t.Fatal("uncertainty classification lost")
					}
					changed := !reflect.DeepEqual(before, catalogSnapshot(t, f))
					if changed != (mode == "unknown_commit") {
						t.Fatal("HTTP outcome vs durable transaction", mode, changed)
					}
				})
			}
		}
	}
	for _, mode := range []string{"audit_failure", "unknown_rollback", "unknown_commit"} {
		t.Run("password_"+mode, func(t *testing.T) {
			backend, _ := open(t)
			f := newManagementFixture(t, backend, 3)
			boundary := &managementBoundary{ManagementBackend: f.runtime, mode: mode}
			h, _ := newManagementHTTP(t, f, boundary)
			status := 500
			if mode != "audit_failure" {
				status = 503
			}
			managementCall(t, h, h.client, "POST", apiPath("users", f.user.ID)+"password/", fmt.Sprintf(`{"password":%q}`, managementNewPassword), "1", true, status)
			if boundary.calls != 1 || f.hasher.calls.Load() != 1 {
				t.Fatal("password hashing/transaction was retried")
			}
			f.assertOutcome(t, mode == "unknown_commit")
		})
	}
	t.Run("protected_delete_cleanup_failure_is_not_input", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		managementHost(t, backend)
		if _, err := hostmodels.GuardObjects.Create(t.Context(), backend, hostmodels.NewGuardCreate(f.user.ID)); err != nil {
			t.Fatal(err)
		}
		fault := &userManagementBoundary{ManagementBackend: f.runtime, mode: "rejected_cleanup"}
		h, _ := newManagementHTTP(t, f, fault)
		before := catalogSnapshot(t, f)
		managementCall(t, h, h.client, "DELETE", apiPath("users", f.user.ID), "", "1", true, 500)
		if fault.calls != 1 || fault.faults != 1 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("cleanup error was rendered as input or protection lost")
		}
	})
	t.Run("password_profile_rejection_preserves_infrastructure_errors", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		h, _ := newManagementHTTP(t, f, f.runtime)
		before := catalogSnapshot(t, f)
		oversized := strings.Repeat("x", 1025)
		managementCall(t, h, h.client, "POST", identityapi.BasePath+"users/", fmt.Sprintf(`{"username":"over-profile","password":%q}`, oversized), "", true, 400)
		managementCall(t, h, h.client, "POST", apiPath("users", f.user.ID)+"password/", fmt.Sprintf(`{"password":%q}`, oversized), "1", true, 400)
		if f.hasher.calls.Load() != 2 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("hasher input refusal mutated or retried")
		}
		f.hasher.hook = func(context.Context) error { return errors.New("private-maintenance-fault") }
		managementCall(t, h, h.client, "POST", apiPath("users", f.user.ID)+"password/", `{"password":"valid input"}`, "1", true, 500)
		f.hasher.hook = func(context.Context) error {
			return fmt.Errorf("wrapped input: %w", &auth.Error{Code: auth.CodeInvalidInput, Field: "password"})
		}
		managementCall(t, h, h.client, "POST", apiPath("users", f.user.ID)+"password/", `{"password":"valid input"}`, "1", true, 500)
		if f.hasher.calls.Load() != 4 || !reflect.DeepEqual(before, catalogSnapshot(t, f)) {
			t.Fatal("hash infrastructure error mutated or retried")
		}
	})
	// A handler must never unwrap an arbitrary persistence cause into a 4xx.
	t.Run("read_scope_failure_is_not_published", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		h, _ := newManagementHTTP(t, f, &managementBoundary{ManagementBackend: f.runtime, mode: "read_end_failure"})
		for _, kind := range []string{"users", "groups", "permissions"} {
			managementCall(t, h, h.client, "GET", identityapi.BasePath+kind+"/", "", "", false, 500)
		}
		rows, err := models.UserObjects.Using(backend).All(t.Context())
		if err != nil || len(rows) != 2 {
			t.Fatal("read failure mutated data", err)
		}
	})
}
