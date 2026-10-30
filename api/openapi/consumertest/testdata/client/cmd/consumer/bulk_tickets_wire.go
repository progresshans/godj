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

func checkGeneratedBulkTicketWire(ctx context.Context) error {
	const row = `{"id":1152921504606846977,"subject":"wire","details":null,"closed":false,"category":9223372036854775807,"external_payload_digest":null,"external_url":null,"external_payload":null,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":null,"resolution":null,"due_at":null,"service_on":null,"service_at":null,"reviewed":null,"labels":[9007199254740993]}`
	inputs := []hs.TicketCreate{{Subject: "first"}, {Subject: "second"}}
	calls := 0
	client, err := bulkTicketWireClient(201, "["+row+"]", &calls)
	if err != nil {
		return err
	}
	response, err := client.HelpdeskTicketBulkCreate(ctx, inputs)
	created, ok := response.(*hs.HelpdeskTicketBulkCreateCreatedApplicationJSON)
	if err != nil || !ok || calls != 1 || len(*created) != 1 || (*created)[0].ID != 1152921504606846977 || (*created)[0].Category != 9223372036854775807 || (*created)[0].Labels[0] != 9007199254740993 {
		return fail("generated bulk exact integer wire")
	}
	for _, body := range []string{`{}`, `null`, `[null]`, `[{"id":1}]`, "[" + strings.Replace(row, `"id":1152921504606846977`, `"id":1152921504606846977.0`, 1) + "]", "[" + row + "]true"} {
		calls = 0
		client, err = bulkTicketWireClient(201, body, &calls)
		if err != nil {
			return err
		}
		_, err = client.HelpdeskTicketBulkCreate(ctx, inputs)
		var decode *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decode) {
			return fail("generated bulk rejects malformed successful response")
		}
	}
	// Ogen exposes schema cardinality through Validate; its decoder owns shape.
	if (hs.HelpdeskTicketBulkCreateCreatedApplicationJSON{}).Validate() == nil || make(hs.HelpdeskTicketBulkCreateCreatedApplicationJSON, 41).Validate() == nil {
		return fail("generated bulk response cardinality validator")
	}
	calls = 0
	client, err = bulkTicketWireClient(500, "Internal Server Error\n", &calls)
	if err != nil {
		return err
	}
	response, err = client.HelpdeskTicketBulkCreate(ctx, inputs)
	if _, ok := response.(*hs.HelpdeskTicketBulkCreateInternalServerError); err != nil || !ok || calls != 1 {
		return fail("generated bulk uncertain outcome retried or became success")
	}
	return nil
}

func bulkTicketWireClient(status int, body string, calls *int) (*hs.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPost || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/bulk/" || string(wire) != `[{"subject":"first"},{"subject":"second"}]` {
			return nil, fail("generated bulk request wire")
		}
		media := "application/json"
		if status == 500 {
			media = "text/plain; charset=utf-8"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: transport}))
}
