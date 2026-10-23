package api_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func TestListParserPreservesRequestAdmissionAndBorrowedLifetime(t *testing.T) {
	field, err := serializers.JSONField("payload")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: 64, JSONLimits: serializers.Limits{MaxValues: 6}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, wire, media string
		code              api.FailureCode
	}{
		{"valid", `[{"payload":{"":9007199254740993}},{"payload":false}]`, "application/json", ""},
		{"wrong media", `[]`, "text/plain", api.FailureUnsupportedMedia},
		{"scalar", `[{"payload":1},false]`, "application/json", api.FailureInvalidRequest},
		{"duplicate", `[{"payload":1,"payload":2}]`, "application/json", api.FailureInvalidRequest},
		{"aggregate", `[{"payload":[1,2,3]},{"payload":2}]`, "application/json", api.FailureInvalidRequest},
		{"body cap", `[{"payload":"` + strings.Repeat("x", 64) + `"}]`, "application/json", api.FailureBodyTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rows []serializers.Object
			var failure error
			var borrowed *web.Request
			app := testApplication(t, nil, []web.Route{{Name: "test:array", Method: http.MethodPost, Path: "/array/", Handler: func(request *web.Request) (web.Response, error) {
				borrowed = request
				rows, failure = parser.ParseListFor(request, spec)
				return emptyOK(t), nil
			}}})
			request := httptest.NewRequest(http.MethodPost, "http://example.test/array/", strings.NewReader(test.wire))
			request.ContentLength = -1 // prove the streaming cap, not just the declared length
			request.Header.Set("Content-Type", test.media)
			app.ServeHTTP(httptest.NewRecorder(), request)
			if test.code != "" {
				if rows != nil || !errors.Is(failure, &api.Error{Code: test.code}) {
					t.Fatal("request boundary", failure)
				}
			} else if failure != nil || len(rows) != 2 {
				t.Fatal("valid list", failure)
			}
			if rows, err := parser.ParseListFor(borrowed, spec); rows != nil || !errors.Is(err, &api.Error{Code: api.FailureInvalidRequest}) {
				t.Fatal("expired request reused", err)
			}
		})
	}
	if _, err := parser.ParseListFor(nil, serializers.Spec{}); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatal("zero spec", err)
	}
}

func TestMultiObjectDiagnosticsHaveOneExplicitBudget(t *testing.T) {
	items := make([]validation.Violation, 600)
	for index := range items {
		items[index] = validation.New("subject", "invalid", validation.NewParam("index", "0"))
	}
	failures := validation.NewErrors(items...)
	if response, err := api.ErrorResponse(400, api.CodeValidationError, failures); response.Status() != 0 || !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatal("default diagnostic budget", err)
	}
	if response, err := api.ErrorResponseWithLimits(400, api.CodeValidationError, failures, serializers.Limits{MaxValues: 65536}); err != nil || response.Status() != 400 || !strings.Contains(string(requireBufferedBody(t, response)), `"index"`) {
		t.Fatal("explicit diagnostic budget", err)
	}
	if response, err := api.ErrorResponseWithLimits(400, api.CodeValidationError, failures, serializers.Limits{MaxValues: 65536, MaxDocumentBytes: 100}); response.Status() != 0 || !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) {
		t.Fatal("diagnostic byte budget bypassed", err)
	}
}
