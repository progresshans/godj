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

func checkGeneratedReportSaveWire(ctx context.Context) error {
	const report = `{"id":1152921504606846977,"ticket":9223372036854775807,"summary":"wire","completed":false}`
	for _, status := range []int{http.StatusOK, http.StatusCreated} {
		flag := "false"
		if status == http.StatusCreated {
			flag = "true"
		}
		calls := 0
		client, err := reportSaveWireClient(status, `{"report":`+report+`,"created":`+flag+`}`, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "wire"}, hs.HelpdeskTicketServiceReportSaveParams{ID: 1152921504606846979})
		if err != nil || calls != 1 {
			return fail("generated report save exact result wire")
		}
		var value hs.ServiceReportSaveResult
		switch typed := response.(type) {
		case *hs.HelpdeskTicketServiceReportSaveOK:
			if status != http.StatusOK {
				return fail("generated report save mixed success statuses")
			}
			value = hs.ServiceReportSaveResult(*typed)
		case *hs.HelpdeskTicketServiceReportSaveCreated:
			if status != http.StatusCreated {
				return fail("generated report save mixed success statuses")
			}
			value = hs.ServiceReportSaveResult(*typed)
		default:
			return fail("generated report save lost success type")
		}
		if value.Report.ID != 1152921504606846977 || value.Report.Ticket != 9223372036854775807 || value.Report.Summary != "wire" || value.Report.Completed || value.Created != (status == http.StatusCreated) {
			return fail("generated report save altered identity or creation flag")
		}
		for _, body := range []string{`{"report":` + report + `}`, `{"created":true}`, `{"report":null,"created":true}`, `{"report":` + report + `,"created":null}`, `{"report":` + report + `,"created":0}`, `{"report":` + report + `,"created":"false"}`, `{"report":{"id":1,"summary":"wire"},"created":true}`} {
			calls = 0
			client, err := reportSaveWireClient(status, body, &calls)
			if err != nil {
				return err
			}
			_, err = client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "wire"}, hs.HelpdeskTicketServiceReportSaveParams{ID: 1152921504606846979})
			var decode *ogenerrors.DecodeBodyError
			if calls != 1 || !errors.As(err, &decode) {
				return fail("generated report save admitted missing or malformed required result")
			}
		}
	}
	// This synthetic failure checks only the generated transport's single send.
	// Real audit/rollback/unknown outcomes are owned by both Helpdesk DB tests.
	calls := 0
	client, err := reportSaveWireClient(http.StatusInternalServerError, "Internal Server Error\n", &calls)
	if err != nil {
		return err
	}
	response, err := client.HelpdeskTicketServiceReportSave(ctx, &hs.ServiceReportSave{Summary: "wire"}, hs.HelpdeskTicketServiceReportSaveParams{ID: 1152921504606846979})
	if _, ok := response.(*hs.HelpdeskTicketServiceReportSaveInternalServerError); err != nil || !ok || calls != 1 {
		return fail("generated report save server error retried or became success")
	}
	return nil
}

func reportSaveWireClient(status int, body string, calls *int) (*hs.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPut || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1152921504606846979/service-report/" || string(wire) != `{"summary":"wire"}` {
			return nil, fail("generated report save request changed")
		}
		media := "application/json"
		if status == http.StatusInternalServerError {
			media = "text/plain; charset=utf-8"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: transport}))
}
