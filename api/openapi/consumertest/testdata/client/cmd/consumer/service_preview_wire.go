package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"
)

func checkGeneratedServicePreviewWire(ctx context.Context) error {
	// Synthetic wire values cover the complete schema independently of the
	// real HTTP calculation and storage preservation in service_preview.go.
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"shift":"00:00:00.000001","min_start":"2024-02-29T00:00:00.000001Z","total_tickets":9223372036854775807,"results":[{"id":9223372036854775807,"subject":"range","service_on":"0001-01-01","service_at":"23:59:59.999999","elapsed":"999999999 23:59:59.999999","due_at":"9999-12-31T23:59:59.999999Z","service_start":"0001-01-01T23:59:59.999999Z","adjusted_start":"0001-01-02T00:00:00.000000Z","finish_at":"9999-12-31T23:59:59.999999Z","deadline_slack":"-999999999 00:00:00"},{"id":9007199254740993,"subject":"zero","service_on":"2024-02-29","service_at":"00:00:00","elapsed":"00:00:00","due_at":null,"service_start":"2024-02-29T00:00:00.000000Z","adjusted_start":"2024-02-29T00:00:00.000001Z","finish_at":"2024-02-29T00:00:00.000001Z","deadline_slack":"00:00:00"},{"id":1,"subject":"missing","service_on":null,"service_at":null,"elapsed":null,"due_at":null,"service_start":null,"adjusted_start":null,"finish_at":null,"deadline_slack":null}]}`
	params := hs.HelpdeskServicePreviewParams{P: hs.NewOptInt64(50001), Shift: hs.NewOptString("00:00:00.000001"), MinStart: hs.NewOptDateTime(time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600)))}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/service-preview/" || len(values) != 3 || values.Get("p") != "50001" || values.Get("shift") != "00:00:00.000001" || values.Get("min_start") != "2024-02-29T09:00:00.000001999+09:00" || !strings.Contains(request.URL.RawQuery, "%2B09") {
				return nil, fail("service preview generated offset, submicrosecond and duration query wire")
			}
			for _, values := range values {
				if len(values) != 1 {
					return nil, fail("service preview duplicate generated query parameter")
				}
			}
			media := "application/json"
			if status == http.StatusInternalServerError {
				media = "text/plain; charset=utf-8"
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload)), Request: request}, nil
		})}))
	}
	valid, err := client(http.StatusOK, body)
	if err != nil {
		return err
	}
	response, err := valid.HelpdeskServicePreview(ctx, params)
	page, ok := response.(*hs.ServicePreviewHeaders)
	if err != nil || !ok || calls != 1 || page.Response.Category.ID != 9223372036854775807 || page.Response.Page != 50001 || page.Response.PageSize != 20 || page.Response.Shift != "00:00:00.000001" || !calendarInstantMatches(page.Response.MinStart, hs.NewNilDateTime(params.MinStart.Value.UTC().Truncate(time.Microsecond))) || page.Response.TotalTickets != 9223372036854775807 || len(page.Response.Results) != 3 {
		return fail("service preview generated exact temporal envelope")
	}
	first, zero, missing := page.Response.Results[0], page.Response.Results[1], page.Response.Results[2]
	maximum := hs.NewNilDateTime(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC))
	if first.ID != 9223372036854775807 || first.ServiceOn.Null || !first.ServiceOn.Value.IsZero() || first.ServiceAt != hs.NewNilString("23:59:59.999999") || first.Elapsed != hs.NewNilString("999999999 23:59:59.999999") || !calendarInstantMatches(first.DueAt, maximum) || !calendarInstantMatches(first.ServiceStart, hs.NewNilDateTime(time.Date(1, 1, 1, 23, 59, 59, 999999000, time.UTC))) || !calendarInstantMatches(first.AdjustedStart, hs.NewNilDateTime(time.Date(1, 1, 2, 0, 0, 0, 0, time.UTC))) || !calendarInstantMatches(first.FinishAt, maximum) || first.DeadlineSlack != hs.NewNilString("-999999999 00:00:00") {
		return fail("service preview full scalar domains and int64 wire")
	}
	if zero.ID != 9007199254740993 || zero.ServiceOn.Null || zero.ServiceOn.Value.Format("2006-01-02") != "2024-02-29" || zero.ServiceAt != hs.NewNilString("00:00:00") || zero.Elapsed != hs.NewNilString("00:00:00") || !zero.DueAt.Null || zero.ServiceStart.Null || zero.AdjustedStart.Null || zero.FinishAt.Null || zero.DeadlineSlack != hs.NewNilString("00:00:00") || !missing.ServiceOn.Null || !missing.ServiceAt.Null || !missing.Elapsed.Null || !missing.DueAt.Null || !missing.ServiceStart.Null || !missing.AdjustedStart.Null || !missing.FinishAt.Null || !missing.DeadlineSlack.Null {
		return fail("service preview required NULL differs from zero and midnight")
	}
	type invalidResponse struct {
		payload string
		bounds  bool
	}
	invalid := []invalidResponse{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{
		`"category":{"id":9223372036854775807,"name":"wire"},`, `"page":50001,`, `"page_size":20,`, `"shift":"00:00:00.000001",`, `"min_start":"2024-02-29T00:00:00.000001Z",`, `"total_tickets":9223372036854775807,`,
		`"id":9223372036854775807,`, `"subject":"range",`, `"service_on":"0001-01-01",`, `"service_at":"23:59:59.999999",`, `"elapsed":"999999999 23:59:59.999999",`, `"due_at":"9999-12-31T23:59:59.999999Z",`, `"service_start":"0001-01-01T23:59:59.999999Z",`, `"adjusted_start":"0001-01-02T00:00:00.000000Z",`, `"finish_at":"9999-12-31T23:59:59.999999Z",`, `,"deadline_slack":"-999999999 00:00:00"`,
		`"service_on":null,`, `"service_at":null,`, `"elapsed":null,`, `"due_at":null,`, `"service_start":null,`, `"adjusted_start":null,`, `"finish_at":null,`, `,"deadline_slack":null`,
	} {
		invalid = append(invalid, invalidResponse{strings.Replace(body, field, "", 1), false})
	}
	for _, change := range []struct {
		before, after string
		bounds        bool
	}{
		{`"results":[`, `"results":null,"extra":[`, false},
		{`"id":9007199254740993`, `"id":9223372036854775808`, false},
		{`"page_size":20`, `"page_size":21`, true}, {`"page":50001`, `"page":50002`, true},
		{`"total_tickets":9223372036854775807`, `"total_tickets":-1`, true},
		{`"shift":"00:00:00.000001"`, `"shift":null`, false},
		{`"min_start":"2024-02-29T00:00:00.000001Z"`, `"min_start":"2023-02-29T00:00:00Z"`, false},
		{`"service_on":"0001-01-01"`, `"service_on":"1900-02-29"`, false},
		{`"service_on":"0001-01-01"`, `"service_on":1`, false},
		{`"service_at":"23:59:59.999999"`, `"service_at":"24:00:00"`, true},
		{`"service_at":"23:59:59.999999"`, `"service_at":"12:00:00Z"`, true},
		{`"elapsed":"999999999 23:59:59.999999"`, `"elapsed":"1000000000 00:00:00"`, true},
		{`"elapsed":"999999999 23:59:59.999999"`, `"elapsed":86400000000`, false},
		{`"deadline_slack":"-999999999 00:00:00"`, `"deadline_slack":"0 00:00:00"`, true},
		{`"deadline_slack":"-999999999 00:00:00"`, `"deadline_slack":"00:00:00.1"`, true},
		{`"due_at":"9999-12-31T23:59:59.999999Z"`, `"due_at":false`, false},
		{`"service_start":"0001-01-01T23:59:59.999999Z"`, `"service_start":"2024-01-01T00:00:00"`, false},
		{`"adjusted_start":"0001-01-02T00:00:00.000000Z"`, `"adjusted_start":"2024-13-01T00:00:00Z"`, false},
		{`"finish_at":"9999-12-31T23:59:59.999999Z"`, `"finish_at":42`, false},
	} {
		invalid = append(invalid, invalidResponse{strings.Replace(body, change.before, change.after, 1), change.bounds})
	}
	for _, test := range invalid {
		calls = 0
		malformed, err := client(http.StatusOK, test.payload)
		if err != nil {
			return err
		}
		response, err := malformed.HelpdeskServicePreview(ctx, params)
		var decoding *ogenerrors.DecodeBodyError
		var validation *validate.Error
		if calls != 1 || response != nil || test.bounds && !errors.As(err, &validation) || !test.bounds && !errors.As(err, &decoding) {
			return fail("service preview malformed response rejection category")
		}
	}
	calls = 0
	forbidden, err := client(http.StatusForbidden, `{"code":"permission_denied","errors":[]}`)
	if err != nil {
		return err
	}
	response, err = forbidden.HelpdeskServicePreview(ctx, params)
	if value, ok := response.(*hs.HelpdeskServicePreviewForbidden); err != nil || !ok || value.Response.Code != "permission_denied" || calls != 1 {
		return fail("service preview generated permission response")
	}
	calls = 0
	failed, err := client(http.StatusInternalServerError, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskServicePreview(ctx, params)
	value, ok := response.(*hs.HelpdeskServicePreviewInternalServerError)
	if err != nil || !ok || calls != 1 {
		return fail("service preview storage failure retried or published")
	}
	payload, err := io.ReadAll(value)
	if err != nil || string(payload) != "Internal Server Error\n" {
		return fail("service preview storage error response body")
	}
	return nil
}
