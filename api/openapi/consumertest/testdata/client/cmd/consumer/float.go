package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
)

func checkHelpdeskFloatUpdates(ctx context.Context, client *hs.Client, transport *observedTransport, original hs.Ticket) (hs.Ticket, error) {
	expected := original
	params := hs.HelpdeskTicketPatchParams{ID: original.ID}
	patch := func(request hs.TicketPatch) error {
		response, err := client.HelpdeskTicketPatch(ctx, &request, params)
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || transport.lastStatus() != http.StatusOK || *row != expected {
			return fail("float PATCH/presence")
		}
		if !row.Effort.Null && math.Float64bits(row.Effort.Value) != math.Float64bits(expected.Effort.Value) {
			return fail("float HTTP precision")
		}
		return nil
	}
	expected.Effort = hs.NewNilFloat64(0)
	if err := patch(hs.TicketPatch{Effort: hs.NewOptNilFloat64(0)}); err != nil {
		return hs.Ticket{}, err
	}
	if err := patch(hs.TicketPatch{}); err != nil {
		return hs.Ticket{}, err
	}
	// Effort uses numeric no-op equality and this real fixture uses SQLite.
	if err := patch(hs.TicketPatch{Effort: hs.NewOptNilFloat64(math.Copysign(0, -1))}); err != nil {
		return hs.Ticket{}, err
	}
	response, err := client.HelpdeskTicketUpdate(ctx, &hs.TicketUpdate{Subject: original.Subject}, hs.HelpdeskTicketUpdateParams{ID: original.ID})
	row, ok := response.(*hs.Ticket)
	if err != nil || !ok || *row != expected {
		return hs.Ticket{}, fail("float PUT omission")
	}
	clear := hs.OptNilFloat64{}
	clear.SetToNull()
	expected.Effort.SetToNull()
	if err := patch(hs.TicketPatch{Effort: clear}); err != nil {
		return hs.Ticket{}, err
	}
	for _, value := range []float64{-math.MaxFloat64, math.MaxFloat64, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, 0.1, 1.5} {
		expected.Effort = hs.NewNilFloat64(value)
		if err := patch(hs.TicketPatch{Effort: hs.NewOptNilFloat64(value)}); err != nil {
			return hs.Ticket{}, err
		}
	}
	return expected, nil
}
func checkGeneratedFloatWire(ctx context.Context) error {
	const base = `{"id":1,"subject":"wire","details":null,"closed":false,"category":1,"expected_cost":null,"priority":null,"resolution":null,"due_at":null,"reviewed":null,"service_on":null,"service_at":null,"elapsed":null`
	clear := hs.OptNilFloat64{}
	clear.SetToNull()
	cases := []struct {
		request       hs.TicketPatch
		value         string
		present, null bool
		number        float64
	}{
		{hs.TicketPatch{}, "null", false, true, 0},
		{hs.TicketPatch{Effort: clear}, "null", true, true, 0},
	}
	for _, number := range []float64{0, math.Copysign(0, -1), 0.1, math.SmallestNonzeroFloat64, math.MaxFloat64, -math.MaxFloat64} {
		text := strconv.FormatFloat(number, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		cases = append(cases, struct {
			request       hs.TicketPatch
			value         string
			present, null bool
			number        float64
		}{hs.TicketPatch{Effort: hs.NewOptNilFloat64(number)}, text, true, false, number})
	}
	for _, test := range cases {
		calls := 0
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Method != http.MethodPatch || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/1/" {
				return nil, fail("float wire route")
			}
			wire, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			var object map[string]json.RawMessage
			if err = json.Unmarshal(wire, &object); err != nil {
				return nil, fail("float request is not JSON")
			}
			raw, present := object["effort"]
			expectedCount := 0
			if test.present {
				expectedCount = 1
			}
			if present != test.present || len(object) != expectedCount {
				return nil, fail("float request presence")
			}
			if present {
				if test.null {
					if string(raw) != "null" {
						return nil, fail("float request NULL")
					}
				} else {
					var number float64
					if err = json.Unmarshal(raw, &number); err != nil || math.Float64bits(number) != math.Float64bits(test.number) {
						return nil, fail("float request precision")
					}
				}
			}
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(base + `,"effort":` + test.value + `}`)), Request: request}, nil
		})}
		client, err := hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(httpClient))
		if err != nil {
			return err
		}
		response, err := client.HelpdeskTicketPatch(ctx, &test.request, hs.HelpdeskTicketPatchParams{ID: 1})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.Effort.Null != test.null {
			return fail("float response presence")
		}
		if !test.null && math.Float64bits(row.Effort.Value) != math.Float64bits(test.number) {
			return fail("float response precision")
		}
	}
	for _, suffix := range []string{`}`, `,"effort":true}`, `,"effort":"1.5"}`, `,"effort":"NaN"}`, `,"effort":1e309}`, `,"effort":NaN}`} {
		calls := 0
		client, err := nullableBooleanWireClient(base+suffix, `{}`, &calls)
		if err != nil {
			return err
		}
		if _, err := client.HelpdeskTicketPatch(ctx, &hs.TicketPatch{}, hs.HelpdeskTicketPatchParams{ID: 1}); err == nil || calls != 1 {
			return fail("float response rejects missing, overflow and non-number")
		}
	}
	for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		request := hs.TicketPatch{Effort: hs.NewOptNilFloat64(number)}
		if request.Validate() == nil {
			return fail("explicit generated Float request validation")
		}
	}
	return nil
}
