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

func checkGeneratedDeadlinePreviewWire(ctx context.Context) error {
	// Synthetic responses exercise the wire domain independently of the real
	// server's arithmetic, which checkHelpdeskDeadlinePreview checks separately.
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"as_of":"2024-02-29T00:00:00.000001Z","shift":"00:00:00.000001","min_remaining":"-999999999 00:00:00","total_tickets":9223372036854775807,"results":[{"id":9223372036854775807,"subject":"range","due_at":"0001-01-01T00:00:00.000000Z","adjusted_due_at":"9999-12-31T23:59:59.999999Z","remaining":"999999999 23:59:59.999999"},{"id":9007199254740993,"subject":"zero","due_at":"2024-02-29T00:00:00.000001Z","adjusted_due_at":"2024-02-29T00:00:00.000001Z","remaining":"00:00:00"},{"id":1,"subject":"missing","due_at":null,"adjusted_due_at":null,"remaining":null}]}`
	params := hs.HelpdeskDeadlinePreviewParams{
		P: hs.NewOptInt64(50001), AsOf: time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600)),
		Shift: hs.NewOptString("00:00:00.000001"), MinRemaining: hs.NewOptString("-999999999 00:00:00"),
	}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/deadline-preview/" || len(values) != 4 || values.Get("p") != "50001" || values.Get("as_of") != "2024-02-29T09:00:00.000001999+09:00" || values.Get("shift") != "00:00:00.000001" || values.Get("min_remaining") != "-999999999 00:00:00" || !strings.Contains(request.URL.RawQuery, "%2B09") {
				return nil, fail("deadline preview required offset, submicrosecond and full duration query wire")
			}
			for _, values := range values {
				if len(values) != 1 {
					return nil, fail("deadline preview duplicate generated query parameter")
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
	response, err := valid.HelpdeskDeadlinePreview(ctx, params)
	page, ok := response.(*hs.DeadlinePreviewHeaders)
	if err != nil || !ok || calls != 1 || page.Response.Category.ID != 9223372036854775807 || page.Response.Page != 50001 || page.Response.PageSize != 20 || !page.Response.AsOf.Equal(params.AsOf.UTC().Truncate(time.Microsecond)) || page.Response.Shift != "00:00:00.000001" || page.Response.MinRemaining.Null || page.Response.MinRemaining.Value != "-999999999 00:00:00" || page.Response.TotalTickets != 9223372036854775807 || len(page.Response.Results) != 3 {
		return fail("deadline preview generated exact temporal envelope")
	}
	first, zero, missing := page.Response.Results[0], page.Response.Results[1], page.Response.Results[2]
	maximum := time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	if first.ID != 9223372036854775807 || first.DueAt.Null || !first.DueAt.Value.IsZero() || first.AdjustedDueAt.Null || !first.AdjustedDueAt.Value.Equal(maximum) || first.Remaining.Null || first.Remaining.Value != "999999999 23:59:59.999999" || zero.ID != 9007199254740993 || zero.Remaining.Null || zero.Remaining.Value != "00:00:00" || zero.DueAt.Null || zero.AdjustedDueAt.Null || !missing.DueAt.Null || !missing.AdjustedDueAt.Null || !missing.Remaining.Null {
		return fail("deadline preview full DateTime and Duration domains, int64 and required NULL wire")
	}
	type invalidResponse struct {
		payload string
		bounds  bool
	}
	invalid := []invalidResponse{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{
		`"category":{"id":9223372036854775807,"name":"wire"},`, `"page":50001,`, `"page_size":20,`,
		`"as_of":"2024-02-29T00:00:00.000001Z",`, `"shift":"00:00:00.000001",`, `"min_remaining":"-999999999 00:00:00",`,
		`"total_tickets":9223372036854775807,`, `"id":9223372036854775807,`, `"subject":"range",`,
		`"due_at":"0001-01-01T00:00:00.000000Z",`, `"adjusted_due_at":"9999-12-31T23:59:59.999999Z",`, `,"remaining":"999999999 23:59:59.999999"`,
		`"due_at":null,`, `"adjusted_due_at":null,`, `,"remaining":null`,
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
		{`"as_of":"2024-02-29T00:00:00.000001Z"`, `"as_of":null`, false},
		{`"as_of":"2024-02-29T00:00:00.000001Z"`, `"as_of":"2023-02-29T00:00:00Z"`, false},
		{`"due_at":"0001-01-01T00:00:00.000000Z"`, `"due_at":1`, false},
		{`"due_at":"0001-01-01T00:00:00.000000Z"`, `"due_at":"2024-01-01T00:00:00"`, false},
		{`"adjusted_due_at":"9999-12-31T23:59:59.999999Z"`, `"adjusted_due_at":"2024-13-01T00:00:00Z"`, false},
		{`"shift":"00:00:00.000001"`, `"shift":null`, false},
		{`"remaining":"999999999 23:59:59.999999"`, `"remaining":86400000000`, false},
		{`"remaining":"999999999 23:59:59.999999"`, `"remaining":"1000000000 00:00:00"`, true},
		{`"remaining":"999999999 23:59:59.999999"`, `"remaining":"0 00:00:00"`, true},
		{`"remaining":"999999999 23:59:59.999999"`, `"remaining":"00:00:00.1"`, true},
		{`"remaining":"999999999 23:59:59.999999"`, `"remaining":"24:00:00"`, true},
	} {
		invalid = append(invalid, invalidResponse{strings.Replace(body, change.before, change.after, 1), change.bounds})
	}
	for _, test := range invalid {
		calls = 0
		malformed, err := client(http.StatusOK, test.payload)
		if err != nil {
			return err
		}
		response, err := malformed.HelpdeskDeadlinePreview(ctx, params)
		var decoding *ogenerrors.DecodeBodyError
		var validation *validate.Error
		if calls != 1 || response != nil || test.bounds && !errors.As(err, &validation) || !test.bounds && !errors.As(err, &decoding) {
			return fail("deadline preview malformed response rejection category")
		}
	}
	calls = 0
	forbidden, err := client(http.StatusForbidden, `{"code":"permission_denied","errors":[]}`)
	if err != nil {
		return err
	}
	response, err = forbidden.HelpdeskDeadlinePreview(ctx, params)
	if value, ok := response.(*hs.HelpdeskDeadlinePreviewForbidden); err != nil || !ok || value.Response.Code != "permission_denied" || calls != 1 {
		return fail("deadline preview generated permission response")
	}
	calls = 0
	failed, err := client(http.StatusInternalServerError, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskDeadlinePreview(ctx, params)
	value, ok := response.(*hs.HelpdeskDeadlinePreviewInternalServerError)
	if err != nil || !ok || calls != 1 {
		return fail("deadline preview storage failure retried or published")
	}
	payload, err := io.ReadAll(value)
	if err != nil || string(payload) != "Internal Server Error\n" {
		return fail("deadline preview storage error response body")
	}
	return nil
}
