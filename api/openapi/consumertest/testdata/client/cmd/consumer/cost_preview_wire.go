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

func checkGeneratedCostPreviewWire(ctx context.Context) error {
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"factor":"1.2345","delta":"999999999999.99","min_cost":"-0.000001","total_tickets":9223372036854775807,"results":[{"id":9223372036854775807,"subject":"exact","expected_cost":"999999999999.99","adjusted_cost":"12345678901234.123456"},{"id":9007199254740993,"subject":"zero","expected_cost":"0","adjusted_cost":"0"},{"id":1,"subject":"missing","expected_cost":null,"adjusted_cost":null}]}`
	params := hs.HelpdeskCostPreviewParams{P: hs.NewOptInt64(50001), Factor: hs.NewOptString("1.2345"), Delta: hs.NewOptString("999999999999.99"), MinCost: hs.NewOptString("-0.000001")}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/cost-preview/" || len(values) != 4 || values.Get("p") != "50001" || values.Get("factor") != "1.2345" || values.Get("delta") != "999999999999.99" || values.Get("min_cost") != "-0.000001" {
				return nil, fail("cost preview exact decimal query wire")
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
	response, err := valid.HelpdeskCostPreview(ctx, params)
	page, ok := response.(*hs.CostPreviewHeaders)
	if err != nil || !ok || calls != 1 || page.Response.Category.ID != 9223372036854775807 || page.Response.Page != 50001 || page.Response.PageSize != 20 || page.Response.Factor != "1.2345" || page.Response.Delta != "999999999999.99" || page.Response.MinCost.Null || page.Response.MinCost.Value != "-0.000001" || page.Response.TotalTickets != 9223372036854775807 || len(page.Response.Results) != 3 {
		return fail("cost preview exact generated envelope")
	}
	first, zero, missing := page.Response.Results[0], page.Response.Results[1], page.Response.Results[2]
	if first.ID != 9223372036854775807 || first.ExpectedCost.Null || first.ExpectedCost.Value != "999999999999.99" || first.AdjustedCost.Null || first.AdjustedCost.Value != "12345678901234.123456" || zero.ID != 9007199254740993 || zero.AdjustedCost.Null || zero.AdjustedCost.Value != "0" || !missing.ExpectedCost.Null || !missing.AdjustedCost.Null {
		return fail("cost preview exact output digits and NULL distinction")
	}
	invalid := []struct {
		payload string
		bounds  bool
	}{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{`"page":50001,`, `"page_size":20,`, `"factor":"1.2345",`, `"delta":"999999999999.99",`, `"min_cost":"-0.000001",`, `"total_tickets":9223372036854775807,`, `"id":9223372036854775807,`, `"subject":"exact",`, `"expected_cost":"999999999999.99",`, `,"adjusted_cost":"12345678901234.123456"`} {
		invalid = append(invalid, struct {
			payload string
			bounds  bool
		}{strings.Replace(body, field, "", 1), false})
	}
	for _, change := range []struct {
		before, after string
		bounds        bool
	}{
		{`"results":[`, `"results":null,"extra":[`, false}, {`"id":9007199254740993`, `"id":9223372036854775808`, false},
		{`"page_size":20`, `"page_size":21`, true}, {`"page":50001`, `"page":50002`, true}, {`"total_tickets":9223372036854775807`, `"total_tickets":-1`, true},
		{`"factor":"1.2345"`, `"factor":1.2345`, false}, {`"factor":"1.2345"`, `"factor":null`, false},
		{`"expected_cost":"999999999999.99"`, `"expected_cost":999999999999.99`, false},
		{`"adjusted_cost":"12345678901234.123456"`, `"adjusted_cost":"1.00"`, true},
		{`"adjusted_cost":"12345678901234.123456"`, `"adjusted_cost":"1e2"`, true},
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
		response, err := malformed.HelpdeskCostPreview(ctx, params)
		var decoding *ogenerrors.DecodeBodyError
		var validation *validate.Error
		if calls != 1 || response != nil || test.bounds && !errors.As(err, &validation) || !test.bounds && !errors.As(err, &decoding) {
			return fail("cost preview invalid response rejection category")
		}
	}
	calls = 0
	forbidden, err := client(403, `{"code":"permission_denied","errors":[]}`)
	if err != nil {
		return err
	}
	response, err = forbidden.HelpdeskCostPreview(ctx, params)
	if value, ok := response.(*hs.HelpdeskCostPreviewForbidden); err != nil || !ok || value.Response.Code != "permission_denied" || calls != 1 {
		return fail("cost preview generated permission response")
	}
	calls = 0
	failed, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskCostPreview(ctx, params)
	if _, ok := response.(*hs.HelpdeskCostPreviewInternalServerError); err != nil || !ok || calls != 1 {
		return fail("cost preview storage error retried or published")
	}
	return nil
}
