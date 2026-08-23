package godj

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const (
	authSessionLoginPath     = "/admin/login/"
	authSessionIndexPath     = "/admin/"
	authSessionProtectedPath = "/admin/articles/"
	authSessionMutationPath  = "/admin/articles/mutate/"
	authSessionLogoutPath    = "/admin/logout/"
	authSessionStatePath     = "/_conformance/auth-state/"

	authSessionAdminUsername    = "admin"
	authSessionAdminPassword    = "correct-admin-password"
	authSessionLimitedUsername  = "limited"
	authSessionLimitedPassword  = "correct-limited-password"
	authSessionInactiveUsername = "inactive"
	authSessionInactivePassword = "correct-inactive-password"
)

var authSessionClock = time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)

type authSessionScenario func(context.Context, string) (protocol.Observation, error)

var authSessionScenarioRegistry = map[string]authSessionScenario{
	"django.auth_session.anonymous_request":            authSessionAnonymousRequest,
	"django.auth_session.valid_login_rotation":         authSessionValidLoginRotation,
	"django.auth_session.rejected_login":               authSessionRejectedLogin,
	"django.auth_session.logout_flush":                 authSessionLogoutFlush,
	"django.auth_session.cookie_policy":                authSessionCookiePolicy,
	"django.auth_session.permission_and_safe_next":     authSessionPermissionAndSafeNext,
	"django.auth_session.csrf_rejection":               authSessionCSRFRejection,
	"django.auth_session.csrf_acceptance_and_rotation": authSessionCSRFAcceptanceAndRotation,
}

// authSessionScenarioHandler is the GDJ-0043 auth/session registry boundary.
// Every handler executes product packages through web.Application; expected
// oracle and deviation artifacts are deliberately absent from this adapter.
func authSessionScenarioHandler(scenario string) (scenarioHandler, bool) {
	run, ok := authSessionScenarioRegistry[scenario]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
		return run(ctx, contract.ID)
	}, true
}

func authSessionAnonymousRequest(ctx context.Context, contractID string) (protocol.Observation, error) {
	fixture, err := newAuthSessionFixture(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	client := fixture.newClient()
	writesBefore := fixture.store.Writes()
	authenticated, permission, err := fixture.authState(ctx, client)
	if err != nil {
		return protocol.Observation{}, err
	}
	response, err := client.do(ctx, http.MethodGet, authSessionIndexPath, nil, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"authenticated": protocol.Boolean(authenticated),
		"permission":    protocol.Boolean(permission),
		"redirect":      protocol.String(authSessionRedirectCategory(response)),
		"status":        authSessionInteger(response.status),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"session_writes": authSessionInteger64(fixture.store.Writes() - writesBefore),
	})
	return authSessionObservation(contractID, protocol.PhaseEvaluation, result, nil, metrics)
}

func authSessionValidLoginRotation(ctx context.Context, contractID string) (protocol.Observation, error) {
	fixture, err := newAuthSessionFixture(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	oldRecord, err := fixture.manager.Create(ctx, map[string]string{"fixture_marker": "preserved"})
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("create pre-login session: %w", err)
	}
	client := fixture.newClient()
	client.setCookie(sessionauth.DefaultSessionCookieName, oldRecord.ID().Encoded())
	rowsBefore := fixture.store.Rows()
	oldCookie := client.cookieValue(sessionauth.DefaultSessionCookieName)
	loginResponse, err := fixture.login(
		ctx,
		client,
		authSessionAdminUsername,
		authSessionAdminPassword,
		authSessionIndexPath,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	newCookie := client.cookieValue(sessionauth.DefaultSessionCookieName)
	authenticated, _, err := fixture.authState(ctx, client)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, oldFound, err := fixture.store.Load(ctx, oldRecord.ID())
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("load old session: %w", err)
	}
	newID, err := sessions.ParseID(newCookie)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("parse rotated session cookie: %w", err)
	}
	newRecord, newFound, err := fixture.store.Load(ctx, newID)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("load rotated session: %w", err)
	}
	marker, markerFound := newRecord.Value("fixture_marker")
	result := protocol.Object(map[string]protocol.Value{
		"authenticated":       protocol.Boolean(authenticated),
		"local_redirect":      protocol.String(authSessionRedirectCategory(loginResponse)),
		"old_session_removed": protocol.Boolean(!oldFound),
		"rotation":            protocol.Boolean(oldCookie != "" && newCookie != "" && oldCookie != newCookie),
		"session_survives":    protocol.Boolean(newFound && markerFound && marker == "preserved"),
		"status":              authSessionInteger(loginResponse.status),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"session_rows_after":  authSessionInteger(fixture.store.Rows()),
		"session_rows_before": authSessionInteger(rowsBefore),
	})
	return authSessionObservation(contractID, protocol.PhaseCommit, result, nil, metrics)
}

func authSessionRejectedLogin(ctx context.Context, contractID string) (protocol.Observation, error) {
	fixture, err := newAuthSessionFixture(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	writesBefore := fixture.store.Writes()
	tests := []struct {
		name     string
		username string
		password string
	}{
		{name: "invalid", username: authSessionAdminUsername, password: "wrong-password"},
		{name: "inactive", username: authSessionInactiveUsername, password: authSessionInactivePassword},
	}
	cases := make([]protocol.Value, 0, len(tests))
	for _, test := range tests {
		client := fixture.newClient()
		response, err := fixture.login(ctx, client, test.username, test.password, authSessionIndexPath)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("%s login: %w", test.name, err)
		}
		authenticated, _, err := fixture.authState(ctx, client)
		if err != nil {
			return protocol.Observation{}, fmt.Errorf("%s auth state: %w", test.name, err)
		}
		cases = append(cases, protocol.Object(map[string]protocol.Value{
			"authenticated": protocol.Boolean(authenticated),
			"case":          protocol.String(test.name),
			"redirect":      protocol.String(authSessionRedirectCategory(response)),
			"status":        authSessionInteger(response.status),
		}))
	}
	result := protocol.Object(map[string]protocol.Value{"cases": protocol.List(cases...)})
	metrics := protocol.Object(map[string]protocol.Value{
		"auth_state_writes": authSessionInteger64(fixture.store.Writes() - writesBefore),
	})
	return authSessionObservation(contractID, protocol.PhaseEvaluation, result, nil, metrics)
}

func authSessionLogoutFlush(ctx context.Context, contractID string) (observation protocol.Observation, err error) {
	fixture, err := newArticleAdminFixture(ctx, contractID)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	client := newArticleAdminHTTPClient(fixture.application)
	if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/"); err != nil {
		return protocol.Observation{}, err
	}
	oldSessionCookie := client.cookies[sessionauth.DefaultSessionCookieName]
	if oldSessionCookie == nil {
		return protocol.Observation{}, fmt.Errorf("Article Admin login published no session cookie")
	}
	oldCookie := oldSessionCookie.Value
	oldID, err := sessions.ParseID(oldCookie)
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("parse logged-in session: %w", err)
	}
	index, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/", nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	token, err := articleAdminCSRFToken(index.body)
	if err != nil {
		return protocol.Observation{}, err
	}
	logoutResponse, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/logout/", url.Values{
		"csrfmiddlewaretoken": {token},
	})
	if err != nil {
		return protocol.Observation{}, err
	}
	_, oldFound, err := fixture.sessions.Load(ctx, oldID)
	if err != nil {
		return protocol.Observation{}, err
	}
	authState, err := client.do(ctx, http.MethodGet, articleAdminAuthStatePath, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	if authState.status != http.StatusOK {
		return protocol.Observation{}, fmt.Errorf("Article Admin auth-state status = %d, want %d", authState.status, http.StatusOK)
	}
	subsequentAuthenticated, err := strconv.ParseBool(strings.TrimSpace(authState.body))
	if err != nil {
		return protocol.Observation{}, fmt.Errorf("parse Article Admin auth state: %w", err)
	}
	subsequent, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/", nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"old_session_removed":      protocol.Boolean(!oldFound),
		"redirect":                 protocol.String(articleAdminLocationCategory(logoutResponse)),
		"subsequent_authenticated": protocol.Boolean(subsequentAuthenticated),
		"subsequent_redirect":      protocol.String(articleAdminLocationCategory(subsequent)),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"session_rows_after_logout": authSessionInteger(fixture.sessions.Rows()),
	})
	return authSessionObservation(contractID, protocol.PhaseCommit, result, nil, metrics)
}

func authSessionCookiePolicy(ctx context.Context, contractID string) (observation protocol.Observation, err error) {
	fixture, err := newAuthSessionSiteFixture(ctx, contractID)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()

	client := newAuthSessionSiteClient(fixture.application)
	loginResponse, _, _, err := authSessionSiteLogin(ctx, client, articleAdminStaffUsername, authSessionIndexPath)
	if err != nil {
		return protocol.Observation{}, err
	}
	loginCookie, err := authSessionNamedCookie(loginResponse.cookies, sessionauth.DefaultSessionCookieName)
	if err != nil {
		return protocol.Observation{}, err
	}
	token, err := authSessionSiteCSRFToken(ctx, client, authSessionIndexPath)
	if err != nil {
		return protocol.Observation{}, err
	}
	logoutResponse, err := client.do(ctx, http.MethodPost, authSessionLogoutPath, url.Values{
		"csrfmiddlewaretoken": {token},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	if logoutResponse.status != http.StatusFound || authSessionRedirectCategory(logoutResponse) != "admin_login" {
		return protocol.Observation{}, fmt.Errorf("Article Admin logout status/redirect = %d/%s, want 302/admin_login", logoutResponse.status, authSessionRedirectCategory(logoutResponse))
	}
	deleteCookie, err := authSessionNamedCookie(logoutResponse.cookies, sessionauth.DefaultSessionCookieName)
	if err != nil {
		return protocol.Observation{}, err
	}
	result := protocol.Object(map[string]protocol.Value{
		"delete":                  authSessionCookieValue(deleteCookie),
		"delete_semantics":        protocol.Boolean(authSessionDeletesCookie(deleteCookie)),
		"login":                   authSessionCookieValue(loginCookie),
		"session_cookie_category": protocol.String("configured_session_cookie"),
	})
	secretCount, err := authSessionSecretOccurrences(
		[]protocol.Value{result},
		authSessionCookieSecrets(loginResponse.cookies, logoutResponse.cookies)...,
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"cookie_values_serialized": authSessionInteger64(secretCount),
	})
	return authSessionObservation(contractID, protocol.PhaseEvaluation, result, nil, metrics)
}

func authSessionPermissionAndSafeNext(ctx context.Context, contractID string) (protocol.Observation, error) {
	fixture, err := newAuthSessionFixture(ctx)
	if err != nil {
		return protocol.Observation{}, err
	}
	anonResponse, err := fixture.newClient().do(ctx, http.MethodGet, authSessionIndexPath, nil, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	limitedClient := fixture.newClient()
	if _, err := fixture.login(
		ctx,
		limitedClient,
		authSessionLimitedUsername,
		authSessionLimitedPassword,
		authSessionIndexPath,
	); err != nil {
		return protocol.Observation{}, err
	}
	deniedResponse, err := limitedClient.do(ctx, http.MethodGet, authSessionIndexPath, nil, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	unsafeClient := fixture.newClient()
	unsafeResponse, err := fixture.login(
		ctx,
		unsafeClient,
		authSessionAdminUsername,
		authSessionAdminPassword,
		"https://attacker.example/steal",
	)
	if err != nil {
		return protocol.Observation{}, err
	}
	externalRedirects := int64(0)
	for _, response := range []authSessionHTTPResponse{anonResponse, deniedResponse, unsafeResponse} {
		if authSessionExternalRedirect(response) {
			externalRedirects++
		}
	}
	result := protocol.Object(map[string]protocol.Value{
		"anonymous": protocol.Object(map[string]protocol.Value{
			"redirect": protocol.String(authSessionRedirectCategory(anonResponse)),
			"status":   authSessionInteger(anonResponse.status),
		}),
		"authenticated_without_permission": protocol.Object(map[string]protocol.Value{
			"status": authSessionInteger(deniedResponse.status),
		}),
		"unsafe_next": protocol.Object(map[string]protocol.Value{
			"external": protocol.Boolean(authSessionExternalRedirect(unsafeResponse)),
			"redirect": protocol.String(authSessionRedirectCategory(unsafeResponse)),
			"status":   authSessionInteger(unsafeResponse.status),
		}),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"external_redirects": authSessionInteger64(externalRedirects),
	})
	return authSessionObservation(contractID, protocol.PhaseEvaluation, result, nil, metrics)
}

func authSessionCSRFRejection(ctx context.Context, contractID string) (observation protocol.Observation, err error) {
	fixture, err := newAuthSessionSiteFixture(ctx, contractID)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()

	client := newAuthSessionSiteClient(fixture.application)
	if _, _, _, err := authSessionSiteLogin(ctx, client, articleAdminStaffUsername, authSessionIndexPath); err != nil {
		return protocol.Observation{}, err
	}
	before, err := articleAdminRows(ctx, fixture.raw)
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.reset()
	validArticle := url.Values{
		"title":     {"Rejected"},
		"published": {"on"},
		"summary":   {"Summary"},
	}
	missing, err := client.do(ctx, http.MethodPost, authSessionProtectedPath+"add/", validArticle, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	wrongArticle := cloneAuthSessionValues(validArticle)
	wrongArticle.Set("csrfmiddlewaretoken", "wrong-token")
	wrong, err := client.do(ctx, http.MethodPost, authSessionProtectedPath+"add/", wrongArticle, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	after, err := articleAdminRows(ctx, fixture.raw)
	if err != nil {
		return protocol.Observation{}, err
	}
	counts := fixture.observed.snapshot()
	rejectedCount := boolInteger(missing.status == http.StatusForbidden) + boolInteger(wrong.status == http.StatusForbidden)
	result := protocol.Object(map[string]protocol.Value{
		"missing_status": authSessionInteger(missing.status),
		"mutation_zero":  protocol.Boolean(reflect.DeepEqual(before, after) && counts.writes() == 0),
		"wrong_status":   authSessionInteger(wrong.status),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"after":  articleList(after),
		"before": articleList(before),
	})
	metrics := protocol.Object(map[string]protocol.Value{
		"accepted_writes":   authSessionInteger(counts.writes()),
		"rejected_requests": authSessionInteger(rejectedCount),
	})
	return authSessionObservation(contractID, protocol.PhaseEvaluation, result, &dbState, metrics)
}

func authSessionCSRFAcceptanceAndRotation(ctx context.Context, contractID string) (observation protocol.Observation, err error) {
	fixture, err := newAuthSessionSiteFixture(ctx, contractID)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()

	client := newAuthSessionSiteClient(fixture.application)
	if _, _, _, err := authSessionSiteLogin(ctx, client, articleAdminStaffUsername, authSessionIndexPath); err != nil {
		return protocol.Observation{}, err
	}
	formToken, err := authSessionSiteCSRFToken(ctx, client, authSessionProtectedPath+"add/")
	if err != nil {
		return protocol.Observation{}, err
	}
	fixture.observed.reset()
	formResponse, err := client.do(ctx, http.MethodPost, authSessionProtectedPath+"add/", url.Values{
		"csrfmiddlewaretoken": {formToken},
		"title":               {"Form accepted"},
		"published":           {""},
		"summary":             {"Form"},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	headerToken, err := authSessionSiteCSRFToken(ctx, client, authSessionProtectedPath+"change/?id=1")
	if err != nil {
		return protocol.Observation{}, err
	}
	header := make(http.Header)
	header.Set(sessionauth.DefaultCSRFHeader, headerToken)
	headerResponse, err := client.do(ctx, http.MethodPost, authSessionProtectedPath+"change/?id=1", url.Values{
		"title":     {"Header accepted"},
		"published": {"on"},
		"summary":   {"Header"},
	}, header)
	if err != nil {
		return protocol.Observation{}, err
	}

	replayClient := newAuthSessionSiteClient(fixture.application)
	loginResponse, preLoginToken, preLoginCookie, err := authSessionSiteLogin(ctx, replayClient, articleAdminStaffUsername, authSessionIndexPath)
	if err != nil {
		return protocol.Observation{}, err
	}
	postLoginCookie := replayClient.cookieValue(sessionauth.DefaultCSRFCookieName)
	beforeReplay, err := articleAdminRows(ctx, fixture.raw)
	if err != nil {
		return protocol.Observation{}, err
	}
	writesBeforeReplay := fixture.observed.snapshot().writes()
	replayResponse, err := replayClient.do(ctx, http.MethodPost, authSessionProtectedPath+"add/", url.Values{
		"csrfmiddlewaretoken": {preLoginToken},
		"title":               {"Replay rejected"},
		"published":           {""},
		"summary":             {"Replay"},
	}, nil)
	if err != nil {
		return protocol.Observation{}, err
	}
	afterReplay, err := articleAdminRows(ctx, fixture.raw)
	if err != nil {
		return protocol.Observation{}, err
	}
	writesAfterReplay := fixture.observed.snapshot().writes()
	result := protocol.Object(map[string]protocol.Value{
		"form_status":             authSessionInteger(formResponse.status),
		"header_status":           authSessionInteger(headerResponse.status),
		"login_rotated_csrf":      protocol.Boolean(preLoginCookie != "" && postLoginCookie != "" && preLoginCookie != postLoginCookie),
		"pre_login_replay_status": authSessionInteger(replayResponse.status),
		"replay_mutation_zero":    protocol.Boolean(reflect.DeepEqual(beforeReplay, afterReplay) && writesBeforeReplay == writesAfterReplay),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"articles": articleList(afterReplay),
	})
	secrets := []string{
		formToken,
		headerToken,
		preLoginToken,
		preLoginCookie,
		postLoginCookie,
		articleAdminPassword,
	}
	secrets = append(secrets, client.secretValues()...)
	secrets = append(secrets, replayClient.secretValues()...)
	secrets = append(secrets, authSessionCookieSecrets(loginResponse.cookies)...)
	secretCount, err := authSessionSecretOccurrences([]protocol.Value{result, dbState}, secrets...)
	if err != nil {
		return protocol.Observation{}, err
	}
	metrics := protocol.Object(map[string]protocol.Value{
		"accepted_writes":          authSessionInteger(fixture.observed.snapshot().writes()),
		"rejected_replays":         authSessionInteger(boolInteger(replayResponse.status == http.StatusForbidden)),
		"secret_values_serialized": authSessionInteger64(secretCount),
	})
	return authSessionObservation(contractID, protocol.PhaseCommit, result, &dbState, metrics)
}

type authSessionFixture struct {
	runtime          *sessionauth.Runtime
	manager          *sessions.Manager
	store            *authSessionCountingStore
	application      *web.Application
	viewPermission   auth.Permission
	changePermission auth.Permission

	articlesMu       sync.Mutex
	articles         []authSessionArticle
	acceptedWrites   atomic.Int64
	rejectedRequests atomic.Int64
}

func newAuthSessionFixture(ctx context.Context) (*authSessionFixture, error) {
	if ctx == nil {
		return nil, fmt.Errorf("auth/session fixture: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	memory, err := sessions.NewMemoryStore(64)
	if err != nil {
		return nil, err
	}
	store := newAuthSessionCountingStore(memory)
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: 24 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return authSessionClock },
		Random:           rand.Reader,
	})
	if err != nil {
		return nil, err
	}
	accessPermission, err := auth.NewPermission("admin.site.access")
	if err != nil {
		return nil, err
	}
	viewPermission, err := auth.NewPermission("article.article.view")
	if err != nil {
		return nil, err
	}
	changePermission, err := auth.NewPermission("article.article.change")
	if err != nil {
		return nil, err
	}
	admin, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "admin-principal",
		Active:      true,
		Permissions: []auth.Permission{accessPermission, viewPermission, changePermission},
	})
	if err != nil {
		return nil, err
	}
	limited, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "limited-principal",
		Active:      true,
		Permissions: []auth.Permission{accessPermission},
	})
	if err != nil {
		return nil, err
	}
	inactive, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:          "inactive-principal",
		Active:      false,
		Permissions: []auth.Permission{accessPermission, viewPermission, changePermission},
	})
	if err != nil {
		return nil, err
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000, Random: rand.Reader})
	if err != nil {
		return nil, err
	}
	credentialInputs := []struct {
		username  string
		password  string
		principal auth.Principal
	}{
		{username: authSessionAdminUsername, password: authSessionAdminPassword, principal: admin},
		{username: authSessionLimitedUsername, password: authSessionLimitedPassword, principal: limited},
		{username: authSessionInactiveUsername, password: authSessionInactivePassword, principal: inactive},
	}
	credentials := make([]auth.Credential, 0, len(credentialInputs))
	for _, input := range credentialInputs {
		encoded, err := hasher.Hash(ctx, input.password)
		if err != nil {
			return nil, err
		}
		credential, err := auth.NewCredential(input.username, encoded, input.principal)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	if err != nil {
		return nil, err
	}
	runtime, err := sessionauth.New(sessionauth.Config{
		Sessions:      manager,
		Authenticator: authenticator,
		Authorizer:    auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{
			Path:          "/",
			AllowInsecure: true,
			SameSite:      http.SameSiteLaxMode,
			Lifetime:      2 * time.Hour,
		},
		CSRFCookie: sessionauth.CookieConfig{
			Path:          "/",
			AllowInsecure: true,
			SameSite:      http.SameSiteLaxMode,
		},
		LoginPath:    authSessionLoginPath,
		FallbackPath: authSessionIndexPath,
		AllowedNextPaths: []string{
			authSessionIndexPath,
			authSessionProtectedPath,
			authSessionMutationPath,
		},
		Random: rand.Reader,
		Clock:  func() time.Time { return authSessionClock },
	})
	if err != nil {
		return nil, err
	}
	configured, err := settings.New(settings.Definition{
		ProjectName: "gdj0043_auth_session_conformance",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/conformance/runners/godj",
			Label: "admin",
		}},
	})
	if err != nil {
		return nil, err
	}
	fixture := &authSessionFixture{
		runtime:          runtime,
		manager:          manager,
		store:            store,
		viewPermission:   viewPermission,
		changePermission: changePermission,
	}
	application, err := web.NewApplication(web.Config{
		Settings: configured,
		Routes: []web.Route{
			{Name: "admin:auth-session-login-get", Method: http.MethodGet, Path: authSessionLoginPath, Handler: fixture.loginGet},
			{Name: "admin:auth-session-login-post", Method: http.MethodPost, Path: authSessionLoginPath, Handler: fixture.loginPost(accessPermission)},
			{Name: "admin:auth-session-index", Method: http.MethodGet, Path: authSessionIndexPath, Handler: fixture.protectedCSRFPage()},
			{Name: "admin:auth-session-protected", Method: http.MethodGet, Path: authSessionProtectedPath, Handler: fixture.protectedCSRFPage()},
			{Name: "admin:auth-session-mutation", Method: http.MethodPost, Path: authSessionMutationPath, Handler: fixture.mutationHandler()},
			{Name: "admin:auth-session-logout", Method: http.MethodPost, Path: authSessionLogoutPath, Handler: fixture.logoutPost},
			{Name: "admin:auth-session-state", Method: http.MethodGet, Path: authSessionStatePath, Handler: fixture.authStateHandler()},
		},
	})
	if err != nil {
		return nil, err
	}
	fixture.application = application
	return fixture, nil
}

// newAuthSessionSiteFixture keeps the AUT lane on the same deterministic
// cookie/session profile while routing requests through the public admin.Site
// handlers and the real Article typed service/SQLite backend. The generic
// sessionauth-only fixture remains useful for AUT-001..003/006, whose contract
// is the lower Runtime boundary rather than the integrated Admin mutation path.
func newAuthSessionSiteFixture(ctx context.Context, contractID string) (*articleAdminFixture, error) {
	fixture, err := newArticleAdminFixture(ctx, contractID)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*articleAdminFixture, error) {
		return nil, errors.Join(cause, fixture.close())
	}

	configured, err := settings.New(settings.Definition{
		ProjectName: "gdj0043_auth_session_site_conformance",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: "godj_conformance",
		}},
	})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site settings: %w", err))
	}
	memory, err := sessions.NewMemoryStore(64)
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site store: %w", err))
	}
	store := newAuthSessionCountingStore(memory)
	manager, err := sessions.NewManager(store, sessions.Config{
		AbsoluteLifetime: 24 * time.Hour,
		IdleTimeout:      30 * time.Minute,
		Clock:            func() time.Time { return authSessionClock },
		Random:           rand.Reader,
	})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site manager: %w", err))
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID:     "staff",
		Active: true,
		Permissions: []auth.Permission{
			admin.DefaultAccessPermission,
			adminapp.ArticleViewPermission,
			adminapp.ArticleAddPermission,
			adminapp.ArticleChangePermission,
			adminapp.ArticleDeletePermission,
		},
	})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site principal: %w", err))
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000, Random: rand.Reader})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site password hasher: %w", err))
	}
	encoded, err := hasher.Hash(ctx, articleAdminPassword)
	if err != nil {
		return fail(fmt.Errorf("hash auth/session Site credential: %w", err))
	}
	credential, err := auth.NewCredential(articleAdminStaffUsername, encoded, principal)
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site credential: %w", err))
	}
	authenticator, err := auth.NewMemoryAuthenticator([]auth.Credential{credential}, hasher)
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site authenticator: %w", err))
	}
	allowedNext, err := admin.SiteAllowedNextPaths(fixture.registry, articleAdminBasePath)
	if err != nil {
		return fail(fmt.Errorf("derive auth/session Site next paths: %w", err))
	}
	runtime, err := sessionauth.New(sessionauth.Config{
		Sessions:      manager,
		Authenticator: authenticator,
		Authorizer:    auth.PrincipalAuthorizer{},
		SessionCookie: sessionauth.CookieConfig{
			Path:          "/",
			AllowInsecure: true,
			SameSite:      http.SameSiteLaxMode,
			Lifetime:      2 * time.Hour,
		},
		CSRFCookie: sessionauth.CookieConfig{
			Path:          "/",
			AllowInsecure: true,
			SameSite:      http.SameSiteLaxMode,
		},
		LoginPath:        authSessionLoginPath,
		FallbackPath:     authSessionIndexPath,
		AllowedNextPaths: allowedNext,
		Random:           rand.Reader,
		Clock:            func() time.Time { return authSessionClock },
	})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site runtime: %w", err))
	}
	site, err := admin.NewSite(admin.SiteConfig{
		Apps:      configured.Apps(),
		Namespace: "godj_conformance",
		BasePath:  articleAdminBasePath,
		Registry:  fixture.registry,
		Auth:      runtime,
		PageSize:  2,
	})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site: %w", err))
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: site.Routes()})
	if err != nil {
		return fail(fmt.Errorf("create auth/session Site application: %w", err))
	}
	fixture.sessions = store
	fixture.application = application
	return fixture, nil
}

func newAuthSessionSiteClient(application *web.Application) *authSessionClient {
	return &authSessionClient{application: application, cookies: make(map[string]*http.Cookie)}
}

func authSessionSiteLogin(
	ctx context.Context,
	client *authSessionClient,
	username, next string,
) (authSessionHTTPResponse, string, string, error) {
	loginPage, err := client.do(ctx, http.MethodGet, authSessionLoginPath+"?next="+url.QueryEscape(next), nil, nil)
	if err != nil {
		return authSessionHTTPResponse{}, "", "", err
	}
	if loginPage.status != http.StatusOK {
		return authSessionHTTPResponse{}, "", "", fmt.Errorf("Article Admin login GET status = %d, want 200", loginPage.status)
	}
	token, err := articleAdminCSRFToken(loginPage.body)
	if err != nil {
		return authSessionHTTPResponse{}, "", "", err
	}
	preLoginCookie := client.cookieValue(sessionauth.DefaultCSRFCookieName)
	if preLoginCookie == "" {
		return authSessionHTTPResponse{}, "", "", fmt.Errorf("Article Admin login GET published no CSRF cookie")
	}
	response, err := client.do(ctx, http.MethodPost, authSessionLoginPath, url.Values{
		"csrfmiddlewaretoken": {token},
		"next":                {next},
		"password":            {articleAdminPassword},
		"username":            {username},
	}, nil)
	if err != nil {
		return authSessionHTTPResponse{}, "", "", err
	}
	if response.status != http.StatusFound || response.header.Get("Location") != next {
		return authSessionHTTPResponse{}, "", "", fmt.Errorf("Article Admin login POST status/redirect = %d/%s, want 302/%s", response.status, authSessionRedirectCategory(response), next)
	}
	return response, token, preLoginCookie, nil
}

func authSessionSiteCSRFToken(ctx context.Context, client *authSessionClient, path string) (string, error) {
	response, err := client.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("Article Admin CSRF page %s status = %d, want 200", path, response.status)
	}
	return articleAdminCSRFToken(response.body)
}

func cloneAuthSessionValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for name, entries := range values {
		cloned[name] = append([]string(nil), entries...)
	}
	return cloned
}

func (fixture *authSessionFixture) loginGet(request *web.Request) (web.Response, error) {
	if err := fixture.runtime.VerifyCSRF(request, nil); err != nil {
		return web.Response{}, err
	}
	token, err := fixture.runtime.CSRFToken(request)
	if err != nil {
		return web.Response{}, err
	}
	response, err := authSessionResponse(http.StatusOK, "", token.Value())
	if err != nil {
		return web.Response{}, err
	}
	return token.Apply(response)
}

func (fixture *authSessionFixture) loginPost(required auth.Permission) web.Handler {
	return func(request *web.Request) (web.Response, error) {
		raw := request.HTTP()
		if raw == nil {
			return web.Response{}, fmt.Errorf("login request is outside its borrowed lifetime")
		}
		if err := raw.ParseForm(); err != nil {
			return authSessionResponse(http.StatusBadRequest, "", "Bad Request\n")
		}
		if err := fixture.runtime.VerifyCSRF(request, raw.PostForm["csrfmiddlewaretoken"]); err != nil {
			if authSessionCSRFRejected(err) {
				return authSessionResponse(http.StatusForbidden, "", "Forbidden\n")
			}
			return web.Response{}, err
		}
		result, err := fixture.runtime.LoginAuthorized(
			request,
			raw.PostForm.Get("username"),
			raw.PostForm.Get("password"),
			required,
		)
		if errors.Is(err, auth.ErrInvalidCredentials) {
			return authSessionResponse(http.StatusOK, "", "Invalid credentials\n")
		}
		if err != nil {
			return web.Response{}, err
		}
		response, err := authSessionResponse(http.StatusFound, fixture.runtime.SafeNext(raw.PostForm.Get("next")), "Found\n")
		if err != nil {
			return web.Response{}, err
		}
		return result.Apply(response)
	}
}

func (fixture *authSessionFixture) protectedCSRFPage() web.Handler {
	return fixture.runtime.Require(fixture.viewPermission, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		token, err := fixture.runtime.CSRFToken(request)
		if err != nil {
			return web.Response{}, err
		}
		response, err := authSessionResponse(http.StatusOK, "", token.Value())
		if err != nil {
			return web.Response{}, err
		}
		return token.Apply(response)
	})
}

func (fixture *authSessionFixture) authStateHandler() web.Handler {
	return fixture.runtime.Optional(func(_ *web.Request, principal auth.Principal) (web.Response, error) {
		body := strconv.FormatBool(principal.Authenticated()) + "," + strconv.FormatBool(principal.Has(fixture.viewPermission))
		return authSessionResponse(http.StatusOK, "", body)
	})
}

func (fixture *authSessionFixture) mutationHandler() web.Handler {
	authorized := fixture.runtime.Require(fixture.changePermission, func(request *web.Request, _ auth.Principal) (web.Response, error) {
		raw := request.HTTP()
		if raw == nil {
			return web.Response{}, fmt.Errorf("mutation request is outside its borrowed lifetime")
		}
		fixture.articlesMu.Lock()
		switch raw.PostForm.Get("operation") {
		case "add_form":
			if len(fixture.articles) != 0 {
				fixture.articlesMu.Unlock()
				return authSessionResponse(http.StatusConflict, "", "Conflict\n")
			}
			fixture.articles = append(fixture.articles, authSessionArticle{
				ID:        1,
				Title:     "Form accepted",
				Published: false,
				Summary:   "Form",
			})
		case "update_header":
			if len(fixture.articles) != 1 {
				fixture.articlesMu.Unlock()
				return authSessionResponse(http.StatusNotFound, "", "Not Found\n")
			}
			fixture.articles[0].Title = "Header accepted"
			fixture.articles[0].Published = true
			fixture.articles[0].Summary = "Header"
		default:
			fixture.articlesMu.Unlock()
			return authSessionResponse(http.StatusBadRequest, "", "Bad Request\n")
		}
		fixture.articlesMu.Unlock()
		fixture.acceptedWrites.Add(1)
		return authSessionResponse(http.StatusFound, authSessionProtectedPath, "Found\n")
	})
	return func(request *web.Request) (web.Response, error) {
		raw := request.HTTP()
		if raw == nil {
			return web.Response{}, fmt.Errorf("mutation request is outside its borrowed lifetime")
		}
		if err := raw.ParseForm(); err != nil {
			return authSessionResponse(http.StatusBadRequest, "", "Bad Request\n")
		}
		if err := fixture.runtime.VerifyCSRF(request, raw.PostForm["csrfmiddlewaretoken"]); err != nil {
			if authSessionCSRFRejected(err) {
				fixture.rejectedRequests.Add(1)
				return authSessionResponse(http.StatusForbidden, "", "Forbidden\n")
			}
			return web.Response{}, err
		}
		return authorized(request)
	}
}

func (fixture *authSessionFixture) logoutPost(request *web.Request) (web.Response, error) {
	raw := request.HTTP()
	if raw == nil {
		return web.Response{}, fmt.Errorf("logout request is outside its borrowed lifetime")
	}
	if err := raw.ParseForm(); err != nil {
		return authSessionResponse(http.StatusBadRequest, "", "Bad Request\n")
	}
	if err := fixture.runtime.VerifyCSRF(request, raw.PostForm["csrfmiddlewaretoken"]); err != nil {
		if authSessionCSRFRejected(err) {
			return authSessionResponse(http.StatusForbidden, "", "Forbidden\n")
		}
		return web.Response{}, err
	}
	change, err := fixture.runtime.Logout(request)
	if err != nil {
		return web.Response{}, err
	}
	response, err := authSessionResponse(http.StatusOK, "", "Logged out\n")
	if err != nil {
		return web.Response{}, err
	}
	return change.Apply(response)
}

func (fixture *authSessionFixture) login(
	ctx context.Context,
	client *authSessionClient,
	username, password, next string,
) (authSessionHTTPResponse, error) {
	token, err := fixture.csrfToken(ctx, client, authSessionLoginPath)
	if err != nil {
		return authSessionHTTPResponse{}, err
	}
	return fixture.loginWithToken(ctx, client, token, username, password, next)
}

func (fixture *authSessionFixture) loginWithToken(
	ctx context.Context,
	client *authSessionClient,
	token, username, password, next string,
) (authSessionHTTPResponse, error) {
	return client.do(ctx, http.MethodPost, authSessionLoginPath, url.Values{
		"csrfmiddlewaretoken": {token},
		"next":                {next},
		"password":            {password},
		"username":            {username},
	}, nil)
}

func (fixture *authSessionFixture) csrfToken(
	ctx context.Context,
	client *authSessionClient,
	path string,
) (string, error) {
	response, err := client.do(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK {
		return "", fmt.Errorf("GET %s status = %d, want 200", path, response.status)
	}
	token := strings.TrimSpace(response.body)
	if token == "" {
		return "", fmt.Errorf("GET %s returned an empty CSRF token", path)
	}
	return token, nil
}

func (fixture *authSessionFixture) authState(
	ctx context.Context,
	client *authSessionClient,
) (bool, bool, error) {
	response, err := client.do(ctx, http.MethodGet, authSessionStatePath, nil, nil)
	if err != nil {
		return false, false, err
	}
	if response.status != http.StatusOK {
		return false, false, fmt.Errorf("auth-state status = %d, want 200", response.status)
	}
	parts := strings.Split(strings.TrimSpace(response.body), ",")
	if len(parts) != 2 {
		return false, false, fmt.Errorf("auth-state response is malformed")
	}
	authenticated, err := strconv.ParseBool(parts[0])
	if err != nil {
		return false, false, fmt.Errorf("parse authenticated state: %w", err)
	}
	permission, err := strconv.ParseBool(parts[1])
	if err != nil {
		return false, false, fmt.Errorf("parse permission state: %w", err)
	}
	return authenticated, permission, nil
}

func (fixture *authSessionFixture) newClient() *authSessionClient {
	return &authSessionClient{application: fixture.application, cookies: make(map[string]*http.Cookie)}
}

func (fixture *authSessionFixture) articlesSnapshot() []authSessionArticle {
	fixture.articlesMu.Lock()
	defer fixture.articlesMu.Unlock()
	return append([]authSessionArticle(nil), fixture.articles...)
}

type authSessionArticle struct {
	ID        int64
	Title     string
	Published bool
	Summary   string
}

func authSessionArticleList(articles []authSessionArticle) protocol.Value {
	values := make([]protocol.Value, len(articles))
	for index, article := range articles {
		values[index] = protocol.Object(map[string]protocol.Value{
			"id":        protocol.PrimaryKey(protocol.Integer(strconv.FormatInt(article.ID, 10))),
			"published": protocol.Boolean(article.Published),
			"summary":   protocol.String(article.Summary),
			"title":     protocol.String(article.Title),
		})
	}
	return protocol.List(values...)
}

type authSessionHTTPResponse struct {
	status  int
	header  http.Header
	body    string
	cookies []*http.Cookie
}

type authSessionClient struct {
	application *web.Application
	cookies     map[string]*http.Cookie
}

func (client *authSessionClient) do(
	ctx context.Context,
	method, path string,
	form url.Values,
	header http.Header,
) (authSessionHTTPResponse, error) {
	if client == nil || client.application == nil {
		return authSessionHTTPResponse{}, fmt.Errorf("auth/session client is nil")
	}
	if ctx == nil {
		return authSessionHTTPResponse{}, fmt.Errorf("auth/session request context is nil")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://conformance.test"+path, body)
	if err != nil {
		return authSessionHTTPResponse{}, err
	}
	if header != nil {
		request.Header = header.Clone()
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	names := make([]string, 0, len(client.cookies))
	for name := range client.cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cookie := client.cookies[name]
		if cookie != nil {
			request.AddCookie(cookie)
		}
	}
	recorder := httptest.NewRecorder()
	client.application.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	responseCookies := response.Cookies()
	for _, cookie := range responseCookies {
		if cookie == nil {
			continue
		}
		if cookie.MaxAge < 0 || cookie.Value == "" && !cookie.Expires.IsZero() && cookie.Expires.Before(authSessionClock) {
			delete(client.cookies, cookie.Name)
			continue
		}
		clone := *cookie
		client.cookies[cookie.Name] = &clone
	}
	return authSessionHTTPResponse{
		status:  response.StatusCode,
		header:  response.Header.Clone(),
		body:    recorder.Body.String(),
		cookies: responseCookies,
	}, nil
}

func (client *authSessionClient) setCookie(name, value string) {
	client.cookies[name] = &http.Cookie{Name: name, Value: value, Path: "/"}
}

func (client *authSessionClient) cookieValue(name string) string {
	if client == nil || client.cookies[name] == nil {
		return ""
	}
	return client.cookies[name].Value
}

func (client *authSessionClient) secretValues() []string {
	if client == nil {
		return nil
	}
	names := make([]string, 0, len(client.cookies))
	for name := range client.cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, 0, len(names))
	for _, name := range names {
		if value := client.cookieValue(name); value != "" {
			values = append(values, value)
		}
	}
	return values
}

type authSessionCountingStore struct {
	sessions.Store
	mu     sync.Mutex
	rows   map[sessions.ID]struct{}
	writes atomic.Int64
}

func newAuthSessionCountingStore(store sessions.Store) *authSessionCountingStore {
	return &authSessionCountingStore{Store: store, rows: make(map[sessions.ID]struct{})}
}

func (store *authSessionCountingStore) Create(ctx context.Context, record sessions.Record) (bool, error) {
	created, err := store.Store.Create(ctx, record)
	if err == nil && created {
		store.mu.Lock()
		store.rows[record.ID()] = struct{}{}
		store.mu.Unlock()
		store.writes.Add(1)
	}
	return created, err
}

func (store *authSessionCountingStore) Touch(
	ctx context.Context,
	id sessions.ID,
	accessedAt, idleExpiresAt time.Time,
) (sessions.Record, bool, error) {
	record, found, err := store.Store.Touch(ctx, id, accessedAt, idleExpiresAt)
	if err == nil && found {
		store.writes.Add(1)
	}
	return record, found, err
}

func (store *authSessionCountingStore) Rotate(
	ctx context.Context,
	oldID sessions.ID,
	replacement sessions.Record,
) (bool, error) {
	rotated, err := store.Store.Rotate(ctx, oldID, replacement)
	if err == nil && rotated {
		store.mu.Lock()
		delete(store.rows, oldID)
		store.rows[replacement.ID()] = struct{}{}
		store.mu.Unlock()
		store.writes.Add(1)
	}
	return rotated, err
}

func (store *authSessionCountingStore) Delete(ctx context.Context, id sessions.ID) error {
	err := store.Store.Delete(ctx, id)
	if err == nil {
		store.mu.Lock()
		_, existed := store.rows[id]
		delete(store.rows, id)
		store.mu.Unlock()
		if existed {
			store.writes.Add(1)
		}
	}
	return err
}

func (store *authSessionCountingStore) Rows() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.rows)
}

func (store *authSessionCountingStore) Writes() int64 { return store.writes.Load() }

func authSessionObservation(
	contractID string,
	phase protocol.Phase,
	result protocol.Value,
	dbState *protocol.Value,
	metrics protocol.Value,
) (protocol.Observation, error) {
	if contractID == "" {
		return protocol.Observation{}, fmt.Errorf("auth/session contract ID is empty")
	}
	if err := result.Validate(); err != nil {
		return protocol.Observation{}, fmt.Errorf("auth/session result: %w", err)
	}
	if dbState != nil {
		if err := dbState.Validate(); err != nil {
			return protocol.Observation{}, fmt.Errorf("auth/session db state: %w", err)
		}
	}
	if err := metrics.Validate(); err != nil {
		return protocol.Observation{}, fmt.Errorf("auth/session metrics: %w", err)
	}
	return protocol.Observation{
		ID:      contractID,
		Status:  protocol.StatusObserved,
		Phase:   phase,
		Result:  valuePointer(result),
		DBState: dbState,
		Metrics: valuePointer(metrics),
	}, nil
}

func authSessionResponse(status int, location, body string) (web.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "text/plain; charset=utf-8")
	if location != "" {
		header.Set("Location", location)
	}
	return web.NewResponse(status, header, []byte(body))
}

func authSessionCSRFRejected(err error) bool {
	return errors.Is(err, &sessionauth.Error{Code: sessionauth.CodeCSRFRejected})
}

func authSessionRedirectCategory(response authSessionHTTPResponse) string {
	location := response.header.Get("Location")
	if location == "" {
		return "none"
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "external"
	}
	switch parsed.Path {
	case authSessionIndexPath:
		return "admin_index"
	case authSessionLoginPath:
		if strings.HasPrefix(parsed.Query().Get("next"), "/") {
			return "admin_login_local_next"
		}
		return "admin_login"
	case authSessionProtectedPath:
		return "article_list"
	default:
		return "other_local"
	}
}

func authSessionExternalRedirect(response authSessionHTTPResponse) bool {
	location := response.header.Get("Location")
	if location == "" {
		return false
	}
	parsed, err := url.Parse(location)
	return err != nil || parsed.IsAbs() || parsed.Host != ""
}

func authSessionNamedCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("response contains duplicate %s cookies", name)
		}
		clone := *cookie
		found = &clone
	}
	if found == nil {
		return nil, fmt.Errorf("response is missing %s cookie", name)
	}
	return found, nil
}

func authSessionCookieValue(cookie *http.Cookie) protocol.Value {
	maxAge := protocol.Null()
	if cookie.MaxAge < 0 {
		// net/http represents an immediate deletion with a negative value but
		// serializes it as Max-Age=0. Compare the public wire policy rather than
		// leaking Go's in-memory sentinel into the cross-product protocol.
		maxAge = authSessionInteger(0)
	} else if cookie.MaxAge > 0 {
		maxAge = authSessionInteger(cookie.MaxAge)
	}
	return protocol.Object(map[string]protocol.Value{
		"expires_present": protocol.Boolean(!cookie.Expires.IsZero()),
		"http_only":       protocol.Boolean(cookie.HttpOnly),
		"max_age":         maxAge,
		"path":            protocol.String(cookie.Path),
		"same_site":       protocol.String(authSessionSameSite(cookie.SameSite)),
		"secure":          protocol.Boolean(cookie.Secure),
	})
}

func authSessionSameSite(sameSite http.SameSite) string {
	switch sameSite {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return "Default"
	}
}

func authSessionDeletesCookie(cookie *http.Cookie) bool {
	return cookie != nil && cookie.Value == "" &&
		(cookie.MaxAge < 0 || !cookie.Expires.IsZero() && cookie.Expires.Before(authSessionClock))
}

func authSessionCookieSecrets(groups ...[]*http.Cookie) []string {
	values := make([]string, 0)
	for _, cookies := range groups {
		for _, cookie := range cookies {
			if cookie != nil && cookie.Value != "" {
				values = append(values, cookie.Value)
			}
		}
	}
	return values
}

func authSessionSecretOccurrences(values []protocol.Value, secrets ...string) (int64, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return 0, fmt.Errorf("marshal normalized auth/session values: %w", err)
	}
	count := int64(0)
	text := string(encoded)
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, duplicate := seen[secret]; duplicate {
			continue
		}
		seen[secret] = struct{}{}
		count += int64(strings.Count(text, secret))
	}
	return count, nil
}

func authSessionInteger(value int) protocol.Value {
	return protocol.Integer(strconv.Itoa(value))
}

func authSessionInteger64(value int64) protocol.Value {
	return protocol.Integer(strconv.FormatInt(value, 10))
}
