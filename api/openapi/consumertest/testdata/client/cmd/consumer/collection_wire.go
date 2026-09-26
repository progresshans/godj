package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
)

func checkGeneratedCollectionWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"external_payload":null,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"service_on":null,"service_at":null,"reviewed":null`
	const large = int64(1152921504606846977)
	for _, test := range []struct {
		keys []int64
		wire string
	}{{nil, `{}`}, {[]int64{}, `{"labels":[]}`}, {[]int64{7, large}, `{"labels":[7,1152921504606846977]}`}} {
		calls := 0
		client, err := collectionWireClient(base+`,"labels":[1152921504606846977,9223372036854775807]}`, test.wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{Labels: test.keys}, hs.HelpdeskTicketPatchParams{ID: 1})
		value, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || !slices.Equal(value.Labels, []int64{large, 9223372036854775807}) {
			return fail("generated exact integer collection wire")
		}
	}
	for _, suffix := range []string{`}`, `,"labels":null}`, `,"labels":["7"]}`, `,"labels":[7.0]}`, `,"labels":[9223372036854775808]}`} {
		calls := 0
		client, err := collectionWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		_, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1})
		var decodeError *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decodeError) {
			return fail("generated collection rejects missing null and wrong members")
		}
	}
	return nil
}

func collectionWireClient(body, expected string, calls *int) (*hs.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1/" || string(wire) != expected {
			return nil, fail("generated collection request presence changed")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: transport}))
}
