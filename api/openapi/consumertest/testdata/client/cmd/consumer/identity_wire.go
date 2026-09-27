package main

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"

	ib "example.com/godj-openapi-client/identitybearer"
	"github.com/ogen-go/ogen/ogenerrors"
)

const identityWireBase = `{"id":9223372036854775807,"username":"wire","first_name":"","last_name":"","email":"","active":true,"staff":false,"superuser":false,"date_joined":"2026-09-27T00:00:00Z","last_login":null,"revision":1152921504606846977`
const identityWireCollections = `,"groups":[1152921504606846977,9223372036854775807],"permissions":[]}`
const identityWireRevision = int64(1152921504606846977)

// Fixed synthetic responses exercise the independent generated wire decoder.
// They do not count as execution of the real management server's failure paths.
func checkGeneratedIdentityWire(ctx context.Context) error {
	if err := checkGeneratedUnusablePasswordWire(ctx); err != nil {
		return err
	}
	for _, test := range []struct {
		patch    ib.UserPatch
		expected string
	}{
		{ib.UserPatch{}, `{}`},
		{ib.UserPatch{Groups: []int64{}, Permissions: []int64{}}, `{"groups":[],"permissions":[]}`},
		{ib.UserPatch{Groups: []int64{identityWireRevision, math.MaxInt64}}, `{"groups":[1152921504606846977,9223372036854775807]}`},
	} {
		calls := 0
		client, err := identityWireClient(identityWireBase+identityWireCollections, test.expected, "1152921504606846977", 200, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjIdentityIdentityUsersPatch(ctx, &test.patch, ib.GodjIdentityIdentityUsersPatchParams{ID: math.MaxInt64, IfRevision: identityWireRevision})
		user, ok := response.(*ib.UserHeaders)
		if err != nil || !ok || calls != 1 || user.Revision != identityWireRevision || user.Response.Revision != identityWireRevision || user.Response.ID != math.MaxInt64 || !slices.Equal(user.Response.Groups, []int64{identityWireRevision, math.MaxInt64}) || len(user.Response.Permissions) != 0 || !user.Response.LastLogin.Null {
			return fail("generated identity exact integer revision and collections")
		}
	}
	for _, suffix := range []string{`}`, `,"permissions":[]}`, `,"groups":[],"permissions":null}`, `,"groups":null,"permissions":[]}`, `,"groups":["1"],"permissions":[]}`, `,"groups":[1.0],"permissions":[]}`, `,"groups":[9223372036854775808],"permissions":[]}`} {
		calls := 0
		client, err := identityWireClient(identityWireBase+suffix, `{}`, "1152921504606846977", 200, &calls)
		if err != nil {
			return err
		}
		_, err = client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{}, ib.GodjIdentityIdentityUsersPatchParams{ID: math.MaxInt64, IfRevision: identityWireRevision})
		var decode *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decode) {
			return fail("generated identity required collection rejection")
		}
	}
	for _, revision := range []string{"", "0", "-1", "1.0", "9223372036854775808", "invalid"} {
		calls := 0
		client, err := identityWireClient(identityWireBase+identityWireCollections, `{}`, revision, 200, &calls)
		if err != nil {
			return err
		}
		_, err = client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{}, ib.GodjIdentityIdentityUsersPatchParams{ID: math.MaxInt64, IfRevision: identityWireRevision})
		if calls != 1 || err == nil {
			return fail("generated identity required revision header rejection")
		}
	}
	// A failed condition or unknown outcome is a typed result, never a success
	// DTO and never an implicit retry of this write.
	for _, status := range []int{412, 428, 503} {
		code := map[int]string{412: "revision_conflict", 428: "precondition_required", 503: "outcome_unknown"}[status]
		calls := 0
		client, err := identityWireClient(`{"code":"`+code+`","errors":[]}`, `{}`, "", status, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{}, ib.GodjIdentityIdentityUsersPatchParams{ID: math.MaxInt64, IfRevision: identityWireRevision})
		if err != nil || calls != 1 {
			return fail("generated identity failure retried or unclassified")
		}
		var actual string
		switch typed := response.(type) {
		case *ib.GodjIdentityIdentityUsersPatchPreconditionFailed:
			if status != 412 {
				return fail("generated identity conflict classification")
			}
			actual = typed.Code
		case *ib.GodjIdentityIdentityUsersPatchPreconditionRequired:
			if status != 428 {
				return fail("generated identity missing condition classification")
			}
			actual = typed.Code
		case *ib.GodjIdentityIdentityUsersPatchServiceUnavailable:
			if status != 503 {
				return fail("generated identity unknown outcome classification")
			}
			actual = typed.Code
		default:
			return fail("generated identity failed mutation returned success")
		}
		if actual != code {
			return fail("generated identity failure code")
		}
	}
	return nil
}

func identityWireClient(body, expected, revision string, status int, calls *int) (*ib.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/identity/users/9223372036854775807/" || request.Header.Get("If-Revision") != "1152921504606846977" || len(request.Header.Values("If-Revision")) != 1 || request.Header.Get("Authorization") != "Bearer probe-only" || len(request.Cookies()) != 0 || string(wire) != expected {
			return nil, fail("generated identity request wire")
		}
		headers := http.Header{"Content-Type": []string{"application/json"}}
		if revision != "" {
			headers.Set("Revision", revision)
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return ib.NewClient("https://probe.invalid", identityBearerSource{"probe-only"}, ib.WithClient(&http.Client{Transport: transport}))
}
