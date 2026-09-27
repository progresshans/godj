package main

import (
	"context"
	"slices"
	"strings"
	"time"

	ib "example.com/godj-openapi-client/identitybearer"
)

type identityBearerSource struct{ token string }

func (source identityBearerSource) BearerAuth(context.Context, ib.OperationName) (ib.BearerAuth, error) {
	return ib.BearerAuth{Token: source.token}, nil
}

func checkIdentityBearer(ctx context.Context, target identityEndpoint) error {
	httpClient, transport := newHTTPClient()
	defer transport.base.CloseIdleConnections()
	client, err := ib.NewClient(target.URL, identityBearerSource{target.Token}, ib.WithClient(httpClient))
	if err != nil {
		return fail("identity bearer setup")
	}
	list, err := client.GodjIdentityIdentityUsersList(ctx, ib.GodjIdentityIdentityUsersListParams{})
	page, ok := list.(*ib.UserPage)
	if err != nil || !ok || page.Count != 4 || len(page.Items) != 4 {
		return fail("identity bearer initial list")
	}
	for _, user := range page.Items {
		if !user.PasswordUsable {
			return fail("identity bearer list password status")
		}
	}
	loginAt := time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)
	detail, err := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: target.ActorID})
	logged, ok := detail.(*ib.UserHeaders)
	if err != nil || !ok || logged.Response.LastLogin.Null || !logged.Response.LastLogin.Value.Equal(loginAt) || logged.Response.Revision != 1 {
		return fail("identity identity_bearer native login timestamp")
	}
	for _, user := range page.Items {
		wantLogin := user.ID == target.ActorID || user.ID == target.TargetID
		if wantLogin {
			if user.LastLogin.Null || !user.LastLogin.Value.Equal(loginAt) {
				return fail("identity identity_bearer list login timestamp")
			}
		} else if !user.LastLogin.Null {
			return fail("identity identity_bearer never logged in timestamp")
		}
	}
	viewer, err := ib.NewClient(target.URL, identityBearerSource{target.ReadOnlyToken}, ib.WithClient(httpClient))
	if err != nil {
		return fail("identity bearer viewer setup")
	}
	view, err := viewer.GodjIdentityIdentityUsersList(ctx, ib.GodjIdentityIdentityUsersListParams{})
	viewPage, ok := view.(*ib.UserPage)
	if err != nil || !ok || viewPage.Count != 4 {
		return fail("identity bearer view admission")
	}
	denied, err := viewer.GodjIdentityIdentityUsersCreate(ctx, &ib.UserCreate{Username: "forbidden", Password: ib.NewNilString("never persisted")})
	forbidden, ok := denied.(*ib.GodjIdentityIdentityUsersCreateForbidden)
	if err != nil || !ok || forbidden.Response.Code != "permission_denied" || !forbidden.WWWAuthenticate.Set || !strings.Contains(forbidden.WWWAuthenticate.Value, "insufficient_scope") {
		return fail("identity bearer read only challenge")
	}
	invalid, err := ib.NewClient(target.URL, identityBearerSource{"invalid-probe-token"}, ib.WithClient(httpClient))
	if err != nil {
		return fail("identity invalid bearer setup")
	}
	rejected, err := invalid.GodjIdentityIdentityUsersList(ctx, ib.GodjIdentityIdentityUsersListParams{})
	unauthorized, ok := rejected.(*ib.GodjIdentityIdentityUsersListUnauthorized)
	if err != nil || !ok || unauthorized.Response.Code != "not_authenticated" || !unauthorized.WWWAuthenticate.Set || !strings.Contains(unauthorized.WWWAuthenticate.Value, "invalid_token") {
		return fail("identity bearer invalid token challenge")
	}

	createdPermission, err := client.GodjIdentityIdentityPermissionsCreate(ctx, &ib.PermissionCreate{Code: "consumer.bearer.view", Name: "Bearer permission"})
	permission, ok := createdPermission.(*ib.PermissionHeaders)
	if err != nil || !ok || permission.Revision != 1 || permission.Response.Revision != 1 {
		return fail("identity bearer permission create")
	}
	permissionID := permission.Response.ID
	createdGroup, err := client.GodjIdentityIdentityGroupsCreate(ctx, &ib.GroupCreate{Name: "Bearer group", Permissions: []int64{permissionID}})
	group, ok := createdGroup.(*ib.GroupHeaders)
	if err != nil || !ok || group.Revision != 1 || group.Response.Revision != 1 || !slices.Equal(group.Response.Permissions, []int64{permissionID}) {
		return fail("identity bearer group create")
	}
	groupID := group.Response.ID
	createdUser, err := client.GodjIdentityIdentityUsersCreate(ctx, &ib.UserCreate{Username: "Bearer-user", Password: ib.NewNilString("  Bearer password  "), Groups: []int64{groupID}, Permissions: []int64{permissionID}})
	user, ok := createdUser.(*ib.UserHeaders)
	if err != nil || !ok || user.Revision != 1 || user.Response.Revision != 1 || !slices.Equal(user.Response.Groups, []int64{groupID}) || !slices.Equal(user.Response.Permissions, []int64{permissionID}) {
		return fail("identity bearer user create")
	}
	if !user.Response.PasswordUsable {
		return fail("identity bearer usable creation status")
	}
	id := user.Response.ID
	patched, err := client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{FirstName: ib.NewOptString("Bearer edited")}, ib.GodjIdentityIdentityUsersPatchParams{ID: id, IfRevision: 1})
	user, ok = patched.(*ib.UserHeaders)
	if err != nil || !ok || user.Revision != 2 || user.Response.Revision != 2 || user.Response.FirstName != "Bearer edited" || !slices.Equal(user.Response.Groups, []int64{groupID}) {
		return fail("identity bearer conditional patch")
	}
	stale, err := client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{Active: ib.NewOptBool(false)}, ib.GodjIdentityIdentityUsersPatchParams{ID: id, IfRevision: 1})
	conflict, ok := stale.(*ib.GodjIdentityIdentityUsersPatchPreconditionFailed)
	if err != nil || !ok || conflict.Code != "revision_conflict" {
		return fail("identity bearer stale revision")
	}
	deletedPermission, err := client.GodjIdentityIdentityPermissionsDelete(ctx, ib.GodjIdentityIdentityPermissionsDeleteParams{ID: permissionID, IfRevision: 1})
	if _, ok := deletedPermission.(*ib.GodjIdentityIdentityPermissionsDeleteNoContent); err != nil || !ok {
		return fail("identity bearer permission delete")
	}
	detailGroup, err := client.GodjIdentityIdentityGroupsDetail(ctx, ib.GodjIdentityIdentityGroupsDetailParams{ID: groupID})
	group, ok = detailGroup.(*ib.GroupHeaders)
	if err != nil || !ok || group.Revision != 2 || group.Response.Revision != 2 || len(group.Response.Permissions) != 0 {
		return fail("identity bearer group relation refresh")
	}
	deletedGroup, err := client.GodjIdentityIdentityGroupsDelete(ctx, ib.GodjIdentityIdentityGroupsDeleteParams{ID: groupID, IfRevision: 2})
	if _, ok := deletedGroup.(*ib.GodjIdentityIdentityGroupsDeleteNoContent); err != nil || !ok {
		return fail("identity bearer group delete")
	}
	detailUser, err := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: id})
	user, ok = detailUser.(*ib.UserHeaders)
	if err != nil || !ok || user.Revision != 4 || user.Response.Revision != 4 || len(user.Response.Groups) != 0 || len(user.Response.Permissions) != 0 || user.Response.FirstName != "Bearer edited" {
		return fail("identity bearer user relation refresh")
	}
	disabled, err := client.GodjIdentityIdentityUsersPassword(ctx, &ib.PasswordReplacement{Password: ib.NilString{Null: true}}, ib.GodjIdentityIdentityUsersPasswordParams{ID: id, IfRevision: 4})
	disabledUser, ok := disabled.(*ib.UserSummaryHeaders)
	if err != nil || !ok || disabledUser.Revision != 5 || disabledUser.Response.Revision != 5 || !disabledUser.Response.Active {
		return fail("identity bearer password disablement")
	}
	if disabledUser.Response.PasswordUsable {
		return fail("identity bearer disablement status")
	}
	current, err := viewer.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: id})
	currentUser, ok := current.(*ib.UserHeaders)
	if err != nil || !ok || currentUser.Response.PasswordUsable || !currentUser.Response.Active || currentUser.Response.Revision != 5 {
		return fail("identity bearer view-only password status")
	}
	deletedUser, err := client.GodjIdentityIdentityUsersDelete(ctx, ib.GodjIdentityIdentityUsersDeleteParams{ID: id, IfRevision: 5})
	if _, ok := deletedUser.(*ib.GodjIdentityIdentityUsersDeleteNoContent); err != nil || !ok {
		return fail("identity bearer user delete")
	}
	return nil
}

// The Session flow has now disabled the stored actor. The fixture's Bearer
// verifier still supplies the originally active superuser snapshot, so only
// the management service's live authorization can reject this read and write.
func checkIdentityStaleBearer(ctx context.Context, target identityEndpoint) error {
	httpClient, transport := newHTTPClient()
	defer transport.base.CloseIdleConnections()
	client, err := ib.NewClient(target.URL, identityBearerSource{target.Token}, ib.WithClient(httpClient))
	if err != nil {
		return fail("identity stale bearer setup")
	}
	list, err := client.GodjIdentityIdentityUsersList(ctx, ib.GodjIdentityIdentityUsersListParams{})
	denied, ok := list.(*ib.GodjIdentityIdentityUsersListForbidden)
	if err != nil || !ok || denied.Response.Code != "permission_denied" {
		return fail("identity stale bearer read rejected by current storage")
	}
	patch, err := client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{Active: ib.NewOptBool(true)}, ib.GodjIdentityIdentityUsersPatchParams{ID: target.ActorID, IfRevision: 2})
	forbidden, ok := patch.(*ib.GodjIdentityIdentityUsersPatchForbidden)
	if err != nil || !ok || forbidden.Response.Code != "permission_denied" {
		return fail("identity stale bearer write rejected by current storage")
	}
	return nil
}
