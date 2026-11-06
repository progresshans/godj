package input_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

type jsonChild struct {
	Payload input.Presence[jsonvalue.Value]
}
type jsonEnvelope struct{ Left, Right jsonChild }

func jsonEnvelopeBody(t *testing.T, after func(), config api.ParserConfig) input.Body[jsonEnvelope] {
	t.Helper()
	f := require[serializers.Field](t)(serializers.JSONField("payload"))
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f}))
	// An envelope owns one new whole-request budget; child HTTP parsers must
	// never run or reset that budget for each member.
	leaf := require[input.Body[jsonChild]](t)(input.New(spec, api.ParserConfig{MaxBodyBytes: 2}, input.Field("payload", input.JSON(), func(out *jsonChild, value input.Presence[jsonvalue.Value]) { out.Payload = value; after() })))
	return require[input.Body[jsonEnvelope]](t)(input.Object(config,
		input.ObjectField("left", leaf, func(out *jsonEnvelope, value jsonChild) { out.Left = value; after() }),
		input.ObjectField("right", leaf, func(out *jsonEnvelope, value jsonChild) { out.Right = value; after() }),
	))
}

func objectInputApplication(t *testing.T, handler web.Handler) *web.Application {
	t.Helper()
	configured := require[settings.Settings](t)(settings.New(settings.Definition{ProjectName: "object_input", InstalledApps: []apps.Config{{Name: "example.test/object", Label: "test"}}}))
	return require[*web.Application](t)(web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "test:object", Method: http.MethodPost, Path: "/", Handler: handler}}}))
}

func TestObjectBodyActualRequestKeepsOneBudgetAndBorrowedLifetime(t *testing.T) {
	valid := `{"left":{"payload":[1,2]},"right":{"payload":[3,4]}}`
	private := errors.New("private reader cause")
	for _, test := range []struct {
		name, raw, media                                                   string
		code                                                               api.FailureCode
		diagnostics                                                        []string
		cancelBefore, cancelRead, readFailure, oversizeLength, invalidMode bool
	}{
		{name: "accepted", raw: valid, media: api.JSONContentType},
		{name: "arbitrary_json_keys", raw: `{"left":{"payload":{"":1}},"right":{"payload":{"x/y":true}}}`, media: api.JSONContentType},
		{name: "aggregate_values", raw: `{"left":{"payload":[1,2,3]},"right":{"payload":[4,5]}}`, media: api.JSONContentType, code: api.FailureInvalidRequest},
		{name: "depth", raw: `{"left":{"payload":[[1]]},"right":{"payload":2}}`, media: api.JSONContentType, code: api.FailureInvalidRequest},
		{name: "bytes", raw: valid + strings.Repeat(" ", 128), media: api.JSONContentType, code: api.FailureBodyTooLarge},
		{name: "duplicate", raw: `{"left":{"payload":1},"left":{},"right":{}}`, media: api.JSONContentType, code: api.FailureInvalidRequest},
		{name: "wrong_json_path", raw: `{"left":{"other":{"":1}},"right":{"payload":2}}`, media: api.JSONContentType, code: api.FailureInvalidRequest},
		{name: "missing_member", raw: `{"left":{"payload":1}}`, media: api.JSONContentType, diagnostics: []string{"/right/required"}},
		{name: "null_member", raw: `{"left":null,"right":{"payload":null}}`, media: api.JSONContentType, diagnostics: []string{"/left/type", "/right/payload/null"}},
		{name: "unknown_member", raw: `{"left":{"payload":1},"right":{"payload":2},"secret":3}`, media: api.JSONContentType, diagnostics: []string{"/secret/unknown"}},
		{name: "media", raw: valid, media: "text/plain", code: api.FailureUnsupportedMedia},
		{name: "read_failure", raw: valid, media: api.JSONContentType, readFailure: true, code: api.FailureBodyRead},
		{name: "cancel_before", raw: valid, media: api.JSONContentType, cancelBefore: true, code: api.FailureBodyRead},
		{name: "cancel_read", raw: valid, media: api.JSONContentType, cancelRead: true, code: api.FailureBodyRead},
		{name: "length_before_read", raw: valid, media: api.JSONContentType, oversizeLength: true, code: api.FailureBodyTooLarge},
		{name: "mode_before_read", raw: valid, media: api.JSONContentType, invalidMode: true, code: api.FailureInvalidConfig},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			body := jsonEnvelopeBody(t, func() { calls++ }, api.ParserConfig{MaxBodyBytes: 128, JSONLimits: serializers.Limits{MaxValues: 9, MaxDepth: 4}})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}
			reader := &borrowedBody{reader: strings.NewReader(test.raw)}
			if test.cancelRead {
				reader.cancel = cancel
			}
			if test.readFailure {
				reader.failure = private
			}
			mode := serializers.ModeFull
			if test.invalidMode {
				mode = 0
			}
			var value jsonEnvelope
			var diagnostics validation.Errors
			var failure error
			var borrowed *web.Request
			application := objectInputApplication(t, func(request *web.Request) (web.Response, error) {
				borrowed = request
				value, diagnostics, failure = body.Parse(request, mode)
				return api.NoContent()
			})
			request := httptest.NewRequest(http.MethodPost, "http://example.test/", nil).WithContext(ctx)
			request.Body, request.ContentLength = reader, -1
			if test.oversizeLength {
				request.ContentLength = 129
			}
			request.Header.Set("Content-Type", test.media)
			response := httptest.NewRecorder()
			application.ServeHTTP(response, request)
			if response.Code != 204 || borrowed == nil || reader.closes != 0 {
				t.Fatal("borrowed lifetime", response.Code, reader.closes)
			}
			if test.code != "" || len(test.diagnostics) > 0 {
				if !reflect.DeepEqual(value, jsonEnvelope{}) || calls != 0 {
					t.Fatal("failed envelope ran setters or exposed child", calls)
				}
			} else {
				left, lp := value.Left.Payload.Get()
				right, rp := value.Right.Payload.Get()
				if failure != nil || !diagnostics.Empty() || calls != 4 || !lp || !rp || !left.Valid() || !right.Valid() {
					t.Fatal("envelope parsing", failure, diagnostics, calls)
				}
			}
			if test.code != "" {
				if !errors.Is(failure, &api.Error{Code: test.code}) || !diagnostics.Empty() {
					t.Fatal("parser classification", failure, diagnostics)
				}
			} else if failure != nil || !slices.Equal(codes(diagnostics), test.diagnostics) {
				t.Fatal("field diagnostics", failure, codes(diagnostics))
			}
			if test.cancelBefore || test.cancelRead {
				if !errors.Is(failure, context.Canceled) {
					t.Fatal("cancellation lost", failure)
				}
			}
			if test.readFailure && (!errors.Is(failure, private) || strings.Contains(failure.Error(), "private")) {
				t.Fatal("reader error leaked or lost cause", failure)
			}
			if test.cancelBefore || test.invalidMode || test.oversizeLength || test.media != "application/json" {
				if reader.reads != 0 {
					t.Fatal("pre-read rejection read body", reader.reads)
				}
			}
			if _, _, err := body.Parse(borrowed, serializers.ModeFull); !errors.Is(err, &api.Error{Code: api.FailureInvalidRequest}) {
				t.Fatal("expired request accepted", err)
			}
		})
	}
}

func TestObjectListKeepsNestedGrammarAndIndexedAtomicBindings(t *testing.T) {
	valid := `{"left":{"payload":[1,2]},"right":{"payload":[3,4]}}`
	for _, test := range []struct {
		name, raw   string
		maximum     int
		code        api.FailureCode
		want        []string
		calls, rows int
	}{
		{name: "accepted", raw: "[" + valid + "," + valid + "]", maximum: 19, calls: 8, rows: 2},
		{name: "shared_budget", raw: "[" + valid + "," + valid + "]", maximum: 18, code: api.FailureInvalidRequest},
		{name: "json_keys", raw: `[{"left":{"payload":{"":1}},"right":{"payload":{"":2}}}]`, maximum: 19, calls: 4, rows: 1},
		{name: "invalid_second", raw: "[" + valid + `,{"left":{"payload":null},"right":{"payload":1}}]`, maximum: 19, want: []string{"/left/payload/null/index=1"}, calls: 4},
		{name: "wrong_root", raw: valid, maximum: 19, code: api.FailureInvalidRequest},
		{name: "wrong_path", raw: `[{"left":{"other":{"":1}},"right":{"payload":2}}]`, maximum: 19, code: api.FailureInvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			body := jsonEnvelopeBody(t, func() { calls++ }, api.ParserConfig{MaxBodyBytes: 2})
			list := require[input.ListBody[jsonEnvelope]](t)(input.NewList(body, input.ListConfig{MinItems: 1, MaxItems: 3, Parser: api.ParserConfig{MaxBodyBytes: 256, JSONLimits: serializers.Limits{MaxValues: test.maximum, MaxDepth: 5}}}))
			var values []jsonEnvelope
			var diagnostics validation.Errors
			var failure error
			application := objectInputApplication(t, func(request *web.Request) (web.Response, error) {
				values, diagnostics, failure = list.Parse(request, serializers.ModeFull)
				return api.NoContent()
			})
			reader := &borrowedBody{reader: strings.NewReader(test.raw)}
			request := httptest.NewRequest(http.MethodPost, "http://example.test/", nil)
			request.Body, request.ContentLength = reader, -1
			request.Header.Set("Content-Type", api.JSONContentType)
			response := httptest.NewRecorder()
			application.ServeHTTP(response, request)
			if response.Code != 204 || reader.closes != 0 || calls != test.calls || len(values) != test.rows {
				t.Fatal("array ownership/atomic binding", response.Code, calls, len(values), reader.closes)
			}
			if test.code != "" {
				if !errors.Is(failure, &api.Error{Code: test.code}) || !diagnostics.Empty() {
					t.Fatal("array parser failure", failure, diagnostics)
				}
			} else if failure != nil || !slices.Equal(codes(diagnostics), test.want) {
				t.Fatal("indexed nested diagnostics", failure, codes(diagnostics))
			}
			if test.rows == 0 && values != nil {
				t.Fatal("failed list returned partial rows")
			}
		})
	}
}
