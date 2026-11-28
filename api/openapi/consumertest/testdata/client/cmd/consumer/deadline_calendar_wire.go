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
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"as_of":"2024-02-29T00:00:00.000001Z","period":"month","time_zone":"Asia/Seoul","from_date":"0001-01-01","until_date":"9999-12-31","total_groups":9223372036854775807,"results":[{"period_start":"0001-01-01","total":9223372036854775807,"open":9007199254740993,"overdue":1,"first_due_at":"0001-01-01T00:00:00.000000Z","last_due_at":"0001-01-31T23:59:59.999999Z"},{"period_start":"9999-12-01","total":1,"open":1,"overdue":0,"first_due_at":"9999-12-31T00:00:00.000001Z","last_due_at":"9999-12-31T23:59:59.999999Z"},{"period_start":null,"total":1,"open":0,"overdue":0,"first_due_at":null,"last_due_at":null}]}`
	params := hs.HelpdeskDeadlineCalendarParams{TimeZone: hs.NewOptHelpdeskDeadlineCalendarTimeZone("Asia/Seoul"), P: hs.NewOptInt64(50001), Period: hs.NewOptHelpdeskDeadlineCalendarPeriod(hs.HelpdeskDeadlineCalendarPeriodMonth), AsOf: time.Date(2024, 2, 29, 9, 0, 0, 1999, time.FixedZone("caller", 9*3600)), FromDate: hs.NewOptDate(time.Date(1, 1, 1, 23, 59, 59, 0, time.FixedZone("calendar", 14*3600))), UntilDate: hs.NewOptDate(time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC))}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/deadline-calendar/" || len(values) != 6 || values.Get("time_zone") != "Asia/Seoul" || values.Get("period") != "month" || values.Get("p") != "50001" || values.Get("as_of") != "2024-02-29T09:00:00.000001999+09:00" || values.Get("from_date") != "0001-01-01" || values.Get("until_date") != "9999-12-31" || !strings.Contains(request.URL.RawQuery, "%2B09") {
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
	if err != nil || !ok || calls != 1 || page.Response.Category.ID != 9223372036854775807 || page.Response.Page != 50001 || page.Response.PageSize != 20 || page.Response.Period != hs.DeadlineCalendarPeriodMonth || page.Response.TimeZone != "Asia/Seoul" || page.Response.TotalGroups != 9223372036854775807 || !page.Response.AsOf.Equal(params.AsOf.UTC().Truncate(time.Microsecond)) || page.Response.FromDate.Null || !page.Response.FromDate.Value.IsZero() || page.Response.UntilDate.Null || page.Response.UntilDate.Value.Format("2006-01-02") != "9999-12-31" || len(page.Response.Results) != 3 {
		return fail("calendar wire retained exact integer/date/instant envelope")
	}
	first, last, missing := page.Response.Results[0], page.Response.Results[1], page.Response.Results[2]
	if first.PeriodStart.Null || !first.PeriodStart.Value.IsZero() || first.Total != 9223372036854775807 || first.Open != 9007199254740993 || first.FirstDueAt.Null || !first.FirstDueAt.Value.IsZero() || first.LastDueAt.Null || !first.LastDueAt.Value.Equal(time.Date(1, 1, 31, 23, 59, 59, 999999000, time.UTC)) || last.PeriodStart.Null || last.PeriodStart.Value.Format("2006-01-02") != "9999-12-01" || last.FirstDueAt.Null || !last.FirstDueAt.Value.Equal(time.Date(9999, 12, 31, 0, 0, 0, 1000, time.UTC)) || last.LastDueAt.Null || !last.LastDueAt.Value.Equal(time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)) || !missing.PeriodStart.Null || !missing.FirstDueAt.Null || !missing.LastDueAt.Null {
		return fail("calendar wire kept full Date/DateTime domains and required NULLs")
	}
	// The schema's DateTime extension documents the server's UTC microsecond
	// contract. The external decoder preserves RFC3339Nano values; the actual
	// HTTP consumer above independently verifies server canonicalization.
	calls = 0
	precision, err := client(200, strings.Replace(body, `"first_due_at":"0001-01-01T00:00:00.000000Z"`, `"first_due_at":"0001-01-01T09:00:00.000001999+09:00"`, 1))
	if err != nil {
		return err
	}
	response, err = precision.HelpdeskDeadlineCalendar(ctx, params)
	preserved, ok := response.(*hs.DeadlineCalendarHeaders)
	if err != nil || !ok || calls != 1 || preserved.Response.Results[0].FirstDueAt.Value.Nanosecond() != 1999 {
		return fail("calendar wire decoder lost declared RFC3339Nano precision")
	}

	type invalidResponse struct {
		payload string
		bounds  bool
	}
	invalid := []invalidResponse{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{`"category":{"id":9223372036854775807,"name":"wire"},`, `"page":50001,`, `"page_size":20,`, `"as_of":"2024-02-29T00:00:00.000001Z",`, `"period":"month",`, `"time_zone":"Asia/Seoul",`, `"from_date":"0001-01-01",`, `"until_date":"9999-12-31",`, `"total_groups":9223372036854775807,`, `"period_start":"0001-01-01",`, `"total":9223372036854775807,`, `"open":9007199254740993,`, `"overdue":1,`, `"first_due_at":"0001-01-01T00:00:00.000000Z",`, `,"last_due_at":"0001-01-31T23:59:59.999999Z"`, `"period_start":null,`, `"first_due_at":null,`, `,"last_due_at":null`} {
		invalid = append(invalid, invalidResponse{strings.Replace(body, field, "", 1), false})
	}
	for _, change := range []struct {
		before, after string
		bounds        bool
	}{
		{`"results":[`, `"results":null,"extra":[`, false}, {`"open":9007199254740993`, `"open":9223372036854775808`, false},
		{`"page":50001`, `"page":50002`, true}, {`"page_size":20`, `"page_size":21`, true}, {`"total_groups":9223372036854775807`, `"total_groups":-1`, true}, {`"total":9223372036854775807`, `"total":0`, true}, {`"overdue":1`, `"overdue":-1`, true},
		{`"as_of":"2024-02-29T00:00:00.000001Z"`, `"as_of":null`, false},
		{`"period_start":"0001-01-01"`, `"period_start":"2023-02-29"`, false}, {`"period_start":"0001-01-01"`, `"period_start":"2024-02-29T00:00:00Z"`, false}, {`"period_start":"0001-01-01"`, `"period_start":1`, false},
		{`"time_zone":"Asia/Seoul"`, `"time_zone":null`, false}, {`"time_zone":"Asia/Seoul"`, `"time_zone":1`, false},
		{`"time_zone":"Asia/Seoul"`, `"time_zone":""`, true}, {`"time_zone":"Asia/Seoul"`, `"time_zone":"Local"`, true},
		{`"time_zone":"Asia/Seoul"`, `"time_zone":"Asia/seoul"`, true}, {`"time_zone":"Asia/Seoul"`, `"time_zone":"../UTC"`, true},
		{`"period":"month"`, `"period":null`, false}, {`"period":"month"`, `"period":1`, false},
		{`"period":"month"`, `"period":"hour"`, true}, {`"period":"month"`, `"period":"Month"`, true}, {`"period":"month"`, `"period":""`, true},
		{`"first_due_at":"0001-01-01T00:00:00.000000Z"`, `"first_due_at":0`, false},
		{`"first_due_at":"0001-01-01T00:00:00.000000Z"`, `"first_due_at":"2024-02-29T24:00:00Z"`, false},
		{`"first_due_at":"0001-01-01T00:00:00.000000Z"`, `"first_due_at":"2024-02-29T00:00:00"`, false},
		{`"last_due_at":"0001-01-31T23:59:59.999999Z"`, `"last_due_at":"2023-02-29T23:59:59.999999Z"`, false},
		{`"last_due_at":"0001-01-31T23:59:59.999999Z"`, `"last_due_at":"23:59:59.999999"`, false},
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
	if value, ok := response.(*hs.HelpdeskDeadlineCalendarForbidden); err != nil || !ok || value.Response.Code != "permission_denied" || calls != 1 {
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
