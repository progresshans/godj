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
	list, err := client.GodjIdentityAccountsList(ctx, ib.GodjIdentityAccountsListParams{})
	page, ok := list.(*ib.AccountPage)
	if err != nil || !ok || page.Count != 4 || len(page.Items) != 4 {
		return fail("identity bearer initial list")
	}
	for _, user := range page.Items {
		if !user.PasswordUsable {
			return fail("identity bearer list password status")
		}
	}
	if err := checkIdentityBearerEmails(ctx, client, target); err != nil {
		return err
	}
	if err := checkIdentityBearerHeaders(ctx, client, httpClient, target); err != nil {
		return err
	}
	loginAt := time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)
	detail, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(target.ActorID)})
	logged, ok := detail.(*ib.AccountHeaders)
	if err != nil || !ok || logged.Response.Fields.LastLogin.Null || !logged.Response.Fields.LastLogin.Value.Equal(loginAt) || logged.Response.Revision != 1 {
		return fail("identity identity_bearer native login timestamp")
	}
	for _, user := range page.Items {
		wantLogin := user.ID == target.ActorID || user.ID == target.TargetID
		if wantLogin {
			if user.Fields.LastLogin.Null || !user.Fields.LastLogin.Value.Equal(loginAt) {
				return fail("identity identity_bearer list login timestamp")
			}
		} else if !user.Fields.LastLogin.Null {
			return fail("identity identity_bearer never logged in timestamp")
		}
	}
	viewer, err := ib.NewClient(target.URL, identityBearerSource{target.ReadOnlyToken}, ib.WithClient(httpClient))
	if err != nil {
		return fail("identity bearer viewer setup")
	}
	view, err := viewer.GodjIdentityAccountsList(ctx, ib.GodjIdentityAccountsListParams{})
	viewPage, ok := view.(*ib.AccountPage)
	if err != nil || !ok || viewPage.Count != 4 {
		return fail("identity bearer view admission")
	}
	denied, err := viewer.GodjIdentityAccountsCreate(ctx, &ib.AccountCreate{Fields: ib.AccountCreateFields{Username: "forbidden"}, Credential: ib.AccountCreateCredential{Password: ib.NewNilString("never persisted")}})
	forbidden, ok := denied.(*ib.GodjIdentityAccountsCreateForbidden)
	if err != nil || !ok || forbidden.Response.Code != "permission_denied" || !forbidden.WWWAuthenticate.Set || !strings.Contains(forbidden.WWWAuthenticate.Value, "insufficient_scope") {
		return fail("identity bearer read only challenge")
	}
	invalid, err := ib.NewClient(target.URL, identityBearerSource{"invalid-probe-token"}, ib.WithClient(httpClient))
	if err != nil {
		return fail("identity invalid bearer setup")
	}
	rejected, err := invalid.GodjIdentityAccountsList(ctx, ib.GodjIdentityAccountsListParams{})
	unauthorized, ok := rejected.(*ib.GodjIdentityAccountsListUnauthorized)
	if err != nil || !ok || unauthorized.Response.Code != "not_authenticated" || !unauthorized.WWWAuthenticate.Set || !strings.Contains(unauthorized.WWWAuthenticate.Value, "invalid_token") {
		return fail("identity bearer invalid token challenge")
	}

	createdPermission, err := client.GodjIdentityPermissionsCreate(ctx, &ib.PermissionCreate{Fields: ib.PermissionCreateFields{Code: "consumer.bearer.view", Name: "Bearer permission"}})
	permission, ok := createdPermission.(*ib.GodjIdentityPermissionsCreateCreatedHeaders)
	if err != nil || !ok || permission.Revision != 1 || permission.Response.Revision != 1 {
		return fail("identity bearer permission creation receipt")
	}
	permissionID := permission.Response.ID
	permissionRead, err := client.GodjIdentityPermissionsDetail(ctx, ib.GodjIdentityPermissionsDetailParams{ID: identityID(permissionID)})
	p, ok := permissionRead.(*ib.PermissionHeaders)
	if err != nil || !ok || p.Revision != 1 || p.Response.Revision != 1 || p.Response.ID != permissionID || p.Response.Fields.Code != "consumer.bearer.view" || p.Response.Fields.Name != "Bearer permission" {
		return fail("identity bearer permission readback")
	}
	createdGroup, err := client.GodjIdentityGroupsCreate(ctx, &ib.GroupCreate{Fields: ib.GroupCreateFields{Name: "Bearer group", Permissions: []int64{permissionID}}})
	groupReceipt, ok := createdGroup.(*ib.GodjIdentityGroupsCreateCreatedHeaders)
	if err != nil || !ok || groupReceipt.Revision != 1 || groupReceipt.Response.Revision != 1 {
		return fail("identity bearer group creation receipt")
	}
	groupID := groupReceipt.Response.ID
	groupRead, err := client.GodjIdentityGroupsDetail(ctx, ib.GodjIdentityGroupsDetailParams{ID: identityID(groupID)})
	group, ok := groupRead.(*ib.GroupHeaders)
	if err != nil || !ok || group.Revision != 1 || group.Response.Revision != 1 || group.Response.ID != groupID || group.Response.Fields.Name != "Bearer group" || !slices.Equal(group.Response.Fields.Permissions, []int64{permissionID}) {
		return fail("identity bearer group readback")
	}
	createdUser, err := client.GodjIdentityAccountsCreate(ctx, &ib.AccountCreate{Fields: ib.AccountCreateFields{Username: "Bearer-user", Email: ib.NewOptString("  MiXeD@EXAMPLE.COM  "), Groups: []int64{groupID}, Permissions: []int64{permissionID}}, Credential: ib.AccountCreateCredential{Password: ib.NewNilString("  Bearer password  ")}})
	createdReceipt, ok := createdUser.(*ib.GodjIdentityAccountsCreateCreatedHeaders)
	if err != nil || !ok || createdReceipt.Revision != 1 || createdReceipt.Response.Revision != 1 {
		return fail("identity bearer account creation receipt")
	}
	id := createdReceipt.Response.ID
	user, err := identityBearerAccount(ctx, client, id, 1)
	if err != nil || user.Fields.Username != "Bearer-user" || user.Fields.Email != "MiXeD@example.com" || !slices.Equal(user.Fields.Groups, []int64{groupID}) || !slices.Equal(user.Fields.Permissions, []int64{permissionID}) || !user.PasswordUsable {
		return fail("identity bearer usable creation readback")
	}
	patched, err := client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{FirstName: ib.NewOptString("Bearer edited")}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(id), IfRevision: 1})
	patchedReceipt, ok := patched.(*ib.GodjIdentityAccountsPatchOKHeaders)
	if err != nil || !ok || patchedReceipt.Revision != 2 || patchedReceipt.Response.Revision != 2 || patchedReceipt.Response.ID != id {
		return fail("identity bearer patch receipt")
	}
	user, err = identityBearerAccount(ctx, client, id, 2)
	if err != nil || user.Fields.FirstName != "Bearer edited" || !slices.Equal(user.Fields.Groups, []int64{groupID}) {
		return fail("identity bearer conditional patch readback")
	}
	stale, err := client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{Active: ib.NewOptBool(false)}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(id), IfRevision: 1})
	conflict, ok := stale.(*ib.GodjIdentityAccountsPatchPreconditionFailed)
	if err != nil || !ok || conflict.Code != "revision_conflict" {
		return fail("identity bearer stale revision")
	}
	deletedPermission, err := client.GodjIdentityPermissionsDelete(ctx, ib.GodjIdentityPermissionsDeleteParams{ID: identityID(permissionID), IfRevision: 1})
	if receipt, ok := deletedPermission.(*ib.GodjIdentityPermissionsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 1 || receipt.Response.Revision != 1 || receipt.Response.ID != permissionID {
		return fail("identity bearer permission delete")
	}
	detailGroup, err := client.GodjIdentityGroupsDetail(ctx, ib.GodjIdentityGroupsDetailParams{ID: identityID(groupID)})
	group, ok = detailGroup.(*ib.GroupHeaders)
	if err != nil || !ok || group.Revision != 2 || group.Response.Revision != 2 || len(group.Response.Fields.Permissions) != 0 {
		return fail("identity bearer group relation refresh")
	}
	deletedGroup, err := client.GodjIdentityGroupsDelete(ctx, ib.GodjIdentityGroupsDeleteParams{ID: identityID(groupID), IfRevision: 2})
	if receipt, ok := deletedGroup.(*ib.GodjIdentityGroupsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 2 || receipt.Response.Revision != 2 || receipt.Response.ID != groupID {
		return fail("identity bearer group delete")
	}
	user, err = identityBearerAccount(ctx, client, id, 4)
	if err != nil || len(user.Fields.Groups) != 0 || len(user.Fields.Permissions) != 0 || user.Fields.FirstName != "Bearer edited" {
		return fail("identity bearer account relation refresh")
	}
	disabled, err := client.GodjIdentityAccountsPassword(ctx, &ib.AccountPassword{Credential: ib.AccountPasswordCredential{Password: ib.NilString{Null: true}}}, ib.GodjIdentityAccountsPasswordParams{ID: identityID(id), IfRevision: 4})
	disabledReceipt, ok := disabled.(*ib.GodjIdentityAccountsPasswordOKHeaders)
	if err != nil || !ok || disabledReceipt.Revision != 5 || disabledReceipt.Response.Revision != 5 || disabledReceipt.Response.ID != id {
		return fail("identity bearer password disablement receipt")
	}
	disabledUser, err := identityBearerAccount(ctx, client, id, 5)
	if err != nil || disabledUser.PasswordUsable || !disabledUser.Fields.Active {
		return fail("identity bearer disablement status readback")
	}
	current, err := viewer.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(id)})
	currentUser, ok := current.(*ib.AccountHeaders)
	if err != nil || !ok || currentUser.Response.PasswordUsable || !currentUser.Response.Fields.Active || currentUser.Response.Revision != 5 {
		return fail("identity bearer view-only password status")
	}
	deletedUser, err := client.GodjIdentityAccountsDelete(ctx, ib.GodjIdentityAccountsDeleteParams{ID: identityID(id), IfRevision: 5})
	if receipt, ok := deletedUser.(*ib.GodjIdentityAccountsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 5 || receipt.Response.Revision != 5 || receipt.Response.ID != id {
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
	list, err := client.GodjIdentityAccountsList(ctx, ib.GodjIdentityAccountsListParams{})
	denied, ok := list.(*ib.GodjIdentityAccountsListForbidden)
	if err != nil || !ok || denied.Response.Code != "permission_denied" {
		return fail("identity stale bearer read rejected by current storage")
	}
	patch, err := client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{Active: ib.NewOptBool(true)}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(target.ActorID), IfRevision: 2})
	forbidden, ok := patch.(*ib.GodjIdentityAccountsPatchForbidden)
	if err != nil || !ok || forbidden.Response.Code != "permission_denied" {
		return fail("identity stale bearer write rejected by current storage")
	}
	return nil
}

func identityBearerAccount(ctx context.Context, client *ib.Client, id, revision int64) (ib.Account, error) {
	response, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(id)})
	value, ok := response.(*ib.AccountHeaders)
	if err != nil || !ok || value.Revision != revision || value.Response.Revision != revision || value.Response.ID != id {
		return ib.Account{}, fail("identity bearer selected account readback")
	}
	return value.Response, nil
}
