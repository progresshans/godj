package main

import (
	"context"
	"io"
	"net/http"
	"strings"

	ab "example.com/godj-openapi-client/articlebearer"
	as "example.com/godj-openapi-client/articlesession"
	hs "example.com/godj-openapi-client/helpdesksession"
)

// Synthetic wire checks cover the generated response choices and exact path.
// The actual server flows and native DB owners prove authorization and outcomes.
func checkGeneratedNoContentWire(ctx context.Context) error {
	for _, profile := range []string{"article-bearer", "article-session", "label-session"} {
		for _, status := range []int{204, 400, 500} {
			calls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				path := "/api/articles/1152921504606846977/"
				if profile == "label-session" {
					path = "/api/labels/1152921504606846977/"
				}
				if request.Method != "DELETE" || request.URL.Host != "probe.invalid" || request.URL.Path != path || request.URL.RawQuery != "" || request.ContentLength != 0 {
					return nil, fail("generated empty response request changed")
				}
				if request.Body != nil {
					body, err := io.ReadAll(request.Body)
					if err != nil || len(body) != 0 {
						return nil, fail("generated delete acquired request content")
					}
				}
				header, body := make(http.Header), ""
				if status == 400 {
					header.Set("Content-Type", "application/json")
					body = `{"code":"validation_error","errors":[{"field":"__all__","code":"protected","params":[]}]}`
				}
				if status == 500 {
					header.Set("Content-Type", "text/plain; charset=utf-8")
					body = "Internal Server Error\n"
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			httpClient := &http.Client{Transport: transport}
			var response any
			var err error
			switch profile {
			case "article-bearer":
				client, failure := ab.NewClient("https://probe.invalid", bearerSource{token: "probe-only"}, ab.WithClient(httpClient))
				if failure != nil {
					return failure
				}
				response, err = client.GodjConformanceArticleDelete(ctx, ab.GodjConformanceArticleDeleteParams{ID: 1152921504606846977})
			case "article-session":
				client, failure := as.NewClient("https://probe.invalid", noContentArticleSecurity{}, as.WithClient(httpClient))
				if failure != nil {
					return failure
				}
				response, err = client.GodjConformanceArticleDelete(ctx, as.GodjConformanceArticleDeleteParams{ID: 1152921504606846977})
			case "label-session":
				client, failure := hs.NewClient("https://probe.invalid", wireHelpdeskSecurity{}, hs.WithClient(httpClient))
				if failure != nil {
					return failure
				}
				response, err = client.HelpdeskLabelDelete(ctx, hs.HelpdeskLabelDeleteParams{ID: 1152921504606846977})
			}
			if err != nil || calls != 1 {
				return fail("generated delete failed or retried")
			}
			decoded := 0
			switch value := response.(type) {
			case *ab.GodjConformanceArticleDeleteNoContent, *as.GodjConformanceArticleDeleteNoContent, *hs.HelpdeskLabelDeleteNoContent:
				decoded = 204
			case *ab.GodjConformanceArticleDeleteBadRequest:
				if value.Response.Code == "validation_error" && len(value.Response.Errors) == 1 && value.Response.Errors[0].Code == "protected" {
					decoded = 400
				}
			case *as.GodjConformanceArticleDeleteBadRequest:
				if value.Code == "validation_error" && len(value.Errors) == 1 && value.Errors[0].Code == "protected" {
					decoded = 400
				}
			case *hs.HelpdeskLabelDeleteBadRequest:
				if value.Code == "validation_error" && len(value.Errors) == 1 && value.Errors[0].Code == "protected" {
					decoded = 400
				}
			case *ab.GodjConformanceArticleDeleteInternalServerError, *as.GodjConformanceArticleDeleteInternalServerError, *hs.HelpdeskLabelDeleteInternalServerError:
				decoded = 500
			}
			if decoded != status {
				return fail("generated delete confused success and failure")
			}
		}
	}
	return nil
}

type noContentArticleSecurity struct{}

func (noContentArticleSecurity) SessionAuth(context.Context, as.OperationName) (as.SessionAuth, error) {
	return as.SessionAuth{APIKey: "probe-session"}, nil
}
func (noContentArticleSecurity) CsrfCookie(context.Context, as.OperationName) (as.CsrfCookie, error) {
	return as.CsrfCookie{APIKey: "probe-cookie"}, nil
}
func (noContentArticleSecurity) CsrfHeader(context.Context, as.OperationName) (as.CsrfHeader, error) {
	return as.CsrfHeader{APIKey: "probe-header"}, nil
}
