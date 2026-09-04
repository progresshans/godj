package godj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/progresshans/godj/api"
	apisessionauth "github.com/progresshans/godj/api/sessionauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/apiapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

const (
	articleAPIAllUsername     = "all-user"
	articleAPIViewUsername    = "view-user"
	articleAPIDeniedUsername  = "denied-user"
	articleAPIFixturePassword = "conformance-only-password"
	articleAPIFixturePrefix   = "/__godj_conformance__/"
	articleAPILoginPath       = "/__godj_conformance__/api-login/"
	articleAPICSRFPath        = "/__godj_conformance__/api-csrf/"
	articleAPIEchoPath        = "/__godj_conformance__/echo/"
	articleAPIEmptyPath       = "/__godj_conformance__/echo/empty/"
	articleAPIPrincipalKey    = "_godj_principal_id"
)

type articleAPIWriteCounts struct {
	inserts int
	updates int
	deletes int
}

func (counts articleAPIWriteCounts) writes() int {
	return counts.inserts + counts.updates + counts.deletes
}

type articleAPIObservedBackend struct {
	raw *sqlite.Backend
	mu  sync.Mutex
	all articleAPIWriteCounts
}

func (backend *articleAPIObservedBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return backend.raw.Query(ctx, plan)
}

func (backend *articleAPIObservedBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	backend.recordInsert()
	return backend.raw.Insert(ctx, plan)
}

func (backend *articleAPIObservedBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	backend.recordUpdate()
	return backend.raw.Update(ctx, plan)
}

func (backend *articleAPIObservedBackend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	backend.recordDelete()
	return backend.raw.Delete(ctx, plan)
}

func (backend *articleAPIObservedBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	if callback == nil {
		return backend.raw.Atomic(ctx, nil)
	}
	return backend.raw.Atomic(ctx, func(session db.Session) error {
		return callback(articleAPIObservedSession{session: session, owner: backend})
	})
}

func (backend *articleAPIObservedBackend) recordInsert() {
	backend.mu.Lock()
	backend.all.inserts++
	backend.mu.Unlock()
}

func (backend *articleAPIObservedBackend) recordUpdate() {
	backend.mu.Lock()
	backend.all.updates++
	backend.mu.Unlock()
}

func (backend *articleAPIObservedBackend) recordDelete() {
	backend.mu.Lock()
	backend.all.deletes++
	backend.mu.Unlock()
}

func (backend *articleAPIObservedBackend) reset() {
	backend.mu.Lock()
	backend.all = articleAPIWriteCounts{}
	backend.mu.Unlock()
}

func (backend *articleAPIObservedBackend) snapshot() articleAPIWriteCounts {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.all
}

type articleAPIObservedSession struct {
	session db.Session
	owner   *articleAPIObservedBackend
}

func (session articleAPIObservedSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return session.session.Query(ctx, plan)
}

func (session articleAPIObservedSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.owner.recordInsert()
	return session.session.Insert(ctx, plan)
}

func (session articleAPIObservedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.owner.recordUpdate()
	return session.session.Update(ctx, plan)
}

func (session articleAPIObservedSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session.owner.recordDelete()
	return session.session.Delete(ctx, plan)
}

type articleAPIAuthenticator struct {
	principals map[string]auth.Principal
}

func (authenticator articleAPIAuthenticator) Authenticate(_ context.Context, username, password string) (auth.Principal, error) {
	if password != articleAPIFixturePassword {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	principal, ok := authenticator.principals[username]
	if !ok {
		return auth.Principal{}, auth.ErrInvalidCredentials
	}
	return principal, nil
}

func (authenticator articleAPIAuthenticator) Resolve(_ context.Context, id string) (auth.Principal, error) {
	for _, principal := range authenticator.principals {
		if principal.ID() == id {
			return principal, nil
		}
	}
	return auth.Principal{}, auth.ErrInvalidCredentials
}

type articleAPIFixture struct {
	raw           *sqlite.Backend
	observed      *articleAPIObservedBackend
	repository    articleapp.Repository
	application   *web.Application
	allSession    *http.Cookie
	viewSession   *http.Cookie
	deniedSession *http.Cookie
}

func newArticleAPIFixture(ctx context.Context, contractID string) (*articleAPIFixture, error) {
	raw, err := openEmptyArticleDatabase(ctx, contractID)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*articleAPIFixture, error) {
		return nil, errors.Join(cause, raw.Close())
	}
	observed := &articleAPIObservedBackend{raw: raw}
	repository, err := articleapp.NewRepository(observed)
	if err != nil {
		return fail(fmt.Errorf("create Article API repository: %w", err))
	}

	allPrincipal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID: "api-all", Active: true,
		Permissions: []auth.Permission{
			articleapp.ArticleViewPermission,
			articleapp.ArticleAddPermission,
			articleapp.ArticleChangePermission,
			articleapp.ArticleDeletePermission,
		},
	})
	if err != nil {
		return fail(err)
	}
	viewPrincipal, err := auth.NewPrincipal(auth.PrincipalConfig{
		ID: "api-view", Active: true, Permissions: []auth.Permission{articleapp.ArticleViewPermission},
	})
	if err != nil {
		return fail(err)
	}
	deniedPrincipal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "api-denied", Active: true})
	if err != nil {
		return fail(err)
	}
	authenticator := articleAPIAuthenticator{principals: map[string]auth.Principal{
		articleAPIAllUsername:    allPrincipal,
		articleAPIViewUsername:   viewPrincipal,
		articleAPIDeniedUsername: deniedPrincipal,
	}}
	store, err := sessions.NewMemoryStore(64)
	if err != nil {
		return fail(err)
	}
	fixedTime := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	manager, err := sessions.NewManager(store, sessions.Config{
		Clock:  func() time.Time { return fixedTime },
		Random: bytes.NewReader(articleAPIEntropy(8192, 3)),
	})
	if err != nil {
		return fail(err)
	}
	allSession, err := articleAPISessionCookie(ctx, manager, allPrincipal)
	if err != nil {
		return fail(err)
	}
	viewSession, err := articleAPISessionCookie(ctx, manager, viewPrincipal)
	if err != nil {
		return fail(err)
	}
	deniedSession, err := articleAPISessionCookie(ctx, manager, deniedPrincipal)
	if err != nil {
		return fail(err)
	}
	webRuntime, err := websessionauth.New(websessionauth.Config{
		Sessions: manager, Authenticator: authenticator, Authorizer: auth.PrincipalAuthorizer{},
		SessionCookie: websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:    websessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:     articleAPILoginPath, FallbackPath: apiapp.ListPath,
		AllowedNextPaths: []string{apiapp.ListPath},
		Clock:            func() time.Time { return fixedTime },
		Random:           bytes.NewReader(articleAPIEntropy(1<<20, 7)),
	})
	if err != nil {
		return fail(err)
	}
	apiRuntime, err := apisessionauth.New(webRuntime)
	if err != nil {
		return fail(err)
	}
	adapter, err := apiapp.New(observed, apiRuntime)
	if err != nil {
		return fail(err)
	}
	echoParser, err := api.NewParser(api.ParserConfig{
		MaxBodyBytes: 4096,
		JSONLimits: serializers.Limits{
			MaxDocumentBytes: 4096,
			MaxDepth:         16,
			MaxStringBytes:   1024,
		},
	})
	if err != nil {
		return fail(err)
	}
	routes := adapter.Routes()
	routes = append(routes,
		web.Route{
			Name: apiapp.Namespace + ":conformance-echo", Method: http.MethodPost, Path: articleAPIEchoPath,
			Handler: func(request *web.Request) (web.Response, error) {
				object, parseErr := echoParser.ParseObject(request)
				if parseErr == nil {
					return api.JSON(http.StatusOK, object.Value())
				}
				response, expected, responseErr := api.RequestErrorResponse(parseErr)
				if responseErr != nil {
					return web.Response{}, responseErr
				}
				if expected {
					return response, nil
				}
				return web.Response{}, parseErr
			},
		},
		web.Route{
			Name: apiapp.Namespace + ":conformance-empty", Method: http.MethodGet, Path: articleAPIEmptyPath,
			Handler: func(*web.Request) (web.Response, error) { return api.NoContent() },
		},
		web.Route{
			Name: apiapp.Namespace + ":conformance-csrf", Method: http.MethodGet, Path: articleAPICSRFPath,
			Handler: func(request *web.Request) (web.Response, error) {
				token, tokenErr := webRuntime.CSRFToken(request)
				if tokenErr != nil {
					return web.Response{}, tokenErr
				}
				response, responseErr := api.NoContent()
				if responseErr != nil {
					return web.Response{}, responseErr
				}
				header := response.Header()
				header.Set(webRuntime.CSRFHeader(), token.Value())
				response, responseErr = web.NewResponse(response.Status(), header, response.Body())
				if responseErr != nil {
					return web.Response{}, responseErr
				}
				return token.Apply(response)
			},
		},
		web.Route{
			Name: apiapp.Namespace + ":conformance-login", Method: http.MethodPost, Path: articleAPILoginPath,
			Handler: func(request *web.Request) (web.Response, error) {
				login, loginErr := webRuntime.Login(request, articleAPIAllUsername, articleAPIFixturePassword)
				if loginErr != nil {
					return web.Response{}, loginErr
				}
				response, responseErr := api.NoContent()
				if responseErr != nil {
					return web.Response{}, responseErr
				}
				return login.Apply(response)
			},
		},
	)
	middleware, err := apiapp.Middleware()
	if err != nil {
		return fail(err)
	}
	fixtureNegotiation, err := api.JSONNegotiation(articleAPIFixturePrefix)
	if err != nil {
		return fail(err)
	}
	fixtureRepresentation, err := api.Representation(articleAPIFixturePrefix)
	if err != nil {
		return fail(err)
	}
	middleware = append(middleware, fixtureNegotiation, fixtureRepresentation)
	configured, err := settings.New(settings.Definition{
		ProjectName: "godj_article_api_conformance",
		InstalledApps: []apps.Config{{
			Name: "github.com/progresshans/godj/examples/article/apiapp", Label: apiapp.Namespace,
		}},
	})
	if err != nil {
		return fail(err)
	}
	application, err := web.NewApplication(web.Config{Settings: configured, Routes: routes, Middleware: middleware})
	if err != nil {
		return fail(err)
	}
	return &articleAPIFixture{
		raw: raw, observed: observed, repository: repository, application: application,
		allSession: allSession, viewSession: viewSession, deniedSession: deniedSession,
	}, nil
}

func (fixture *articleAPIFixture) close() error {
	if fixture == nil || fixture.raw == nil {
		return nil
	}
	return fixture.raw.Close()
}

type articleAPISeed struct {
	id        int64
	title     string
	published bool
	summary   *string
}

func (fixture *articleAPIFixture) seed(ctx context.Context, rows ...articleAPISeed) error {
	for _, row := range rows {
		if row.id <= 0 || row.title == "" {
			return fmt.Errorf("seed Article API row: invalid row")
		}
		if _, err := fixture.raw.ExecContext(
			ctx,
			`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (?, ?, ?, ?)`,
			row.id, row.title, row.published, row.summary,
		); err != nil {
			return fmt.Errorf("seed Article API row %d: %w", row.id, err)
		}
	}
	return nil
}

func (fixture *articleAPIFixture) state(ctx context.Context) (protocol.Value, error) {
	page, err := fixture.repository.List(ctx, articleapp.ListOptions{Limit: articleapp.MaximumPageSize})
	if err != nil {
		return protocol.Value{}, err
	}
	rows := make([]protocol.Value, len(page.Articles))
	for index, article := range page.Articles {
		rows[index] = articleAPIArticleValue(article)
	}
	return protocol.List(rows...), nil
}

type articleAPIRequestOptions struct {
	body        string
	contentType string
	accept      string
	token       string
}

type articleAPIHTTPResponse struct {
	status int
	header http.Header
	body   []byte
}

type articleAPIClient struct {
	application *web.Application
	cookies     map[string]*http.Cookie
}

func (fixture *articleAPIFixture) client(session *http.Cookie) *articleAPIClient {
	client := &articleAPIClient{application: fixture.application, cookies: make(map[string]*http.Cookie)}
	if session != nil {
		clone := *session
		client.cookies[clone.Name] = &clone
	}
	return client
}

func (client *articleAPIClient) do(ctx context.Context, method, target string, options articleAPIRequestOptions) (articleAPIHTTPResponse, error) {
	request := httptest.NewRequestWithContext(ctx, method, "http://attacker.example"+target, strings.NewReader(options.body))
	accept := options.accept
	if accept == "" {
		accept = api.JSONContentType
	}
	request.Header.Set("Accept", accept)
	if options.contentType != "" {
		request.Header.Set("Content-Type", options.contentType)
	}
	if options.token != "" {
		request.Header.Set(websessionauth.DefaultCSRFHeader, options.token)
	}
	for _, cookie := range client.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	client.application.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return articleAPIHTTPResponse{}, err
	}
	for _, cookie := range response.Cookies() {
		clone := *cookie
		if clone.MaxAge < 0 {
			delete(client.cookies, clone.Name)
			continue
		}
		client.cookies[clone.Name] = &clone
	}
	return articleAPIHTTPResponse{status: response.StatusCode, header: response.Header.Clone(), body: body}, nil
}

func (client *articleAPIClient) csrf(ctx context.Context, target string) (string, error) {
	response, err := client.do(ctx, http.MethodGet, target, articleAPIRequestOptions{})
	if err != nil {
		return "", err
	}
	if response.status != http.StatusOK && response.status != http.StatusNoContent {
		return "", fmt.Errorf("seed API CSRF: status %d", response.status)
	}
	token := response.header.Get(websessionauth.DefaultCSRFHeader)
	if len(token) != 128 {
		return "", fmt.Errorf("seed API CSRF: token length %d", len(token))
	}
	if cookie := client.cookies[websessionauth.DefaultCSRFCookieName]; cookie == nil || !cookie.HttpOnly || cookie.Value == "" || cookie.Value == token {
		return "", fmt.Errorf("seed API CSRF: cookie policy mismatch")
	}
	return token, nil
}

func articleAPISessionCookie(ctx context.Context, manager *sessions.Manager, principal auth.Principal) (*http.Cookie, error) {
	record, err := manager.Create(ctx, map[string]string{articleAPIPrincipalKey: principal.ID()})
	if err != nil {
		return nil, err
	}
	return &http.Cookie{Name: websessionauth.DefaultSessionCookieName, Value: record.ID().Encoded(), Path: "/"}, nil
}

func articleAPIEntropy(size int, seed byte) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = byte((index*17+int(seed))%251) + 1
	}
	return result
}

func articleAPIStringPointer(value string) *string { return &value }

func articleAPIArticleValue(article articleapp.Article) protocol.Value {
	summary := protocol.Null()
	if article.Summary != nil {
		summary = protocol.String(*article.Summary)
	}
	return protocol.Object(map[string]protocol.Value{
		"id":        protocol.PrimaryKey(protocol.Integer(strconv.FormatInt(article.ID, 10))),
		"published": protocol.Boolean(article.Published),
		"summary":   summary,
		"title":     protocol.String(article.Title),
	})
}

func articleAPIResponseValue(response articleAPIHTTPResponse) (protocol.Value, error) {
	contentType := response.header.Get("Content-Type")
	if before, _, found := strings.Cut(contentType, ";"); found {
		contentType = before
	}
	fields := map[string]protocol.Value{
		"body_empty":       protocol.Boolean(len(response.body) == 0),
		"content_type":     articleAPIOptionalString(contentType),
		"location":         articleAPIOptionalString(response.header.Get("Location")),
		"redirect":         protocol.Boolean(articleAPIRedirectStatus(response.status)),
		"status":           parameterRoutingInt(response.status),
		"www_authenticate": protocol.Boolean(response.header.Get("WWW-Authenticate") != ""),
	}
	if response.status >= 400 {
		codes, err := articleAPIErrorCodes(response.body)
		if err != nil {
			return protocol.Value{}, err
		}
		fields["error_codes"] = codes
	} else if len(response.body) > 0 && response.status != http.StatusNoContent {
		data, err := articleAPIJSONBody(response.body)
		if err != nil {
			return protocol.Value{}, err
		}
		fields["data"] = data
	}
	return protocol.Object(fields), nil
}

func articleAPIJSONBody(body []byte) (protocol.Value, error) {
	object, err := serializers.DecodeObject(body, serializers.Limits{})
	if err != nil {
		return protocol.Value{}, fmt.Errorf("decode Article API response: %w", err)
	}
	return articleAPISerializerValue(object.Value())
}

func articleAPIErrorCodes(body []byte) (protocol.Value, error) {
	object, err := serializers.DecodeObject(body, serializers.Limits{})
	if err != nil {
		return protocol.Value{}, fmt.Errorf("decode Article API error response: %w", err)
	}
	codeValue, codeFound := object.Get("code")
	code, codeValid := codeValue.AsString()
	if !codeFound || !codeValid {
		return protocol.Value{}, fmt.Errorf("decode Article API error response: code is missing or invalid")
	}
	errorsValue, errorsFound := object.Get("errors")
	errorsList, errorsValid := errorsValue.AsList()
	if !errorsFound || !errorsValid {
		return protocol.Value{}, fmt.Errorf("decode Article API error response: errors are missing or invalid")
	}
	if len(errorsList) == 0 {
		return protocol.Object(map[string]protocol.Value{"detail": protocol.String(code)}), nil
	}
	byField := make(map[string][]protocol.Value)
	for index, value := range errorsList {
		diagnostic, ok := value.AsObject()
		if !ok {
			return protocol.Value{}, fmt.Errorf("decode Article API error response: errors[%d] is invalid", index)
		}
		fieldValue, fieldFound := diagnostic.Get("field")
		field, fieldValid := fieldValue.AsString()
		itemCodeValue, itemCodeFound := diagnostic.Get("code")
		itemCode, itemCodeValid := itemCodeValue.AsString()
		if !fieldFound || !fieldValid || !itemCodeFound || !itemCodeValid {
			return protocol.Value{}, fmt.Errorf("decode Article API error response: errors[%d] fields are invalid", index)
		}
		byField[field] = append(byField[field], protocol.String(itemCode))
	}
	fields := make(map[string]protocol.Value, len(byField))
	for field, values := range byField {
		fields[field] = protocol.List(values...)
	}
	return protocol.Object(fields), nil
}

func articleAPIOptionalString(value string) protocol.Value {
	if value == "" {
		return protocol.Null()
	}
	return protocol.String(value)
}

func articleAPIRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func articleAPISerializerValue(value serializers.Value) (protocol.Value, error) {
	switch value.Kind() {
	case serializers.ValueNull:
		return protocol.Null(), nil
	case serializers.ValueString:
		text, ok := value.AsString()
		if !ok {
			return protocol.Value{}, fmt.Errorf("convert serializer string: invalid value")
		}
		return protocol.String(text), nil
	case serializers.ValueBoolean:
		boolean, ok := value.AsBoolean()
		if !ok {
			return protocol.Value{}, fmt.Errorf("convert serializer boolean: invalid value")
		}
		return protocol.Boolean(boolean), nil
	case serializers.ValueInteger:
		integer, ok := value.AsInteger()
		if !ok {
			return protocol.Value{}, fmt.Errorf("convert serializer integer: invalid value")
		}
		return protocol.Integer(strconv.FormatInt(integer, 10)), nil
	case serializers.ValueList:
		items, ok := value.AsList()
		if !ok {
			return protocol.Value{}, fmt.Errorf("convert serializer list: invalid value")
		}
		converted := make([]protocol.Value, len(items))
		for index := range items {
			item, err := articleAPISerializerValue(items[index])
			if err != nil {
				return protocol.Value{}, err
			}
			converted[index] = item
		}
		return protocol.List(converted...), nil
	case serializers.ValueObject:
		object, ok := value.AsObject()
		if !ok {
			return protocol.Value{}, fmt.Errorf("convert serializer object: invalid value")
		}
		converted := make(map[string]protocol.Value, object.Len())
		for _, member := range object.Members() {
			item, err := articleAPISerializerValue(member.Value())
			if err != nil {
				return protocol.Value{}, err
			}
			converted[member.Name()] = item
		}
		return protocol.Object(converted), nil
	default:
		return protocol.Value{}, fmt.Errorf("convert serializer value: unsupported kind %d", value.Kind())
	}
}
