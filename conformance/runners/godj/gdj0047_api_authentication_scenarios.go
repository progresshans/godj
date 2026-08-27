package godj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/web"
)

type gdj0047ScenarioRegistration struct {
	id      string
	phase   protocol.Phase
	handler scenarioHandler
}

var gdj0047ScenarioRegistry = map[string]gdj0047ScenarioRegistration{
	"godj.api_authentication.common_authentication_boundary": {
		id: "AUT-009", phase: protocol.PhaseConstruction, handler: gdj0047CommonAuthenticationBoundary,
	},
	"godj.api_authentication.bounded_bearer_header": {
		id: "AUT-010", phase: protocol.PhaseEvaluation, handler: gdj0047BoundedBearerHeader,
	},
	"drf.api_authentication.missing_and_unsupported": {
		id: "AUT-011", phase: protocol.PhaseEvaluation, handler: gdj0047MissingAndUnsupported,
	},
	"drf.api_authentication.invalid_and_valid_token": {
		id: "AUT-012", phase: protocol.PhaseEvaluation, handler: gdj0047InvalidAndValidToken,
	},
	"drf.api_authentication.permission_denial": {
		id: "AUT-013", phase: protocol.PhaseEvaluation, handler: gdj0047PermissionDenial,
	},
	"drf.api_authentication.unsafe_without_csrf": {
		id: "AUT-014", phase: protocol.PhaseCommit, handler: gdj0047UnsafeWithoutCSRF,
	},
	"drf.api_authentication.profile_isolation": {
		id: "AUT-015", phase: protocol.PhaseEvaluation, handler: gdj0047ProfileIsolation,
	},
	"godj.api_authentication.secret_and_failure_boundary": {
		id: "AUT-016", phase: protocol.PhaseEvaluation, handler: gdj0047SecretAndFailureBoundary,
	},
	"godj.api_authentication.article_route_reuse": {
		id: "API-011", phase: protocol.PhaseCommit, handler: gdj0047ArticleRouteReuse,
	},
	"godj.api_authentication.denial_mutation_boundary": {
		id: "API-012", phase: protocol.PhaseEvaluation, handler: gdj0047DenialMutationBoundary,
	},
}

func gdj0047APIScenarioHandler(scenario string) (scenarioHandler, bool) {
	registration, ok := gdj0047ScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		if contract.ID != registration.id {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 scenario %q contract id %q; want %q", scenario, contract.ID, registration.id)
		}
		if contract.Phase != registration.phase {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 scenario %q phase %q; want %q", scenario, contract.Phase, registration.phase)
		}
		return registration.handler(ctx, contract)
	}, true
}

func gdj0047CommonAuthenticationBoundary(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	principal, err := gdj0047Principal("gdj0047-construction", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"construction": {principal: principal},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()

	if len(fixture.sessionAdapter.Routes()) == 0 || len(fixture.bearerAdapter.Routes()) == 0 {
		return protocol.Observation{}, errors.New("GDJ-0047 explicit authentication profile published no Article routes")
	}
	var typedNil *bearerauth.Runtime
	var typedNilAuthentication api.Authentication = typedNil
	typedNilApplication, typedNilErr := apiapp.New(fixture.observed, typedNilAuthentication)
	if typedNilErr == nil || typedNilApplication != nil {
		return protocol.Observation{}, errors.New("GDJ-0047 typed-nil authentication was published")
	}
	nilHandler, nilHandlerErr := fixture.bearerRuntime.Require(articleapp.ArticleViewPermission, nil)
	if nilHandlerErr == nil || nilHandler != nil {
		return protocol.Observation{}, errors.New("GDJ-0047 nil authenticated handler was published")
	}
	partial := &gdj0047PartialAuthentication{failAt: 3, principal: principal}
	partialApplication, partialErr := apiapp.New(fixture.observed, partial)
	if partialErr == nil || partialApplication != nil || partial.calls != partial.failAt {
		return protocol.Observation{}, errors.New("GDJ-0047 partial wrapper failure did not fail atomically")
	}

	cases := protocol.List(
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("session_profile"), "construction": protocol.String("accepted"),
			"principal_argument": protocol.String("explicit"),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("bearer_profile"), "construction": protocol.String("accepted"),
			"principal_argument": protocol.String("explicit"),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("typed_nil_authentication"), "construction": protocol.String("invalid_configuration"),
			"routes_published": parameterRoutingInt(0),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("nil_authenticated_handler"), "construction": protocol.String("invalid_handler"),
			"routes_published": parameterRoutingInt(0),
		}),
		protocol.Object(map[string]protocol.Value{
			"case": protocol.String("partial_wrapper_failure"), "construction": protocol.String("failed_atomically"),
			"routes_published": parameterRoutingInt(0),
		}),
	)
	observation = gdj0047Observation(contract, protocol.Object(map[string]protocol.Value{
		"cases":             cases,
		"contract_owner":    protocol.String("api"),
		"handler":           protocol.String("typed_principal_argument"),
		"method":            protocol.String("Require(permission, authenticated_handler) -> (web_handler, error)"),
		"profile_selection": protocol.String("construction_time_exactly_one"),
	}), nil, protocol.Object(map[string]protocol.Value{
		"compatibility_aliases":   parameterRoutingInt(0),
		"construction_cases":      parameterRoutingInt(len(cases.Items)),
		"context_principal_slots": parameterRoutingInt(0),
	}))
	return fixture.finalize(observation, gdj0047RawToken("construction"))
}

type gdj0047PartialAuthentication struct {
	failAt    int
	calls     int
	principal auth.Principal
}

func (authentication *gdj0047PartialAuthentication) Require(
	_ auth.Permission,
	handler api.AuthenticatedHandler,
) (web.Handler, error) {
	authentication.calls++
	if authentication.calls == authentication.failAt {
		return nil, errors.New("intentional GDJ-0047 wrapper construction failure")
	}
	return func(request *web.Request) (web.Response, error) {
		return handler(request, authentication.principal)
	}, nil
}

func gdj0047BoundedBearerHeader(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	principal, err := gdj0047Principal("gdj0047-grammar", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, nil, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	acceptedRecord := gdj0047VerificationRecord{principal: principal}
	unsupportedToken := gdj0047RawToken("grammar-unsupported")
	simpleToken := gdj0047RawToken("grammar-simple")
	rfcToken := gdj0047RawToken("grammar-rfc") + "-Z_~+/9=="
	duplicateToken := gdj0047RawToken("grammar-duplicate")
	interiorPaddingToken := gdj0047RawToken("grammar-interior-padding") + "=x"
	nonASCIIToken := gdj0047RawToken("grammar-non-ascii") + "é"
	maxToken := gdj0047SizedToken("grammar-max", bearerauth.MaxTokenBytes)
	overLimitToken := maxToken + "a"
	for _, encoded := range []string{simpleToken, rfcToken, maxToken} {
		fixture.verifier.addEncoded(encoded, acceptedRecord)
	}

	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "missing"},
		{name: "unsupported_scheme", header: gdj0047Authorization("Basic " + unsupportedToken)},
		{name: "one_space", header: gdj0047Authorization("Bearer " + simpleToken)},
		{name: "multiple_spaces", header: gdj0047Authorization("bEaReR   " + simpleToken)},
		{name: "rfc_alphabet", header: gdj0047Authorization("Bearer " + rfcToken)},
		{name: "duplicate_fields", header: http.Header{"Authorization": {"Bearer " + simpleToken, "Bearer " + duplicateToken}}},
		{name: "joined_fields", header: gdj0047Authorization("Bearer " + simpleToken + ", Bearer " + duplicateToken)},
		{name: "tab_separator", header: gdj0047Authorization("Bearer\t" + simpleToken)},
		{name: "empty", header: gdj0047Authorization("Bearer ")},
		{name: "interior_padding", header: gdj0047Authorization("Bearer " + interiorPaddingToken)},
		{name: "non_ascii", header: gdj0047Authorization("Bearer " + nonASCIIToken)},
		{name: "token_bytes_4096", header: gdj0047Authorization("Bearer " + maxToken)},
		{name: "token_bytes_4097", header: gdj0047Authorization("Bearer " + overLimitToken)},
	}
	client := fixture.bearerClient()
	cases := make([]protocol.Value, 0, len(tests))
	acceptedCases := 0
	preVerifierRejections := 0
	for _, test := range tests {
		before := fixture.verifier.snapshot()
		invocationBefore := fixture.bearerAuthentication.snapshot()
		response, requestErr := client.do(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{header: test.header})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		after := fixture.verifier.snapshot()
		verifierCalls := int(after.calls - before.calls)
		outcome, outcomeErr := gdj0047BearerOutcome(test.header, response, verifierCalls)
		if outcomeErr != nil {
			return protocol.Observation{}, fmt.Errorf("bounded Bearer case %s: %w", test.name, outcomeErr)
		}
		wantInvocations := int64(0)
		if outcome == "accepted" {
			wantInvocations = 1
		}
		if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), wantInvocations, principal.ID()); err != nil {
			return protocol.Observation{}, fmt.Errorf("bounded Bearer case %s: %w", test.name, err)
		}
		if outcome == "accepted" {
			acceptedCases++
		}
		if outcome == "invalid_request" {
			preVerifierRejections++
		}
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"case": protocol.String(test.name), "outcome": protocol.String(outcome),
			"verifier_calls": parameterRoutingInt(verifierCalls),
		}))
	}

	alternateRaw := gdj0047RawToken("alternate-transport")
	fixture.verifier.addEncoded(alternateRaw, acceptedRecord)
	alternateCases := []struct {
		name    string
		target  string
		body    string
		cookie  *http.Cookie
		content string
	}{
		{name: "query", target: apiapp.ListPath + "?access_token=" + alternateRaw},
		{name: "body", target: apiapp.ListPath, body: "access_token=" + alternateRaw, content: "application/x-www-form-urlencoded"},
		{name: "cookie", target: apiapp.ListPath, cookie: &http.Cookie{Name: "access_token", Value: alternateRaw, Path: "/"}},
	}
	transport := make(map[string]protocol.Value, len(alternateCases))
	for _, alternate := range alternateCases {
		probe := fixture.bearerClient(alternate.cookie)
		before := fixture.verifier.snapshot()
		invocationBefore := fixture.bearerAuthentication.snapshot()
		response, requestErr := probe.do(ctx, http.MethodGet, alternate.target, gdj0047RequestOptions{
			body: alternate.body, contentType: alternate.content,
		})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		if response.status != http.StatusUnauthorized || fixture.verifier.snapshot().calls != before.calls {
			return protocol.Observation{}, fmt.Errorf("alternate Bearer %s transport was not ignored", alternate.name)
		}
		if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), 0, ""); err != nil {
			return protocol.Observation{}, fmt.Errorf("alternate Bearer %s transport: %w", alternate.name, err)
		}
		transport[alternate.name] = protocol.String("ignored")
	}

	observation = gdj0047Observation(contract, protocol.Object(map[string]protocol.Value{
		"alternate_transports": protocol.Object(transport),
		"cases":                protocol.List(cases...),
		"token_byte_limit":     parameterRoutingInt(bearerauth.MaxTokenBytes),
	}), nil, protocol.Object(map[string]protocol.Value{
		"accepted_cases":          parameterRoutingInt(acceptedCases),
		"cases":                   parameterRoutingInt(len(cases)),
		"pre_verifier_rejections": parameterRoutingInt(preVerifierRejections),
	}))
	return fixture.finalize(
		observation,
		unsupportedToken, simpleToken, rfcToken, duplicateToken, interiorPaddingToken,
		nonASCIIToken, maxToken, overLimitToken, alternateRaw,
	)
}

func gdj0047BearerOutcome(header http.Header, response articleAPIHTTPResponse, verifierCalls int) (string, error) {
	challenge := response.header.Get("WWW-Authenticate")
	if verifierCalls == 1 && response.status == http.StatusOK {
		return "accepted", nil
	}
	if verifierCalls != 0 {
		return "", fmt.Errorf("rejected header invoked verifier %d times", verifierCalls)
	}
	if response.status == http.StatusBadRequest && challenge == `Bearer error="invalid_request"` {
		return "invalid_request", nil
	}
	if response.status == http.StatusUnauthorized && challenge == "Bearer" {
		if len(header) == 0 {
			return "missing", nil
		}
		return "unsupported", nil
	}
	return "", fmt.Errorf("unexpected response status=%d challenge=%q", response.status, challenge)
}

func gdj0047MissingAndUnsupported(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, nil, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	unsupportedBasic := gdj0047RawToken("unsupported-basic")
	unsupportedToken := gdj0047RawToken("unsupported-token")
	inputs := []struct {
		name   string
		header http.Header
	}{
		{name: "missing"},
		{name: "unsupported_basic", header: gdj0047Authorization("Basic " + unsupportedBasic)},
		{name: "unsupported_token", header: gdj0047Authorization("Token " + unsupportedToken)},
	}
	client := fixture.bearerClient()
	cases := make([]protocol.Value, 0, len(inputs))
	redirects := 0
	for _, input := range inputs {
		invocationBefore := fixture.bearerAuthentication.snapshot()
		value, response, requestErr := client.authenticated(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{header: input.header})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		if articleAPIRedirectStatus(response.status) {
			redirects++
		}
		if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), 0, ""); err != nil {
			return protocol.Observation{}, fmt.Errorf("missing/unsupported case %s: %w", input.name, err)
		}
		cases = append(cases, articleAPICaseResponse(input.name, value))
	}
	observation = gdj0047Observation(contract, protocol.List(cases...), nil, protocol.Object(map[string]protocol.Value{
		"credential_verifications": parameterRoutingInt64(fixture.verifier.snapshot().calls),
		"redirects":                parameterRoutingInt(redirects),
		"requests":                 parameterRoutingInt(len(cases)),
	}))
	return fixture.finalize(observation, unsupportedBasic, unsupportedToken)
}

func gdj0047InvalidAndValidToken(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	active, err := gdj0047Principal("gdj0047-active-token-user", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	inactive, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID: "gdj0047-inactive-token-user", Active: false, Permissions: []auth.Permission{articleapp.ArticleViewPermission},
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"active": {principal: active}, "inactive": {principal: inactive},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Visible", published: true}); err != nil {
		return protocol.Observation{}, err
	}
	inputs := []struct{ name, label string }{{"unknown", "unknown"}, {"inactive", "inactive"}, {"valid", "active"}}
	client := fixture.bearerClient()
	cases := make([]protocol.Value, 0, len(inputs))
	successful := 0
	for _, input := range inputs {
		invocationBefore := fixture.bearerAuthentication.snapshot()
		value, _, requestErr := client.authenticated(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{header: gdj0047Bearer(input.label)})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		authenticated := gdj0047ObjectBoolean(value, "authenticated")
		if authenticated {
			successful++
		}
		wantInvocations := int64(0)
		wantPrincipalID := ""
		if input.name == "valid" {
			wantInvocations = 1
			wantPrincipalID = active.ID()
		}
		if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), wantInvocations, wantPrincipalID); err != nil {
			return protocol.Observation{}, fmt.Errorf("token case %s: %w", input.name, err)
		}
		cases = append(cases, articleAPICaseResponse(input.name, value))
	}
	observation = gdj0047Observation(contract, protocol.List(cases...), nil, protocol.Object(map[string]protocol.Value{
		"credential_verifications":   parameterRoutingInt64(fixture.verifier.snapshot().calls),
		"requests":                   parameterRoutingInt(len(cases)),
		"successful_authentications": parameterRoutingInt(successful),
	}))
	return fixture.finalize(
		observation,
		gdj0047RawToken("unknown"), gdj0047RawToken("inactive"), gdj0047RawToken("active"),
	)
}

func gdj0047PermissionDenial(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	principal, err := gdj0047Principal("gdj0047-permission-denied")
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"permission-denied": {principal: principal},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Preserve"}); err != nil {
		return protocol.Observation{}, err
	}
	before, err := fixture.state(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.reset()
	invocationBefore := fixture.bearerAuthentication.snapshot()
	value, _, err := fixture.bearerClient().authenticated(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{
		header: gdj0047Bearer("permission-denied"),
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), 0, ""); err != nil {
		return protocol.Observation{}, fmt.Errorf("permission denial: %w", err)
	}
	after, err := fixture.state(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	mutations := 0
	if !reflect.DeepEqual(before, after) || fixture.observed.snapshot().writes() != 0 {
		mutations = 1
	}
	observation = gdj0047Observation(contract, value, &after, protocol.Object(map[string]protocol.Value{
		"article_mutations":      parameterRoutingInt(mutations),
		"authenticated_requests": parameterRoutingInt64(fixture.verifier.snapshot().success),
		"requests":               parameterRoutingInt(1),
	}))
	return fixture.finalize(observation, gdj0047RawToken("permission-denied"))
}

func gdj0047UnsafeWithoutCSRF(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	principal, err := gdj0047Principal(
		"gdj0047-unsafe-token-user", articleapp.ArticleAddPermission, articleapp.ArticleViewPermission,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"unsafe": {principal: principal},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	before, err := fixture.count(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	invocationBefore := fixture.bearerAuthentication.snapshot()
	value, _, err := fixture.bearerClient().authenticated(ctx, http.MethodPost, apiapp.ListPath, gdj0047RequestOptions{
		header: gdj0047Bearer("unsafe"), contentType: api.JSONContentType,
		body: `{"title":"Created without CSRF","published":true,"summary":null}`,
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), 1, principal.ID()); err != nil {
		return protocol.Observation{}, fmt.Errorf("unsafe Bearer create: %w", err)
	}
	after, err := fixture.count(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	state, err := fixture.state(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	observation = gdj0047Observation(contract, value, &state, protocol.Object(map[string]protocol.Value{
		"article_row_delta":         parameterRoutingInt64(after - before),
		"csrf_credentials_supplied": parameterRoutingInt(0),
		"requests":                  parameterRoutingInt(1),
	}))
	return fixture.finalize(observation, gdj0047RawToken("unsafe"))
}

func gdj0047ProfileIsolation(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	principal, err := gdj0047Principal(
		"gdj0047-profile-isolation", articleapp.ArticleAddPermission, articleapp.ArticleViewPermission,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"profile-valid": {principal: principal},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Preserve"}); err != nil {
		return protocol.Observation{}, err
	}
	sessionInvocationBefore := fixture.sessionAuthentication.snapshot()
	sessionProbe, err := fixture.sessionClient(fixture.sessionCookie).do(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{})
	if err != nil || sessionProbe.status != http.StatusOK {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 session credential is not valid: status=%d error=%w", sessionProbe.status, err)
	}
	if err := gdj0047RequireInvocation(sessionInvocationBefore, fixture.sessionAuthentication.snapshot(), 1, "gdj0047-session-user"); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 session credential principal: %w", err)
	}
	before, err := fixture.state(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.reset()
	client := fixture.bearerClient(fixture.sessionCookie)
	validRaw := gdj0047RawToken("profile-valid")
	inputs := []struct {
		name    string
		method  string
		target  string
		header  http.Header
		body    string
		content string
	}{
		{name: "session_cookie_only", method: http.MethodGet, target: apiapp.ListPath},
		{name: "invalid_bearer_with_session", method: http.MethodGet, target: apiapp.ListPath, header: gdj0047Bearer("profile-unknown")},
		{name: "query_token_with_session", method: http.MethodGet, target: apiapp.ListPath + "?access_token=" + validRaw},
		{name: "body_token_with_session", method: http.MethodPost, target: apiapp.ListPath,
			body: `{"access_token":"` + validRaw + `","title":"Must not create"}`, content: api.JSONContentType},
	}
	cases := make([]protocol.Value, 0, len(inputs))
	fallbackAuthentications := 0
	for _, input := range inputs {
		invocationBefore := fixture.bearerAuthentication.snapshot()
		value, _, requestErr := client.authenticated(ctx, input.method, input.target, gdj0047RequestOptions{
			header: input.header, body: input.body, contentType: input.content,
		})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		if gdj0047ObjectBoolean(value, "authenticated") {
			fallbackAuthentications++
		}
		if err := gdj0047RequireInvocation(invocationBefore, fixture.bearerAuthentication.snapshot(), 0, ""); err != nil {
			return protocol.Observation{}, fmt.Errorf("profile isolation case %s: %w", input.name, err)
		}
		cases = append(cases, articleAPICaseResponse(input.name, value))
	}
	after, err := fixture.state(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	mutations := 0
	if !reflect.DeepEqual(before, after) || fixture.observed.snapshot().writes() != 0 {
		mutations = 1
	}
	observation = gdj0047Observation(contract, protocol.List(cases...), &after, protocol.Object(map[string]protocol.Value{
		"article_mutations":        parameterRoutingInt(mutations),
		"fallback_authentications": parameterRoutingInt(fallbackAuthentications),
		"requests":                 parameterRoutingInt(len(cases)),
		"session_cookie_present":   parameterRoutingInt(1),
	}))
	return fixture.finalize(
		observation,
		gdj0047RawToken("profile-valid"), gdj0047RawToken("profile-unknown"), fixture.sessionCookie.Value,
	)
}

func gdj0047SecretAndFailureBoundary(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	viewPrincipal, err := gdj0047Principal("gdj0047-format-principal", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	authorizerPrincipal, err := gdj0047Principal("gdj0047-authorizer-principal", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	authorizerCancelPrincipal, err := gdj0047Principal("gdj0047-authorizer-cancel-principal", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	authorizerDeadlinePrincipal, err := gdj0047Principal("gdj0047-authorizer-deadline-principal", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	const verifierCause = "gdj0047-verifier-private-cause"
	const authorizerCause = "gdj0047-authorizer-private-cause"
	authorizer := &gdj0047Authorizer{causes: map[string]error{
		authorizerPrincipal.ID():         errors.New(authorizerCause),
		authorizerCancelPrincipal.ID():   context.Canceled,
		authorizerDeadlinePrincipal.ID(): context.DeadlineExceeded,
	}}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"format":              {principal: viewPrincipal},
		"infra":               {err: errors.New(verifierCause)},
		"verifier-cancel":     {err: context.Canceled},
		"verifier-deadline":   {err: context.DeadlineExceeded},
		"authorizer":          {principal: authorizerPrincipal},
		"authorizer-cancel":   {principal: authorizerCancelPrincipal},
		"authorizer-deadline": {principal: authorizerDeadlinePrincipal},
	}, authorizer)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()

	var formatted []string
	fixture.verifier.inspect = func(token bearerauth.Token) {
		encoded, encodeErr := json.Marshal(token)
		if encodeErr != nil {
			formatted = append(formatted, "json_error")
			return
		}
		formatted = []string{fmt.Sprint(token), fmt.Sprintf("%#v", token), string(encoded)}
	}
	fixture.verifier.retain.Store(true)
	client := fixture.bearerClient()
	beforeFormat := fixture.verifier.snapshot()
	beforeFormatAuthorization := fixture.authorizer.calls.Load()
	beforeFormatInvocation := fixture.bearerAuthentication.snapshot()
	formatResponse, err := client.do(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{header: gdj0047Bearer("format")})
	if err != nil || formatResponse.status != http.StatusOK || fixture.verifier.snapshot().calls-beforeFormat.calls != 1 {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 token formatting probe failed: status=%d error=%w", formatResponse.status, err)
	}
	fixture.verifier.retain.Store(false)
	if !fixture.verifier.consumeRetainedReleased() {
		return protocol.Observation{}, errors.New("GDJ-0047 retained Token remained readable after Verify returned")
	}
	if fixture.authorizer.calls.Load()-beforeFormatAuthorization != 1 {
		return protocol.Observation{}, errors.New("GDJ-0047 token formatting authorization was retried")
	}
	if err := gdj0047RequireInvocation(beforeFormatInvocation, fixture.bearerAuthentication.snapshot(), 1, viewPrincipal.ID()); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 token formatting principal: %w", err)
	}
	fixture.verifier.inspect = nil
	if len(formatted) != 3 {
		return protocol.Observation{}, errors.New("GDJ-0047 token formatting probe did not exercise all surfaces")
	}
	formatRaw := gdj0047RawToken("format")
	formatCases := make([]protocol.Value, 0, len(formatted))
	formatNames := []string{"ordinary_format", "go_format", "json_format"}
	for index, diagnostic := range formatted {
		outcome := "redacted"
		rawOccurrences := strings.Count(diagnostic, formatRaw)
		if rawOccurrences != 0 || !strings.Contains(diagnostic, "redacted") {
			outcome = "exposed"
		}
		formatCases = append(formatCases, protocol.Object(map[string]protocol.Value{
			"case": protocol.String(formatNames[index]), "outcome": protocol.String(outcome),
			"raw_occurrences": parameterRoutingInt(rawOccurrences),
		}))
	}

	type failureProbe struct {
		name, label, expectedHTTP string
		authorizerCalls           int64
	}
	failures := []failureProbe{
		{name: "invalid_credentials", label: "invalid", expectedHTTP: "invalid_token"},
		{name: "verifier_infrastructure_failure", label: "infra", expectedHTTP: "framework_error"},
		{name: "authorizer_failure", label: "authorizer", expectedHTTP: "framework_error", authorizerCalls: 1},
	}
	cases := append([]protocol.Value(nil), formatCases...)
	for index, failure := range failures {
		before := fixture.verifier.snapshot()
		beforeAuthorization := fixture.authorizer.calls.Load()
		beforeInvocation := fixture.bearerAuthentication.snapshot()
		response, requestErr := client.do(ctx, http.MethodGet, apiapp.ListPath, gdj0047RequestOptions{header: gdj0047Bearer(failure.label)})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		if fixture.verifier.snapshot().calls-before.calls != 1 {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 %s verifier was retried", failure.name)
		}
		if fixture.authorizer.calls.Load()-beforeAuthorization != failure.authorizerCalls {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 %s authorizer call delta changed", failure.name)
		}
		if err := gdj0047RequireInvocation(beforeInvocation, fixture.bearerAuthentication.snapshot(), 0, ""); err != nil {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 %s: %w", failure.name, err)
		}
		httpOutcome := gdj0047FailureHTTPOutcome(response)
		if httpOutcome != failure.expectedHTTP {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 %s outcome %q; want %q", failure.name, httpOutcome, failure.expectedHTTP)
		}
		failureCase := protocol.Object(map[string]protocol.Value{
			"case": protocol.String(failure.name), "http": protocol.String(httpOutcome), "retries": parameterRoutingInt(0),
		})
		if index == 2 {
			// Preserve the locked case order: verifier cancellation precedes the
			// ordinary authorizer failure, while the stronger context probes stay
			// internal to this observation.
			if err := gdj0047ValidateContextErrorProbe(ctx, fixture, "verifier-cancel", context.Canceled, 0); err != nil {
				return protocol.Observation{}, err
			}
			cases = append(cases, protocol.Object(map[string]protocol.Value{
				"case": protocol.String("verifier_cancellation"), "http": protocol.String("context_error"), "retries": parameterRoutingInt(0),
			}))
		}
		cases = append(cases, failureCase)
	}
	if err := gdj0047ValidateContextErrorProbe(ctx, fixture, "verifier-deadline", context.DeadlineExceeded, 0); err != nil {
		return protocol.Observation{}, err
	}
	if err := gdj0047ValidateContextErrorProbe(ctx, fixture, "authorizer-cancel", context.Canceled, 1); err != nil {
		return protocol.Observation{}, err
	}
	if err := gdj0047ValidateContextErrorProbe(ctx, fixture, "authorizer-deadline", context.DeadlineExceeded, 1); err != nil {
		return protocol.Observation{}, err
	}
	visible := fixture.artifacts.String() + fixture.logs.String()
	causeReflected := strings.Contains(visible, verifierCause) || strings.Contains(visible, authorizerCause)
	observation = gdj0047Observation(contract, protocol.Object(map[string]protocol.Value{
		"cases":                         protocol.List(cases...),
		"injected_cause_text_reflected": protocol.Boolean(causeReflected),
		"token_accessor_scope":          protocol.String("verifier_only"),
	}), nil, protocol.Object(map[string]protocol.Value{
		"automatic_retries":      parameterRoutingInt(0),
		"cases":                  parameterRoutingInt(len(cases)),
		"raw_bearer_occurrences": parameterRoutingInt(0),
	}))
	return fixture.finalize(
		observation,
		gdj0047RawToken("format"), gdj0047RawToken("invalid"), gdj0047RawToken("infra"),
		gdj0047RawToken("verifier-cancel"), gdj0047RawToken("verifier-deadline"),
		gdj0047RawToken("authorizer"), gdj0047RawToken("authorizer-cancel"),
		gdj0047RawToken("authorizer-deadline"),
	)
}

func gdj0047FailureHTTPOutcome(response articleAPIHTTPResponse) string {
	if response.status == http.StatusUnauthorized && response.header.Get("WWW-Authenticate") == `Bearer error="invalid_token"` {
		return "invalid_token"
	}
	if response.status == http.StatusInternalServerError && response.header.Get("WWW-Authenticate") == "" {
		return "framework_error"
	}
	return "unexpected"
}

func gdj0047ValidateContextErrorProbe(
	ctx context.Context,
	fixture *gdj0047APIFixture,
	label string,
	want error,
	wantAuthorizerCalls int64,
) error {
	capture := &gdj0047ErrorCapture{}
	var handlerCalls atomic.Int64
	application, err := gdj0047ProtectedErrorApplication(fixture.bearerRuntime, capture, &handlerCalls, fixture.logs)
	if err != nil {
		return err
	}
	beforeVerification := fixture.verifier.snapshot()
	beforeAuthorization := fixture.authorizer.calls.Load()
	response, err := fixture.client(application).do(ctx, http.MethodGet, "/__godj0047_error_probe__/", gdj0047RequestOptions{
		header: gdj0047Bearer(label),
	})
	if err != nil {
		return err
	}
	captured := capture.snapshot()
	if response.status != http.StatusInternalServerError || captured.calls != 1 {
		return fmt.Errorf("GDJ-0047 context probe %s response/capture = %d/%d", label, response.status, captured.calls)
	}
	if captured.err != want || !errors.Is(captured.err, want) {
		return fmt.Errorf("GDJ-0047 context probe %s error identity was not preserved", label)
	}
	if fixture.verifier.snapshot().calls-beforeVerification.calls != 1 {
		return fmt.Errorf("GDJ-0047 context probe %s verifier call count changed", label)
	}
	if fixture.authorizer.calls.Load()-beforeAuthorization != wantAuthorizerCalls {
		return fmt.Errorf("GDJ-0047 context probe %s authorizer call count changed", label)
	}
	if handlerCalls.Load() != 0 {
		return fmt.Errorf("GDJ-0047 context probe %s invoked its protected handler", label)
	}
	return nil
}

func gdj0047ArticleRouteReuse(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	principal, err := gdj0047Principal(
		"gdj0047-route-reuse", articleapp.ArticleViewPermission, articleapp.ArticleAddPermission,
		articleapp.ArticleChangePermission, articleapp.ArticleDeletePermission,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"route-reuse": {principal: principal},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	bearerRoutes := gdj0047CRUDRoutes(fixture.bearerAdapter.Routes())
	sessionRoutes := gdj0047CRUDRoutes(fixture.sessionAdapter.Routes())
	if !slices.Equal(bearerRoutes, sessionRoutes) || len(bearerRoutes) != 6 {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 Article route profiles differ: bearer=%v session=%v", bearerRoutes, sessionRoutes)
	}
	fixture.observed.reset()
	bearerAtomicBefore := fixture.bearerBackend.atomicCalls.Load()
	bearerInvocationBefore := fixture.bearerAuthentication.snapshot()
	bearerResponse, err := fixture.bearerClient().do(ctx, http.MethodPost, apiapp.ListPath, gdj0047RequestOptions{
		header: gdj0047Bearer("route-reuse"), contentType: api.JSONContentType,
		body: `{"title":"Bearer Created","published":true,"summary":null}`,
	})
	if err != nil || bearerResponse.status != http.StatusCreated {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 Bearer Article mutation: status=%d error=%w", bearerResponse.status, err)
	}
	if fixture.bearerBackend.atomicCalls.Load()-bearerAtomicBefore != 1 {
		return protocol.Observation{}, errors.New("GDJ-0047 Bearer Article create did not use exactly one Atomic call")
	}
	if err := gdj0047RequireInvocation(bearerInvocationBefore, fixture.bearerAuthentication.snapshot(), 1, principal.ID()); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 Bearer Article create principal: %w", err)
	}
	sessionClient := fixture.sessionClient(fixture.sessionCookie)
	sessionSafeAtomicBefore := fixture.sessionBackend.atomicCalls.Load()
	sessionSafeInvocationBefore := fixture.sessionAuthentication.snapshot()
	csrf, err := sessionClient.csrf(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	if fixture.sessionBackend.atomicCalls.Load() != sessionSafeAtomicBefore {
		return protocol.Observation{}, errors.New("GDJ-0047 session safe request unexpectedly opened an Atomic write")
	}
	if err := gdj0047RequireInvocation(sessionSafeInvocationBefore, fixture.sessionAuthentication.snapshot(), 1, "gdj0047-session-user"); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 session safe request principal: %w", err)
	}
	sessionAtomicBefore := fixture.sessionBackend.atomicCalls.Load()
	sessionInvocationBefore := fixture.sessionAuthentication.snapshot()
	sessionResponse, err := sessionClient.do(ctx, http.MethodPost, apiapp.ListPath, gdj0047RequestOptions{
		csrf: csrf, contentType: api.JSONContentType,
		body: `{"title":"Session Created","published":false,"summary":null}`,
	})
	if err != nil || sessionResponse.status != http.StatusCreated {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 session Article mutation: status=%d error=%w", sessionResponse.status, err)
	}
	if fixture.sessionBackend.atomicCalls.Load()-sessionAtomicBefore != 1 {
		return protocol.Observation{}, errors.New("GDJ-0047 session Article create did not use exactly one Atomic call")
	}
	if err := gdj0047RequireInvocation(sessionInvocationBefore, fixture.sessionAuthentication.snapshot(), 1, "gdj0047-session-user"); err != nil {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 session Article create principal: %w", err)
	}
	rows, err := fixture.count(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	if rows != 2 || fixture.observed.snapshot().inserts != 2 {
		return protocol.Observation{}, fmt.Errorf("GDJ-0047 shared Article repository rows/writes = %d/%d", rows, fixture.observed.snapshot().inserts)
	}
	routeValues := make([]protocol.Value, len(bearerRoutes))
	for index, route := range bearerRoutes {
		routeValues[index] = protocol.String(route)
	}
	profile := func() protocol.Value {
		return protocol.Object(map[string]protocol.Value{
			"handlers": protocol.String("shared"), "repository": protocol.String("shared"),
			"representation": protocol.String("shared"), "routes": protocol.List(routeValues...),
		})
	}
	observation = gdj0047Observation(contract, protocol.Object(map[string]protocol.Value{
		"profiles":                        protocol.Object(map[string]protocol.Value{"bearer": profile(), "session": profile()}),
		"token_format_visible_to_article": protocol.Boolean(false),
	}), valuePointer(protocol.Object(map[string]protocol.Value{
		"bearer_profile_mutation_path":  protocol.String("article_repository_transaction"),
		"profile_specific_tables":       parameterRoutingInt(0),
		"session_profile_mutation_path": protocol.String("article_repository_transaction"),
	})), protocol.Object(map[string]protocol.Value{
		"duplicated_article_handlers": parameterRoutingInt(0),
		"profile_count":               parameterRoutingInt(2),
		"shared_routes":               parameterRoutingInt(len(bearerRoutes)),
	}))
	return fixture.finalize(observation, gdj0047RawToken("route-reuse"))
}

func gdj0047CRUDRoutes(routes []web.Route) []string {
	result := make([]string, 0, 6)
	for _, route := range routes {
		if route.Method == http.MethodHead || route.Method == http.MethodOptions {
			continue
		}
		path := strings.ReplaceAll(route.Path, "<int64:id>", ":id")
		result = append(result, route.Method+" "+path)
	}
	return result
}

func gdj0047DenialMutationBoundary(ctx context.Context, contract protocol.Contract) (observation protocol.Observation, err error) {
	denied, err := gdj0047Principal("gdj0047-denied", articleapp.ArticleViewPermission)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture, err := newGDJ0047APIFixture(ctx, contract.ID, map[string]gdj0047VerificationRecord{
		"denied": {principal: denied},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	if err := fixture.seed(ctx, articleAPISeed{id: 1, title: "Preserve"}); err != nil {
		return protocol.Observation{}, err
	}
	before, err := fixture.count(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.reset()
	invocationBefore := fixture.bearerAuthentication.snapshot()
	atomicBefore := fixture.bearerBackend.atomicCalls.Load()
	payload := `{"title":"Must not create","published":true,"summary":null}`
	malformedRaw := gdj0047RawToken("malformed-denial") + "=x"
	inputs := []struct {
		name   string
		header http.Header
		cookie *http.Cookie
	}{
		{name: "missing"},
		{name: "malformed", header: gdj0047Authorization("Bearer " + malformedRaw)},
		{name: "invalid", header: gdj0047Bearer("invalid-denial")},
		{name: "permission_denied", header: gdj0047Bearer("denied")},
		{name: "session_cookie_fallback", cookie: fixture.sessionCookie},
	}
	cases := make([]protocol.Value, 0, len(inputs))
	totalMutations := 0
	for _, input := range inputs {
		client := fixture.bearerClient(input.cookie)
		writesBefore := fixture.observed.snapshot().writes()
		caseInvocationBefore := fixture.bearerAuthentication.snapshot()
		response, requestErr := client.do(ctx, http.MethodPost, apiapp.ListPath, gdj0047RequestOptions{
			header: input.header, body: payload, contentType: api.JSONContentType,
		})
		if requestErr != nil {
			return protocol.Observation{}, requestErr
		}
		mutations := fixture.observed.snapshot().writes() - writesBefore
		totalMutations += mutations
		if err := gdj0047RequireInvocation(caseInvocationBefore, fixture.bearerAuthentication.snapshot(), 0, ""); err != nil {
			return protocol.Observation{}, fmt.Errorf("GDJ-0047 denial case %s: %w", input.name, err)
		}
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"case":      protocol.String(input.name),
			"challenge": articleAPIOptionalString(response.header.Get("WWW-Authenticate")),
			"mutations": parameterRoutingInt(mutations),
			"status":    parameterRoutingInt(response.status),
		}))
	}
	after, err := fixture.count(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	changed := 0
	if after != before {
		changed = 1
	}
	handlerInvocations := fixture.bearerAuthentication.snapshot().calls - invocationBefore.calls
	if fixture.bearerBackend.atomicCalls.Load() != atomicBefore {
		return protocol.Observation{}, errors.New("GDJ-0047 rejected Bearer request opened an Atomic mutation")
	}
	observation = gdj0047Observation(contract, protocol.Object(map[string]protocol.Value{
		"cases": protocol.List(cases...), "handler_invocations": parameterRoutingInt64(handlerInvocations),
	}), valuePointer(protocol.Object(map[string]protocol.Value{
		"article_rows_after": parameterRoutingInt64(after), "article_rows_before": parameterRoutingInt64(before),
		"article_rows_changed": parameterRoutingInt(changed),
	})), protocol.Object(map[string]protocol.Value{
		"attempts": parameterRoutingInt(len(cases)), "raw_bearer_occurrences": parameterRoutingInt(0),
		"total_mutations": parameterRoutingInt(totalMutations),
	}))
	return fixture.finalize(
		observation,
		malformedRaw, gdj0047RawToken("invalid-denial"), gdj0047RawToken("denied"), fixture.sessionCookie.Value,
	)
}

func gdj0047Observation(contract protocol.Contract, result protocol.Value, state *protocol.Value, metrics protocol.Value) protocol.Observation {
	return protocol.Observation{
		ID: contract.ID, Status: protocol.StatusObserved, Phase: contract.Phase,
		Result: valuePointer(result), DBState: state, Metrics: valuePointer(metrics),
	}
}

func gdj0047ObjectBoolean(object protocol.Value, field string) bool {
	for _, named := range object.Fields {
		if named.Name == field && named.Value.Bool != nil {
			return *named.Value.Bool
		}
	}
	return false
}
