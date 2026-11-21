package accountstoretest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	accountmodel "github.com/progresshans/godj/identity/model"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

func catalogRace(t *testing.T, calls ...func(context.Context) error) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	type completion struct {
		index int
		err   error
	}
	start, done := make(chan struct{}), make(chan completion, len(calls))
	for index, call := range calls {
		go func() {
			<-start
			done <- completion{index, call(ctx)}
		}()
	}
	close(start)
	failures := make([]error, len(calls))
	// Observe every return before releasing the fixture and its DB connections.
	for range calls {
		result := <-done
		failures[result.index] = result.err
	}
	return failures
}

func catalogUniqueCompetition(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	other, err := project.NewAccountCatalog(catalogSessions(t, reopen(t)), auth.PrincipalAuthorizer{}, catalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	owners := []*accountmodel.Catalog[models.Group, models.Permission]{f.catalog, other}
	formValidationSessions(t, backend, f.credential)
	for _, kind := range []string{"group", "permission"} {
		if !t.Run(kind, func(t *testing.T) {
			name, code := "One "+kind+" owner", "competition.one_owner"
			groupInput := catalogGroupInput(t, map[string]query.Value{"name": query.String(name)}, nil)
			permissionInput := catalogPermissionInput(t, map[string]query.Value{"code": query.String(code), "name": query.String(name)})
			var id int64
			if !t.Run("create", func(t *testing.T) {
				expected := accountNativeSnapshot(t, reopen(t))
				results := make([]accountmodel.CatalogChange, len(owners))
				calls := make([]func(context.Context) error, len(owners))
				for index, owner := range owners {
					calls[index] = func(ctx context.Context) (err error) {
						if kind == "group" {
							results[index], err = owner.Groups().Create(ctx, actor, groupInput)
						} else {
							results[index], err = owner.Permissions().Create(ctx, actor, permissionInput)
						}
						return err
					}
				}
				failures := catalogRace(t, calls...)
				fields, unique := []string{"name", "permissions"}, "name"
				if kind == "permission" {
					fields, unique = []string{"code", "name"}, "code"
				}
				winners := 0
				for index, failure := range failures {
					result := results[index]
					if failure == nil {
						winners++
						id = result.ID
						if id <= 0 || result.Revision != 1 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, fields) {
							t.Fatal("winning catalog creation published incomplete metadata")
						}
					} else {
						diagnostics, rejected := validation.Rejected(failure)
						if !rejected || diagnostics.ByField(validation.Field(unique)).Empty() || !reflect.DeepEqual(result, accountmodel.CatalogChange{}) {
							t.Fatal("competing catalog creation did not reject current uniqueness", failure)
						}
					}
				}
				if winners != 1 {
					t.Fatal("catalog creation did not have exactly one winner", winners)
				}
				if kind == "group" {
					row := models.NewGroupWithID(id)
					row.Name, row.Revision = name, 1
					expected.groups = append(expected.groups, row)
				} else {
					row := models.NewPermissionWithID(id)
					row.Code, row.Name, row.Revision = code, name, 1
					expected.permissions = append(expected.permissions, row)
				}
				assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{kind, id, admin.ActionAdd, fields})
			}) {
				return
			}
			t.Run("edit", func(t *testing.T) {
				expected := accountNativeSnapshot(t, reopen(t))
				results := make([]accountmodel.CatalogChange, len(owners))
				calls := make([]func(context.Context) error, len(owners))
				groupInput := catalogGroupInput(t, map[string]query.Value{"name": query.String("One edit")}, nil)
				permissionInput := catalogPermissionInput(t, map[string]query.Value{"name": query.String("One edit")})
				for index, owner := range owners {
					calls[index] = func(ctx context.Context) (err error) {
						if kind == "group" {
							results[index], err = owner.Groups().Update(ctx, actor, id, 1, groupInput)
						} else {
							results[index], err = owner.Permissions().Update(ctx, actor, id, 1, permissionInput)
						}
						return err
					}
				}
				failures := catalogRace(t, calls...)
				winners := 0
				for index, failure := range failures {
					result := results[index]
					if failure == nil {
						winners++
						if result.ID != id || result.Revision != 2 || result.RevokedSessions != 0 || !slices.Equal(result.ChangedFields, []string{"name"}) {
							t.Fatal("winning catalog edit published incomplete metadata")
						}
					} else if !errors.Is(failure, &accountmodel.Error{Code: accountmodel.CodeConflict}) || !reflect.DeepEqual(result, accountmodel.CatalogChange{}) {
						t.Fatal("competing catalog edit did not reject its stale revision", failure)
					}
				}
				if winners != 1 {
					t.Fatal("catalog edit did not have exactly one winner", winners)
				}
				if kind == "group" {
					for index := range expected.groups {
						if expected.groups[index].ID == id {
							expected.groups[index].Name, expected.groups[index].Revision = "One edit", 2
						}
					}
				} else {
					for index := range expected.permissions {
						if expected.permissions[index].ID == id {
							expected.permissions[index].Name, expected.permissions[index].Revision = "One edit", 2
						}
					}
				}
				assertCatalogState(t, reopen(t), expected, actor, catalogExpectedEvent{kind, id, admin.ActionChange, []string{"name"}})
			})
		}) {
			return
		}
	}
}

func catalogGroupEditVersusJoin(t *testing.T, backend Backend, reopen func(*testing.T) Backend) {
	f := provisionCatalog(t, backend)
	actor := f.credential.Principal()
	keys := catalogBoundGrants(t, backend)
	group, err := f.catalog.Groups().Create(t.Context(), actor, catalogGroupInput(t, map[string]query.Value{"name": query.String("Concurrent union")}, map[string][]int64{"permissions": keys[:255]}))
	if err != nil {
		t.Fatal(err)
	}
	member := roleTarget(t, backend, f.account, "joining-member")
	if _, err := models.UserPermissionsLinkObjects.Create(t.Context(), backend, models.NewUserPermissionsLinkCreate(member.ID, f.account.grants[0].ID)); err != nil {
		t.Fatal(err)
	}
	credential, present, err := f.account.store.CredentialByID(t.Context(), member.PrincipalID)
	if err != nil || !present || len(credential.Principal().Permissions()) != 1 {
		t.Fatal("joining member fixture", err)
	}
	formValidationSessions(t, backend, credential)
	editor := roleEditor(t, catalogSessions(t, reopen(t)))
	groupInput := catalogGroupInput(t, nil, map[string][]int64{"permissions": keys})
	userInput := roleInput(t, nil, map[string][]int64{"groups": {group.ID}})
	expected := accountNativeSnapshot(t, reopen(t))
	var groupResult accountmodel.CatalogChange
	var userResult accountmodel.AccountChange
	failures := catalogRace(t,
		func(ctx context.Context) (err error) {
			groupResult, err = f.catalog.Groups().Update(ctx, actor, group.ID, 1, groupInput)
			return err
		},
		func(ctx context.Context) (err error) {
			userResult, err = editor.Update(ctx, actor, member.ID, 1, userInput)
			return err
		},
	)
	if (failures[0] == nil) == (failures[1] == nil) {
		t.Fatal("group expansion and member join did not produce exactly one winner", failures)
	}
	after := accountNativeSnapshot(t, reopen(t))
	var event catalogExpectedEvent
	wantPermissions := 1
	if failures[0] == nil {
		if groupResult.ID != group.ID || groupResult.Revision != 2 || groupResult.RevokedSessions != 0 || !slices.Equal(groupResult.ChangedFields, []string{"permissions"}) || !reflect.DeepEqual(userResult, accountmodel.AccountChange{}) {
			t.Fatal("winning group expansion published incomplete or losing account metadata")
		}
		if diagnostics, rejected := validation.Rejected(failures[1]); !rejected || diagnostics.Empty() {
			t.Fatal("losing join was not an effective-permission rejection", failures[1])
		}
		for index := range expected.groups {
			if expected.groups[index].ID == group.ID {
				expected.groups[index].Revision = 2
			}
		}
		expected.grants = catalogExpectedGrantAdditions(t, expected.grants, after.grants, group.ID, keys[255:])
		event = catalogExpectedEvent{"group", group.ID, admin.ActionChange, []string{"permissions"}}
	} else {
		if userResult.ID != member.ID || userResult.Revision != 2 || userResult.RevokedSessions != 0 || !slices.Equal(userResult.ChangedFields, []string{"groups"}) || !reflect.DeepEqual(groupResult, accountmodel.CatalogChange{}) {
			t.Fatal("winning join published incomplete or losing group metadata")
		}
		if diagnostics, rejected := validation.Rejected(failures[0]); !rejected || diagnostics.ByField("permissions").Empty() {
			t.Fatal("losing group edit was not an effective-permission rejection", failures[0])
		}
		for index := range expected.users {
			if expected.users[index].ID == member.ID {
				expected.users[index].Revision = 2
			}
		}
		found := 0
		for _, row := range after.memberships {
			if row.SourceID == member.ID && row.TargetID == group.ID {
				if row.ID <= 0 {
					t.Fatal("committed membership has no physical key")
				}
				expected.memberships = append(expected.memberships, row)
				found++
			}
		}
		if found != 1 {
			t.Fatal("winning join omitted or repeated its membership")
		}
		event = catalogExpectedEvent{"user", member.ID, admin.ActionChange, []string{"groups"}}
		wantPermissions = 256
	}
	assertCatalogState(t, reopen(t), expected, actor, event)
	current, present, err := f.account.store.CredentialByID(t.Context(), member.PrincipalID)
	if err != nil || !present || len(current.Principal().Permissions()) != wantPermissions || !current.MatchesSessionStamp(credential.SessionStamp()) {
		t.Fatal("competing group/join writes committed the wrong effective grants or credential", err)
	}
}
