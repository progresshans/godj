package web_test

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/web"
)

func TestRouteDescriptionPreservesRouterShapeAndParameterOrder(t *testing.T) {
	tests := []struct {
		path       string
		template   string
		parameters []string
	}{
		{path: "/", template: "/"},
		{path: "/reports/report.v1/", template: "/reports/report.v1/"},
		{path: "/articles/<int64:id>", template: "/articles/{id}", parameters: []string{"id"}},
		{path: "/articles/<int64:id>/revisions/<int64:revision>/", template: "/articles/{id}/revisions/{revision}/", parameters: []string{"id", "revision"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			description, err := web.DescribeRoutePath(test.path)
			if err != nil || description.Template != test.template || !reflect.DeepEqual(description.Parameters, test.parameters) {
				t.Fatalf("DescribeRoutePath() = %#v, %v", description, err)
			}
			application := newTestApplication(t, web.Config{Routes: []web.Route{{
				Name: "articles:description", Method: http.MethodGet, Path: test.path, Handler: textHandler("matched"),
			}}})
			arguments := make([]web.ReverseArgument, len(description.Parameters))
			expectedPath := description.Template
			for index, name := range description.Parameters {
				arguments[index] = web.Int64Argument(name, 0)
				expectedPath = strings.ReplaceAll(expectedPath, "{"+name+"}", "0")
			}
			path, err := application.ReverseWith("articles:description", arguments...)
			if err != nil || path != expectedPath {
				t.Fatalf("description disagrees with router reversal: path=%q want=%q err=%v", path, expectedPath, err)
			}
		})
	}
}

func TestRouteDescriptionRejectsUnroutableGrammar(t *testing.T) {
	for _, path := range []string{
		"", "articles/", "/articles/{id}/", "/articles/<uuid:id>/",
		"/<int64:id>/<int64:id>/", "/articles/prefix<int64:id>/",
		"/articles/../<int64:id>/", "/articles//<int64:id>/",
	} {
		t.Run(path, func(t *testing.T) {
			description, err := web.DescribeRoutePath(path)
			if !errors.Is(err, &web.Error{Code: web.CodeInvalidRoute}) {
				t.Fatalf("DescribeRoutePath(%q) error = %v", path, err)
			}
			if description.Template != "" || len(description.Parameters) != 0 {
				t.Fatalf("invalid route published a partial description: %#v", description)
			}
		})
	}
}

func TestRouteDescriptionParametersAreDetached(t *testing.T) {
	const path = "/articles/<int64:id>/"
	description, err := web.DescribeRoutePath(path)
	if err != nil {
		t.Fatal(err)
	}
	description.Parameters[0] = "mutated"
	again, err := web.DescribeRoutePath(path)
	if err != nil || !reflect.DeepEqual(again.Parameters, []string{"id"}) || again.Template != "/articles/{id}/" {
		t.Fatalf("caller mutation changed route description: %#v, %v", again, err)
	}
}
