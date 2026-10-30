package main

import (
	"context"
	"strings"

	ib "example.com/godj-openapi-client/identitybearer"
	is "example.com/godj-openapi-client/identitysession"
)

func checkIdentitySessionEmails(ctx context.Context, client *is.Client, target identityEndpoint, state *sessionState) error {
	initial, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	original, ok := initial.(*is.AccountHeaders)
	if err != nil || !ok || original.Response.Fields.Email != "  legacy-address  " || original.Response.Revision != 1 || !original.XGodjCsrftoken.Set || !state.ready(original.XGodjCsrftoken.Value) {
		return fail("identity session legacy email response")
	}
	for _, probe := range []struct{ value, code string }{{"not-an-email", "invalid"}, {strings.Repeat("a", 243) + "@example.com", "max_length"}} {
		rejected, err := client.GodjIdentityAccountsCreate(ctx, &is.AccountCreate{Fields: is.AccountCreateFields{Username: "Email-rejection", Email: is.NewOptString(probe.value)}, Credential: is.AccountCreateCredential{Password: is.NewNilString("independent strong credential")}})
		failure, ok := rejected.(*is.GodjIdentityAccountsCreateBadRequest)
		if err != nil || !ok || failure.Code != "validation_error" || len(failure.Errors) != 1 || failure.Errors[0].Field != "/fields/email" || failure.Errors[0].Code != probe.code {
			return fail("identity session email creation rejection")
		}
		patched, err := client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{Email: is.NewOptString(probe.value)}}, is.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 1})
		patchFailure, ok := patched.(*is.GodjIdentityAccountsPatchBadRequest)
		if err != nil || !ok || patchFailure.Code != "validation_error" || len(patchFailure.Errors) != 1 || patchFailure.Errors[0].Field != "/fields/email" || patchFailure.Errors[0].Code != probe.code {
			return fail("identity session email patch rejection")
		}
	}
	final, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	unchanged, ok := final.(*is.AccountHeaders)
	if err != nil || !ok || unchanged.Response.Fields.Email != original.Response.Fields.Email || unchanged.Response.Revision != original.Response.Revision || !unchanged.XGodjCsrftoken.Set || !state.ready(unchanged.XGodjCsrftoken.Value) {
		return fail("identity session invalid email mutated user")
	}
	return nil
}

func checkIdentityBearerEmails(ctx context.Context, client *ib.Client, target identityEndpoint) error {
	initial, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	original, ok := initial.(*ib.AccountHeaders)
	if err != nil || !ok || original.Response.Fields.Email != "  legacy-address  " || original.Response.Revision != 1 {
		return fail("identity bearer legacy email response")
	}
	for _, probe := range []struct{ value, code string }{{"not-an-email", "invalid"}, {strings.Repeat("a", 243) + "@example.com", "max_length"}} {
		rejected, err := client.GodjIdentityAccountsCreate(ctx, &ib.AccountCreate{Fields: ib.AccountCreateFields{Username: "Email-rejection", Email: ib.NewOptString(probe.value)}, Credential: ib.AccountCreateCredential{Password: ib.NewNilString("independent strong credential")}})
		failure, ok := rejected.(*ib.GodjIdentityAccountsCreateBadRequest)
		if err != nil || !ok || failure.Response.Code != "validation_error" || len(failure.Response.Errors) != 1 || failure.Response.Errors[0].Field != "/fields/email" || failure.Response.Errors[0].Code != probe.code {
			return fail("identity bearer email creation rejection")
		}
		patched, err := client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{Email: ib.NewOptString(probe.value)}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 1})
		patchFailure, ok := patched.(*ib.GodjIdentityAccountsPatchBadRequest)
		if err != nil || !ok || patchFailure.Response.Code != "validation_error" || len(patchFailure.Response.Errors) != 1 || patchFailure.Response.Errors[0].Field != "/fields/email" || patchFailure.Response.Errors[0].Code != probe.code {
			return fail("identity bearer email patch rejection")
		}
	}
	final, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	unchanged, ok := final.(*ib.AccountHeaders)
	if err != nil || !ok || unchanged.Response.Fields.Email != original.Response.Fields.Email || unchanged.Response.Revision != original.Response.Revision {
		return fail("identity bearer invalid email mutated user")
	}
	return nil
}
