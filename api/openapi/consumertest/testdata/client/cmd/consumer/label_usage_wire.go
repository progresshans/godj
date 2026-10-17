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

func checkGeneratedLabelUsageWire(ctx context.Context) error {
	const body = `{"category":{"id":9223372036854775807,"name":"wire"},"page":50001,"page_size":20,"usage":"all","factor":"1.25","delta":"0.1","elapsed_factor":"0.5","elapsed_divisor":"1","total_labels":9223372036854775807,"results":[{"id":9223372036854775807,"name":"wire","total":3,"open":2,"closed":1,"expected_cost_total":"10999999999999.89","adjusted_cost_total":"13750000000000.1625","effort_average":1.5,"elapsed_total":"-999999999 00:00:00","adjusted_elapsed_total":"-500000000 12:00:00"},{"id":9007199254740993,"name":"zero","total":1,"open":0,"closed":1,"expected_cost_total":"0","adjusted_cost_total":"0.1","effort_average":0,"elapsed_total":"00:00:00","adjusted_elapsed_total":"00:00:00"},{"id":1,"name":"unused","total":0,"open":0,"closed":0,"expected_cost_total":null,"adjusted_cost_total":null,"effort_average":null,"elapsed_total":null,"adjusted_elapsed_total":null}]}`
	params := hs.HelpdeskLabelUsageParams{P: hs.NewOptInt64(50001), Usage: hs.NewOptHelpdeskLabelUsageUsage(hs.HelpdeskLabelUsageUsageAll), Factor: hs.NewOptString("1.25"), Delta: hs.NewOptString("0.1"), ElapsedFactor: hs.NewOptString("0.5"), ElapsedDivisor: hs.NewOptString("1")}
	calls := 0
	client := func(status int, payload string) (*hs.Client, error) {
		return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			values := request.URL.Query()
			if request.Method != http.MethodGet || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/labels/usage/" || len(values) != 6 || values.Get("p") != "50001" || values.Get("usage") != "all" || values.Get("factor") != "1.25" || values.Get("delta") != "0.1" || values.Get("elapsed_factor") != "0.5" || values.Get("elapsed_divisor") != "1" {
				return nil, fail("label usage exact query wire")
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
	response, err := valid.HelpdeskLabelUsage(ctx, params)
	page, ok := response.(*hs.LabelUsageHeaders)
	if err != nil || !ok || calls != 1 || page.Response.Category.ID != 9223372036854775807 || page.Response.Page != 50001 || page.Response.PageSize != 20 || page.Response.Usage != "all" || page.Response.Factor != "1.25" || page.Response.Delta != "0.1" || page.Response.ElapsedFactor != "0.5" || page.Response.ElapsedDivisor != "1" || page.Response.TotalLabels != 9223372036854775807 || len(page.Response.Results) != 3 {
		return fail("label usage exact int64 envelope")
	}
	first, zero, missing := page.Response.Results[0], page.Response.Results[1], page.Response.Results[2]
	if first.ID != 9223372036854775807 || first.Total != 3 || first.Open != 2 || first.Closed != 1 || first.ExpectedCostTotal.Null || first.ExpectedCostTotal.Value != "10999999999999.89" || first.AdjustedCostTotal.Null || first.AdjustedCostTotal.Value != "13750000000000.1625" || first.EffortAverage.Null || first.EffortAverage.Value != 1.5 || first.ElapsedTotal.Null || first.ElapsedTotal.Value != "-999999999 00:00:00" || first.AdjustedElapsedTotal.Null || first.AdjustedElapsedTotal.Value != "-500000000 12:00:00" || zero.AdjustedElapsedTotal.Null || zero.AdjustedElapsedTotal.Value != "00:00:00" || !missing.AdjustedElapsedTotal.Null || zero.ID != 9007199254740993 || zero.ExpectedCostTotal.Null || zero.ExpectedCostTotal.Value != "0" || zero.AdjustedCostTotal.Null || zero.AdjustedCostTotal.Value != "0.1" || zero.EffortAverage.Null || zero.EffortAverage.Value != 0 || zero.ElapsedTotal.Null || zero.ElapsedTotal.Value != "00:00:00" || !missing.ExpectedCostTotal.Null || !missing.AdjustedCostTotal.Null || !missing.EffortAverage.Null || !missing.ElapsedTotal.Null {
		return fail("label usage exact nullable numeric values")
	}
	invalid := []struct {
		payload string
		bounds  bool
	}{{`{}`, false}, {`null`, false}, {body + `true`, false}}
	for _, field := range []string{`"category":{"id":9223372036854775807,"name":"wire"},`, `"page":50001,`, `"page_size":20,`, `"usage":"all",`, `"factor":"1.25",`, `"delta":"0.1",`, `"elapsed_factor":"0.5",`, `"elapsed_divisor":"1",`, `"total_labels":9223372036854775807,`, `"id":9223372036854775807,`, `"name":"wire",`, `"total":3,`, `"open":2,`, `"closed":1,`, `"expected_cost_total":"10999999999999.89",`, `"adjusted_cost_total":"13750000000000.1625",`, `"effort_average":1.5,`, `"elapsed_total":"-999999999 00:00:00",`, `,"adjusted_elapsed_total":"-500000000 12:00:00"`} {
		// Name occurs in category and row; omitting either required name is invalid.
		invalid = append(invalid, struct {
			payload string
			bounds  bool
		}{strings.Replace(body, field, "", 1), false})
	}
	for _, change := range []struct {
		before, after string
		bounds        bool
	}{
		{`"results":[`, `"results":null,"extra":[`, false}, {`"total":3`, `"total":3.0`, false}, {`"total":3`, `"total":-1`, true},
		{`"id":9007199254740993`, `"id":9223372036854775808`, false}, {`"id":9007199254740993`, `"id":0`, true},
		{`"open":2`, `"open":null`, false}, {`"usage":"all"`, `"usage":null`, false}, {`"page_size":20`, `"page_size":21`, true}, {`"page":50001`, `"page":50002`, true},
		{`"expected_cost_total":"10999999999999.89"`, `"expected_cost_total":10999999999999.89`, false},
		{`"expected_cost_total":"10999999999999.89"`, `"expected_cost_total":"1.00"`, true},
		{`"factor":"1.25"`, `"factor":1.25`, false}, {`"factor":"1.25"`, `"factor":null`, false},
		{`"factor":"1.25"`, `"factor":"1.00"`, true}, {`"delta":"0.1"`, `"delta":"1e2"`, true},
		{`"delta":"0.1"`, `"delta":null`, false}, {`"delta":"0.1"`, `"delta":0.1`, false},
		{`"adjusted_cost_total":"13750000000000.1625"`, `"adjusted_cost_total":13750000000000.1625`, false},
		{`"adjusted_cost_total":"13750000000000.1625"`, `"adjusted_cost_total":"1.00"`, true},
		{`"adjusted_cost_total":"13750000000000.1625"`, `"adjusted_cost_total":"1e2"`, true},
		{`"effort_average":1.5`, `"effort_average":"1.5"`, false}, {`"effort_average":1.5`, `"effort_average":1e309`, false},
		{`"elapsed_total":"-999999999 00:00:00"`, `"elapsed_total":"24:00:00"`, true},
		{`"elapsed_factor":"0.5"`, `"elapsed_factor":0.5`, false}, {`"elapsed_factor":"0.5"`, `"elapsed_factor":"0.50"`, true},
		{`"elapsed_divisor":"1"`, `"elapsed_divisor":null`, false}, {`"elapsed_divisor":"1"`, `"elapsed_divisor":"1e0"`, true},
		{`"adjusted_elapsed_total":"-500000000 12:00:00"`, `"adjusted_elapsed_total":1`, false},
		{`"adjusted_elapsed_total":"-500000000 12:00:00"`, `"adjusted_elapsed_total":"24:00:00"`, true},
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
		response, err := malformed.HelpdeskLabelUsage(ctx, params)
		var decoding *ogenerrors.DecodeBodyError
		var validation *validate.Error
		if calls != 1 || response != nil || test.bounds && !errors.As(err, &validation) || !test.bounds && !errors.As(err, &decoding) {
			return fail("label usage invalid response rejection category")
		}
	}
	calls = 0
	forbidden, err := client(403, `{"code":"permission_denied","errors":[]}`)
	if err != nil {
		return err
	}
	response, err = forbidden.HelpdeskLabelUsage(ctx, params)
	if value, ok := response.(*hs.GoDjAPIError); err != nil || !ok || value.Code != "permission_denied" || calls != 1 {
		return fail("label usage generated permission response")
	}
	calls = 0
	failed, err := client(500, "Internal Server Error\n")
	if err != nil {
		return err
	}
	response, err = failed.HelpdeskLabelUsage(ctx, params)
	if _, ok := response.(*hs.HelpdeskLabelUsageInternalServerError); err != nil || !ok || calls != 1 {
		return fail("label usage generated storage error retried or published")
	}
	return nil
}
