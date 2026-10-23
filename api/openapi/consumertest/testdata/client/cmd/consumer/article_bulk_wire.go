package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	ab "example.com/godj-openapi-client/articlebearer"
	as "example.com/godj-openapi-client/articlesession"
	"github.com/ogen-go/ogen/ogenerrors"
)

func checkGeneratedArticleBulkWire(ctx context.Context) error {
	const first = `{"id":9007199254740993,"title":"first","published":false,"summary":null,"slug":"legacy/slug"}`
	const second = `{"id":9223372036854775807,"title":"second","published":true,"summary":"stored","slug":null}`
	inputs := []ab.ArticleCreate{{Title: "first", Published: ab.NewOptBool(false)}, {Title: "second"}}
	inputs[0].Summary.SetToNull()
	calls := 0
	client, err := articleBulkWireClient(201, "["+first+","+second+"]", &calls)
	if err != nil {
		return err
	}
	result, err := client.GodjConformanceArticleBulkCreate(ctx, inputs)
	rows, ok := result.(*ab.GodjConformanceArticleBulkCreateCreatedApplicationJSON)
	if err != nil || !ok || calls != 1 || len(*rows) != 2 || (*rows)[0].ID != 9007199254740993 || (*rows)[1].ID != 9223372036854775807 || (*rows)[0].Slug.Value != "legacy/slug" || !(*rows)[0].Summary.Null || !(*rows)[1].Slug.Null || rows.Validate() != nil {
		return fail("generated Article bulk exact integer and nullable wire")
	}
	for _, body := range []string{`{}`, `null`, `[null]`, `[{"id":1}]`, "[" + strings.Replace(first, `"published":false`, `"published":null`, 1) + "]", "[" + strings.Replace(first, `"id":9007199254740993`, `"id":9007199254740993.0`, 1) + "]", "[" + first + "]true"} {
		calls = 0
		client, err = articleBulkWireClient(201, body, &calls)
		if err != nil {
			return err
		}
		_, err = client.GodjConformanceArticleBulkCreate(ctx, inputs)
		var decode *ogenerrors.DecodeBodyError
		if calls != 1 || !errors.As(err, &decode) {
			return fail("generated Article bulk rejected response shape")
		}
	}
	if (ab.GodjConformanceArticleBulkCreateCreatedApplicationJSON{}).Validate() == nil || make(ab.GodjConformanceArticleBulkCreateCreatedApplicationJSON, 41).Validate() == nil || (as.GodjConformanceArticleBulkCreateCreatedApplicationJSON{}).Validate() == nil || make(as.GodjConformanceArticleBulkCreateCreatedApplicationJSON, 41).Validate() == nil {
		return fail("generated Article bulk explicit cardinality validation")
	}
	calls = 0
	client, err = articleBulkWireClient(500, "Internal Server Error\n", &calls)
	if err != nil {
		return err
	}
	result, err = client.GodjConformanceArticleBulkCreate(ctx, inputs)
	if _, ok := result.(*ab.GodjConformanceArticleBulkCreateInternalServerError); err != nil || !ok || calls != 1 {
		return fail("generated Article bulk unknown outcome was retried or returned success")
	}
	return nil
}

func articleBulkWireClient(status int, body string, calls *int) (*ab.Client, error) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		*calls++
		wire, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPost || request.URL.Host != "probe.invalid" || request.URL.Path != "/api/articles/bulk/" || request.Header.Get("Authorization") != "Bearer probe-only" || len(request.Cookies()) != 0 || request.Header.Get(csrfHeaderName) != "" || string(wire) != `[{"title":"first","published":false,"summary":null},{"title":"second"}]` {
			return nil, fail("generated Article bulk typed request and authentication wire")
		}
		media := "application/json"
		if status == 500 {
			media = "text/plain; charset=utf-8"
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	return ab.NewClient("https://probe.invalid", bearerSource{token: "probe-only"}, ab.WithClient(&http.Client{Transport: transport}))
}
