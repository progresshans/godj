package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

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
	httpClient, transport, state, err := newSessionClient(target.endpoint, target.Session, "/api/identity/accounts/")
	if err != nil {
		return err
	}
	defer transport.base.CloseIdleConnections()
	client, err := is.NewClient(target.URL, identitySessionSource{state}, is.WithClient(httpClient))
	if err != nil {
		return fail("identity session setup")
	}
	listed, err := client.GodjIdentityAccountsList(ctx, is.GodjIdentityAccountsListParams{Limit: is.NewOptInt64(1)})
	page, ok := listed.(*is.AccountPageHeaders)
	if err != nil || !ok || page.Response.Count != 4 || len(page.Response.Items) != 1 || page.Response.Limit != 1 || page.Response.Offset != 0 || !transport.capturedCSRF() || !page.XGodjCsrftoken.Set || !state.ready(page.XGodjCsrftoken.Value) {
		return fail("identity session bounded list and csrf")
	}
	if !page.Response.Items[0].PasswordUsable {
		return fail("identity session list password status")
	}
	if err := checkIdentitySessionEmails(ctx, client, target, state); err != nil {
		return err
	}
	if err := checkIdentitySessionHeaders(ctx, client, httpClient, target, state); err != nil {
		return err
	}
	loginAt := time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)
	loginDetail, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(target.ActorID)})
	logged, ok := loginDetail.(*is.AccountHeaders)
	if err != nil || !ok || logged.Response.Fields.LastLogin.Null || !logged.Response.Fields.LastLogin.Value.Equal(loginAt) || logged.Response.Revision != 1 {
		return fail("identity identity_session native login timestamp")
	}
	for _, user := range page.Response.Items {
		wantLogin := user.ID == target.ActorID || user.ID == target.TargetID
		if wantLogin {
			if user.Fields.LastLogin.Null || !user.Fields.LastLogin.Value.Equal(loginAt) {
				return fail("identity identity_session list login timestamp")
			}
		} else if !user.Fields.LastLogin.Null {
			return fail("identity identity_session never logged in timestamp")
		}
	}
	groups, err := client.GodjIdentityGroupsList(ctx, is.GodjIdentityGroupsListParams{})
	groupPage, ok := groups.(*is.GroupPageHeaders)
	if err != nil || !ok || groupPage.Response.Count != 2 || len(groupPage.Response.Items) != 2 || !groupPage.XGodjCsrftoken.Set || !state.ready(groupPage.XGodjCsrftoken.Value) {
		return fail("identity session groups list")
	}
	permissions, err := client.GodjIdentityPermissionsList(ctx, is.GodjIdentityPermissionsListParams{})
	permissionPage, ok := permissions.(*is.PermissionPageHeaders)
	if err != nil || !ok || permissionPage.Response.Count != 5 || len(permissionPage.Response.Items) != 5 || !permissionPage.XGodjCsrftoken.Set || !state.ready(permissionPage.XGodjCsrftoken.Value) {
		return fail("identity session permissions list")
	}

	viewerHTTP, viewerTransport, viewerState, err := newSessionClient(target.endpoint, target.ReadOnlySession, "/api/identity/accounts/")
	if err != nil {
		return err
	}
	defer viewerTransport.base.CloseIdleConnections()
	viewer, err := is.NewClient(target.URL, identitySessionSource{viewerState}, is.WithClient(viewerHTTP))
	if err != nil {
		return fail("identity viewer setup")
	}
	viewed, err := viewer.GodjIdentityAccountsList(ctx, is.GodjIdentityAccountsListParams{})
	viewPage, ok := viewed.(*is.AccountPageHeaders)
	if err != nil || !ok || viewPage.Response.Count != 4 || !viewPage.XGodjCsrftoken.Set || !viewerState.ready(viewPage.XGodjCsrftoken.Value) {
		return fail("identity viewer read")
	}
	denied, err := viewer.GodjIdentityAccountsCreate(ctx, &is.AccountCreate{Fields: is.AccountCreateFields{Username: "forbidden"}, Credential: is.AccountCreateCredential{Password: is.NewNilString("never persisted")}})
	forbidden, ok := denied.(*is.GodjIdentityAccountsCreateForbidden)
	if err != nil || !ok || forbidden.Code != "permission_denied" {
		return fail("identity session read only denied")
	}
	state.setInvalid(true)
	csrf, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{Active: is.NewOptBool(false)}}, is.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 1})
	state.setInvalid(false)
	csrfDenied, ok := csrf.(*is.GodjIdentityAccountsPatchForbidden)
	if err != nil || !ok || csrfDenied.Code != "csrf_rejected" {
		return fail("identity session invalid csrf")
	}
	// Drop the generated required header only for this negative transport probe.
	// Normal application calls below always use the generated int64 parameter.
	httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request.Header.Del("If-Revision")
		return transport.RoundTrip(request)
	})
	missing, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{Active: is.NewOptBool(false)}}, is.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 1})
	httpClient.Transport = transport
	missingRevision, ok := missing.(*is.GodjIdentityAccountsPatchPreconditionRequired)
	if err != nil || !ok || !identitySessionHeaderError(missingRevision.Code, missingRevision.Errors, "required") {
		return fail("identity required revision")
	}
	invalid, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{Active: is.NewOptBool(false)}}, is.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 0})
	invalidRevision, ok := invalid.(*is.GodjIdentityAccountsPatchBadRequest)
	if err != nil || !ok || !identitySessionHeaderError(invalidRevision.Code, invalidRevision.Errors, "invalid") {
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
		rejected, err := client.GodjIdentityAccountsCreate(ctx, &is.AccountCreate{Fields: is.AccountCreateFields{Username: "PolicyCandidate"}, Credential: is.AccountCreateCredential{Password: is.NewNilString(probe.password)}})
		failure, ok := rejected.(*is.GodjIdentityAccountsCreateBadRequest)
		if err != nil || !ok || failure.Code != "validation_error" || len(failure.Errors) != len(probe.codes) {
			return fail("identity generated built-in creation policy")
		}
		for i, code := range probe.codes {
			if failure.Errors[i].Field != "/credential/password" || failure.Errors[i].Code != code {
				return fail("identity generated creation policy order")
			}
		}
		replaced, err := client.GodjIdentityAccountsPassword(ctx, &is.AccountPassword{Credential: is.AccountPasswordCredential{Password: is.NewNilString(probe.password)}}, is.GodjIdentityAccountsPasswordParams{ID: identityID(target.TargetID), IfRevision: 1})
		passwordFailure, ok := replaced.(*is.GodjIdentityAccountsPasswordBadRequest)
		if err != nil || !ok || passwordFailure.Code != "validation_error" || len(passwordFailure.Errors) != len(probe.codes) {
			return fail("identity generated built-in replacement policy")
		}
		for i, code := range probe.codes {
			if passwordFailure.Errors[i].Field != "/credential/password" || passwordFailure.Errors[i].Code != code {
				return fail("identity generated replacement policy order")
			}
		}
	}
	created, err := client.GodjIdentityAccountsCreate(ctx, &is.AccountCreate{Fields: is.AccountCreateFields{Username: identityInputUsername, Email: is.NewOptString("  MiXeD@EXAMPLE.COM  "), FirstName: is.NewOptString("Created"), Staff: is.NewOptBool(true), Groups: []int64{groupID}, Permissions: []int64{permissionID}}, Credential: is.AccountCreateCredential{Password: is.NilString{Null: true}}})
	receipt, ok := created.(*is.GodjIdentityAccountsCreateCreatedHeaders)
	if err != nil || !ok || receipt.Revision != 1 || receipt.Response.Revision != 1 {
		return fail("identity session account creation receipt")
	}
	id := receipt.Response.ID
	user, err := identitySessionAccount(ctx, client, state, id, 1)
	if err != nil || user.Fields.Username != identityCreatedUsername || user.Fields.Email != "MiXeD@example.com" || !user.Fields.Active || !user.Fields.Staff || !slices.Equal(user.Fields.Groups, []int64{groupID}) || !slices.Equal(user.Fields.Permissions, []int64{permissionID}) {
		return fail("identity session account creation readback")
	}
	if user.PasswordUsable {
		return fail("identity session unusable creation status")
	}
	duplicate, err := client.GodjIdentityAccountsCreate(ctx, &is.AccountCreate{Fields: is.AccountCreateFields{Username: identityCreatedUsername}, Credential: is.AccountCreateCredential{Password: is.NewNilString("duplicate rejected")}})
	duplicateUser, ok := duplicate.(*is.GodjIdentityAccountsCreateBadRequest)
	if err != nil || !ok || duplicateUser.Code != "validation_error" || len(duplicateUser.Errors) != 1 || duplicateUser.Errors[0].Field != "/fields/username" || duplicateUser.Errors[0].Code != "unique" {
		return fail("identity generated normalized unique error")
	}
	if _, err := identityUserPatch(ctx, client, state, id, 1, 2, is.AccountPatch{Fields: is.AccountPatchFields{FirstName: is.NewOptString("Edited")}}); err != nil {
		return err
	}
	stale, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{FirstName: is.NewOptString("Lost")}}, is.GodjIdentityAccountsPatchParams{ID: identityID(id), IfRevision: 1})
	conflict, ok := stale.(*is.GodjIdentityAccountsPatchPreconditionFailed)
	if err != nil || !ok || conflict.Code != "revision_conflict" {
		return fail("identity generated stale conflict")
	}
	noOp, err := identityUserPatch(ctx, client, state, id, 2, 2, is.AccountPatch{Fields: is.AccountPatchFields{}})
	if err != nil || noOp.Fields.FirstName != "Edited" || !slices.Equal(noOp.Fields.Groups, []int64{groupID}) || !slices.Equal(noOp.Fields.Permissions, []int64{permissionID}) {
		return fail("identity user omission and noop")
	}
	if noOp.PasswordUsable {
		return fail("identity session edit changed password status")
	}
	put, err := client.GodjIdentityAccountsPut(ctx, &is.AccountPut{Fields: is.AccountPutFields{Username: identityCreatedUsername}}, is.GodjIdentityAccountsPutParams{ID: identityID(id), IfRevision: 2})
	putReceipt, ok := put.(*is.GodjIdentityAccountsPutOKHeaders)
	if err != nil || !ok || putReceipt.Revision != 3 || putReceipt.Response.Revision != 3 || putReceipt.Response.ID != id {
		return fail("identity account full update receipt")
	}
	full, err := identitySessionAccount(ctx, client, state, id, 3)
	if err != nil || full.Fields.FirstName != "" || full.Fields.Staff || !full.Fields.Active || !slices.Equal(full.Fields.Groups, []int64{groupID}) || !slices.Equal(full.Fields.Permissions, []int64{permissionID}) {
		return fail("identity account full defaults preserve collections")
	}
	cleared, err := identityUserPatch(ctx, client, state, id, 3, 4, is.AccountPatch{Fields: is.AccountPatchFields{Groups: []int64{}, Permissions: []int64{}}})
	if err != nil || len(cleared.Fields.Groups) != 0 || len(cleared.Fields.Permissions) != 0 {
		return fail("identity user explicit empty collections")
	}
	if _, err := identityUserPatch(ctx, client, state, id, 4, 5, is.AccountPatch{Fields: is.AccountPatchFields{Groups: []int64{groupID}, Permissions: []int64{permissionID}}}); err != nil {
		return err
	}

	replaced, err := identitySessionPassword(ctx, client, state, target.TargetID, 1, is.NewNilString("  SDK replacement password  "))
	if err != nil || !replaced.PasswordUsable {
		return fail("identity session password replacement status")
	}
	if err := identityRevokedSession(ctx, viewer); err != nil {
		return err
	}
	for revision := int64(2); revision <= 3; revision++ {
		value, err := identitySessionPassword(ctx, client, state, target.TargetID, revision, is.NilString{Null: true})
		if err != nil || !value.Fields.Active || value.PasswordUsable {
			return fail("identity session repeated password disablement")
		}
	}
	restored, err := identitySessionPassword(ctx, client, state, target.TargetID, 4, is.NewNilString("  SDK replacement password  "))
	if err != nil || !restored.PasswordUsable {
		return fail("identity session restoration status")
	}
	inactive, err := identityUserPatch(ctx, client, state, target.TargetID, 5, 6, is.AccountPatch{Fields: is.AccountPatchFields{Active: is.NewOptBool(false)}})
	if err != nil || !inactive.PasswordUsable || inactive.Fields.Active {
		return fail("identity password status independent of activation")
	}
	if _, err := identityUserPatch(ctx, client, state, target.TargetID, 6, 7, is.AccountPatch{Fields: is.AccountPatchFields{Active: is.NewOptBool(true)}}); err != nil {
		return err
	}
	if err := identityRevokedSession(ctx, viewer); err != nil {
		return err
	}

	removedPermission, err := client.GodjIdentityPermissionsDelete(ctx, is.GodjIdentityPermissionsDeleteParams{ID: identityID(permissionID), IfRevision: 3})
	if receipt, ok := removedPermission.(*is.GodjIdentityPermissionsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 3 || receipt.Response.Revision != 3 || receipt.Response.ID != permissionID {
		return fail("identity permission delete")
	}
	staleGroup, err := client.GodjIdentityGroupsPatch(ctx, &is.GroupPatch{Fields: is.GroupPatchFields{Name: is.NewOptString("Lost")}}, is.GodjIdentityGroupsPatchParams{ID: identityID(groupID), IfRevision: 4})
	groupConflict, ok := staleGroup.(*is.GodjIdentityGroupsPatchPreconditionFailed)
	if err != nil || !ok || groupConflict.Code != "revision_conflict" {
		return fail("identity permission deletion advances group revision")
	}
	detail, err := client.GodjIdentityGroupsDetail(ctx, is.GodjIdentityGroupsDetailParams{ID: identityID(groupID)})
	currentGroup, ok := detail.(*is.GroupHeaders)
	if err != nil || !ok || currentGroup.Revision != 5 || currentGroup.Response.Revision != 5 || len(currentGroup.Response.Fields.Permissions) != 0 || !currentGroup.XGodjCsrftoken.Set || !state.ready(currentGroup.XGodjCsrftoken.Value) {
		return fail("identity group relation refresh")
	}
	removedGroup, err := client.GodjIdentityGroupsDelete(ctx, is.GodjIdentityGroupsDeleteParams{ID: identityID(groupID), IfRevision: 5})
	if receipt, ok := removedGroup.(*is.GodjIdentityGroupsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 5 || receipt.Response.Revision != 5 || receipt.Response.ID != groupID {
		return fail("identity group delete")
	}
	staleUser, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{Email: is.NewOptString("lost@example.test")}}, is.GodjIdentityAccountsPatchParams{ID: identityID(id), IfRevision: 6})
	userConflict, ok := staleUser.(*is.GodjIdentityAccountsPatchPreconditionFailed)
	if err != nil || !ok || userConflict.Code != "revision_conflict" {
		return fail("identity group deletion advances user revision")
	}
	final, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(id)})
	finalUser, ok := final.(*is.AccountHeaders)
	if err != nil || !ok || finalUser.Revision != 7 || finalUser.Response.Revision != 7 || finalUser.Response.Fields.Username != identityCreatedUsername || len(finalUser.Response.Fields.Groups) != 0 || len(finalUser.Response.Fields.Permissions) != 0 || !finalUser.XGodjCsrftoken.Set || !state.ready(finalUser.XGodjCsrftoken.Value) {
		return fail("identity session final user")
	}
	history, err := client.GodjIdentityAccountsHistory(ctx, is.GodjIdentityAccountsHistoryParams{ID: identityID(target.TargetID), Limit: is.NewOptInt64(10)})
	events, ok := history.(*is.GodjIdentityAccountsHistoryOKHeaders)
	if err != nil || !ok || len(events.Response) != 6 || !events.XGodjCsrftoken.Set || !state.ready(events.XGodjCsrftoken.Value) {
		return fail("selected account history")
	}
	for i, event := range events.Response {
		field := "password"
		if i >= 4 {
			field = "active"
		}
		if event.Action != "change" || !slices.Equal(event.ChangedFields, []string{field}) || event.At.IsZero() || event.Sequence <= 0 || i > 0 && event.Sequence <= events.Response[i-1].Sequence {
			return fail("selected account history data")
		}
	}
	if err := identityHostDeletion(ctx, client, target); err != nil {
		return err
	}
	// Self-deactivation must return the committed receipt even though this
	// actor can no longer read the target or keep its session afterward.
	deactivated, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{Active: is.NewOptBool(false)}}, is.GodjIdentityAccountsPatchParams{ID: identityID(target.ActorID), IfRevision: 1})
	confirmed, ok := deactivated.(*is.GodjIdentityAccountsPatchOKHeaders)
	if err != nil || !ok || confirmed.Revision != 2 || confirmed.Response.Revision != 2 || confirmed.Response.ID != target.ActorID {
		return fail("identity self-deactivation receipt")
	}
	return identityRevokedSession(ctx, client)
}

// Every mutation is checked as a metadata receipt before issuing this
// independent safe read. A read never substitutes for a successful write.
func identitySessionAccount(ctx context.Context, client *is.Client, state *sessionState, id, revision int64) (is.Account, error) {
	response, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(id)})
	value, ok := response.(*is.AccountHeaders)
	if err != nil || !ok || value.Revision != revision || value.Response.Revision != revision || value.Response.ID != id || !value.XGodjCsrftoken.Set || !state.ready(value.XGodjCsrftoken.Value) {
		return is.Account{}, fail("identity selected account readback")
	}
	return value.Response, nil
}

func identityUserPatch(ctx context.Context, client *is.Client, state *sessionState, id, before, after int64, patch is.AccountPatch) (is.Account, error) {
	response, err := client.GodjIdentityAccountsPatch(ctx, &patch, is.GodjIdentityAccountsPatchParams{ID: identityID(id), IfRevision: before})
	value, ok := response.(*is.GodjIdentityAccountsPatchOKHeaders)
	if err != nil || !ok || value.Revision != after || value.Response.Revision != after || value.Response.ID != id {
		return is.Account{}, fail("identity session account patch receipt")
	}
	return identitySessionAccount(ctx, client, state, id, after)
}

func identitySessionPassword(ctx context.Context, client *is.Client, state *sessionState, id, before int64, password is.NilString) (is.Account, error) {
	response, err := client.GodjIdentityAccountsPassword(ctx, &is.AccountPassword{Credential: is.AccountPasswordCredential{Password: password}}, is.GodjIdentityAccountsPasswordParams{ID: identityID(id), IfRevision: before})
	value, ok := response.(*is.GodjIdentityAccountsPasswordOKHeaders)
	if err != nil || !ok || value.Revision != before+1 || value.Response.Revision != before+1 || value.Response.ID != id {
		return is.Account{}, fail("identity session password receipt")
	}
	return identitySessionAccount(ctx, client, state, id, before+1)
}

func identityRevokedSession(ctx context.Context, client *is.Client) error {
	response, err := client.GodjIdentityAccountsList(ctx, is.GodjIdentityAccountsListParams{})
	denied, ok := response.(*is.GodjIdentityAccountsListForbidden)
	if err != nil || !ok || denied.Response.Code != "not_authenticated" {
		return fail("identity revoked session denial")
	}
	return nil
}

func identitySessionCatalog(ctx context.Context, client *is.Client, state *sessionState) (int64, int64, error) {
	created, err := client.GodjIdentityPermissionsCreate(ctx, &is.PermissionCreate{Fields: is.PermissionCreateFields{Code: "consumer.manage.view", Name: "SDK permission"}})
	p, ok := created.(*is.GodjIdentityPermissionsCreateCreatedHeaders)
	if err != nil || !ok || p.Revision != 1 || p.Response.Revision != 1 {
		return 0, 0, fail("identity permission creation receipt")
	}
	permissionID := p.Response.ID
	if err := identitySessionPermission(ctx, client, state, permissionID, 1, "SDK permission"); err != nil {
		return 0, 0, err
	}
	put, err := client.GodjIdentityPermissionsPut(ctx, &is.PermissionPut{Fields: is.PermissionPutFields{Code: "consumer.manage.view", Name: "SDK permission full"}}, is.GodjIdentityPermissionsPutParams{ID: identityID(permissionID), IfRevision: 1})
	p2, ok := put.(*is.GodjIdentityPermissionsPutOKHeaders)
	if err != nil || !ok || p2.Revision != 2 || p2.Response.Revision != 2 || p2.Response.ID != permissionID {
		return 0, 0, fail("identity permission put receipt")
	}
	if err := identitySessionPermission(ctx, client, state, permissionID, 2, "SDK permission full"); err != nil {
		return 0, 0, err
	}
	patch, err := client.GodjIdentityPermissionsPatch(ctx, &is.PermissionPatch{Fields: is.PermissionPatchFields{Name: is.NewOptString("SDK permission patch")}}, is.GodjIdentityPermissionsPatchParams{ID: identityID(permissionID), IfRevision: 2})
	p3, ok := patch.(*is.GodjIdentityPermissionsPatchOKHeaders)
	if err != nil || !ok || p3.Revision != 3 || p3.Response.Revision != 3 || p3.Response.ID != permissionID {
		return 0, 0, fail("identity permission patch receipt")
	}
	if err := identitySessionPermission(ctx, client, state, permissionID, 3, "SDK permission patch"); err != nil {
		return 0, 0, err
	}
	group, err := client.GodjIdentityGroupsCreate(ctx, &is.GroupCreate{Fields: is.GroupCreateFields{Name: "  SDK group  ", Permissions: []int64{permissionID, permissionID}}})
	g, ok := group.(*is.GodjIdentityGroupsCreateCreatedHeaders)
	if err != nil || !ok || g.Revision != 1 || g.Response.Revision != 1 {
		return 0, 0, fail("identity group creation receipt")
	}
	groupID := g.Response.ID
	if err := identitySessionGroup(ctx, client, state, groupID, 1, "SDK group", []int64{permissionID}); err != nil {
		return 0, 0, err
	}
	full, err := client.GodjIdentityGroupsPut(ctx, &is.GroupPut{Fields: is.GroupPutFields{Name: "SDK group full"}}, is.GodjIdentityGroupsPutParams{ID: identityID(groupID), IfRevision: 1})
	g2, ok := full.(*is.GodjIdentityGroupsPutOKHeaders)
	if err != nil || !ok || g2.Revision != 2 || g2.Response.Revision != 2 || g2.Response.ID != groupID {
		return 0, 0, fail("identity group put receipt")
	}
	if err := identitySessionGroup(ctx, client, state, groupID, 2, "SDK group full", []int64{permissionID}); err != nil {
		return 0, 0, err
	}
	for _, step := range []struct {
		patch         is.GroupPatch
		before, after int64
		permissions   []int64
	}{
		{is.GroupPatch{Fields: is.GroupPatchFields{Permissions: []int64{}}}, 2, 3, []int64{}},
		{is.GroupPatch{Fields: is.GroupPatchFields{}}, 3, 3, []int64{}},
		{is.GroupPatch{Fields: is.GroupPatchFields{Permissions: []int64{permissionID}}}, 3, 4, []int64{permissionID}},
	} {
		response, err := client.GodjIdentityGroupsPatch(ctx, &step.patch, is.GodjIdentityGroupsPatchParams{ID: identityID(groupID), IfRevision: step.before})
		changed, ok := response.(*is.GodjIdentityGroupsPatchOKHeaders)
		if err != nil || !ok || changed.Revision != step.after || changed.Response.Revision != step.after || changed.Response.ID != groupID {
			return 0, 0, fail("identity group patch receipt")
		}
		if err := identitySessionGroup(ctx, client, state, groupID, step.after, "SDK group full", step.permissions); err != nil {
			return 0, 0, err
		}
	}
	permissionHistory, err := client.GodjIdentityPermissionsHistory(ctx, is.GodjIdentityPermissionsHistoryParams{ID: identityID(permissionID)})
	permissions, ok := permissionHistory.(*is.GodjIdentityPermissionsHistoryOKHeaders)
	if err != nil || !ok || len(permissions.Response) != 3 || !permissions.XGodjCsrftoken.Set || !state.ready(permissions.XGodjCsrftoken.Value) {
		return 0, 0, fail("selected permission history")
	}
	for i, event := range permissions.Response {
		action, fields := "change", []string{"name"}
		if i == 0 {
			action, fields = "add", []string{"code", "name"}
		}
		if event.Action != action || !slices.Equal(event.ChangedFields, fields) || event.At.IsZero() || event.Sequence <= 0 || i > 0 && event.Sequence <= permissions.Response[i-1].Sequence {
			return 0, 0, fail("selected permission history data")
		}
	}
	groupHistory, err := client.GodjIdentityGroupsHistory(ctx, is.GodjIdentityGroupsHistoryParams{ID: identityID(groupID)})
	groups, ok := groupHistory.(*is.GodjIdentityGroupsHistoryOKHeaders)
	if err != nil || !ok || len(groups.Response) != 4 || !groups.XGodjCsrftoken.Set || !state.ready(groups.XGodjCsrftoken.Value) {
		return 0, 0, fail("selected group history")
	}
	for i, event := range groups.Response {
		action, fields := "change", []string{"permissions"}
		if i == 0 {
			action, fields = "add", []string{"name", "permissions"}
		} else if i == 1 {
			fields = []string{"name"}
		}
		if event.Action != action || !slices.Equal(event.ChangedFields, fields) || event.At.IsZero() || event.Sequence <= 0 || i > 0 && event.Sequence <= groups.Response[i-1].Sequence {
			return 0, 0, fail("selected group history data")
		}
	}
	return permissionID, groupID, nil
}

func identitySessionPermission(ctx context.Context, client *is.Client, state *sessionState, id, revision int64, name string) error {
	response, err := client.GodjIdentityPermissionsDetail(ctx, is.GodjIdentityPermissionsDetailParams{ID: identityID(id)})
	current, ok := response.(*is.PermissionHeaders)
	if err != nil || !ok || current.Revision != revision || current.Response.Revision != revision || current.Response.ID != id || current.Response.Fields.Name != name || current.Response.Fields.Code != "consumer.manage.view" || !current.XGodjCsrftoken.Set || !state.ready(current.XGodjCsrftoken.Value) {
		return fail("identity permission readback")
	}
	return nil
}

func identitySessionGroup(ctx context.Context, client *is.Client, state *sessionState, id, revision int64, name string, permissions []int64) error {
	response, err := client.GodjIdentityGroupsDetail(ctx, is.GodjIdentityGroupsDetailParams{ID: identityID(id)})
	current, ok := response.(*is.GroupHeaders)
	if err != nil || !ok || current.Revision != revision || current.Response.Revision != revision || current.Response.ID != id || current.Response.Fields.Name != name || !slices.Equal(current.Response.Fields.Permissions, permissions) || !current.XGodjCsrftoken.Set || !state.ready(current.XGodjCsrftoken.Value) {
		return fail("identity group readback")
	}
	return nil
}

func identityHostDeletion(ctx context.Context, client *is.Client, target identityEndpoint) error {
	user, err := client.GodjIdentityAccountsDelete(ctx, is.GodjIdentityAccountsDeleteParams{ID: identityID(target.ProtectedUserID), IfRevision: 1})
	u, ok := user.(*is.GodjIdentityAccountsDeleteBadRequest)
	if err != nil || !ok || !identityProtected(u.Code, u.Errors) {
		return fail("identity user protected host")
	}
	group, err := client.GodjIdentityGroupsDelete(ctx, is.GodjIdentityGroupsDeleteParams{ID: identityID(target.ProtectedGroupID), IfRevision: 1})
	g, ok := group.(*is.GodjIdentityGroupsDeleteBadRequest)
	if err != nil || !ok || !identityProtected(g.Code, g.Errors) {
		return fail("identity group protected host")
	}
	permission, err := client.GodjIdentityPermissionsDelete(ctx, is.GodjIdentityPermissionsDeleteParams{ID: identityID(target.ProtectedPermissionID), IfRevision: 1})
	p, ok := permission.(*is.GodjIdentityPermissionsDeleteBadRequest)
	if err != nil || !ok || !identityProtected(p.Code, p.Errors) {
		return fail("identity permission protected host")
	}
	deletedUser, err := client.GodjIdentityAccountsDelete(ctx, is.GodjIdentityAccountsDeleteParams{ID: identityID(target.CascadeUserID), IfRevision: 1})
	if receipt, ok := deletedUser.(*is.GodjIdentityAccountsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 1 || receipt.Response.Revision != 1 || receipt.Response.ID != target.CascadeUserID {
		return fail("identity user host cascade")
	}
	deletedGroup, err := client.GodjIdentityGroupsDelete(ctx, is.GodjIdentityGroupsDeleteParams{ID: identityID(target.CascadeGroupID), IfRevision: 1})
	if receipt, ok := deletedGroup.(*is.GodjIdentityGroupsDeleteOKHeaders); err != nil || !ok || receipt.Revision != 1 || receipt.Response.Revision != 1 || receipt.Response.ID != target.CascadeGroupID {
		return fail("identity group host set null")
	}
	deletedPermission, err := client.GodjIdentityPermissionsDelete(ctx, is.GodjIdentityPermissionsDeleteParams{ID: identityID(target.CascadePermissionID), IfRevision: 1})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fail("identity permission host cascade request deadline")
		}
		if errors.Is(err, context.Canceled) {
			return fail("identity permission host cascade request canceled")
		}
		return fail("identity permission host cascade request failed")
	}
	if receipt, ok := deletedPermission.(*is.GodjIdentityPermissionsDeleteOKHeaders); !ok || receipt.Revision != 1 || receipt.Response.Revision != 1 || receipt.Response.ID != target.CascadePermissionID {
		return fail("identity permission host cascade response type")
	}
	return nil
}

func identityProtected(code string, fields []is.GoDjAPIErrorErrorsItem) bool {
	return code == "validation_error" && len(fields) == 1 && fields[0].Field == "/fields/__all__" && fields[0].Code == "protected"
}
