package main

import (
	"context"
	"io"
	"math"
	"net/http"
	"strings"

	ib "example.com/godj-openapi-client/identitybearer"
)

func checkGeneratedPasswordStatusWire(ctx context.Context) error {
	for _, summary := range []bool{false, true} {
		for _, status := range []string{"true", "false", "", "null", "1", `"true"`} {
			member := ""
			if status != "" {
				member = `,"password_usable":` + status
			}
			body := strings.Replace(identityWireBase, `,"password_usable":true`, member, 1)
			if summary {
				body += "}"
			} else {
				body += identityWireCollections
			}
			calls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "Revision": {"1152921504606846977"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			client, err := ib.NewClient("https://probe.invalid", identityBearerSource{"probe-only"}, ib.WithClient(&http.Client{Transport: transport}))
			if err != nil {
				return err
			}
			var usable, typed bool
			if summary {
				response, callErr := client.GodjIdentityIdentityUsersPassword(ctx, &ib.PasswordReplacement{Password: ib.NilString{Null: true}}, ib.GodjIdentityIdentityUsersPasswordParams{ID: math.MaxInt64, IfRevision: identityWireRevision})
				err = callErr
				if value, ok := response.(*ib.UserSummaryHeaders); ok {
					usable, typed = value.Response.PasswordUsable, true
				}
			} else {
				response, callErr := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: math.MaxInt64})
				err = callErr
				if value, ok := response.(*ib.UserHeaders); ok {
					usable, typed = value.Response.PasswordUsable, true
				}
			}
			if calls != 1 {
				return fail("generated password status decode retried")
			}
			if status == "true" || status == "false" {
				if err != nil || !typed || usable != (status == "true") {
					return fail("generated password status boolean response")
				}
			} else if err == nil || typed {
				return fail("generated password status accepted missing or invalid response")
			}
		}
	}
	return nil
}
