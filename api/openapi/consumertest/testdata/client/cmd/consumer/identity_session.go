package main

import (
	"context"
	"net/http"
	"slices"
	"strings"

	is "example.com/godj-openapi-client/identitysession"
)

var identityCreatedUsername = "Fred" + strings.Repeat("\U000105c0", 146)
var identityInputUsername = "Ｆｒｅｄ" + strings.Repeat("\U000105c0", 146)

type identitySessionSource struct{ *sessionState }

func (source identitySessionSource) SessionAuth(context.Context, is.OperationName) (is.SessionAuth, error) {
	return is.SessionAuth{APIKey: source.cookie(sessionCookieName)}, nil
}
func (source identitySessionSource) CsrfCookie(context.Context, is.OperationName) (is.CsrfCookie, error) {
	return is.CsrfCookie{APIKey: source.cookie(csrfCookieName)}, nil
}
func (source identitySessionSource) CsrfHeader(context.Context, is.OperationName) (is.CsrfHeader, error) {
	return is.CsrfHeader{APIKey: source.header()}, nil
}

func checkIdentitySession(ctx context.Context, target identityEndpoint) error {
	httpClient, transport, state, err := newSessionClient(target.endpoint, target.Session, "/api/identity/users/")
	if err != nil {
		return err
	}
	defer transport.base.CloseIdleConnections()
	client, err := is.NewClient(target.URL, identitySessionSource{state}, is.WithClient(httpClient))
	if err != nil {
		return fail("identity session setup")
	}
	listed, err := client.GodjIdentityIdentityUsersList(ctx, is.GodjIdentityIdentityUsersListParams{Limit: is.NewOptInt64(1)})
	page, ok := listed.(*is.UserPageHeaders)
	if err != nil || !ok || page.Response.Count != 4 || len(page.Response.Items) != 1 || page.Response.Limit != 1 || page.Response.Offset != 0 || !transport.capturedCSRF() || !page.XGodjCsrftoken.Set || !state.ready(page.XGodjCsrftoken.Value) {
		return fail("identity session bounded list and csrf")
	}
	groups, err := client.GodjIdentityIdentityGroupsList(ctx, is.GodjIdentityIdentityGroupsListParams{})
	groupPage, ok := groups.(*is.GroupPageHeaders)
	if err != nil || !ok || groupPage.Response.Count != 2 || len(groupPage.Response.Items) != 2 || !groupPage.XGodjCsrftoken.Set || !state.ready(groupPage.XGodjCsrftoken.Value) {
		return fail("identity session groups list")
	}
	permissions, err := client.GodjIdentityIdentityPermissionsList(ctx, is.GodjIdentityIdentityPermissionsListParams{})
	permissionPage, ok := permissions.(*is.PermissionPageHeaders)
	if err != nil || !ok || permissionPage.Response.Count != 5 || len(permissionPage.Response.Items) != 5 || !permissionPage.XGodjCsrftoken.Set || !state.ready(permissionPage.XGodjCsrftoken.Value) {
		return fail("identity session permissions list")
	}

	viewerHTTP, viewerTransport, viewerState, err := newSessionClient(target.endpoint, target.ReadOnlySession, "/api/identity/users/")
	if err != nil {
		return err
	}
	defer viewerTransport.base.CloseIdleConnections()
	viewer, err := is.NewClient(target.URL, identitySessionSource{viewerState}, is.WithClient(viewerHTTP))
	if err != nil {
		return fail("identity viewer setup")
	}
	viewed, err := viewer.GodjIdentityIdentityUsersList(ctx, is.GodjIdentityIdentityUsersListParams{})
	viewPage, ok := viewed.(*is.UserPageHeaders)
	if err != nil || !ok || viewPage.Response.Count != 4 || !viewPage.XGodjCsrftoken.Set || !viewerState.ready(viewPage.XGodjCsrftoken.Value) {
		return fail("identity viewer read")
	}
	denied, err := viewer.GodjIdentityIdentityUsersCreate(ctx, &is.UserCreate{Username: "forbidden", Password: is.NewNilString("never persisted")})
	forbidden, ok := denied.(*is.GodjIdentityIdentityUsersCreateForbidden)
	if err != nil || !ok || forbidden.Code != "permission_denied" {
		return fail("identity session read only denied")
	}
	state.setInvalid(true)
	csrf, err := client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{Active: is.NewOptBool(false)}, is.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 1})
	state.setInvalid(false)
	csrfDenied, ok := csrf.(*is.GodjIdentityIdentityUsersPatchForbidden)
	if err != nil || !ok || csrfDenied.Code != "csrf_rejected" {
		return fail("identity session invalid csrf")
	}
	// Drop the generated required header only for this negative transport probe.
	// Normal application calls below always use the generated int64 parameter.
	httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request.Header.Del("If-Revision")
		return transport.RoundTrip(request)
	})
	missing, err := client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{Active: is.NewOptBool(false)}, is.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 1})
	httpClient.Transport = transport
	missingRevision, ok := missing.(*is.GodjIdentityIdentityUsersPatchPreconditionRequired)
	if err != nil || !ok || missingRevision.Code != "precondition_required" {
		return fail("identity required revision")
	}
	invalid, err := client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{Active: is.NewOptBool(false)}, is.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 0})
	invalidRevision, ok := invalid.(*is.GodjIdentityIdentityUsersPatchBadRequest)
	if err != nil || !ok || invalidRevision.Code != "invalid_precondition" {
		return fail("identity invalid revision")
	}

	permissionID, groupID, err := identitySessionCatalog(ctx, client, state)
	if err != nil {
		return err
	}
	// Host-selected built-ins must remain visible through the independent SDK.
	// Rejected creates and password commands must leave later counts/revisions intact.
	for _, probe := range []struct {
		password string
		codes    []string
	}{
		{"123", []string{"password_too_short", "password_too_common", "password_entirely_numeric"}},
		{" PASSWORD ", []string{"password_too_common"}},
		{"¹²³⁴⁵⁶⁷⁸", []string{"password_entirely_numeric"}},
	} {
		rejected, err := client.GodjIdentityIdentityUsersCreate(ctx, &is.UserCreate{Username: "PolicyCandidate", Password: is.NewNilString(probe.password)})
		failure, ok := rejected.(*is.GodjIdentityIdentityUsersCreateBadRequest)
		if err != nil || !ok || failure.Code != "validation_error" || len(failure.Errors) != len(probe.codes) {
			return fail("identity generated built-in creation policy")
		}
		for i, code := range probe.codes {
			if failure.Errors[i].Field != "password" || failure.Errors[i].Code != code {
				return fail("identity generated creation policy order")
			}
		}
		replaced, err := client.GodjIdentityIdentityUsersPassword(ctx, &is.PasswordReplacement{Password: is.NewNilString(probe.password)}, is.GodjIdentityIdentityUsersPasswordParams{ID: target.TargetID, IfRevision: 1})
		passwordFailure, ok := replaced.(*is.GodjIdentityIdentityUsersPasswordBadRequest)
		if err != nil || !ok || passwordFailure.Code != "validation_error" || len(passwordFailure.Errors) != len(probe.codes) {
			return fail("identity generated built-in replacement policy")
		}
		for i, code := range probe.codes {
			if passwordFailure.Errors[i].Field != "password" || passwordFailure.Errors[i].Code != code {
				return fail("identity generated replacement policy order")
			}
		}
	}
	created, err := client.GodjIdentityIdentityUsersCreate(ctx, &is.UserCreate{Username: identityInputUsername, Password: is.NilString{Null: true}, FirstName: is.NewOptString("Created"), Staff: is.NewOptBool(true), Groups: []int64{groupID}, Permissions: []int64{permissionID}})
	user, ok := created.(*is.UserHeaders)
	if err != nil || !ok || user.Revision != 1 || user.Response.Revision != 1 || user.Response.Username != identityCreatedUsername || !user.Response.Active || !user.Response.Staff || !slices.Equal(user.Response.Groups, []int64{groupID}) || !slices.Equal(user.Response.Permissions, []int64{permissionID}) {
		return fail("identity session user create")
	}
	id := user.Response.ID
	duplicate, err := client.GodjIdentityIdentityUsersCreate(ctx, &is.UserCreate{Username: identityCreatedUsername, Password: is.NewNilString("duplicate rejected")})
	duplicateUser, ok := duplicate.(*is.GodjIdentityIdentityUsersCreateBadRequest)
	if err != nil || !ok || duplicateUser.Code != "validation_error" || len(duplicateUser.Errors) != 1 || duplicateUser.Errors[0].Field != "username" || duplicateUser.Errors[0].Code != "unique" {
		return fail("identity generated normalized unique error")
	}
	if _, err := identityUserPatch(ctx, client, id, 1, 2, is.UserPatch{FirstName: is.NewOptString("Edited")}); err != nil {
		return err
	}
	stale, err := client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{FirstName: is.NewOptString("Lost")}, is.GodjIdentityIdentityUsersPatchParams{ID: id, IfRevision: 1})
	conflict, ok := stale.(*is.GodjIdentityIdentityUsersPatchPreconditionFailed)
	if err != nil || !ok || conflict.Code != "revision_conflict" {
		return fail("identity generated stale conflict")
	}
	noOp, err := identityUserPatch(ctx, client, id, 2, 2, is.UserPatch{})
	if err != nil || noOp.FirstName != "Edited" || !slices.Equal(noOp.Groups, []int64{groupID}) || !slices.Equal(noOp.Permissions, []int64{permissionID}) {
		return fail("identity user omission and noop")
	}
	put, err := client.GodjIdentityIdentityUsersUpdate(ctx, &is.UserUpdate{Username: identityCreatedUsername}, is.GodjIdentityIdentityUsersUpdateParams{ID: id, IfRevision: 2})
	full, ok := put.(*is.UserHeaders)
	if err != nil || !ok || full.Revision != 3 || full.Response.Revision != 3 || full.Response.FirstName != "" || full.Response.Staff || !full.Response.Active || !slices.Equal(full.Response.Groups, []int64{groupID}) || !slices.Equal(full.Response.Permissions, []int64{permissionID}) {
		return fail("identity user full defaults preserve collections")
	}
	cleared, err := identityUserPatch(ctx, client, id, 3, 4, is.UserPatch{Groups: []int64{}, Permissions: []int64{}})
	if err != nil || len(cleared.Groups) != 0 || len(cleared.Permissions) != 0 {
		return fail("identity user explicit empty collections")
	}
	if _, err := identityUserPatch(ctx, client, id, 4, 5, is.UserPatch{Groups: []int64{groupID}, Permissions: []int64{permissionID}}); err != nil {
		return err
	}

	password, err := client.GodjIdentityIdentityUsersPassword(ctx, &is.PasswordReplacement{Password: is.NewNilString("  SDK replacement password  ")}, is.GodjIdentityIdentityUsersPasswordParams{ID: target.TargetID, IfRevision: 1})
	replaced, ok := password.(*is.UserSummaryHeaders)
	if err != nil || !ok || replaced.Revision != 2 || replaced.Response.Revision != 2 || replaced.Response.ID != target.TargetID {
		return fail("identity password replacement")
	}
	if err := identityRevokedSession(ctx, viewer); err != nil {
		return err
	}
	for revision := int64(2); revision <= 3; revision++ {
		disabled, err := client.GodjIdentityIdentityUsersPassword(ctx, &is.PasswordReplacement{Password: is.NilString{Null: true}}, is.GodjIdentityIdentityUsersPasswordParams{ID: target.TargetID, IfRevision: revision})
		value, ok := disabled.(*is.UserSummaryHeaders)
		if err != nil || !ok || value.Revision != revision+1 || value.Response.Revision != revision+1 || !value.Response.Active {
			return fail("identity session repeated password disablement")
		}
	}
	restored, err := client.GodjIdentityIdentityUsersPassword(ctx, &is.PasswordReplacement{Password: is.NewNilString("  SDK replacement password  ")}, is.GodjIdentityIdentityUsersPasswordParams{ID: target.TargetID, IfRevision: 4})
	restoredUser, ok := restored.(*is.UserSummaryHeaders)
	if err != nil || !ok || restoredUser.Revision != 5 || restoredUser.Response.Revision != 5 {
		return fail("identity session password restoration")
	}
	if _, err := identityUserPatch(ctx, client, target.TargetID, 5, 6, is.UserPatch{Active: is.NewOptBool(false)}); err != nil {
		return err
	}
	if _, err := identityUserPatch(ctx, client, target.TargetID, 6, 7, is.UserPatch{Active: is.NewOptBool(true)}); err != nil {
		return err
	}
	if err := identityRevokedSession(ctx, viewer); err != nil {
		return err
	}

	removedPermission, err := client.GodjIdentityIdentityPermissionsDelete(ctx, is.GodjIdentityIdentityPermissionsDeleteParams{ID: permissionID, IfRevision: 3})
	if _, ok := removedPermission.(*is.GodjIdentityIdentityPermissionsDeleteNoContent); err != nil || !ok {
		return fail("identity permission delete")
	}
	staleGroup, err := client.GodjIdentityIdentityGroupsPatch(ctx, &is.GroupPatch{Name: is.NewOptString("Lost")}, is.GodjIdentityIdentityGroupsPatchParams{ID: groupID, IfRevision: 4})
	groupConflict, ok := staleGroup.(*is.GodjIdentityIdentityGroupsPatchPreconditionFailed)
	if err != nil || !ok || groupConflict.Code != "revision_conflict" {
		return fail("identity permission deletion advances group revision")
	}
	detail, err := client.GodjIdentityIdentityGroupsDetail(ctx, is.GodjIdentityIdentityGroupsDetailParams{ID: groupID})
	currentGroup, ok := detail.(*is.GodjIdentityIdentityGroupsDetailOKHeaders)
	if err != nil || !ok || currentGroup.Revision != 5 || currentGroup.Response.Revision != 5 || len(currentGroup.Response.Permissions) != 0 || !currentGroup.XGodjCsrftoken.Set || !state.ready(currentGroup.XGodjCsrftoken.Value) {
		return fail("identity group relation refresh")
	}
	removedGroup, err := client.GodjIdentityIdentityGroupsDelete(ctx, is.GodjIdentityIdentityGroupsDeleteParams{ID: groupID, IfRevision: 5})
	if _, ok := removedGroup.(*is.GodjIdentityIdentityGroupsDeleteNoContent); err != nil || !ok {
		return fail("identity group delete")
	}
	staleUser, err := client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{Email: is.NewOptString("lost@example.test")}, is.GodjIdentityIdentityUsersPatchParams{ID: id, IfRevision: 6})
	userConflict, ok := staleUser.(*is.GodjIdentityIdentityUsersPatchPreconditionFailed)
	if err != nil || !ok || userConflict.Code != "revision_conflict" {
		return fail("identity group deletion advances user revision")
	}
	final, err := client.GodjIdentityIdentityUsersDetail(ctx, is.GodjIdentityIdentityUsersDetailParams{ID: id})
	finalUser, ok := final.(*is.GodjIdentityIdentityUsersDetailOKHeaders)
	if err != nil || !ok || finalUser.Revision != 7 || finalUser.Response.Revision != 7 || finalUser.Response.Username != identityCreatedUsername || len(finalUser.Response.Groups) != 0 || len(finalUser.Response.Permissions) != 0 || !finalUser.XGodjCsrftoken.Set || !state.ready(finalUser.XGodjCsrftoken.Value) {
		return fail("identity session final user")
	}
	if err := identityHostDeletion(ctx, client, target); err != nil {
		return err
	}
	if _, err := identityUserPatch(ctx, client, target.ActorID, 1, 2, is.UserPatch{Active: is.NewOptBool(false)}); err != nil {
		return err
	}
	return identityRevokedSession(ctx, client)
}

func identityUserPatch(ctx context.Context, client *is.Client, id, before, after int64, patch is.UserPatch) (is.User, error) {
	response, err := client.GodjIdentityIdentityUsersPatch(ctx, &patch, is.GodjIdentityIdentityUsersPatchParams{ID: id, IfRevision: before})
	value, ok := response.(*is.UserHeaders)
	if err != nil || !ok || value.Revision != after || value.Response.Revision != after || value.Response.ID != id {
		return is.User{}, fail("identity session user patch")
	}
	return value.Response, nil
}

func identityRevokedSession(ctx context.Context, client *is.Client) error {
	response, err := client.GodjIdentityIdentityUsersList(ctx, is.GodjIdentityIdentityUsersListParams{})
	denied, ok := response.(*is.GodjIdentityIdentityUsersListForbidden)
	if err != nil || !ok || denied.Code != "not_authenticated" {
		return fail("identity revoked session denial")
	}
	return nil
}

func identitySessionCatalog(ctx context.Context, client *is.Client, state *sessionState) (int64, int64, error) {
	created, err := client.GodjIdentityIdentityPermissionsCreate(ctx, &is.PermissionCreate{Code: "consumer.manage.view", Name: "SDK permission"})
	p, ok := created.(*is.PermissionHeaders)
	if err != nil || !ok || p.Revision != 1 || p.Response.Revision != 1 || p.Response.Code != "consumer.manage.view" {
		return 0, 0, fail("identity session permission create")
	}
	permissionID := p.Response.ID
	put, err := client.GodjIdentityIdentityPermissionsUpdate(ctx, &is.PermissionUpdate{Code: "consumer.manage.view", Name: "SDK permission full"}, is.GodjIdentityIdentityPermissionsUpdateParams{ID: permissionID, IfRevision: 1})
	p, ok = put.(*is.PermissionHeaders)
	if err != nil || !ok || p.Revision != 2 || p.Response.Revision != 2 || p.Response.Name != "SDK permission full" {
		return 0, 0, fail("identity session permission put")
	}
	patch, err := client.GodjIdentityIdentityPermissionsPatch(ctx, &is.PermissionPatch{Name: is.NewOptString("SDK permission patch")}, is.GodjIdentityIdentityPermissionsPatchParams{ID: permissionID, IfRevision: 2})
	p, ok = patch.(*is.PermissionHeaders)
	if err != nil || !ok || p.Revision != 3 || p.Response.Revision != 3 || p.Response.Name != "SDK permission patch" {
		return 0, 0, fail("identity session permission patch")
	}
	detail, err := client.GodjIdentityIdentityPermissionsDetail(ctx, is.GodjIdentityIdentityPermissionsDetailParams{ID: permissionID})
	current, ok := detail.(*is.GodjIdentityIdentityPermissionsDetailOKHeaders)
	if err != nil || !ok || current.Revision != 3 || current.Response != p.Response || !current.XGodjCsrftoken.Set || !state.ready(current.XGodjCsrftoken.Value) {
		return 0, 0, fail("identity session permission detail")
	}
	group, err := client.GodjIdentityIdentityGroupsCreate(ctx, &is.GroupCreate{Name: "  SDK group  ", Permissions: []int64{permissionID, permissionID}})
	g, ok := group.(*is.GroupHeaders)
	if err != nil || !ok || g.Revision != 1 || g.Response.Revision != 1 || g.Response.Name != "SDK group" || !slices.Equal(g.Response.Permissions, []int64{permissionID}) {
		return 0, 0, fail("identity session group create normalization")
	}
	groupID := g.Response.ID
	full, err := client.GodjIdentityIdentityGroupsUpdate(ctx, &is.GroupUpdate{Name: "SDK group full"}, is.GodjIdentityIdentityGroupsUpdateParams{ID: groupID, IfRevision: 1})
	g, ok = full.(*is.GroupHeaders)
	if err != nil || !ok || g.Revision != 2 || g.Response.Revision != 2 || g.Response.Name != "SDK group full" || !slices.Equal(g.Response.Permissions, []int64{permissionID}) {
		return 0, 0, fail("identity session group put omission")
	}
	for _, step := range []struct {
		patch         is.GroupPatch
		before, after int64
		permissions   []int64
	}{
		{is.GroupPatch{Permissions: []int64{}}, 2, 3, []int64{}},
		{is.GroupPatch{}, 3, 3, []int64{}},
		{is.GroupPatch{Permissions: []int64{permissionID}}, 3, 4, []int64{permissionID}},
	} {
		response, err := client.GodjIdentityIdentityGroupsPatch(ctx, &step.patch, is.GodjIdentityIdentityGroupsPatchParams{ID: groupID, IfRevision: step.before})
		g, ok := response.(*is.GroupHeaders)
		if err != nil || !ok || g.Revision != step.after || g.Response.Revision != step.after || !slices.Equal(g.Response.Permissions, step.permissions) {
			return 0, 0, fail("identity session group collection presence and noop")
		}
	}
	return permissionID, groupID, nil
}

func identityHostDeletion(ctx context.Context, client *is.Client, target identityEndpoint) error {
	user, err := client.GodjIdentityIdentityUsersDelete(ctx, is.GodjIdentityIdentityUsersDeleteParams{ID: target.ProtectedUserID, IfRevision: 1})
	u, ok := user.(*is.GodjIdentityIdentityUsersDeleteBadRequest)
	if err != nil || !ok || !identityProtected(u.Code, u.Errors) {
		return fail("identity user protected host")
	}
	group, err := client.GodjIdentityIdentityGroupsDelete(ctx, is.GodjIdentityIdentityGroupsDeleteParams{ID: target.ProtectedGroupID, IfRevision: 1})
	g, ok := group.(*is.GodjIdentityIdentityGroupsDeleteBadRequest)
	if err != nil || !ok || !identityProtected(g.Code, g.Errors) {
		return fail("identity group protected host")
	}
	permission, err := client.GodjIdentityIdentityPermissionsDelete(ctx, is.GodjIdentityIdentityPermissionsDeleteParams{ID: target.ProtectedPermissionID, IfRevision: 1})
	p, ok := permission.(*is.GodjIdentityIdentityPermissionsDeleteBadRequest)
	if err != nil || !ok || !identityProtected(p.Code, p.Errors) {
		return fail("identity permission protected host")
	}
	deletedUser, err := client.GodjIdentityIdentityUsersDelete(ctx, is.GodjIdentityIdentityUsersDeleteParams{ID: target.CascadeUserID, IfRevision: 1})
	if _, ok := deletedUser.(*is.GodjIdentityIdentityUsersDeleteNoContent); err != nil || !ok {
		return fail("identity user host cascade")
	}
	deletedGroup, err := client.GodjIdentityIdentityGroupsDelete(ctx, is.GodjIdentityIdentityGroupsDeleteParams{ID: target.CascadeGroupID, IfRevision: 1})
	if _, ok := deletedGroup.(*is.GodjIdentityIdentityGroupsDeleteNoContent); err != nil || !ok {
		return fail("identity group host set null")
	}
	deletedPermission, err := client.GodjIdentityIdentityPermissionsDelete(ctx, is.GodjIdentityIdentityPermissionsDeleteParams{ID: target.CascadePermissionID, IfRevision: 1})
	if _, ok := deletedPermission.(*is.GodjIdentityIdentityPermissionsDeleteNoContent); err != nil || !ok {
		return fail("identity permission host cascade")
	}
	return nil
}

func identityProtected(code string, fields []is.GoDjAPIErrorErrorsItem) bool {
	return code == "validation_error" && len(fields) == 1 && fields[0].Field == "__all__" && fields[0].Code == "protected"
}
