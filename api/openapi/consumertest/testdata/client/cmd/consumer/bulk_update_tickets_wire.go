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

func checkGeneratedBulkUpdateTicketWire(ctx context.Context) error {
	const row = `{"id":1152921504606846977,"subject":"wire","details":null,"closed":false,"category":9223372036854775807,"external_payload_digest":null,"external_url":null,"external_payload":null,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"service_on":null,"service_at":null,"reviewed":null,"labels":[]}`
	inputs := []hs.TicketBulkPatch{{ID: 1152921504606846977, Closed: hs.NewOptBool(false), Labels: []int64{}}, {ID: 9223372036854775807}}
	calls := 0
	client := func(status int, body string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			wire, err := io.ReadAll(request.Body)
			if err != nil || request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/bulk/" || string(wire) != `[{"closed":false,"labels":[],"id":1152921504606846977},{"id":9223372036854775807}]` {
				return nil, fail("generated bulk update exact request wire")
			}
			media := "application/json"
			if status == 500 {
				media = "text/plain; charset=utf-8"
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}))
	}
	valid, err := client(200, "["+row+"]")
	if err != nil {
		return err
	}
	response, err := valid.HelpdeskTicketBulkUpdate(ctx, inputs)
	values, ok := response.(*hs.HelpdeskTicketBulkUpdateOKApplicationJSON)
	if err != nil || !ok || calls != 1 || len(*values) != 1 || (*values)[0].ID != 1152921504606846977 {
		return fail("generated bulk update exact int64 response")
	}
	for _, body := range []string{`{}`, `null`, `[null]`, `[{"id":1}]`, "[" + strings.Replace(row, `"id":1152921504606846977`, `"id":1152921504606846977.0`, 1) + "]", "[" + row + "]true"} {
		calls = 0
		malformed, err := client(200, body)
		if err != nil {
			return err
		}
		_, err = malformed.HelpdeskTicketBulkUpdate(ctx, inputs)
		var failure *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &failure) {
			return fail("generated bulk update malformed success")
		}
	}
	if (&hs.TicketBulkPatch{ID: 0}).Validate() == nil || (&hs.TicketBulkPatch{ID: -1}).Validate() == nil || (&hs.TicketBulkPatch{ID: 9223372036854775807}).Validate() != nil || (hs.HelpdeskTicketBulkUpdateOKApplicationJSON{}).Validate() == nil || make(hs.HelpdeskTicketBulkUpdateOKApplicationJSON, 41).Validate() == nil {
		return fail("generated bulk update required ID/count validators")
	}
	calls = 0
	unknown, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = unknown.HelpdeskTicketBulkUpdate(ctx, inputs)
	if _, ok := response.(*hs.HelpdeskTicketBulkUpdateInternalServerError); err != nil || !ok || calls != 1 {
		return fail("generated bulk update uncertain outcome retried or published success")
	}
	return nil
}
