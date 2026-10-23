package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/ogen-go/ogen/validate"
)

func checkGeneratedReportStatusWire(ctx context.Context) error {
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"comparison":"different","total_reports":9223372036854775807,"results":[{"id":9223372036854775807,"ticket":9223372036854775807,"summary":"wire","completed":true,"ticket_closed":false,"matched":false}]}`
	params := hs.HelpdeskServiceReportStatusParams{P: hs.NewOptInt64(50001), Comparison: hs.NewOptHelpdeskServiceReportStatusComparison(hs.HelpdeskServiceReportStatusComparisonDifferent)}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/service-reports/status/" || len(values) != 2 || values.Get("p") != "50001" || values.Get("comparison") != "different" {
				return nil, fail("report status generated exact query wire")
			}
			media := "application/json"
			if status == 500 {
				media = "text/plain; charset=utf-8"
			}
			return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload)), Request: request}, nil
		})}))
	}
	valid, err := client(200, body)
	if err != nil {
		return err
	}
	response, err := valid.HelpdeskServiceReportStatus(ctx, params)
	value, ok := response.(*hs.ServiceReportStatusHeaders)
	if err != nil || !ok || calls != 1 || value.Response.Category.ID != 9223372036854775807 || value.Response.TotalReports != 9223372036854775807 || value.Response.Page != 50001 || value.Response.PageSize != 20 || value.Response.Comparison != "different" || len(value.Response.Results) != 1 {
		return fail("report status generated required envelope and exact int64")
	}
	row := value.Response.Results[0]
	if row.ID != 9223372036854775807 || row.Ticket != 9223372036854775807 || row.Summary != "wire" || !row.Completed || row.TicketClosed || row.Matched {
		return fail("report status generated required boolean values")
	}
	invalid := []struct {
		payload string
		bounds  bool
	}{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{`"category":{"id":9223372036854775807,"name":"wire"},`, `"page":50001,`, `"page_size":20,`, `"comparison":"different",`, `"total_reports":9223372036854775807,`, `"id":9223372036854775807,`, `"ticket":9223372036854775807,`, `"summary":"wire",`, `"completed":true,`, `"ticket_closed":false,`, `,"matched":false`} {
		invalid = append(invalid, struct {
			payload string
			bounds  bool
		}{strings.Replace(body, field, "", 1), false})
	}
	for _, change := range []struct {
		before, after string
		bounds        bool
	}{
		{`"results":[{"id":9223372036854775807,`, `"results":[{`, false},
		{`"results":[`, `"results":null,"extra":[`, false},
		{`"completed":true`, `"completed":null`, false},
		{`"ticket_closed":false`, `"ticket_closed":"false"`, false},
		{`"matched":false`, `"matched":0`, false},
		{`"page":50001`, `"page":null`, false},
		{`"comparison":"different"`, `"comparison":null`, false},
		{`"total_reports":9223372036854775807`, `"total_reports":9223372036854775808`, false},
		{`"ticket":9223372036854775807`, `"ticket":1.0`, false},
		{`"page_size":20`, `"page_size":21`, true},
		{`"page":50001`, `"page":50002`, true},
		{`"ticket":9223372036854775807`, `"ticket":0`, true},
		{`"total_reports":9223372036854775807`, `"total_reports":-1`, true},
	} {
		invalid = append(invalid, struct {
			payload string
			bounds  bool
		}{strings.Replace(body, change.before, change.after, 1), change.bounds})
	}
	for _, test := range invalid {
		calls = 0
		malformed, err := client(200, test.payload)
		if err != nil {
			return err
		}
		response, err := malformed.HelpdeskServiceReportStatus(ctx, params)
		var decoding *ogenerrors.DecodeBodyError
		var validation *validate.Error
		if calls != 1 || response != nil || test.bounds && !errors.As(err, &validation) || !test.bounds && !errors.As(err, &decoding) {
			return fail("report status generated invalid response rejection category")
		}
	}
	calls = 0
	forbidden, err := client(403, `{"code":"permission_denied","errors":[]}`)
	if err != nil {
		return err
	}
	response, err = forbidden.HelpdeskServiceReportStatus(ctx, params)
	if value, ok := response.(*hs.GoDjAPIError); err != nil || !ok || value.Code != "permission_denied" || calls != 1 {
		return fail("report status generated permission response")
	}
	calls = 0
	failed, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskServiceReportStatus(ctx, params)
	if _, ok := response.(*hs.HelpdeskServiceReportStatusInternalServerError); err != nil || !ok || calls != 1 {
		return fail("report status generated storage error retried or published")
	}
	return nil
}
