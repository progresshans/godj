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

func checkGeneratedSummaryWire(ctx context.Context) error {
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"min_open":9223372036854775807,"total_groups":3,"results":[{"priority":null,"priority_label":"Not set","total":3,"open":2,"expected_cost_total":"10999999999999.89","effort_average":1.5,"elapsed_total":"-999999999 00:00:00"},{"priority":-9223372036854775808,"priority_label":"legacy","total":1,"open":0,"expected_cost_total":"0","effort_average":0,"elapsed_total":"00:00:00"},{"priority":9223372036854775807,"priority_label":"legacy","total":1,"open":0,"expected_cost_total":null,"effort_average":null,"elapsed_total":null}]}`
	params := hs.HelpdeskTicketSummaryParams{P: hs.NewOptInt64(50001), MinOpen: hs.NewOptInt64(9223372036854775807)}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/summary/" || len(values) != 2 || values.Get("p") != "50001" || values.Get("min_open") != "9223372036854775807" {
				return nil, fail("summary generated exact query wire")
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
	response, err := valid.HelpdeskTicketSummary(ctx, params)
	value, ok := response.(*hs.TicketSummaryHeaders)
	if err != nil || !ok || calls != 1 || value.Response.Category.ID != 9223372036854775807 || len(value.Response.Results) != 3 || !value.Response.Results[0].Priority.Null || value.Response.Results[1].Priority.Value != -9223372036854775808 || value.Response.Results[2].Priority.Value != 9223372036854775807 {
		return fail("summary generated nullable and exact legacy int64 response")
	}
	first, zero, missing := value.Response.Results[0], value.Response.Results[1], value.Response.Results[2]
	if first.ExpectedCostTotal.Null || first.ExpectedCostTotal.Value != "10999999999999.89" || first.EffortAverage.Null || first.EffortAverage.Value != 1.5 || first.ElapsedTotal.Null || first.ElapsedTotal.Value != "-999999999 00:00:00" || zero.ExpectedCostTotal.Null || zero.ExpectedCostTotal.Value != "0" || zero.EffortAverage.Null || zero.EffortAverage.Value != 0 || zero.ElapsedTotal.Null || zero.ElapsedTotal.Value != "00:00:00" || !missing.ExpectedCostTotal.Null || !missing.EffortAverage.Null || !missing.ElapsedTotal.Null {
		return fail("summary generated exact metrics and null/zero distinction")
	}
	for index, payload := range []string{`{}`, `null`, strings.Replace(body, `"priority":null,`, "", 1), strings.Replace(body, `"results":[`, `"results":null,"extra":[`, 1), strings.Replace(body, `"total":3`, `"total":3.0`, 1), strings.Replace(body, `"total":3`, `"total":-1`, 1), strings.Replace(body, `"priority":null`, `"priority":false`, 1), strings.Replace(body, `"page_size":20`, `"page_size":21`, 1), strings.Replace(body, `"open":2`, `"open":9223372036854775808`, 1), body + `true`,
		strings.Replace(body, `"expected_cost_total":"10999999999999.89",`, "", 1),
		strings.Replace(body, `"expected_cost_total":"10999999999999.89"`, `"expected_cost_total":10999999999999.89`, 1),
		strings.Replace(body, `"expected_cost_total":"10999999999999.89"`, `"expected_cost_total":"1.00"`, 1),
		strings.Replace(body, `"effort_average":1.5`, `"effort_average":"1.5"`, 1),
		strings.Replace(body, `"effort_average":1.5`, `"effort_average":1e309`, 1),
		strings.Replace(body, `"elapsed_total":"-999999999 00:00:00"`, `"elapsed_total":"24:00:00"`, 1),
		strings.Replace(body, `,"elapsed_total":"-999999999 00:00:00"`, "", 1),
		strings.Replace(body, `"expected_cost_total":"10999999999999.89"`, `"expected_cost_total":"01"`, 1),
		strings.Replace(body, `"expected_cost_total":"10999999999999.89"`, `"expected_cost_total":"-0.0"`, 1),
	} {
		calls = 0
		malformed, err := client(200, payload)
		if err != nil {
			return err
		}
		response, err = malformed.HelpdeskTicketSummary(ctx, params)
		var decoding *ogenerrors.DecodeBodyError
		var validation *validate.Error
		// Numeric schema bounds run after successful JSON decoding. Both must
		// reject the response, while retaining their distinct error categories.
		bounds := index == 5 || index == 7 || index == 12 || index == 15 || index == 17 || index == 18
		if calls != 1 || response != nil || bounds && !errors.As(err, &validation) || !bounds && !errors.As(err, &decoding) {
			return fail("summary generated invalid response rejection category")
		}
	}
	calls = 0
	failed, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskTicketSummary(ctx, params)
	if _, ok := response.(*hs.HelpdeskTicketSummaryInternalServerError); err != nil || !ok || calls != 1 {
		return fail("summary generated storage error retried or published")
	}
	return nil
}
