package main

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	ab "example.com/godj-openapi-client/articlebearer"
	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
)

const maxArticleJSON = `{"id":9223372036854775807,"title":"wire","published":false,"summary":null}`

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// These responses belong exclusively to the generated-code wire regression.
// The preceding application flows use the three real parent-owned servers.
func checkGeneratedWire(ctx context.Context) error {
	cases := []struct {
		name   string
		body   string
		reject bool
	}{
		{"maximum int64", maxArticleJSON, false},
		{"int64 overflow", `{"id":9223372036854775808,"title":"wire","published":false,"summary":null}`, true},
		{"required nullable response", `{"id":1,"title":"wire","published":false}`, true},
		{"required readonly response", `{"title":"wire","published":false,"summary":null}`, true},
		{"closed response", `{"id":1,"title":"wire","published":false,"summary":null,"unknown":true}`, true},
	}
	for _, test := range cases {
		calls := 0
		client, err := mockArticleClient(test.body, http.MethodGet, "", &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjConformanceArticleDetail(ctx, ab.GodjConformanceArticleDetailParams{ID: math.MaxInt64})
		if calls != 1 {
			return fail("generated response single HTTP exchange")
		}
		if test.reject {
			var decodeError *ogenerrors.DecodeBodyError
			if !errors.As(err, &decodeError) {
				return fail("generated " + test.name + " rejection")
			}
			continue
		}
		article, ok := response.(*ab.Article)
		if err != nil || !ok || article.ID != math.MaxInt64 || !article.Summary.Null || article.Title != "wire" || article.Published {
			return fail("generated full int64 response")
		}
	}
	clear := ab.OptNilString{}
	clear.SetToNull()
	requests := []struct {
		body ab.ArticlePatch
		wire string
	}{
		{ab.ArticlePatch{}, `{}`},
		{ab.ArticlePatch{Published: ab.NewOptBool(false), Summary: clear}, `{"published":false,"summary":null}`},
		{ab.ArticlePatch{Summary: ab.NewOptNilString("")}, `{"summary":""}`},
	}
	for _, test := range requests {
		calls := 0
		client, err := mockArticleClient(maxArticleJSON, http.MethodPatch, test.wire, &calls)
		if err != nil {
			return err
		}
		response, err := client.GodjConformanceArticlePartialUpdate(ctx, &test.body, ab.GodjConformanceArticlePartialUpdateParams{ID: math.MaxInt64})
		article, ok := response.(*ab.Article)
		if err != nil || !ok || calls != 1 || article.ID != math.MaxInt64 {
			return fail("generated optional nullable request wire")
		}
	}
	if err := checkGeneratedChoiceResponseWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedNullableBooleanWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedCalendarDateWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedClockTimeWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedDurationWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedFloatWire(ctx); err != nil {
		return err
	}
	if err := checkGeneratedDecimalWire(ctx); err != nil {
		return err
	}
	return checkGeneratedUUIDWire(ctx)
}

type wireHelpdeskSecurity struct{}

func (wireHelpdeskSecurity) SessionAuth(context.Context, hs.OperationName) (hs.SessionAuth, error) {
	return hs.SessionAuth{APIKey: "probe-session"}, nil
}
func (wireHelpdeskSecurity) CsrfCookie(context.Context, hs.OperationName) (hs.CsrfCookie, error) {
	return hs.CsrfCookie{APIKey: "probe-cookie"}, nil
}
func (wireHelpdeskSecurity) CsrfHeader(context.Context, hs.OperationName) (hs.CsrfHeader, error) {
	return hs.CsrfHeader{APIKey: "probe-header"}, nil
}

func checkGeneratedChoiceResponseWire(ctx context.Context) error {
	for _, value := range []int64{math.MinInt64, math.MaxInt64, 99} {
		calls := 0
		body := `{"id":1,"subject":"Legacy priority","details":null,"closed":false,"category":1,"external_reference":null,"expected_cost":null,"effort":null,"elapsed":null,"priority":` + strconv.FormatInt(value, 10) + `,"resolution":null,"due_at":null,"service_on":null,"service_at":null,"reviewed":null}`
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			wire, err := io.ReadAll(request.Body)
			if err != nil || request.Method != http.MethodPost || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/tickets/" || !strings.Contains(string(wire), `"priority":0`) {
				return nil, fail("generated choice request wire")
			}
			return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})}
		client, err := hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(httpClient))
		if err != nil {
			return fail("generated choice mock setup")
		}
		response, err := client.HelpdeskTicketCreate(ctx, &hs.TicketCreate{Subject: "Choice", Priority: hs.NewOptNilTicketCreatePriority(hs.TicketCreatePriority0)})
		row, ok := response.(*hs.Ticket)
		if err != nil || !ok || calls != 1 || row.Priority.Null || row.Priority.Value != value {
			return fail("generated response excludes stored priority values")
		}
	}
	return nil
}

func mockArticleClient(body, method, requestBody string, calls *int) (*ab.Client, error) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls = *calls + 1
		if request.Method != method || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/articles/9223372036854775807/" || request.Header.Get("Authorization") != "Bearer probe-only" || len(request.Cookies()) != 0 || request.Header.Get(csrfHeaderName) != "" {
			return nil, fail("generated int64 path and bearer wire")
		}
		if method == http.MethodPatch {
			data, err := io.ReadAll(request.Body)
			if err != nil || string(data) != requestBody {
				return nil, fail("generated request presence wire")
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	client, err := ab.NewClient("https://probe.invalid", bearerSource{token: "probe-only"}, ab.WithClient(httpClient))
	if err != nil {
		return nil, fail("generated mock client setup")
	}
	return client, nil
}
