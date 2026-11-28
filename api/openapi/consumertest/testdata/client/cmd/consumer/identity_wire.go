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
	"github.com/ogen-go/ogen/validate"
)

const identityWireBase = `{"id":9223372036854775807,"revision":1152921504606846977,"password_usable":true,"fields":{"username":"wire","first_name":"","last_name":"","email":"","active":true,"staff":false,"superuser":false,"date_joined":"2026-09-27T00:00:00Z","last_login":null`
const identityWireCollections = `,"groups":[1152921504606846977,9223372036854775807],"permissions":[]}}`
const identityWireReceipt = `{"id":9223372036854775807,"revision":1152921504606846977}`
const identityWireRevision = int64(1152921504606846977)

// Fixed synthetic responses exercise the independent generated wire decoder.
// They do not count as execution of the real management server's failure paths.
func checkGeneratedIdentityWire(ctx context.Context) error {
	if err := checkGeneratedPasswordStatusWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedUnusablePasswordWire(ctx); err != nil {
		return err
	}
	for _, test := range []struct {
		patch    ib.AccountPatch
		expected string
	}{
		{ib.AccountPatch{Fields: ib.AccountPatchFields{}}, `{"fields":{}}`},
		{ib.AccountPatch{Fields: ib.AccountPatchFields{Groups: []int64{}, Permissions: []int64{}}}, `{"fields":{"groups":[],"permissions":[]}}`},
		{ib.AccountPatch{Fields: ib.AccountPatchFields{Groups: []int64{identityWireRevision, math.MaxInt64}}}, `{"fields":{"groups":[1152921504606846977,9223372036854775807]}}`},
	} {
		calls := 0
		client, err := identityWireClient("PATCH", identityWireReceipt, test.expected, "1152921504606846977", 200, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjIdentityAccountsPatch(ctx, &test.patch, ib.GodjIdentityAccountsPatchParams{ID: identityID(math.MaxInt64), IfRevision: identityWireRevision})
		receipt, ok := response.(*ib.GodjIdentityAccountsPatchOKHeaders)
		if err != nil || !ok || calls != 1 || receipt.Revision != identityWireRevision || receipt.Response.Revision != identityWireRevision || receipt.Response.ID != math.MaxInt64 {
			return fail("generated selected mutation exact integers and collections")
		}
	}
	calls := 0
	client, err := identityWireClient("GET", identityWireBase+identityWireCollections, "", "1152921504606846977", 200, &calls)
	if err != nil {
		return err
	}
	read, err := client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(math.MaxInt64)})
	account, ok := read.(*ib.AccountHeaders)
	if err != nil || !ok || calls != 1 || account.Revision != identityWireRevision || account.Response.Revision != identityWireRevision || account.Response.ID != math.MaxInt64 || !slices.Equal(account.Response.Fields.Groups, []int64{identityWireRevision, math.MaxInt64}) || len(account.Response.Fields.Permissions) != 0 || !account.Response.Fields.LastLogin.Null {
		return fail("generated selected read exact integers and collections")
	}
	for _, suffix := range []string{`}}`, `,"permissions":[]}}`, `,"groups":[],"permissions":null}}`, `,"groups":null,"permissions":[]}}`, `,"groups":["1"],"permissions":[]}}`, `,"groups":[1.0],"permissions":[]}}`, `,"groups":[9223372036854775808],"permissions":[]}}`} {
		calls := 0
		client, err := identityWireClient("GET", identityWireBase+suffix, "", "1152921504606846977", 200, &calls)
		if err != nil {
			return err
		}
		_, err = client.GodjIdentityAccountsDetail(ctx, ib.GodjIdentityAccountsDetailParams{ID: identityID(math.MaxInt64)})
		var decode *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decode) {
			return fail("generated selected read required collection rejection")
		}
	}
	for _, body := range []string{`{}`, `{"id":1}`, `{"revision":1}`, `{"id":"1","revision":1}`, `{"id":9223372036854775808,"revision":1}`} {
		calls := 0
		client, err := identityWireClient("PATCH", body, `{"fields":{}}`, "1152921504606846977", 200, &calls)
		if err != nil {
			return err
		}
		_, err = client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(math.MaxInt64), IfRevision: identityWireRevision})
		var decode *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decode) {
			return fail("generated selected mutation required metadata rejection")
		}
	}
	// A well-formed int64 outside the schema range reaches ogen's response
	// validator after decoding. It must still return no receipt and no retry.
	for _, revision := range []string{"0", "-1"} {
		calls := 0
		client, err := identityWireClient("PATCH", `{"id":1,"revision":`+revision+`}`, `{"fields":{}}`, "1152921504606846977", 200, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(math.MaxInt64), IfRevision: identityWireRevision})
		var invalid *validate.Error
		if calls != 1 || response != nil || !errors.As(err, &invalid) {
			return fail("generated selected mutation revision range rejection")
		}
	}
	for _, revision := range []string{"", "0", "-1", "1.0", "9223372036854775808", "invalid"} {
		calls := 0
		client, err := identityWireClient("PATCH", identityWireReceipt, `{"fields":{}}`, revision, 200, &calls)
		if err != nil {
			return err
		}
		_, err = client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(math.MaxInt64), IfRevision: identityWireRevision})
		if calls != 1 || err == nil {
			return fail("generated identity required revision header rejection")
		}
	}
	// Failed conditions and unknown outcomes remain typed results and are
	// never accepted as receipts or retried as writes.
	for _, status := range []int{412, 428, 503} {
		code := map[int]string{412: "revision_conflict", 428: "validation_error", 503: "outcome_unknown"}[status]
		fields := `[]`
		if status == 428 {
			fields = `[{"field":"If-Revision","code":"required","params":[]}]`
		}
		calls := 0
		client, err := identityWireClient("PATCH", `{"code":"`+code+`","errors":`+fields+`}`, `{"fields":{}}`, "", status, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjIdentityAccountsPatch(ctx, &ib.AccountPatch{Fields: ib.AccountPatchFields{}}, ib.GodjIdentityAccountsPatchParams{ID: identityID(math.MaxInt64), IfRevision: identityWireRevision})
		if err != nil || calls != 1 {
			return fail("generated identity failure retried or unclassified")
		}
		var actual string
		switch typed := response.(type) {
		case *ib.GodjIdentityAccountsPatchPreconditionFailed:
			if status != 412 {
				return fail("generated identity conflict classification")
			}
			actual = typed.Code
		case *ib.GodjIdentityAccountsPatchPreconditionRequired:
			if status != 428 || !identityBearerHeaderError(typed.Code, typed.Errors, "required") {
				return fail("generated identity missing condition classification")
			}
			actual = typed.Code
		case *ib.GodjIdentityAccountsPatchServiceUnavailable:
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

func identityWireClient(method, body, expected, revision string, status int, calls *int) (*ib.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		var wire []byte
		var err error
		if request.Body != nil {
			wire, err = io.ReadAll(request.Body)
		}
		condition := method == "GET" && request.Header.Get("If-Revision") == "" || method == "PATCH" && request.Header.Get("If-Revision") == "1152921504606846977" && len(request.Header.Values("If-Revision")) == 1
		if err != nil || request.Method != method || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/identity/accounts/9223372036854775807/" || !condition || request.Header.Get("Authorization") != "Bearer probe-only" || len(request.Cookies()) != 0 || string(wire) != expected {
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
