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

func checkGeneratedPriorityWire(ctx context.Context) error {
	const row = `{"id":1152921504606846977,"subject":"wire","details":null,"closed":false,"category":9223372036854775807,"external_payload_digest":null,"external_url":null,"external_payload":null,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":-9223372036854775808,"resolution":null,"due_at":null,"service_on":null,"service_at":null,"reviewed":null,"labels":[]}`
	input := &hs.TicketPriorityRaise{Ids: []int64{1152921504606846977, 9223372036854775807}}
	calls := 0
	client := func(status int, body string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			wire, err := io.ReadAll(request.Body)
			if err != nil || request.Method != http.MethodPost || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/raise-priority/" || string(wire) != `{"ids":[1152921504606846977,9223372036854775807]}` {
				return nil, fail("priority generated exact request wire")
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
	response, err := valid.HelpdeskTicketRaisePriority(ctx, input)
	rows, ok := response.(*hs.HelpdeskTicketRaisePriorityOKApplicationJSON)
	if err != nil || !ok || calls != 1 || len(*rows) != 1 || (*rows)[0].ID != 1152921504606846977 || (*rows)[0].Priority.Null || (*rows)[0].Priority.Value != -9223372036854775808 {
		return fail("priority generated exact legacy int64 response")
	}
	for _, body := range []string{`{}`, `null`, `[null]`, `[{"id":1}]`, "[" + strings.Replace(row, `"priority":-9223372036854775808,`, "", 1) + "]", "[" + strings.Replace(row, `"priority":-9223372036854775808`, `"priority":true`, 1) + "]", "[" + strings.Replace(row, `"id":1152921504606846977`, `"id":1152921504606846977.0`, 1) + "]", "[" + row + "]true"} {
		calls = 0
		malformed, err := client(200, body)
		if err != nil {
			return err
		}
		_, err = malformed.HelpdeskTicketRaisePriority(ctx, input)
		var failure *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &failure) {
			return fail("priority generated malformed response")
		}
	}
	for _, ids := range [][]int64{nil, {}, make([]int64, 41), {0}, {-1}} {
		if (&hs.TicketPriorityRaise{Ids: ids}).Validate() == nil {
			return fail("priority generated selection validator")
		}
	}
	if input.Validate() != nil || (hs.HelpdeskTicketRaisePriorityOKApplicationJSON{}).Validate() == nil || make(hs.HelpdeskTicketRaisePriorityOKApplicationJSON, 41).Validate() == nil {
		return fail("priority generated bounds validator")
	}
	calls = 0
	unknown, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = unknown.HelpdeskTicketRaisePriority(ctx, input)
	if _, ok := response.(*hs.HelpdeskTicketRaisePriorityInternalServerError); err != nil || !ok || calls != 1 {
		return fail("priority unknown outcome retried or published success")
	}
	return nil
}
