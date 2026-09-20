package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
)

func checkGeneratedNullableBooleanWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"service_on":null,"service_at":null`
	clear := hs.OptNilBool{}
	clear.SetToNull()
	for _, test := range []struct {
		request     hs.TicketPatch
		wire, value string
	}{
		{hs.TicketPatch{}, `{}`, "null"},
		{hs.TicketPatch{Reviewed: hs.NewOptNilBool(false)}, `{"reviewed":false}`, "false"},
		{hs.TicketPatch{Reviewed: hs.NewOptNilBool(true)}, `{"reviewed":true}`, "true"},
		{hs.TicketPatch{Reviewed: clear}, `{"reviewed":null}`, "null"},
	} {
		calls := 0
		client, err := nullableBooleanWireClient(base+`,"reviewed":`+test.value+`}`, test.wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.Reviewed.Null != (test.value == "null") || row.Reviewed.Value != (test.value == "true") {
			return fail("generated nullable Boolean request or response wire")
		}
	}
	for _, suffix := range []string{`}`, `,"reviewed":0}`, `,"reviewed":"false"}`, `,"reviewed":[]}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		_, err = client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1})
		var decodeError *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decodeError) {
			return fail("generated required nullable Boolean response rejection")
		}
	}
	return nil
}

func nullableBooleanWireClient(body, expected string, calls *int) (*hs.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1/" || string(wire) != expected {
			return nil, fail("generated Boolean body changed omission or false/null")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: transport}))
}
