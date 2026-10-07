package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/web"
)

type cancellingBody struct {
	wire          []byte
	cancel        context.CancelFunc
	readError     error
	chunk         int
	reads, closes int
}

func (body *cancellingBody) Read(buffer []byte) (int, error) {
	body.reads++
	if body.cancel != nil {
		body.cancel()
	}
	if body.chunk > 0 && len(buffer) > body.chunk {
		buffer = buffer[:body.chunk]
	}
	n := copy(buffer, body.wire)
	body.wire = body.wire[n:]
	if body.readError != nil {
		return n, body.readError
	}
	if len(body.wire) == 0 {
		return n, io.EOF
	}
	return n, nil
}
func (body *cancellingBody) Close() error { body.closes++; return nil }

func TestBodyParsersRespectCancellationAndBorrowedOwnership(t *testing.T) {
	field, err := serializers.JSONField("payload")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := serializers.NewSpec([]serializers.Field{field})
	if err != nil {
		t.Fatal(err)
	}
	parser, err := api.NewParser(api.ParserConfig{MaxBodyBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	forcedRead := errors.New("private body reader error")
	for _, form := range []string{"object", "model", "list"} {
		t.Run(form, func(t *testing.T) {
			for _, test := range []struct {
				name                     string
				before, during, deadline bool
				chunk                    int
				readError                error
				wantReads                int
			}{
				{name: "active", wantReads: 1},
				{name: "before_read", before: true},
				{name: "expired_deadline", deadline: true},
				{name: "last_read", during: true, wantReads: 1},
				{name: "partial_read", during: true, chunk: 1, wantReads: 1},
				{name: "read_error", readError: forcedRead, wantReads: 1},
				{name: "cancel_and_read_error", during: true, readError: forcedRead, wantReads: 1},
			} {
				t.Run(test.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					if test.deadline {
						cancel()
						ctx, cancel = context.WithDeadline(t.Context(), time.Unix(0, 0))
					}
					defer cancel()
					if test.before {
						cancel()
					}
					wire := `{"payload":{"key":1}}`
					if form == "list" {
						wire = "[" + wire + "]"
					}
					body := &cancellingBody{wire: []byte(wire), readError: test.readError, chunk: test.chunk}
					if test.during {
						body.cancel = cancel
					}
					var failure error
					var published bool
					app := testApplication(t, nil, []web.Route{{Name: "test:context", Method: http.MethodPost, Path: "/context/", Handler: func(request *web.Request) (web.Response, error) {
						switch form {
						case "object", "model":
							var value serializers.Object
							if form == "object" {
								value, failure = parser.ParseObject(request)
							} else {
								value, failure = parser.ParseObjectFor(request, spec)
							}
							published = value.Valid()
						default:
							var values []serializers.Object
							values, failure = parser.ParseListFor(request, spec)
							published = values != nil
						}
						return emptyOK(t), nil
					}}})
					request := httptest.NewRequest(http.MethodPost, "http://example.test/context/", nil).WithContext(ctx)
					request.Body, request.ContentLength = body, -1
					request.Header.Set("Content-Type", "application/json")
					app.ServeHTTP(httptest.NewRecorder(), request)
					if body.reads != test.wantReads || body.closes != 0 {
						t.Fatal("body read/close ownership", body.reads, body.closes)
					}
					if test.name == "active" {
						if failure != nil || !published {
							t.Fatal("active request was rejected", failure)
						}
						return
					}
					if published || !errors.Is(failure, &api.Error{Code: api.FailureBodyRead}) {
						t.Fatal("failure published a parsed result", failure, published)
					}
					if test.before || test.during {
						if !errors.Is(failure, context.Canceled) {
							t.Fatal("cancellation cause lost", failure)
						}
					}
					if test.deadline && !errors.Is(failure, context.DeadlineExceeded) {
						t.Fatal("deadline cause lost", failure)
					}
					if test.readError != nil && !errors.Is(failure, forcedRead) {
						t.Fatal("reader cause lost", failure)
					}
					if strings.Contains(failure.Error(), forcedRead.Error()) {
						t.Fatal("outer failure exposed reader detail")
					}
					if response, handled, err := api.RequestErrorResponse(failure); handled || err != nil || response.Status() != 0 {
						t.Fatal("cancellation/read failure became a client-input error", handled, err)
					}
				})
			}
		})
	}
}
