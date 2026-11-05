package accountstoretest

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	identityreference "github.com/progresshans/godj/internal/identitytest/testdata"
	"github.com/progresshans/godj/schema/ir"
)

// This observes selected credentials and permissions on native rows using the
// actions in identity_reference.py. The original captures remain unchanged;
// DB/domain ownership and permission grammar have separate native controls.
func selectedIdentityReference(t *testing.T, vendor string, backend Backend, reopen func(*testing.T) Backend) {
	check := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := identityreference.Identity(vendor)
	check(t, err)
	type roleObservation struct {
		Authenticates bool     `json:"backend_may_authenticate"`
		Registered    bool     `json:"registered_grant"`
		Ungranted     bool     `json:"ungranted_permission"`
		Unregistered  bool     `json:"unregistered_permission"`
		Malformed     bool     `json:"malformed_permission"`
		Effective     []string `json:"effective"`
	}
	var reference struct {
		Django, Python, Backend string
		Sources                 map[string]string `json:"source_sha256"`
		Observations            struct {
			GrantUnion struct {
				Direct, Group, Effective []string
				Memberships              int64 `json:"group_memberships"`
			} `json:"grant_union"`
			Cache    struct{ Held, Fresh []string } `json:"instance_permission_cache"`
			Deletion struct {
				Effective   []string
				Memberships int64
			} `json:"group_deletion"`
			Roles map[string]json.RawMessage
		}
	}
	check(t, json.Unmarshal(raw, &reference))
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Backend != map[string]string{"sqlite": "sqlite", "postgres": "postgresql"}[vendor] || len(reference.Sources) != 3 || len(reference.Observations.Roles) != 8 {
		t.Fatal("selected identity reference is incomplete or belongs to another runtime")
	}
	for _, name := range []string{"auth_models", "backends", "admin_sites"} {
		value := reference.Sources[name]
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != value {
			t.Fatal("independent identity source digest is missing", name)
		}
	}
	f := provision(t, backend, ir.AccountExact)
	expectedDomain, err := f.binding.CredentialDomain(f.ownership.Generation())
	check(t, err)
	for index, name := range []string{"view", "change", "export"} {
		f.grants[index], err = models.PermissionObjects.Patch(t.Context(), backend, f.grants[index], models.PermissionPatch{}.WithCode("helpdesk.ticket."+name))
		check(t, err)
	}
	collections, err := project.BindCollections()
	check(t, err)
	direct, err := collections.IdentityUserPermissions.From(backend, f.user)
	check(t, err)
	check(t, direct.SetKeys(t.Context(), []int64{f.grants[0].ID}))
	groups, err := collections.IdentityUserGroups.From(backend, f.user)
	check(t, err)
	check(t, groups.SetKeys(t.Context(), []int64{f.group.ID, f.group.ID}))
	grants, err := collections.IdentityGroupPermissions.From(backend, f.group)
	check(t, err)
	check(t, grants.SetKeys(t.Context(), []int64{f.grants[0].ID, f.grants[1].ID}))
	codes := func(t *testing.T, permissions []auth.Permission) []string {
		t.Helper()
		result := make([]string, len(permissions))
		for index, permission := range permissions {
			var known bool
			result[index], known = strings.CutPrefix(string(permission), "helpdesk.ticket.")
			if !known {
				t.Fatal("selected reference returned an unrelated permission")
			}
		}
		slices.Sort(result)
		return result
	}
	rowsCodes := func(t *testing.T, rows []models.Permission) []string {
		t.Helper()
		values := make([]auth.Permission, len(rows))
		for index, row := range rows {
			values[index] = auth.Permission(row.Code)
		}
		return codes(t, values)
	}
	held, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
	check(t, err)
	if !found || !expectedDomain.MatchesPrincipal(held.Principal()) {
		t.Fatal("selected reference account or durable domain is missing")
	}
	t.Run("grant_union", func(t *testing.T) {
		directRows, err := direct.All(t.Context())
		check(t, err)
		groupRows, err := grants.All(t.Context())
		check(t, err)
		memberships, err := models.UserGroupsLinkObjects.Using(backend).Filter(models.UserGroupsLinkFields.SourceID.Exact(f.user.ID)).Count(t.Context())
		check(t, err)
		want := reference.Observations.GrantUnion
		if !reflect.DeepEqual(rowsCodes(t, directRows), want.Direct) || !reflect.DeepEqual(rowsCodes(t, groupRows), want.Group) || !reflect.DeepEqual(codes(t, held.Principal().Permissions()), want.Effective) || memberships != want.Memberships {
			t.Fatal("selected grant union differs from the native Django observation")
		}
	})
	writer := reopen(t)
	changed, err := collections.IdentityGroupPermissions.From(writer, f.group)
	check(t, err)
	check(t, changed.SetKeys(t.Context(), []int64{f.grants[0].ID, f.grants[2].ID}))
	t.Run("instance_permission_cache", func(t *testing.T) {
		fresh, found, err := f.store.CredentialByUsername(t.Context(), f.user.Username)
		check(t, err)
		restarted, err := project.NewAccountCredentialStore(reopen(t))
		check(t, err)
		reopened, present, err := restarted.CredentialByID(t.Context(), f.user.PrincipalID)
		check(t, err)
		if !found || !present || !reflect.DeepEqual(codes(t, fresh.Principal().Permissions()), reference.Observations.Cache.Fresh) || !reflect.DeepEqual(codes(t, reopened.Principal().Permissions()), reference.Observations.Cache.Fresh) || !reflect.DeepEqual(codes(t, held.Principal().Permissions()), reference.Observations.Cache.Held) {
			t.Fatal("selected fresh/held observations differ from native Django")
		}
		if !fresh.MatchesSessionStamp(held.SessionStamp()) {
			t.Fatal("permission refresh changed credential identity")
		}
	})
	verifier, err := project.NewAccountAuthenticator(t.Context(), backend, f.hasher)
	check(t, err)
	for _, active := range []bool{false, true} {
		for _, staff := range []bool{false, true} {
			for _, superuser := range []bool{false, true} {
				key := fmt.Sprintf("%d%d%d", boolDigit(active), boolDigit(staff), boolDigit(superuser))
				t.Run("roles_"+key, func(t *testing.T) {
					wire, present := reference.Observations.Roles[key]
					var fields map[string]json.RawMessage
					check(t, json.Unmarshal(wire, &fields))
					if !present || len(fields) != 7 {
						t.Fatal("independent role observation is missing", key)
					}
					for _, name := range []string{"backend_may_authenticate", "registered_grant", "ungranted_permission", "unregistered_permission", "malformed_permission", "effective", "admin_entry"} {
						if _, ok := fields[name]; !ok {
							t.Fatal("independent role observation is incomplete", name)
						}
						if name != "effective" {
							var value *bool
							check(t, json.Unmarshal(fields[name], &value))
							if value == nil {
								t.Fatal("independent role observation contains null", name)
							}
						}
					}
					var want roleObservation
					check(t, json.Unmarshal(wire, &want))
					if want.Effective == nil {
						t.Fatal("independent effective catalog is absent")
					}
					f.user, err = models.UserObjects.Patch(t.Context(), writer, f.user, models.UserPatch{}.WithActive(active).WithStaff(staff).WithSuperuser(superuser).WithRevision(f.user.Revision+1))
					check(t, err)
					observe := func(credential auth.Credential, failure error) {
						t.Helper()
						principal := credential.Principal()
						if !want.Authenticates {
							if !errors.Is(failure, auth.ErrInvalidCredentials) || principal.Authenticated() || principal.ID() != "" || credential.SessionStamp() != "" {
								t.Fatal("selected inactive account published authentication", key, failure)
							}
						} else {
							check(t, failure)
							if !expectedDomain.MatchesPrincipal(principal) || principal.ID() != f.user.PrincipalID || principal.Staff() != staff || principal.Superuser() != superuser {
								t.Fatal("selected authentication lost its domain or stored roles", key)
							}
						}
						if principal.Has("helpdesk.ticket.view") != want.Registered || principal.Has("helpdesk.ticket.change") != want.Ungranted || principal.Has("unregistered.permission") != want.Unregistered {
							t.Fatal("selected authorization differs from native Django", key)
						}
						effective := []string{}
						for _, row := range f.grants {
							if principal.Has(auth.Permission(row.Code)) {
								effective = append(effective, strings.TrimPrefix(row.Code, "helpdesk.ticket."))
							}
						}
						slices.Sort(effective)
						if !reflect.DeepEqual(effective, want.Effective) {
							t.Fatal("selected effective catalog differs from native Django", key)
						}
						// Preserve the declared native difference. Django accepts a
						// malformed permission for an active superuser; GoDj always
						// validates grammar before evaluating that role.
						if want.Malformed != (active && superuser) || principal.Has("not-a-permission") {
							t.Fatal("permission grammar difference was hidden or lost", key)
						}
					}
					credential, failure := verifier.Authenticate(t.Context(), f.user.Username, password)
					observe(credential, failure)
					credential, failure = verifier.Resolve(t.Context(), f.user.PrincipalID)
					observe(credential, failure)
				})
			}
		}
	}
	f.user, err = models.UserObjects.Patch(t.Context(), writer, f.user, models.UserPatch{}.WithActive(true).WithStaff(false).WithSuperuser(false).WithRevision(f.user.Revision+1))
	check(t, err)
	deletions, err := project.BindRelationDeleters()
	check(t, err)
	_, err = deletions.IdentityGroup.Delete(t.Context(), writer, &f.group)
	check(t, err)
	t.Run("group_deletion", func(t *testing.T) {
		current, found, err := f.store.CredentialByID(t.Context(), f.user.PrincipalID)
		check(t, err)
		count, err := models.UserGroupsLinkObjects.Using(backend).Filter(models.UserGroupsLinkFields.SourceID.Exact(f.user.ID)).Count(t.Context())
		check(t, err)
		if !found || !reflect.DeepEqual(codes(t, current.Principal().Permissions()), reference.Observations.Deletion.Effective) || count != reference.Observations.Deletion.Memberships || !reflect.DeepEqual(codes(t, held.Principal().Permissions()), reference.Observations.Cache.Held) {
			t.Fatal("selected group deletion differs from native Django or mutates a held observation")
		}
	})
}

func boolDigit(value bool) int {
	if value {
		return 1
	}
	return 0
}
