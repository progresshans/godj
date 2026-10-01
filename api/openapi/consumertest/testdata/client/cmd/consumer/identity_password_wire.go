package main

import (
	"context"
	"io"
	"net/http"
	"strings"

	ib "example.com/godj-openapi-client/identitybearer"
)

func checkGeneratedUnusablePasswordWire(ctx context.Context) error {
	for _, input := range []struct {
		password ib.NilString
		wire     string
	}{
		{ib.NilString{Null: true}, `{"password":null}`},
		{ib.NilString{}, `{"password":""}`},
		{ib.NewNilString("  preserved  "), `{"password":"  preserved  "}`},
	} {
		calls := 0
		transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			data, err := io.ReadAll(request.Body)
			if err != nil || string(data) != input.wire || request.Method != "POST" || request.URL.Path != "/api/identity/users/1/password/" || request.Header.Get("If-Revision") != "1" {
				return nil, fail("generated identity nullable password bytes")
			}
			return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":"outcome_unknown","errors":[]}`)), Request: request}, nil
		})
		client, err := ib.NewClient("https://probe.invalid", identityBearerSource{"probe-only"}, ib.WithClient(&http.Client{Transport: transport}))
		if err != nil {
			return err
		}
		body := &ib.PasswordReplacement{Password: input.password}
		result, err := client.GodjIdentityIdentityUsersPassword(ctx, body, ib.GodjIdentityIdentityUsersPasswordParams{ID: 1, IfRevision: 1})
		unknown, ok := result.(*ib.GodjIdentityIdentityUsersPasswordServiceUnavailable)
		if err != nil || !ok || unknown.Code != "outcome_unknown" || calls != 1 {
			return fail("generated identity password unknown retried")
		}
	}
	return nil
}
