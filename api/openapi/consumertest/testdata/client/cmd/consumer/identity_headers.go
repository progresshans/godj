package main

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"sync/atomic"

	ib "example.com/godj-openapi-client/identitybearer"
	is "example.com/godj-openapi-client/identitysession"
)

// Only this negative transport probe changes the generated required header.
// The application calls still use the generated exact int64 parameter and
// retain the original credential/CSRF transport and typed response decoder.
func identityHeaderProbes[R any](client *http.Client, path string, invoke func() (R, error), valid func(R, int, string) bool) error {
	transport := client.Transport
	defer func() { client.Transport = transport }()
	for _, test := range []struct {
		name   string
		fields http.Header
		status int
		code   string
	}{
		{"missing", nil, 428, "precondition_required"},
		{"empty", http.Header{"If-Revision": {""}}, 400, "invalid_precondition"},
		{"duplicate", http.Header{"If-Revision": {"1", "1"}}, 400, "invalid_precondition"},
		{"case_alias", http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, 400, "invalid_precondition"},
		{"leading_zero", http.Header{"If-Revision": {"01"}}, 400, "invalid_precondition"},
		{"plus", http.Header{"If-Revision": {"+1"}}, 400, "invalid_precondition"},
		{"encoded_digit", http.Header{"If-Revision": {"%31"}}, 400, "invalid_precondition"},
		{"comma", http.Header{"If-Revision": {"1,1"}}, 400, "invalid_precondition"},
		{"zero", http.Header{"If-Revision": {"0"}}, 400, "invalid_precondition"},
		{"maximum", http.Header{"If-Revision": {"9223372036854775807"}}, 400, "invalid_precondition"},
		{"overflow", http.Header{"If-Revision": {"9223372036854775808"}}, 400, "invalid_precondition"},
		{"byte_limit", http.Header{"If-Revision": {"11111111111111111111"}}, 400, "invalid_precondition"},
		{"large_stale", http.Header{"If-Revision": {"9007199254740993"}}, 412, "revision_conflict"},
		{"maximum_stale", http.Header{"If-Revision": {"9223372036854775806"}}, 412, "revision_conflict"},
		{"mixed_case", http.Header{"iF-rEVISION": {"1"}}, 200, ""},
	} {
		var calls atomic.Int64
		client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			if request.Method != http.MethodPatch || request.URL.Path != path || request.Header.Get("If-Revision") != "1" {
				return nil, fail("identity header probe request")
			}
			request.Header.Del("If-Revision")
			for name, values := range test.fields {
				request.Header[name] = append([]string(nil), values...)
			}
			return transport.RoundTrip(request)
		})
		result, err := invoke()
		if err != nil || calls.Load() != 1 || !valid(result, test.status, test.code) {
			return fail("identity typed header " + test.name + " response or retry")
		}
	}
	return nil
}

func checkIdentityBearerHeaders(ctx context.Context, client *ib.Client, httpClient *http.Client, target identityEndpoint) error {
	before, err := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	initial, ok := before.(*ib.UserHeaders)
	if err != nil || !ok || initial.Response.Revision != 1 {
		return fail("identity bearer header initial state")
	}
	path := "/api/identity/users/" + strconv.FormatInt(target.TargetID, 10) + "/"
	err = identityHeaderProbes(httpClient, path, func() (ib.GodjIdentityIdentityUsersPatchRes, error) {
		return client.GodjIdentityIdentityUsersPatch(ctx, &ib.UserPatch{}, ib.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 1})
	}, func(result ib.GodjIdentityIdentityUsersPatchRes, status int, code string) bool {
		switch response := result.(type) {
		case *ib.GodjIdentityIdentityUsersPatchBadRequest:
			return status == 400 && response.Response.Code == code && len(response.Response.Errors) == 0
		case *ib.GodjIdentityIdentityUsersPatchPreconditionRequired:
			return status == 428 && response.Code == code && len(response.Errors) == 0
		case *ib.GodjIdentityIdentityUsersPatchPreconditionFailed:
			return status == 412 && response.Code == code
		case *ib.UserHeaders:
			return status == 200 && response.Revision == 1 && reflect.DeepEqual(response.Response, initial.Response)
		default:
			return false
		}
	})
	if err != nil {
		return err
	}
	after, err := client.GodjIdentityIdentityUsersDetail(ctx, ib.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	final, ok := after.(*ib.UserHeaders)
	if err != nil || !ok || final.Revision != 1 || !reflect.DeepEqual(final.Response, initial.Response) {
		return fail("identity bearer header changed stored state")
	}
	return nil
}

func checkIdentitySessionHeaders(ctx context.Context, client *is.Client, httpClient *http.Client, target identityEndpoint, state *sessionState) error {
	before, err := client.GodjIdentityIdentityUsersDetail(ctx, is.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	initial, ok := before.(*is.GodjIdentityIdentityUsersDetailOKHeaders)
	if err != nil || !ok || initial.Response.Revision != 1 || !initial.XGodjCsrftoken.Set || !state.ready(initial.XGodjCsrftoken.Value) {
		return fail("identity session header initial state")
	}
	path := "/api/identity/users/" + strconv.FormatInt(target.TargetID, 10) + "/"
	err = identityHeaderProbes(httpClient, path, func() (is.GodjIdentityIdentityUsersPatchRes, error) {
		return client.GodjIdentityIdentityUsersPatch(ctx, &is.UserPatch{}, is.GodjIdentityIdentityUsersPatchParams{ID: target.TargetID, IfRevision: 1})
	}, func(result is.GodjIdentityIdentityUsersPatchRes, status int, code string) bool {
		switch response := result.(type) {
		case *is.GodjIdentityIdentityUsersPatchBadRequest:
			return status == 400 && response.Code == code && len(response.Errors) == 0
		case *is.GodjIdentityIdentityUsersPatchPreconditionRequired:
			return status == 428 && response.Code == code && len(response.Errors) == 0
		case *is.GodjIdentityIdentityUsersPatchPreconditionFailed:
			return status == 412 && response.Code == code
		// Safe detail responses rotate the CSRF pair. The unsafe PATCH
		// response owns only the row revision and represented user.
		case *is.UserHeaders:
			return status == 200 && response.Revision == 1 && reflect.DeepEqual(response.Response, initial.Response)
		default:
			return false
		}
	})
	if err != nil {
		return err
	}
	after, err := client.GodjIdentityIdentityUsersDetail(ctx, is.GodjIdentityIdentityUsersDetailParams{ID: target.TargetID})
	final, ok := after.(*is.GodjIdentityIdentityUsersDetailOKHeaders)
	if err != nil || !ok || final.Revision != 1 || !reflect.DeepEqual(final.Response, initial.Response) || !final.XGodjCsrftoken.Set || !state.ready(final.XGodjCsrftoken.Value) {
		return fail("identity session header changed stored state")
	}
	return nil
}
