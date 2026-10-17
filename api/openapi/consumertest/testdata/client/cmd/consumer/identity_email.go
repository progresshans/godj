package main

import (
	"context"
	"strings"

	ib "example.com/godj-openapi-client/identitybearer"
	is "example.com/godj-openapi-client/identitysession"
)

func checkIdentitySessionEmails(ctx context.Context, client *is.Client, target identityEndpoint, state *sessionState) error {
	initial, err := client.GodjIdentityIdentityUsersDetail(ctx, is.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	original, ok := initial.(*is.GodjIdentityIdentityUsersDetailOKHeaders)
	if err != nil || !ok || original.Response.Email != "  legacy-address  " || original.Response.Revision != 1 || !original.XGodjCsrftoken.Set || !state.ready(original.XGodjCsrftoken.Value) {
		return fail("identity session legacy email response")
	}
	for _, probe := range []struct{ value, code string }{{"not-an-email", "invalid"}, {strings.Repeat("a", 243) + "@example.com", "max_length"}} {
		rejected, err := client.GodjIdentityIdentityUsersCreate(ctx, &is.UserCreate{Username: "Email-rejection", Password: is.NewNilString("independent strong credential"), Email: is.NewOptString(probe.value)})
		failure, ok := rejected.(*is.GodjIdentityIdentityUsersCreateBadRequest)
		if err != nil || !ok || failure.Code != "validation_error" || len(failure.Errors) != 1 || failure.Errors[0].Field != "email" || failure.Errors[0].Code != probe.code {
			return fail("identity session email creation rejection")
		}
		patched, err := client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{Email: is.NewOptString(probe.value)}, is.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 1})
		patchFailure, ok := patched.(*is.GodjIdentityIdentityUsersPatchBadRequest)
		if err != nil || !ok || patchFailure.Code != "validation_error" || len(patchFailure.Errors) != 1 || patchFailure.Errors[0].Field != "email" || patchFailure.Errors[0].Code != probe.code {
			return fail("identity session email patch rejection")
		}
	}
	final, err := client.GodjIdentityIdentityUsersDetail(ctx, is.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	unchanged, ok := final.(*is.GodjIdentityIdentityUsersDetailOKHeaders)
	if err != nil || !ok || unchanged.Response.Email != original.Response.Email || unchanged.Response.Revision != original.Response.Revision || !unchanged.XGodjCsrftoken.Set || !state.ready(unchanged.XGodjCsrftoken.Value) {
		return fail("identity session invalid email mutated user")
	}
	return nil
}

func checkIdentityBearerEmails(ctx context.Context, client *ib.Client, target identityEndpoint) error {
	initial, err := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	original, ok := initial.(*ib.UserHeaders)
	if err != nil || !ok || original.Response.Email != "  legacy-address  " || original.Response.Revision != 1 {
		return fail("identity bearer legacy email response")
	}
	for _, probe := range []struct{ value, code string }{{"not-an-email", "invalid"}, {strings.Repeat("a", 243) + "@example.com", "max_length"}} {
		rejected, err := client.GodjIdentityIdentityUsersCreate(ctx, &ib.UserCreate{Username: "Email-rejection", Password: ib.NewNilString("independent strong credential"), Email: ib.NewOptString(probe.value)})
		failure, ok := rejected.(*ib.GodjIdentityIdentityUsersCreateBadRequest)
		if err != nil || !ok || failure.Response.Code != "validation_error" || len(failure.Response.Errors) != 1 || failure.Response.Errors[0].Field != "email" || failure.Response.Errors[0].Code != probe.code {
			return fail("identity bearer email creation rejection")
		}
		patched, err := client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{Email: ib.NewOptString(probe.value)}, ib.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 1})
		patchFailure, ok := patched.(*ib.GodjIdentityIdentityUsersPatchBadRequest)
		if err != nil || !ok || patchFailure.Response.Code != "validation_error" || len(patchFailure.Response.Errors) != 1 || patchFailure.Response.Errors[0].Field != "email" || patchFailure.Response.Errors[0].Code != probe.code {
			return fail("identity bearer email patch rejection")
		}
	}
	final, err := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	unchanged, ok := final.(*ib.UserHeaders)
	if err != nil || !ok || unchanged.Response.Email != original.Response.Email || unchanged.Response.Revision != original.Response.Revision {
		return fail("identity bearer invalid email mutated user")
	}
	return nil
}
