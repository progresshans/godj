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

func checkGeneratedDeadlineCalendarWire(ctx context.Context) error {
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"as_of":"2024-02-29T00:00:00.000001Z","from_date":"0001-01-01","until_date":"9999-12-31","total_groups":9223372036854775807,"results":[{"date":"0001-01-01","total":9223372036854775807,"open":9007199254740993,"overdue":1,"first_due_time":"00:00:00","last_due_time":"23:59:59.999999"},{"date":"9999-12-31","total":1,"open":1,"overdue":0,"first_due_time":"00:00:00.000001","last_due_time":"00:00:00.000001"},{"date":null,"total":1,"open":0,"overdue":0,"first_due_time":null,"last_due_time":null}]}`
	params := hs.HelpdeskDeadlineCalendarParams{P: hs.NewOptInt64(50001), AsOf: time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600)), FromDate: hs.NewOptDate(time.Date(1, 1, 1, 23, 59, 59, 0, time.FixedZone("calendar", 14*3600))), UntilDate: hs.NewOptDate(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/deadline-calendar/" || len(values) != 4 || values.Get("p") != "50001" || values.Get("as_of") != "2024-02-29T09:00:00.000001999+09:00" || values.Get("from_date") != "0001-01-01" || values.Get("until_date") != "9999-12-31" || !strings.Contains(request.URL.RawQuery, "%2B09") {
				return nil, fail("calendar query separated dates from offset-bearing instant")
			}
			for _, values := range values {
				if len(values) != 1 {
					return nil, fail("calendar generated duplicate parameter")
				}
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
	response, err := valid.HelpdeskDeadlineCalendar(ctx, params)
	page, ok := response.(*hs.DeadlineCalendarHeaders)
	if err != nil || !ok || calls != 1 || page.Response.Category.ID != 9223372036854775807 || page.Response.Page != 50001 || page.Response.PageSize != 20 || page.Response.TotalGroups != 9223372036854775807 || !page.Response.AsOf.Equal(params.AsOf.UTC().Truncate(time.Microsecond)) || page.Response.FromDate.Null || !page.Response.FromDate.Value.IsZero() || page.Response.UntilDate.Null || page.Response.UntilDate.Value.Format("2006-01-02") != "9999-12-31" || len(page.Response.Results) != 3 {
		return fail("calendar wire retained exact integer/date/instant envelope")
	}
	first, last, missing := page.Response.Results[0], page.Response.Results[1], page.Response.Results[2]
	if first.Date.Null || !first.Date.Value.IsZero() || first.Total != 9223372036854775807 || first.Open != 9007199254740993 || first.FirstDueTime.Null || first.FirstDueTime.Value != "00:00:00" || first.LastDueTime.Null || first.LastDueTime.Value != "23:59:59.999999" || last.Date.Null || last.Date.Value.Format("2006-01-02") != "9999-12-31" || last.FirstDueTime.Value != "00:00:00.000001" || !missing.Date.Null || !missing.FirstDueTime.Null || !missing.LastDueTime.Null {
		return fail("calendar wire kept full calendar/clock ranges and required NULLs")
	}
	type invalidResponse struct {
		payload string
		bounds  bool
	}
	invalid := []invalidResponse{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{`"category":{"id":9223372036854775807,"name":"wire"},`, `"page":50001,`, `"page_size":20,`, `"as_of":"2024-02-29T00:00:00.000001Z",`, `"from_date":"0001-01-01",`, `"until_date":"9999-12-31",`, `"total_groups":9223372036854775807,`, `"date":"0001-01-01",`, `"total":9223372036854775807,`, `"open":9007199254740993,`, `"overdue":1,`, `"first_due_time":"00:00:00",`, `,"last_due_time":"23:59:59.999999"`, `"date":null,`, `"first_due_time":null,`, `,"last_due_time":null`} {
		invalid = append(invalid, invalidResponse{strings.Replace(body, field, "", 1), false})
	}
	for _, change := range []struct {
		before, after string
		bounds        bool
	}{
		{`"results":[`, `"results":null,"extra":[`, false}, {`"open":9007199254740993`, `"open":9223372036854775808`, false},
		{`"page":50001`, `"page":50002`, true}, {`"page_size":20`, `"page_size":21`, true}, {`"total_groups":9223372036854775807`, `"total_groups":-1`, true}, {`"total":9223372036854775807`, `"total":0`, true}, {`"overdue":1`, `"overdue":-1`, true},
		{`"as_of":"2024-02-29T00:00:00.000001Z"`, `"as_of":null`, false},
		{`"date":"0001-01-01"`, `"date":"2023-02-29"`, false}, {`"date":"0001-01-01"`, `"date":"2024-02-29T00:00:00Z"`, false}, {`"date":"0001-01-01"`, `"date":1`, false},
		{`"first_due_time":"00:00:00"`, `"first_due_time":0`, false}, {`"first_due_time":"00:00:00"`, `"first_due_time":"24:00:00"`, true}, {`"first_due_time":"00:00:00"`, `"first_due_time":"00:00:00+09:00"`, true}, {`"last_due_time":"23:59:59.999999"`, `"last_due_time":"23:59:59.1"`, true}, {`"last_due_time":"23:59:59.999999"`, `"last_due_time":"23:59:59.9999999"`, true},
	} {
		invalid = append(invalid, invalidResponse{strings.Replace(body, change.before, change.after, 1), change.bounds})
	}
	for _, test := range invalid {
		calls = 0
		bad, err := client(200, test.payload)
		if err != nil {
			return err
		}
		response, err := bad.HelpdeskDeadlineCalendar(ctx, params)
		var decode *ogenerrors.DecodeBodyError
		var validation *validate.Error
		if calls != 1 || response != nil || test.bounds && !errors.As(err, &validation) || !test.bounds && !errors.As(err, &decode) {
			return fail("calendar malformed response category and no prefix")
		}
	}
	calls = 0
	forbidden, err := client(403, `{"code":"permission_denied","errors":[]}`)
	if err != nil {
		return err
	}
	response, err = forbidden.HelpdeskDeadlineCalendar(ctx, params)
	if value, ok := response.(*hs.GoDjAPIError); err != nil || !ok || value.Code != "permission_denied" || calls != 1 {
		return fail("calendar generated permission response")
	}
	calls = 0
	failed, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskDeadlineCalendar(ctx, params)
	value, ok := response.(*hs.HelpdeskDeadlineCalendarInternalServerError)
	if err != nil || !ok || calls != 1 {
		return fail("calendar storage failure retried or published")
	}
	payload, err := io.ReadAll(value)
	if err != nil || string(payload) != "Internal Server Error\n" {
		return fail("calendar generated storage error body")
	}
	return nil
}
