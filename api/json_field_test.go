package api_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

func TestJSONResponseLimitsBoundTheWholeCollection(t *testing.T) {
	document, err := jsonvalue.Parse([]byte("[" + strings.Repeat("0,", 1023) + "0]"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := serializers.NewList(serializers.JSON(document), serializers.JSON(document), serializers.JSON(document), serializers.JSON(document))
	if err != nil {
		t.Fatal(err)
	}
	if response, err := api.JSON(http.StatusOK, value); !errors.Is(err, &serializers.Error{Code: serializers.CodeResourceLimit}) || response.Status() != 0 {
		t.Fatal("default response budget changed or emitted a partial response", err)
	}
	response, err := api.JSONWithLimits(http.StatusOK, value, serializers.Limits{MaxValues: 4101})
	if err != nil || response.Status() != http.StatusOK || response.Header().Get("Content-Type") != api.JSONContentType || string(response.Body()) != "["+strings.TrimSuffix(strings.Repeat(document.Text+",", 4), ",")+"]" {
		t.Fatal("explicit collection budget changed content", err)
	}
	for _, limits := range []serializers.Limits{{MaxValues: 4100}, {MaxValues: 4101, MaxDocumentBytes: 100}, {MaxValues: 4101, MaxDepth: 2}, {MaxValues: 4101, MaxArrayItems: 1023}, {MaxValues: 1<<16 + 1}} {
		response, err := api.JSONWithLimits(http.StatusOK, value, limits)
		if !errors.Is(err, &api.Error{Code: api.FailureInvalidResponse}) || response.Status() != 0 || len(response.Body()) != 0 {
			t.Fatal("collection rendering bypassed a shared budget or hard cap", err)
		}
	}
}

func TestDeclaredJSONRequestParserKeepsNamedEnvelopeAndSharedLimits(t *testing.T) {
	field, err := serializers.JSONField("payload", serializers.WithNullable())
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: 128, JSONLimits: serializers.Limits{MaxValues: 6}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		body string
		code api.FailureCode
		want string
	}{
		{`{"payload":{"":false}}`, "", `{"":false}`},
		{`{"payload":340282366920938463463374607431768211455}`, "", `340282366920938463463374607431768211455`},
		{`{"":0}`, api.FailureInvalidRequest, ""},
		{`{"other":{"":0}}`, api.FailureInvalidRequest, ""},
		{`{"payload":{"":1,"":2}}`, api.FailureInvalidRequest, ""},
		{`{"payload":"\u0000"}`, api.FailureInvalidRequest, ""},
		{`{"payload":"\ud800"}`, api.FailureInvalidRequest, ""},
		{`{"payload":[1,2,3,4,5]}`, api.FailureInvalidRequest, ""},
		{`{"payload":"` + strings.Repeat("x", 130) + `"}`, api.FailureBodyTooLarge, ""},
	} {
		t.Run(test.body, func(t *testing.T) {
			var parsed serializers.Object
			var parseErr error
			var borrowed *web.Request
			application := testApplication(t, nil, []web.Route{{Name: "test:json", Method: http.MethodPost, Path: "/json/", Handler: func(request *web.Request) (web.Response, error) {
				borrowed = request
				parsed, parseErr = parser.ParseObjectFor(request, spec)
				return emptyOK(t), nil
			}}})
			request := httptest.NewRequest(http.MethodPost, "http://example.test/json/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			application.ServeHTTP(httptest.NewRecorder(), request)
			if test.code != "" {
				if !errors.Is(parseErr, &api.Error{Code: test.code}) {
					t.Fatal("JSON request boundary changed", parseErr)
				}
				return
			}
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			value, ok := parsed.Get("payload")
			document, typed := value.AsJSON()
			if !ok || !typed || document.Text != test.want {
				t.Fatal("JSON request lost exact typed document")
			}
			if _, err := parser.ParseObjectFor(borrowed, spec); !errors.Is(err, &api.Error{Code: api.FailureInvalidRequest}) {
				t.Fatal("declared JSON parser reused an expired request", err)
			}
		})
	}
	if _, err := parser.ParseObjectFor(nil, serializers.Spec{}); !errors.Is(err, &api.Error{Code: api.FailureInvalidConfig}) {
		t.Fatal("invalid serializer became a client parsing error", err)
	}
}
