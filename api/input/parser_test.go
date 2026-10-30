package input_test

import (
	"context"
	"errors"
	"io"
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

type borrowedBody struct {
	reader        io.Reader
	cancel        context.CancelFunc
	failure       error
	reads, closes int
}

func (body *borrowedBody) Read(buffer []byte) (int, error) {
	body.reads++
	n, err := body.reader.Read(buffer)
	if body.cancel != nil {
		body.cancel()
	}
	if body.failure != nil {
		return n, body.failure
	}
	return n, err
}
func (body *borrowedBody) Close() error { body.closes++; return nil }

func TestActualRequestUsesSharedParserBudgetsAndBorrowedLifetime(t *testing.T) {
	f := require[serializers.Field](t)
	spec := require[serializers.Spec](t)(serializers.NewSpec([]serializers.Field{f(serializers.JSONField("payload"))}))
	type row struct {
		Payload input.Presence[jsonvalue.Value]
	}
	privateError := errors.New("private underlying reader value")
	for _, test := range []struct {
		name, raw, media                                                     string
		code                                                                 api.FailureCode
		violations                                                           []string
		cancelBefore, cancelRead, readerFailure, invalidMode, oversizeLength bool
	}{
		{name: "JSON field arbitrary keys", raw: `{"payload":{"a b":1,"x/y":[2]}}`, media: "application/json; charset=utf-8"},
		{name: "exact byte boundary", raw: `{"payload":"` + strings.Repeat("x", 50) + `"}`, media: "application/json"},
		{name: "over byte boundary", raw: `{"payload":"` + strings.Repeat("x", 51) + `"}`, media: "application/json", code: api.FailureBodyTooLarge},
		{name: "depth budget", raw: `{"payload":[[[[[1]]]]]}`, media: "application/json", code: api.FailureInvalidRequest},
		{name: "aggregate value budget", raw: `{"payload":[1,2,3,4,5,6,7,8,9,10]}`, media: "application/json", code: api.FailureInvalidRequest},
		{name: "duplicate", raw: `{"payload":1,"payload":2}`, media: "application/json", code: api.FailureInvalidRequest},
		{name: "trailing", raw: `{"payload":1} {}`, media: "application/json", code: api.FailureInvalidRequest},
		{name: "wrong root", raw: `[{"payload":1}]`, media: "application/json", code: api.FailureInvalidRequest},
		{name: "invalid UTF-8", raw: "{\"payload\":\"\xff\"}", media: "application/json", code: api.FailureInvalidRequest},
		{name: "NUL", raw: `{"payload":"\u0000"}`, media: "application/json", code: api.FailureInvalidRequest},
		{name: "missing", raw: `{}`, media: "application/json", violations: []string{"payload/required"}},
		{name: "null", raw: `{"payload":null}`, media: "application/json", violations: []string{"payload/null"}},
		{name: "unknown", raw: `{"payload":1,"z":2}`, media: "application/json", violations: []string{"z/unknown"}},
		{name: "media", raw: `{"payload":1}`, media: "text/plain", code: api.FailureUnsupportedMedia},
		{name: "read failure", raw: `{"payload":1}`, media: "application/json", readerFailure: true, code: api.FailureBodyRead},
		{name: "pre-cancelled", raw: `{"payload":1}`, media: "application/json", cancelBefore: true, code: api.FailureBodyRead},
		{name: "read cancellation", raw: `{"payload":1}`, media: "application/json", cancelRead: true, code: api.FailureBodyRead},
		{name: "invalid mode before IO", raw: `{"payload":1}`, media: "application/json", invalidMode: true, code: api.FailureInvalidConfig},
		{name: "length before IO", raw: `{"payload":1}`, media: "application/json", oversizeLength: true, code: api.FailureBodyTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			body := require[input.Body[row]](t)(input.New(spec, api.ParserConfig{MaxBodyBytes: 64, JSONLimits: serializers.Limits{MaxDepth: 4, MaxValues: 8}},
				input.Field("payload", input.JSON(), func(r *row, v input.Presence[jsonvalue.Value]) { calls++; r.Payload = v })))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if test.cancelBefore {
				cancel()
			}
			reader := &borrowedBody{reader: strings.NewReader(test.raw)}
			if test.readerFailure {
				reader.failure = privateError
			}
			if test.cancelRead {
				reader.cancel = cancel
			}
			var value row
			var diagnostics validation.Errors
			var failure error
			var borrowed *web.Request
			mode := serializers.ModeFull
			if test.invalidMode {
				mode = 0
			}
			configured := require[settings.Settings](t)(settings.New(settings.Definition{ProjectName: "input_test", InstalledApps: []apps.Config{{Name: "example.test/input", Label: "test"}}}))
			application := require[*web.Application](t)(web.NewApplication(web.Config{Settings: configured, Routes: []web.Route{{Name: "test:body", Method: http.MethodPost, Path: "/body/", Handler: func(request *web.Request) (web.Response, error) {
				borrowed = request
				value, diagnostics, failure = body.Parse(request, mode)
				return web.NewResponse(http.StatusNoContent, nil, nil)
			}}}}))
			request := httptest.NewRequest(http.MethodPost, "http://example.test/body/", nil).WithContext(ctx)
			request.Body, request.ContentLength = reader, -1
			if test.oversizeLength {
				request.ContentLength = 65
			}
			request.Header.Set("Content-Type", test.media)
			recorder := httptest.NewRecorder()
			application.ServeHTTP(recorder, request)
			if recorder.Code != 204 || borrowed == nil {
				t.Fatal("handler did not execute", recorder.Code)
			}
			if reader.closes != 0 {
				t.Fatal("binder closed a borrowed body")
			}
			if test.code != "" || len(test.violations) > 0 {
				if !reflect.DeepEqual(value, row{}) || calls != 0 {
					t.Fatal("failed request assigned or published a DTO", value, calls)
				}
			} else {
				payload, present := value.Payload.Get()
				if failure != nil || !diagnostics.Empty() || calls != 1 || !present || !payload.Valid() {
					t.Fatal("accepted request failed", value, diagnostics, failure)
				}
			}
			if test.code != "" {
				if !errors.Is(failure, &api.Error{Code: test.code}) || !diagnostics.Empty() {
					t.Fatal("parser classification", failure, diagnostics)
				}
			} else if failure != nil || !slices.Equal(codes(diagnostics), test.violations) {
				t.Fatal("field classification", failure, diagnostics)
			}
			if test.cancelBefore || test.cancelRead {
				if !errors.Is(failure, context.Canceled) {
					t.Fatal("cancellation lost", failure)
				}
			}
			if test.readerFailure {
				if !errors.Is(failure, privateError) || strings.Contains(failure.Error(), "private") {
					t.Fatal("reader cause or outer secrecy", failure)
				}
			}
			if test.code == api.FailureBodyRead || test.code == api.FailureInvalidConfig {
				if _, handled, err := api.RequestErrorResponse(failure); handled || err != nil {
					t.Fatal("internal failure mapped to client input", handled, err)
				}
			}
			if test.cancelBefore || test.invalidMode || test.oversizeLength || test.code == api.FailureUnsupportedMedia {
				if reader.reads != 0 {
					t.Fatal("invalid request consumed body", reader.reads)
				}
			}
			if _, _, err := body.Parse(borrowed, serializers.ModeFull); !errors.Is(err, &api.Error{Code: api.FailureInvalidRequest}) {
				t.Fatal("expired borrowed request accepted", err)
			}
		})
	}
}
