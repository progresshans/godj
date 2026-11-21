package endpoint_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/endpoint"
	"github.com/progresshans/godj/api/openapi"
	"github.com/progresshans/godj/api/output"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/validation"
	"github.com/progresshans/godj/web"
)

func TestNoContentEndpointBindsAdmissionDeclarationAndPreparedOwner(t *testing.T) {
	empty := output.NoContent()
	called := 0
	config := endpoint.Config[struct{}, struct{}]{
		Route:     web.Route{Name: "test:delete", Method: http.MethodDelete, Path: "/items/"},
		Admission: endpoint.All(change), Input: endpoint.NoInput(), Output: empty,
		Success: []endpoint.Status{{Code: 204, Description: "Deleted."}},
		Handle: func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[struct{}], error) {
			called++
			return empty.Prepare(request.Context(), 204, struct{}{})
		},
	}
	authentication := bearer(t, change)
	prepared, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{prepared})
	if err != nil || len(schemas) != 0 || len(operations) != 1 || len(operations[0].Responses) != 1 {
		t.Fatal("empty endpoint schema", err)
	}
	response := operations[0].Responses[0]
	if response.Status != 204 || response.ContentType != "" || openapi.ValidateSchema(response.Schema) == nil {
		t.Fatal("empty response advertised content")
	}
	if _, err := openapi.New(openapi.Config{Title: "Empty response", Version: "1", Authentication: authentication, Operations: operations, Schemas: schemas}); err != nil {
		t.Fatal(err)
	}
	app := application(t, prepared, nil)
	if result := send(app, http.MethodDelete, "/items/", "", false); result.Code != 401 || called != 0 {
		t.Fatal("unauthenticated deletion reached the handler", result.Code, called)
	}
	if result := send(app, http.MethodDelete, "/items/", "", true); result.Code != 204 || result.Body.Len() != 0 || result.Header().Get("Content-Type") != "" || called != 1 {
		t.Fatal("empty response", result.Code, result.Body, called)
	}
	config.Handle = func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[struct{}], error) {
		return output.NoContent().Prepare(request.Context(), 204, struct{}{})
	}
	foreign, err := endpoint.New(authentication, config)
	if err != nil {
		t.Fatal(err)
	}
	var failure error
	app = application(t, foreign, func(response web.Response, err error) {
		failure = err
		if response.Status() != 0 {
			t.Error("foreign owner published a response")
		}
	})
	if result := send(app, http.MethodDelete, "/items/", "", true); result.Code != 500 || !errors.Is(failure, &api.Error{Code: api.FailureInvalidResponse}) {
		t.Fatal("no-content owner fence", result.Code, failure)
	}
	for _, status := range []int{200, 201, 205, 304, 400} {
		config.Success = []endpoint.Status{{Code: status, Description: "Invalid."}}
		if _, err := endpoint.New(authentication, config); err == nil {
			t.Fatal("no-content endpoint accepted a non-204 success", status)
		}
	}
}

func TestNoContentPublishesOnlyConfirmedCompletion(t *testing.T) {
	private := errors.New("private deletion outcome")
	for _, mode := range []string{"confirmed", "confirmed then cancellation", "cancelled", "failed", "rejected"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			empty := output.NoContent()
			value, err := endpoint.New(bearer(t, change), endpoint.Config[struct{}, struct{}]{
				Route:     web.Route{Name: "test:delete", Method: "DELETE", Path: "/items/"},
				Admission: endpoint.All(change), Input: endpoint.NoInput(), Output: empty,
				Success: []endpoint.Status{{Code: 204, Description: "Deleted."}},
				Errors:  []endpoint.Status{{Code: 404, Description: "Missing."}},
				Handle: func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[struct{}], error) {
					prepared, err := empty.Prepare(request.Context(), 204, struct{}{})
					if err != nil {
						return prepared, err
					}
					switch mode {
					case "confirmed then cancellation":
						cancel()
					case "cancelled":
						cancel()
						return prepared, private
					case "failed":
						return prepared, private
					case "rejected":
						return prepared, endpoint.Reject(404, api.CodeNotFound, validation.NewErrors())
					}
					return prepared, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var failure error
			app := application(t, value, func(response web.Response, err error) {
				failure = err
				if err != nil && response.Status() != 0 {
					t.Error("failure retained a prepared success")
				}
			})
			request := httptest.NewRequest("DELETE", "/items/", nil).WithContext(ctx)
			request.Header.Set("Authorization", "Bearer fixture")
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			want := 500
			if strings.HasPrefix(mode, "confirmed") {
				want = 204
			}
			if mode == "rejected" {
				want = 404
			}
			if response.Code != want || strings.Contains(response.Body.String(), "private") {
				t.Fatal("unconfirmed deletion published success or leaked cause", response.Code, response.Body)
			}
			if (mode == "failed" || mode == "cancelled") && !errors.Is(failure, private) || mode == "cancelled" && !errors.Is(failure, context.Canceled) {
				t.Fatal("deletion failure lost its cause", failure)
			}
		})
	}
}

func TestNoContentResourceMetadataSurvivesHTTPWithoutFraming(t *testing.T) {
	empty := output.NoContent()
	value, err := endpoint.New(bearer(t, change), endpoint.Config[struct{}, struct{}]{
		Route:     web.Route{Name: "test:delete", Method: "DELETE", Path: "/items/"},
		Admission: endpoint.All(change), Input: endpoint.NoInput(), Output: empty,
		Success: []endpoint.Status{{Code: 204, Description: "Deleted."}},
		Handle: func(request *web.Request, _ auth.Principal, _ struct{}) (output.Prepared[struct{}], error) {
			prepared, err := empty.Prepare(request.Context(), 204, struct{}{})
			if err != nil {
				return prepared, err
			}
			return prepared.WithHeaders(http.Header{"Etag": {`"revision-2"`}})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(application(t, value, nil))
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), "DELETE", server.URL+"/items/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer fixture")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 204 || len(body) != 0 || response.Header.Get("Etag") != `"revision-2"` || response.Header.Get("Content-Type") != "" || response.Header.Get("Content-Length") != "" || len(response.TransferEncoding) != 0 || len(response.Trailer) != 0 {
		t.Fatal("204 metadata/framing on the actual HTTP connection", response, err)
	}
}
