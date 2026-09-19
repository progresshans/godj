package godj

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/validation"
)

// articleAPIScenarioHandler owns SQLite observations derived only from the
// public Article API and serializer surfaces. It never reads or replays an
// expected observation. Central registry publication remains explicit so the
// handler inventory and manifest classification can be audited together.
func articleAPIScenarioHandler(scenario string) (scenarioHandler, bool) {
	handlers := map[string]scenarioHandler{
		"drf.article_api.json_transport_boundary":        articleAPIJSONTransportBoundary,
		"drf.article_api.article_serializer_semantics":   articleAPISerializerSemantics,
		"drf.article_api.session_permission_csrf_denial": articleAPISessionPermissionCSRFDenial,
		"drf.article_api.list_filter_order":              articleAPIListFilterOrder,
		"drf.article_api.page_number_pagination":         articleAPIPageNumberPagination,
		"drf.article_api.create_article":                 articleAPICreateArticle,
		"drf.article_api.retrieve_article":               articleAPIRetrieveArticle,
		"drf.article_api.full_update":                    articleAPIFullUpdate,
		"drf.article_api.partial_update":                 articleAPIPartialUpdate,
		"drf.article_api.delete_article":                 articleAPIDeleteArticle,
	}
	handler, ok := handlers[scenario]
	return handler, ok
}

func articleAPIJSONTransportBoundary(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		client := fixture.client(nil)
		cases := []struct {
			name        string
			method      string
			target      string
			payload     string
			contentType string
			accept      string
		}{
			{name: "object", method: http.MethodPost, target: articleAPIEchoPath, payload: `{"value":1}`, contentType: api.JSONContentType},
			{name: "empty_body", method: http.MethodPost, target: articleAPIEchoPath, contentType: api.JSONContentType},
			{name: "duplicate_key", method: http.MethodPost, target: articleAPIEchoPath, payload: `{"value":1,"value":2}`, contentType: api.JSONContentType},
			{name: "top_level_list", method: http.MethodPost, target: articleAPIEchoPath, payload: `[]`, contentType: api.JSONContentType},
			{name: "trailing_data", method: http.MethodPost, target: articleAPIEchoPath, payload: `{}{}`, contentType: api.JSONContentType},
			{name: "top_level_scalar", method: http.MethodPost, target: articleAPIEchoPath, payload: `1`, contentType: api.JSONContentType},
			{name: "non_finite", method: http.MethodPost, target: articleAPIEchoPath, payload: `{"value":NaN}`, contentType: api.JSONContentType},
			{name: "invalid_utf8", method: http.MethodPost, target: articleAPIEchoPath, payload: "{\"value\":\"\xff\"}", contentType: api.JSONContentType},
			{name: "string_limit", method: http.MethodPost, target: articleAPIEchoPath, payload: `{"value":"` + strings.Repeat("x", 1025) + `"}`, contentType: api.JSONContentType},
			{name: "depth_limit", method: http.MethodPost, target: articleAPIEchoPath, payload: `{"value":` + strings.Repeat("[", 17) + `0` + strings.Repeat("]", 17) + `}`, contentType: api.JSONContentType},
			{name: "oversize", method: http.MethodPost, target: articleAPIEchoPath, payload: `{"value":"` + strings.Repeat("x", 4090) + `"}`, contentType: api.JSONContentType},
			{name: "form", method: http.MethodPost, target: articleAPIEchoPath, payload: `value=1`, contentType: "application/x-www-form-urlencoded"},
			{name: "unacceptable_accept", method: http.MethodPost, target: articleAPIEchoPath, payload: `{}`, contentType: api.JSONContentType, accept: "text/html"},
			{name: "empty_204", method: http.MethodGet, target: articleAPIEmptyPath},
		}
		values := make([]protocol.Value, 0, len(cases))
		for _, test := range cases {
			response, err := client.do(ctx, test.method, test.target, articleAPIRequestOptions{
				body: test.payload, contentType: test.contentType, accept: test.accept,
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			semantic, err := articleAPIResponseValue(response)
			if err != nil {
				return protocol.Observation{}, err
			}
			values = append(values, articleAPICaseResponse(test.name, semantic))
		}
		metrics := protocol.Object(map[string]protocol.Value{
			"cases":    parameterRoutingInt(len(values)),
			"requests": parameterRoutingInt(len(values)),
		})
		return articleAPIObservation(contract, protocol.List(values...), nil, metrics), nil
	})
}

func articleAPISerializerSemantics(_ context.Context, contract protocol.Contract) (protocol.Observation, error) {
	spec, err := articleAPIConformanceSpec()
	if err != nil {
		return protocol.Observation{}, err
	}
	fieldOrder := make([]protocol.Value, 0, len(spec.Fields()))
	for _, field := range spec.Fields() {
		fieldOrder = append(fieldOrder, protocol.String(field.Name()))
	}
	cases := []protocol.Value{protocol.Object(map[string]protocol.Value{
		"case": protocol.String("representation"),
		"data": protocol.Object(map[string]protocol.Value{
			"id":        parameterRoutingInt64(7),
			"published": protocol.Boolean(true),
			"summary":   protocol.Null(),
			"title":     protocol.String("Existing"),
		}),
		"field_order": protocol.List(fieldOrder...),
	})}
	inputs := []struct {
		name    string
		mode    serializers.Mode
		members []serializers.Member
	}{
		{name: "full_defaults", mode: serializers.ModeFull, members: []serializers.Member{
			serializers.MemberOf("title", serializers.String("New")),
		}},
		{name: "full_missing_title", mode: serializers.ModeFull, members: []serializers.Member{
			serializers.MemberOf("published", serializers.Boolean(true)),
		}},
		{name: "partial_omitted", mode: serializers.ModePartial, members: []serializers.Member{
			serializers.MemberOf("summary", serializers.Null()),
		}},
		{name: "partial_empty", mode: serializers.ModePartial, members: []serializers.Member{
			serializers.MemberOf("summary", serializers.String("")),
		}},
		{name: "read_only_unknown", mode: serializers.ModePartial, members: []serializers.Member{
			serializers.MemberOf("id", serializers.Integer(9)),
			serializers.MemberOf("zeta", serializers.Integer(1)),
		}},
	}
	for _, input := range inputs {
		object, err := serializers.NewObject(input.members...)
		if err != nil {
			return protocol.Observation{}, err
		}
		result, err := spec.Bind(object, input.mode)
		if err != nil {
			return protocol.Observation{}, err
		}
		errorCodes, errorOrder := articleAPISerializerErrors(result.Errors())
		validated := protocol.Null()
		if result.Valid() {
			validated, err = articleAPISerializerValues(result.Values())
			if err != nil {
				return protocol.Observation{}, err
			}
		}
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"case":        protocol.String(input.name),
			"error_codes": errorCodes,
			"error_order": errorOrder,
			"validated":   validated,
			"valid":       protocol.Boolean(result.Valid()),
		}))
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"database_operations": parameterRoutingInt(0),
		"validations":         parameterRoutingInt(len(inputs)),
	})
	return articleAPIObservation(contract, protocol.List(cases...), nil, metrics), nil
}

func articleAPISessionPermissionCSRFDenial(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing"}); err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		anonymous, err := fixture.client(nil).do(ctx, http.MethodGet, "/api/articles/", articleAPIRequestOptions{})
		if err != nil {
			return protocol.Observation{}, err
		}
		anonymousValue, err := articleAPIResponseValue(anonymous)
		if err != nil {
			return protocol.Observation{}, err
		}
		denied, err := fixture.client(fixture.deniedSession).do(ctx, http.MethodGet, "/api/articles/", articleAPIRequestOptions{})
		if err != nil {
			return protocol.Observation{}, err
		}
		deniedValue, err := articleAPIResponseValue(denied)
		if err != nil {
			return protocol.Observation{}, err
		}

		public := fixture.client(nil)
		oldToken, err := public.csrf(ctx, articleAPICSRFPath)
		if err != nil {
			return protocol.Observation{}, err
		}
		login, err := public.do(ctx, http.MethodPost, articleAPILoginPath, articleAPIRequestOptions{})
		if err != nil || login.status != http.StatusNoContent {
			return protocol.Observation{}, fmt.Errorf("Article API fixture login: status=%d error=%w", login.status, err)
		}
		freshToken, err := public.csrf(ctx, "/api/articles/")
		if err != nil {
			return protocol.Observation{}, err
		}
		attemptInputs := []struct {
			name  string
			token string
		}{
			{name: "missing"},
			{name: "wrong", token: strings.Repeat("x", 64)},
			{name: "prelogin", token: oldToken},
			{name: "fresh", token: freshToken},
		}
		attempts := make([]protocol.Value, 0, len(attemptInputs))
		for _, input := range attemptInputs {
			response, err := public.do(ctx, http.MethodPost, "/api/articles/", articleAPIRequestOptions{
				body: `{}`, contentType: api.JSONContentType, token: input.token,
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			semantic, err := articleAPIResponseValue(response)
			if err != nil {
				return protocol.Observation{}, err
			}
			attempts = append(attempts, articleAPICaseResponse(input.name, semantic))
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"anonymous":         anonymousValue,
			"permission_denied": deniedValue,
			"unsafe_attempts":   protocol.List(attempts...),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"requests":          parameterRoutingInt(2 + len(attempts)),
		})
		return articleAPIObservation(contract, result, &state, metrics), nil
	})
}

func articleAPIListFilterOrder(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := articleAPISeedList(ctx, fixture); err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		client := fixture.client(fixture.allSession)
		inputs := []struct{ name, query string }{
			{name: "combined", query: "search=go&published=true&ordering=-id"},
			{name: "invalid_published", query: "published=yes"},
			{name: "invalid_ordering", query: "ordering=title"},
			{name: "unknown", query: "extra=1"},
			{name: "duplicate", query: "search=go&search=django"},
			{name: "search_too_long", query: "search=" + strings.Repeat("x", 65)},
		}
		values, err := articleAPIGetCases(ctx, client, inputs)
		if err != nil {
			return protocol.Observation{}, err
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"requests":          parameterRoutingInt(len(values)),
		})
		return articleAPIObservation(contract, protocol.List(values...), &state, metrics), nil
	})
}

func articleAPIPageNumberPagination(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := articleAPISeedList(ctx, fixture); err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		client := fixture.client(fixture.allSession)
		inputs := []struct{ name, query string }{
			{name: "page_1", query: "page=1"},
			{name: "page_2", query: "page=2"},
			{name: "page_3", query: "page=3"},
			{name: "zero", query: "page=0"},
			{name: "text", query: "page=nope"},
			{name: "too_high", query: "page=99"},
			{name: "page_size_forbidden", query: "page_size=100"},
		}
		values, err := articleAPIGetCases(ctx, client, inputs)
		if err != nil {
			return protocol.Observation{}, err
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"page_size":         parameterRoutingInt(2),
			"requests":          parameterRoutingInt(len(values)),
		})
		return articleAPIObservation(contract, protocol.List(values...), &state, metrics), nil
	})
}

func articleAPICreateArticle(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing"}); err != nil {
			return protocol.Observation{}, err
		}
		before, err := articleAPICount(ctx, fixture)
		if err != nil {
			return protocol.Observation{}, err
		}
		client := fixture.client(fixture.allSession)
		token, err := client.csrf(ctx, "/api/articles/")
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		response, err := client.do(ctx, http.MethodPost, "/api/articles/", articleAPIRequestOptions{
			body: `{"title":"Created","published":true,"summary":null}`, contentType: api.JSONContentType, token: token,
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		semantic, err := articleAPIResponseValue(response)
		if err != nil {
			return protocol.Observation{}, err
		}
		after, err := articleAPICount(ctx, fixture)
		if err != nil {
			return protocol.Observation{}, err
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		metrics := protocol.Object(map[string]protocol.Value{
			"article_row_delta": parameterRoutingInt64(after - before),
			"requests":          parameterRoutingInt(2),
		})
		return articleAPIObservation(contract, semantic, &state, metrics), nil
	})
}

func articleAPIRetrieveArticle(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing", published: true}); err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		client := fixture.client(fixture.allSession)
		inputs := []struct{ name, target string }{
			{name: "existing", target: "/api/articles/1/"},
			{name: "zero_missing", target: "/api/articles/0/"},
			{name: "leading_zero", target: "/api/articles/01/"},
			{name: "missing", target: "/api/articles/99/"},
			{name: "overflow", target: "/api/articles/9223372036854775808/"},
		}
		values := make([]protocol.Value, 0, len(inputs))
		for _, input := range inputs {
			response, err := client.do(ctx, http.MethodGet, input.target, articleAPIRequestOptions{})
			if err != nil {
				return protocol.Observation{}, err
			}
			semantic, err := articleAPIResponseValue(response)
			if err != nil {
				return protocol.Observation{}, err
			}
			values = append(values, articleAPICaseResponse(input.name, semantic))
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"requests":          parameterRoutingInt(len(values)),
		})
		return articleAPIObservation(contract, protocol.List(values...), &state, metrics), nil
	})
}

func articleAPIFullUpdate(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing", published: true, summary: articleAPIStringPointer("old")}); err != nil {
			return protocol.Observation{}, err
		}
		client := fixture.client(fixture.allSession)
		token, err := client.csrf(ctx, "/api/articles/1/")
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		invalid, err := client.do(ctx, http.MethodPut, "/api/articles/1/", articleAPIRequestOptions{
			body: `{"published":false,"summary":null}`, contentType: api.JSONContentType, token: token,
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		afterInvalid, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		valid, err := client.do(ctx, http.MethodPut, "/api/articles/1/", articleAPIRequestOptions{
			body: `{"title":"Replaced","published":false,"summary":null}`, contentType: api.JSONContentType, token: token,
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		invalidValue, err := articleAPIResponseValue(invalid)
		if err != nil {
			return protocol.Observation{}, err
		}
		validValue, err := articleAPIResponseValue(valid)
		if err != nil {
			return protocol.Observation{}, err
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"after_invalid": afterInvalid, "invalid": invalidValue, "valid": validValue,
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"requests":          parameterRoutingInt(3),
		})
		return articleAPIObservation(contract, result, &state, metrics), nil
	})
}

func articleAPIPartialUpdate(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Existing", published: true, summary: articleAPIStringPointer("old")}); err != nil {
			return protocol.Observation{}, err
		}
		client := fixture.client(fixture.allSession)
		token, err := client.csrf(ctx, "/api/articles/1/")
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		inputs := []struct{ name, body string }{
			{name: "title_only", body: `{"title":"Patched"}`},
			{name: "summary_null", body: `{"summary":null}`},
			{name: "summary_empty", body: `{"summary":""}`},
			{name: "empty_object", body: `{}`},
		}
		values := make([]protocol.Value, 0, len(inputs))
		for _, input := range inputs {
			response, err := client.do(ctx, http.MethodPatch, "/api/articles/1/", articleAPIRequestOptions{
				body: input.body, contentType: api.JSONContentType, token: token,
			})
			if err != nil {
				return protocol.Observation{}, err
			}
			semantic, err := articleAPIResponseValue(response)
			if err != nil {
				return protocol.Observation{}, err
			}
			state, err := fixture.state(ctx)
			if err != nil {
				return protocol.Observation{}, err
			}
			values = append(values, protocol.Object(map[string]protocol.Value{
				"case": protocol.String(input.name), "response": semantic, "state": state,
			}))
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		metrics := protocol.Object(map[string]protocol.Value{
			"article_mutations": parameterRoutingInt(fixture.observed.snapshot().writes()),
			"requests":          parameterRoutingInt(5),
		})
		return articleAPIObservation(contract, protocol.List(values...), &state, metrics), nil
	})
}

func articleAPIDeleteArticle(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAPIFixture(ctx, contract, func(fixture *articleAPIFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx,
			articleAPISeed{id: 1, title: "Delete"},
			articleAPISeed{id: 2, title: "Keep", published: true, summary: articleAPIStringPointer("safe")},
		); err != nil {
			return protocol.Observation{}, err
		}
		before, err := articleAPICount(ctx, fixture)
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		allowed := fixture.client(fixture.allSession)
		allowedToken, err := allowed.csrf(ctx, "/api/articles/1/")
		if err != nil {
			return protocol.Observation{}, err
		}
		deleted, err := allowed.do(ctx, http.MethodDelete, "/api/articles/1/", articleAPIRequestOptions{token: allowedToken})
		if err != nil {
			return protocol.Observation{}, err
		}
		repeated, err := allowed.do(ctx, http.MethodDelete, "/api/articles/1/", articleAPIRequestOptions{token: allowedToken})
		if err != nil {
			return protocol.Observation{}, err
		}
		missingCSRF, err := allowed.do(ctx, http.MethodDelete, "/api/articles/2/", articleAPIRequestOptions{})
		if err != nil {
			return protocol.Observation{}, err
		}
		denied := fixture.client(fixture.viewSession)
		deniedToken, err := denied.csrf(ctx, "/api/articles/2/")
		if err != nil {
			return protocol.Observation{}, err
		}
		forbidden, err := denied.do(ctx, http.MethodDelete, "/api/articles/2/", articleAPIRequestOptions{token: deniedToken})
		if err != nil {
			return protocol.Observation{}, err
		}
		deletedValue, err := articleAPIResponseValue(deleted)
		if err != nil {
			return protocol.Observation{}, err
		}
		repeatedValue, err := articleAPIResponseValue(repeated)
		if err != nil {
			return protocol.Observation{}, err
		}
		missingValue, err := articleAPIResponseValue(missingCSRF)
		if err != nil {
			return protocol.Observation{}, err
		}
		forbiddenValue, err := articleAPIResponseValue(forbidden)
		if err != nil {
			return protocol.Observation{}, err
		}
		after, err := articleAPICount(ctx, fixture)
		if err != nil {
			return protocol.Observation{}, err
		}
		state, err := fixture.state(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"allowed":      deletedValue,
			"forbidden":    forbiddenValue,
			"missing_csrf": missingValue,
			"repeated":     repeatedValue,
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"article_row_delta": parameterRoutingInt64(after - before),
			"requests":          parameterRoutingInt(6),
		})
		return articleAPIObservation(contract, result, &state, metrics), nil
	})
}

func withArticleAPIFixture(
	ctx context.Context,
	contract protocol.Contract,
	run func(*articleAPIFixture) (protocol.Observation, error),
) (observation protocol.Observation, err error) {
	fixture, err := newArticleAPIFixture(ctx, contract.ID)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	return run(fixture)
}

func articleAPIObservation(contract protocol.Contract, result protocol.Value, state *protocol.Value, metrics protocol.Value) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: valuePointer(result), DBState: state, Metrics: valuePointer(metrics),
	}
}

func articleAPICaseResponse(name string, response protocol.Value) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"case": protocol.String(name), "response": response,
	})
}

func articleAPISeedList(ctx context.Context, fixture *articleAPIFixture) error {
	return fixture.seed(ctx,
		articleAPISeed{id: 1, title: "Go Guide", published: true},
		articleAPISeed{id: 2, title: "Django Notes", summary: articleAPIStringPointer("ORM")},
		articleAPISeed{id: 3, title: "Other", published: true, summary: articleAPIStringPointer("misc")},
		articleAPISeed{id: 4, title: "Go Deep Dive", published: true, summary: articleAPIStringPointer("API")},
		articleAPISeed{id: 5, title: "Go Draft", summary: articleAPIStringPointer("draft")},
	)
}

func articleAPIGetCases(ctx context.Context, client *articleAPIClient, inputs []struct{ name, query string }) ([]protocol.Value, error) {
	values := make([]protocol.Value, 0, len(inputs))
	for _, input := range inputs {
		response, err := client.do(ctx, http.MethodGet, "/api/articles/?"+input.query, articleAPIRequestOptions{})
		if err != nil {
			return nil, err
		}
		semantic, err := articleAPIResponseValue(response)
		if err != nil {
			return nil, err
		}
		values = append(values, articleAPICaseResponse(input.name, semantic))
	}
	return values, nil
}

func articleAPICount(ctx context.Context, fixture *articleAPIFixture) (int64, error) {
	page, err := fixture.repository.List(ctx, articleAPIListCountOptions())
	if err != nil {
		return 0, err
	}
	return page.Total, nil
}

func articleAPIListCountOptions() articleapp.ListOptions {
	return articleapp.ListOptions{Limit: 1}
}

func articleAPIConformanceSpec() (serializers.Spec, error) {
	id, err := serializers.IntegerField("id", serializers.WithReadOnly())
	if err != nil {
		return serializers.Spec{}, err
	}
	title, err := serializers.StringField("title", serializers.WithMaxLength(200))
	if err != nil {
		return serializers.Spec{}, err
	}
	published, err := serializers.BooleanField("published", serializers.WithDefault(serializers.Boolean(false)))
	if err != nil {
		return serializers.Spec{}, err
	}
	summary, err := serializers.StringField(
		"summary", serializers.WithRequired(false), serializers.WithNullable(), serializers.WithAllowEmpty(),
	)
	if err != nil {
		return serializers.Spec{}, err
	}
	return serializers.NewSpec([]serializers.Field{id, title, published, summary})
}

func articleAPISerializerErrors(errors validation.Errors) (protocol.Value, protocol.Value) {
	byField := make(map[string][]protocol.Value)
	order := make([]protocol.Value, 0)
	seen := make(map[string]struct{})
	for _, violation := range errors.All() {
		field := string(violation.Field())
		byField[field] = append(byField[field], protocol.String(string(violation.Code())))
		if _, found := seen[field]; !found {
			seen[field] = struct{}{}
			order = append(order, protocol.String(field))
		}
	}
	fields := make(map[string]protocol.Value, len(byField))
	for field, codes := range byField {
		fields[field] = protocol.List(codes...)
	}
	return protocol.Object(fields), protocol.List(order...)
}

func articleAPISerializerValues(values serializers.Values) (protocol.Value, error) {
	fields := make(map[string]protocol.Value)
	for _, entry := range values.All() {
		value, err := articleAPISerializerValue(entry.Value())
		if err != nil {
			return protocol.Value{}, err
		}
		fields[entry.Name()] = value
	}
	return protocol.Object(fields), nil
}
