package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	hs "example.com/godj-openapi-client/helpdesksession"
	"github.com/ogen-go/ogen/ogenerrors"
)

func checkGeneratedLabelEnsureWire(ctx context.Context) error {
	const label = `{"id":1152921504606846977,"name":"wire","category":9223372036854775807}`
	for _, status := range []int{http.StatusOK, http.StatusCreated} {
		flag := "false"
		if status == http.StatusCreated {
			flag = "true"
		}
		calls := 0
		client, err := labelEnsureWireClient(status, `{"label":`+label+`,"created":`+flag+`}`, &calls)
		if err != nil {
			return err
		}
		response, err := client.HelpdeskLabelEnsure(ctx, &hs.LabelCreate{Name: "wire"})
		if err != nil || calls != 1 {
			return fail("generated ensure exact result wire")
		}
		var value hs.LabelEnsureResult
		switch typed := response.(type) {
		case *hs.HelpdeskLabelEnsureOK:
			if status != http.StatusOK {
				return fail("generated ensure mixed success statuses")
			}
			value = hs.LabelEnsureResult(*typed)
		case *hs.HelpdeskLabelEnsureCreated:
			if status != http.StatusCreated {
				return fail("generated ensure mixed success statuses")
			}
			value = hs.LabelEnsureResult(*typed)
		default:
			return fail("generated ensure lost success type")
		}
		if value.Label.ID != 1152921504606846977 || value.Label.Category != 9223372036854775807 || value.Label.Name != "wire" || value.Created != (status == http.StatusCreated) {
			return fail("generated ensure altered identity or creation flag")
		}
		for _, body := range []string{`{"label":` + label + `}`, `{"created":true}`, `{"label":null,"created":true}`, `{"label":` + label + `,"created":null}`, `{"label":` + label + `,"created":0}`, `{"label":` + label + `,"created":"false"}`, `{"label":{"id":1,"name":"wire"},"created":true}`} {
			calls = 0
			client, err := labelEnsureWireClient(status, body, &calls)
			if err != nil {
				return err
			}
			_, err = client.HelpdeskLabelEnsure(ctx, &hs.LabelCreate{Name: "wire"})
			var decode *ogenerrors.DecodeBodyError
			if calls != 1 || !errors.As(err, &decode) {
				return fail("generated ensure admitted missing or malformed required result")
			}
		}
	}
	// This synthetic failure checks only the generated transport's single send.
	// Real audit/rollback/unknown outcomes are owned by both Helpdesk DB tests.
	calls := 0
	client, err := labelEnsureWireClient(http.StatusInternalServerError, "Internal Server Error\n", &calls)
	if err != nil {
		return err
	}
	response, err := client.HelpdeskLabelEnsure(ctx, &hs.LabelCreate{Name: "wire"})
	if _, ok := response.(*hs.HelpdeskLabelEnsureInternalServerError); err != nil || !ok || calls != 1 {
		return fail("generated ensure server error retried or became success")
	}
	return nil
}

func labelEnsureWireClient(status int, body string, calls *int) (*hs.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPost || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/labels/ensure/" || string(wire) != `{"name":"wire"}` {
			return nil, fail("generated ensure request changed")
		}
		media := "application/json"
		if status == http.StatusInternalServerError {
			media = "text/plain; charset=utf-8"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(&http.Client{Transport: transport}))
}
