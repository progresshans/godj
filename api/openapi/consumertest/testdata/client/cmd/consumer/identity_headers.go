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
		{"missing", nil, 428, "required"},
		{"empty", http.Header{"If-Revision": {""}}, 400, "invalid"},
		{"duplicate", http.Header{"If-Revision": {"1", "1"}}, 400, "invalid"},
		{"case_alias", http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, 400, "invalid"},
		{"leading_zero", http.Header{"If-Revision": {"01"}}, 400, "invalid"},
		{"plus", http.Header{"If-Revision": {"+1"}}, 400, "invalid"},
		{"encoded_digit", http.Header{"If-Revision": {"%31"}}, 400, "invalid"},
		{"comma", http.Header{"If-Revision": {"1,1"}}, 400, "invalid"},
		{"zero", http.Header{"If-Revision": {"0"}}, 400, "invalid"},
		{"maximum", http.Header{"If-Revision": {"9223372036854775807"}}, 400, "invalid"},
		{"overflow", http.Header{"If-Revision": {"9223372036854775808"}}, 400, "invalid"},
		{"byte_limit", http.Header{"If-Revision": {"11111111111111111111"}}, 400, "invalid"},
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
	before, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	initial, ok := before.(*ib.AccountHeaders)
	if err != nil || !ok || initial.Response.Revision != 1 {
		return fail("identity bearer header initial state")
	}
	path := "/api/identity/accounts/" + strconv.FormatInt(target.TargetID, 10) + "/"
	err = identityHeaderProbes(httpClient, path, func() (ib.GodjIdentityAccountsPatchRes, error) {
		return client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 1})
	}, func(result ib.GodjIdentityAccountsPatchRes, status int, code string) bool {
		switch response := result.(type) {
		case *ib.GodjIdentityAccountsPatchBadRequest:
			return status == 400 && identityBearerHeaderError(response.Response.Code, response.Response.Errors, code)
		case *ib.GodjIdentityAccountsPatchPreconditionRequired:
			return status == 428 && identityBearerHeaderError(response.Code, response.Errors, code)
		case *ib.GodjIdentityAccountsPatchPreconditionFailed:
			return status == 412 && response.Code == code
		case *ib.GodjIdentityAccountsPatchOKHeaders:
			return status == 200 && response.Revision == 1 && response.Response.Revision == 1 && response.Response.ID == target.TargetID
		default:
			return false
		}
	})
	if err != nil {
		return err
	}
	after, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	final, ok := after.(*ib.AccountHeaders)
	if err != nil || !ok || final.Revision != 1 || !reflect.DeepEqual(final.Response, initial.Response) {
		return fail("identity bearer header changed stored state")
	}
	return nil
}

func checkIdentitySessionHeaders(ctx context.Context, client *is.Client, httpClient *http.Client, target identityEndpoint, state *sessionState) error {
	before, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	initial, ok := before.(*is.AccountHeaders)
	if err != nil || !ok || initial.Response.Revision != 1 || !initial.XGodjCsrftoken.Set || !state.ready(initial.XGodjCsrftoken.Value) {
		return fail("identity session header initial state")
	}
	path := "/api/identity/accounts/" + strconv.FormatInt(target.TargetID, 10) + "/"
	err = identityHeaderProbes(httpClient, path, func() (is.GodjIdentityAccountsPatchRes, error) {
		return client.GodjIdentityAccountsPatch(ctx, &is.AccountPatch{Fields: is.AccountPatchFields{}}, is.GodjIdentityAccountsPatchParams{ID: identityID(target.TargetID), IfRevision: 1})
	}, func(result is.GodjIdentityAccountsPatchRes, status int, code string) bool {
		switch response := result.(type) {
		case *is.GodjIdentityAccountsPatchBadRequest:
			return status == 400 && identitySessionHeaderError(response.Code, response.Errors, code)
		case *is.GodjIdentityAccountsPatchPreconditionRequired:
			return status == 428 && identitySessionHeaderError(response.Code, response.Errors, code)
		case *is.GodjIdentityAccountsPatchPreconditionFailed:
			return status == 412 && response.Code == code
		// Safe detail responses rotate the CSRF pair. The unsafe PATCH
		// response owns only confirmed identity and revision metadata.
		case *is.GodjIdentityAccountsPatchOKHeaders:
			return status == 200 && response.Revision == 1 && response.Response.Revision == 1 && response.Response.ID == target.TargetID
		default:
			return false
		}
	})
	if err != nil {
		return err
	}
	after, err := client.GodjIdentityAccountsDetail(ctx, is.GodjIdentityAccountsDetailParams{ID: identityID(target.TargetID)})
	final, ok := after.(*is.AccountHeaders)
	if err != nil || !ok || final.Revision != 1 || !reflect.DeepEqual(final.Response, initial.Response) || !final.XGodjCsrftoken.Set || !state.ready(final.XGodjCsrftoken.Value) {
		return fail("identity session header changed stored state")
	}
	return nil
}

func identityID(value int64) string { return strconv.FormatInt(value, 10) }

func identityBearerHeaderError(code string, fields []ib.GoDjAPIErrorErrorsItem, reason string) bool {
	return code == "validation_error" && len(fields) == 1 && fields[0].Field == "If-Revision" && fields[0].Code == reason
}
func identitySessionHeaderError(code string, fields []is.GoDjAPIErrorErrorsItem, reason string) bool {
	return code == "validation_error" && len(fields) == 1 && fields[0].Field == "If-Revision" && fields[0].Code == reason
}
