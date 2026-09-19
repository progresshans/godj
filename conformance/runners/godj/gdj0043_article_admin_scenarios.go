package godj

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

const (
	articleAdminBasePath          = "/admin"
	articleAdminStaffUsername     = "staff"
	articleAdminNonstaffUsername  = "nonstaff"
	articleAdminDeniedUsername    = "denied"
	articleAdminPassword          = "conformance-only-password"
	articleAdminForceNonstaffPath = "/__godj_conformance__/force-nonstaff/"
	articleAdminAuthStatePath     = "/__godj_conformance__/auth-state/"
)

// articleAdminScenarioHandler deliberately owns only the Phase D Article
// Admin scenarios. The central registry decides when this bounded lane is
// published; this local registry remains independently testable meanwhile.
func articleAdminScenarioHandler(scenario string) (scenarioHandler, bool) {
	handlers := map[string]scenarioHandler{
		"django.article_admin.access_matrix":     articleAdminAccessMatrix,
		"django.article_admin.stable_list":       articleAdminStableList,
		"django.article_admin.search_boundary":   articleAdminSearchBoundary,
		"django.article_admin.change_form_shape": articleAdminChangeFormShape,
		"django.article_admin.invalid_edit":      articleAdminInvalidEdit,
		"django.article_admin.valid_add":         articleAdminValidAdd,
		"django.article_admin.valid_edit":        articleAdminValidEdit,
		"django.article_admin.delete_boundaries": articleAdminDeleteBoundaries,
		"django.article_admin.semantic_history":  articleAdminSemanticHistory,
		"django.article_admin.publish_action":    articleAdminPublishAction,
	}
	handler, ok := handlers[scenario]
	return handler, ok
}

type articleAdminSeed struct {
	id        int64
	title     string
	published bool
	summary   *string
}

type articleAdminFixture struct {
	raw         *sqlite.Backend
	observed    *articleAdminObservedBackend
	audit       *admin.AuditLog
	service     adminapp.Service
	registry    admin.Registry
	sessions    *authSessionCountingStore
	application *web.Application
}

func newArticleAdminFixture(ctx context.Context, contractID string) (*articleAdminFixture, error) {
	raw, err := openEmptyArticleDatabase(ctx, contractID)
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*articleAdminFixture, error) {
		return nil, errors.Join(cause, raw.Close())
	}

	observed := &articleAdminObservedBackend{backend: raw}
	audit, err := admin.NewAuditLog(256)
	if err != nil {
		return fail(fmt.Errorf("create Article Admin audit log: %w", err))
	}
	service, err := adminapp.NewService(observed, audit)
	if err != nil {
		return fail(fmt.Errorf("create Article Admin service: %w", err))
	}
	projectSettings, err := settings.New(settings.Definition{
		ProjectName: "godj_article_admin_conformance",
		InstalledApps: []apps.Config{{
			Name:  "github.com/progresshans/godj/examples/article/models",
			Label: "godj_conformance",
		}},
	})
	if err != nil {
		return fail(fmt.Errorf("create Article Admin settings: %w", err))
	}
	builder := admin.NewBuilder(projectSettings.Apps())
	if err := adminapp.RegisterArticle(builder, service); err != nil {
		return fail(fmt.Errorf("register Article Admin model: %w", err))
	}
	registry, err := builder.Build()
	if err != nil {
		return fail(fmt.Errorf("build Article Admin registry: %w", err))
	}

	memoryStore, err := sessions.NewMemoryStore(32)
	if err != nil {
		return fail(fmt.Errorf("create Article Admin session store: %w", err))
	}
	store := newAuthSessionCountingStore(memoryStore)
	manager, err := sessions.NewManager(store, sessions.Config{})
	if err != nil {
		return fail(fmt.Errorf("create Article Admin session manager: %w", err))
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10_000})
	if err != nil {
		return fail(fmt.Errorf("create Article Admin password hasher: %w", err))
	}
	encodedPassword, err := hasher.Hash(ctx, articleAdminPassword)
	if err != nil {
		return fail(fmt.Errorf("hash Article Admin fixture password: %w", err))
	}
	credentials := make([]auth.Credential, 0, 3)
	principalDefinitions := []struct {
		username    string
		id          string
		permissions []auth.Permission
	}{
		{
			username: articleAdminStaffUsername,
			id:       "staff",
			permissions: []auth.Permission{
				admin.DefaultAccessPermission,
				adminapp.ArticleViewPermission,
				adminapp.ArticleAddPermission,
				adminapp.ArticleChangePermission,
				adminapp.ArticleDeletePermission,
			},
		},
		{username: articleAdminNonstaffUsername, id: "nonstaff"},
		{
			username: articleAdminDeniedUsername,
			id:       "denied",
			permissions: []auth.Permission{
				admin.DefaultAccessPermission,
				adminapp.ArticleViewPermission,
			},
		},
	}
	for _, definition := range principalDefinitions {
		principal, principalErr := auth.NewPrincipal(auth.PrincipalConfig{
			ID:          definition.id,
			Active:      true,
			Permissions: definition.permissions,
		})
		if principalErr != nil {
			return fail(fmt.Errorf("create Article Admin principal: %w", principalErr))
		}
		credential, credentialErr := auth.NewCredential(definition.username, encodedPassword, principal)
		if credentialErr != nil {
			return fail(fmt.Errorf("create Article Admin credential: %w", credentialErr))
		}
		credentials = append(credentials, credential)
	}
	authenticator, err := auth.NewMemoryAuthenticator(credentials, hasher)
	if err != nil {
		return fail(fmt.Errorf("create Article Admin authenticator: %w", err))
	}
	allowedNext, err := admin.SiteAllowedNextPaths(registry, articleAdminBasePath)
	if err != nil {
		return fail(fmt.Errorf("derive Article Admin next paths: %w", err))
	}
	authRuntime, err := sessionauth.New(sessionauth.Config{
		Sessions:         manager,
		Authenticator:    authenticator,
		Authorizer:       auth.PrincipalAuthorizer{},
		SessionCookie:    sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		CSRFCookie:       sessionauth.CookieConfig{Path: "/", AllowInsecure: true},
		LoginPath:        articleAdminBasePath + "/login/",
		FallbackPath:     articleAdminBasePath + "/",
		AllowedNextPaths: allowedNext,
	})
	if err != nil {
		return fail(fmt.Errorf("create Article Admin auth runtime: %w", err))
	}
	site, err := admin.NewSite(admin.SiteConfig{
		Apps:      projectSettings.Apps(),
		Namespace: "godj_conformance",
		BasePath:  articleAdminBasePath,
		Registry:  registry,
		Auth:      authRuntime,
		PageSize:  2,
	})
	if err != nil {
		return fail(fmt.Errorf("create Article Admin site: %w", err))
	}
	routes := site.Routes()
	// Django's access contract uses force_login to distinguish an authenticated
	// nonstaff principal from an anonymous request. This fixture-only route uses
	// the public GoDj login/session boundary to establish that same precondition;
	// it is outside the immutable Site route collection and publishes no secret.
	routes = append(routes, web.Route{
		Name:   "godj_conformance:article-admin-force-nonstaff",
		Method: http.MethodPost,
		Path:   articleAdminForceNonstaffPath,
		Handler: func(request *web.Request) (web.Response, error) {
			login, loginErr := authRuntime.Login(request, articleAdminNonstaffUsername, articleAdminPassword)
			if loginErr != nil {
				return web.Response{}, loginErr
			}
			response, responseErr := web.NewResponse(http.StatusNoContent, make(http.Header), nil)
			if responseErr != nil {
				return web.Response{}, responseErr
			}
			return login.Apply(response)
		},
	})
	routes = append(routes, web.Route{
		Name:   "godj_conformance:article-admin-auth-state",
		Method: http.MethodGet,
		Path:   articleAdminAuthStatePath,
		Handler: authRuntime.Optional(func(_ *web.Request, principal auth.Principal) (web.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "text/plain; charset=utf-8")
			return web.NewResponse(http.StatusOK, header, []byte(strconv.FormatBool(principal.Authenticated())))
		}),
	})
	application, err := web.NewApplication(web.Config{Settings: projectSettings, Routes: routes})
	if err != nil {
		return fail(fmt.Errorf("create Article Admin application: %w", err))
	}
	return &articleAdminFixture{
		raw:         raw,
		observed:    observed,
		audit:       audit,
		service:     service,
		registry:    registry,
		sessions:    store,
		application: application,
	}, nil
}

func (fixture *articleAdminFixture) close() error {
	if fixture == nil || fixture.raw == nil {
		return nil
	}
	return fixture.raw.Close()
}

func withArticleAdminFixture(
	ctx context.Context,
	contract protocol.Contract,
	run func(context.Context, *articleAdminFixture) (protocol.Observation, error),
) (observation protocol.Observation, err error) {
	fixture, err := newArticleAdminFixture(ctx, contract.ID)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer func() { err = errors.Join(err, fixture.close()) }()
	return run(ctx, fixture)
}

func (fixture *articleAdminFixture) seed(ctx context.Context, rows ...articleAdminSeed) error {
	for _, row := range rows {
		if row.id <= 0 || row.title == "" {
			return fmt.Errorf("seed Article Admin row: invalid fixture row")
		}
		if _, err := fixture.raw.ExecContext(
			ctx,
			`INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES (?, ?, ?, ?)`,
			row.id, row.title, row.published, row.summary,
		); err != nil {
			return fmt.Errorf("seed Article Admin row %d: %w", row.id, err)
		}
	}
	return nil
}

func articleAdminStableRows() []articleAdminSeed {
	return []articleAdminSeed{
		{id: 1, title: "Alpine Guide", published: false},
		{id: 2, title: "django Tips", published: false, summary: articleAdminStringPointer("ORM")},
		{id: 3, title: "Django Deep Dive", published: true, summary: articleAdminStringPointer("Guide")},
		{id: 4, title: "Other", published: false},
		{id: 5, title: "Go Admin", published: true, summary: articleAdminStringPointer("Django")},
	}
}

func articleAdminStringPointer(value string) *string { return &value }

type articleAdminWriteCounts struct {
	inserts int
	updates int
	deletes int
	atomic  int
}

func (counts articleAdminWriteCounts) writes() int {
	return counts.inserts + counts.updates + counts.deletes
}

type articleAdminObservedBackend struct {
	backend *sqlite.Backend
	mu      sync.Mutex
	counts  articleAdminWriteCounts
}

func (backend *articleAdminObservedBackend) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return backend.backend.Query(ctx, plan)
}

func (backend *articleAdminObservedBackend) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	backend.recordInsert()
	return backend.backend.Insert(ctx, plan)
}

func (backend *articleAdminObservedBackend) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	backend.recordUpdate()
	return backend.backend.Update(ctx, plan)
}

func (backend *articleAdminObservedBackend) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	backend.recordDelete()
	return backend.backend.Delete(ctx, plan)
}

func (backend *articleAdminObservedBackend) Atomic(ctx context.Context, callback func(db.Session) error) error {
	backend.mu.Lock()
	backend.counts.atomic++
	backend.mu.Unlock()
	if callback == nil {
		return backend.backend.Atomic(ctx, nil)
	}
	return backend.backend.Atomic(ctx, func(session db.Session) error {
		return callback(articleAdminObservedSession{session: session, owner: backend})
	})
}

func (backend *articleAdminObservedBackend) recordInsert() {
	backend.mu.Lock()
	backend.counts.inserts++
	backend.mu.Unlock()
}

func (backend *articleAdminObservedBackend) recordUpdate() {
	backend.mu.Lock()
	backend.counts.updates++
	backend.mu.Unlock()
}

func (backend *articleAdminObservedBackend) recordDelete() {
	backend.mu.Lock()
	backend.counts.deletes++
	backend.mu.Unlock()
}

func (backend *articleAdminObservedBackend) reset() {
	backend.mu.Lock()
	backend.counts = articleAdminWriteCounts{}
	backend.mu.Unlock()
}

func (backend *articleAdminObservedBackend) snapshot() articleAdminWriteCounts {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.counts
}

type articleAdminObservedSession struct {
	session db.Session
	owner   *articleAdminObservedBackend
}

func (session articleAdminObservedSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	return session.session.Query(ctx, plan)
}

func (session articleAdminObservedSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	session.owner.recordInsert()
	return session.session.Insert(ctx, plan)
}

func (session articleAdminObservedSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	session.owner.recordUpdate()
	return session.session.Update(ctx, plan)
}

func (session articleAdminObservedSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	session.owner.recordDelete()
	return session.session.Delete(ctx, plan)
}

type articleAdminHTTPClient struct {
	application *web.Application
	cookies     map[string]*http.Cookie
}

type articleAdminHTTPResponse struct {
	status int
	header http.Header
	body   string
}

func newArticleAdminHTTPClient(application *web.Application) *articleAdminHTTPClient {
	return &articleAdminHTTPClient{application: application, cookies: make(map[string]*http.Cookie)}
}

func (client *articleAdminHTTPClient) do(
	ctx context.Context,
	method string,
	target string,
	values url.Values,
) (articleAdminHTTPResponse, error) {
	if err := ctx.Err(); err != nil {
		return articleAdminHTTPResponse{}, err
	}
	body := strings.NewReader("")
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request := httptest.NewRequest(method, "http://example.test"+target, body).WithContext(ctx)
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, cookie := range client.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	client.application.ServeHTTP(recorder, request)
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(client.cookies, cookie.Name)
			continue
		}
		clone := *cookie
		client.cookies[cookie.Name] = &clone
	}
	return articleAdminHTTPResponse{
		status: recorder.Code,
		header: recorder.Header().Clone(),
		body:   recorder.Body.String(),
	}, nil
}

func (client *articleAdminHTTPClient) login(ctx context.Context, username, next string) error {
	loginPage, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/login/?next="+url.QueryEscape(next), nil)
	if err != nil {
		return err
	}
	if loginPage.status != http.StatusOK {
		return fmt.Errorf("Article Admin login GET status = %d, want %d", loginPage.status, http.StatusOK)
	}
	token, err := articleAdminCSRFToken(loginPage.body)
	if err != nil {
		return err
	}
	login, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/login/", url.Values{
		"csrfmiddlewaretoken": {token},
		"username":            {username},
		"password":            {articleAdminPassword},
		"next":                {next},
	})
	if err != nil {
		return err
	}
	if login.status != http.StatusFound || login.header.Get("Location") != next {
		return fmt.Errorf("Article Admin login POST status/location = %d/%q", login.status, articleAdminLocationCategory(login))
	}
	return nil
}

func (client *articleAdminHTTPClient) forceNonstaff(ctx context.Context) error {
	response, err := client.do(ctx, http.MethodPost, articleAdminForceNonstaffPath, nil)
	if err != nil {
		return err
	}
	if response.status != http.StatusNoContent {
		return fmt.Errorf("Article Admin nonstaff fixture login status = %d", response.status)
	}
	return nil
}

func (client *articleAdminHTTPClient) authenticated(ctx context.Context) (bool, error) {
	response, err := client.do(ctx, http.MethodGet, articleAdminAuthStatePath, nil)
	if err != nil {
		return false, err
	}
	if err := articleAdminRequireStatus(response, http.StatusOK, "auth-state observation"); err != nil {
		return false, err
	}
	authenticated, err := strconv.ParseBool(strings.TrimSpace(response.body))
	if err != nil {
		return false, fmt.Errorf("Article Admin auth-state response is malformed")
	}
	return authenticated, nil
}

func articleAdminCSRFToken(body string) (string, error) {
	const marker = `name="csrfmiddlewaretoken" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		return "", fmt.Errorf("Article Admin response contains no CSRF token")
	}
	remainder := body[start+len(marker):]
	end := strings.IndexByte(remainder, '"')
	if end < 0 || end == 0 {
		return "", fmt.Errorf("Article Admin response contains malformed CSRF token")
	}
	return remainder[:end], nil
}

func articleAdminLocationCategory(response articleAdminHTTPResponse) string {
	if response.status < 300 || response.status >= 400 {
		return "none"
	}
	parsed, err := url.Parse(response.header.Get("Location"))
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return "external"
	}
	switch parsed.Path {
	case articleAdminBasePath + "/login/":
		next := parsed.Query().Get("next")
		if strings.HasPrefix(next, articleAdminBasePath+"/") {
			return "admin_login_local_next"
		}
		return "admin_login"
	case articleAdminBasePath + "/":
		return "admin_index"
	case articleAdminBasePath + "/articles/":
		return "article_list"
	}
	return "other_local"
}

func articleAdminAttribute(body, name string) (string, bool) {
	marker := name + `="`
	start := strings.Index(body, marker)
	if start < 0 {
		return "", false
	}
	remainder := body[start+len(marker):]
	end := strings.IndexByte(remainder, '"')
	if end < 0 {
		return "", false
	}
	return remainder[:end], true
}

func articleAdminAttributeValues(body, name string) ([]string, error) {
	marker := name + `="`
	values := make([]string, 0)
	remaining := body
	for {
		start := strings.Index(remaining, marker)
		if start < 0 {
			return values, nil
		}
		remaining = remaining[start+len(marker):]
		end := strings.IndexByte(remaining, '"')
		if end < 0 {
			return nil, fmt.Errorf("Article Admin response contains malformed %s", name)
		}
		values = append(values, remaining[:end])
		remaining = remaining[end+1:]
	}
}

type articleAdminHTMLTag struct {
	attributes map[string]string
	innerHTML  string
}

func (tag articleAdminHTMLTag) attribute(name string) (string, bool) {
	value, ok := tag.attributes[name]
	return value, ok
}

// articleAdminHTMLTags parses the small, framework-owned semantic HTML
// surface emitted by admin.Site. It intentionally is not a general HTML
// parser: it accepts quoted or bare attributes, validates tag termination and
// returns the exact inner fragment for non-void elements. Conformance
// normalizers use this observation instead of reconstructing a view model from
// the registry, service or reference payload.
func articleAdminHTMLTags(document, element string) ([]articleAdminHTMLTag, error) {
	if element == "" || strings.ContainsAny(element, "<> ") {
		return nil, fmt.Errorf("Article Admin semantic element is invalid")
	}
	prefix := "<" + element
	closing := "</" + element + ">"
	void := element == "input"
	tags := make([]articleAdminHTMLTag, 0)
	for offset := 0; offset < len(document); {
		relative := strings.Index(document[offset:], prefix)
		if relative < 0 {
			break
		}
		start := offset + relative
		afterName := start + len(prefix)
		if afterName >= len(document) || !articleAdminHTMLTagBoundary(document[afterName]) {
			offset = afterName
			continue
		}
		end, err := articleAdminHTMLTagEnd(document, afterName)
		if err != nil {
			return nil, err
		}
		attributes, err := articleAdminHTMLAttributes(document[afterName:end])
		if err != nil {
			return nil, err
		}
		tag := articleAdminHTMLTag{attributes: attributes}
		if !void {
			innerEnd := strings.Index(document[end+1:], closing)
			if innerEnd < 0 {
				return nil, fmt.Errorf("Article Admin response contains unclosed <%s>", element)
			}
			tag.innerHTML = document[end+1 : end+1+innerEnd]
		}
		tags = append(tags, tag)
		offset = end + 1
	}
	return tags, nil
}

func articleAdminHTMLTagBoundary(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '>', '/':
		return true
	default:
		return false
	}
}

func articleAdminHTMLTagEnd(document string, start int) (int, error) {
	var quote byte
	for index := start; index < len(document); index++ {
		switch character := document[index]; {
		case quote != 0 && character == quote:
			quote = 0
		case quote == 0 && (character == '"' || character == '\''):
			quote = character
		case quote == 0 && character == '>':
			return index, nil
		}
	}
	return 0, fmt.Errorf("Article Admin response contains an unterminated tag")
}

func articleAdminHTMLAttributes(fragment string) (map[string]string, error) {
	attributes := make(map[string]string)
	for offset := 0; ; {
		for offset < len(fragment) && articleAdminHTMLSpace(fragment[offset]) {
			offset++
		}
		if offset >= len(fragment) || fragment[offset] == '/' {
			return attributes, nil
		}
		nameStart := offset
		for offset < len(fragment) && !articleAdminHTMLSpace(fragment[offset]) && fragment[offset] != '=' && fragment[offset] != '/' {
			offset++
		}
		if nameStart == offset {
			return nil, fmt.Errorf("Article Admin response contains a malformed attribute")
		}
		name := fragment[nameStart:offset]
		if _, duplicate := attributes[name]; duplicate {
			return nil, fmt.Errorf("Article Admin response contains duplicate attribute %q", name)
		}
		for offset < len(fragment) && articleAdminHTMLSpace(fragment[offset]) {
			offset++
		}
		if offset >= len(fragment) || fragment[offset] != '=' {
			attributes[name] = ""
			continue
		}
		offset++
		for offset < len(fragment) && articleAdminHTMLSpace(fragment[offset]) {
			offset++
		}
		if offset >= len(fragment) {
			return nil, fmt.Errorf("Article Admin response contains an attribute without a value")
		}
		valueStart := offset
		var value string
		if fragment[offset] == '"' || fragment[offset] == '\'' {
			quote := fragment[offset]
			offset++
			valueStart = offset
			for offset < len(fragment) && fragment[offset] != quote {
				offset++
			}
			if offset >= len(fragment) {
				return nil, fmt.Errorf("Article Admin response contains an unterminated attribute %q", name)
			}
			value = fragment[valueStart:offset]
			offset++
		} else {
			for offset < len(fragment) && !articleAdminHTMLSpace(fragment[offset]) && fragment[offset] != '/' {
				offset++
			}
			value = fragment[valueStart:offset]
		}
		attributes[name] = html.UnescapeString(value)
	}
}

func articleAdminHTMLSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func articleAdminHTMLText(fragment string) string {
	var result strings.Builder
	for offset := 0; offset < len(fragment); {
		start := strings.IndexByte(fragment[offset:], '<')
		if start < 0 {
			result.WriteString(fragment[offset:])
			break
		}
		start += offset
		result.WriteString(fragment[offset:start])
		end := strings.IndexByte(fragment[start:], '>')
		if end < 0 {
			result.WriteString(fragment[start:])
			break
		}
		offset = start + end + 1
	}
	return strings.TrimSpace(html.UnescapeString(result.String()))
}

func articleAdminInput(body, name string) (articleAdminHTMLTag, bool, error) {
	inputs, err := articleAdminHTMLTags(body, "input")
	if err != nil {
		return articleAdminHTMLTag{}, false, err
	}
	var match articleAdminHTMLTag
	found := false
	for _, input := range inputs {
		inputName, ok := input.attribute("name")
		if !ok || inputName != name {
			continue
		}
		if found {
			return articleAdminHTMLTag{}, false, fmt.Errorf("Article Admin response contains duplicate input %q", name)
		}
		match, found = input, true
	}
	return match, found, nil
}

func articleAdminNamedInputValues(body, name string) ([]string, error) {
	inputs, err := articleAdminHTMLTags(body, "input")
	if err != nil {
		return nil, err
	}
	values := make([]string, 0)
	for _, input := range inputs {
		inputName, ok := input.attribute("name")
		if !ok || inputName != name {
			continue
		}
		value, ok := input.attribute("value")
		if !ok {
			return nil, fmt.Errorf("Article Admin input %q has no value", name)
		}
		values = append(values, value)
	}
	return values, nil
}

func articleAdminRenderedColumns(body string, renderedIDs []int64) ([]string, error) {
	headers, err := articleAdminHTMLTags(body, "th")
	if err != nil {
		return nil, err
	}
	selected, err := articleAdminNamedInputValues(body, "selected")
	if err != nil {
		return nil, err
	}
	selectedIDs := make([]int64, len(selected))
	for index, value := range selected {
		id, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil || id <= 0 {
			return nil, fmt.Errorf("Article Admin list contains invalid selection value")
		}
		selectedIDs[index] = id
	}
	if !reflect.DeepEqual(selectedIDs, renderedIDs) {
		return nil, fmt.Errorf("Article Admin rendered selection surface differs from rendered rows")
	}
	columns := make([]string, 0, len(headers))
	selectionObserved := false
	for _, header := range headers {
		if field, ok := header.attribute("data-list-field"); ok {
			if field == "" {
				return nil, fmt.Errorf("Article Admin list contains an empty field header")
			}
			columns = append(columns, field)
			continue
		}
		switch articleAdminHTMLText(header.innerHTML) {
		case "Select":
			if selectionObserved || len(selectedIDs) == 0 {
				return nil, fmt.Errorf("Article Admin list selection header has no checkbox surface")
			}
			// action_checkbox is the cross-framework semantic category for
			// the actually rendered Select header plus per-row selected inputs.
			columns = append(columns, "action_checkbox")
			selectionObserved = true
		case "Operations":
			// Row operation links are a separate contract surface, not a
			// Django list_display column.
		default:
			return nil, fmt.Errorf("Article Admin list contains an unknown header")
		}
	}
	if !selectionObserved {
		return nil, fmt.Errorf("Article Admin list contains no rendered selection column")
	}
	return columns, nil
}

func articleAdminRenderedActions(body string) (map[string]string, []string, error) {
	buttons, err := articleAdminHTMLTags(body, "button")
	if err != nil {
		return nil, nil, err
	}
	paths := make(map[string]string)
	ordered := make([]string, 0)
	for _, button := range buttons {
		name, ok := button.attribute("data-action")
		if !ok {
			continue
		}
		path, pathOK := button.attribute("formaction")
		if name == "" || !pathOK || path == "" {
			return nil, nil, fmt.Errorf("Article Admin action surface is malformed")
		}
		if _, duplicate := paths[name]; duplicate {
			return nil, nil, fmt.Errorf("Article Admin action %q is rendered more than once", name)
		}
		paths[name] = path
		ordered = append(ordered, name)
	}
	return paths, ordered, nil
}

func articleAdminRenderedModelPaths(body string) ([]string, error) {
	anchors, err := articleAdminHTMLTags(body, "a")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, anchor := range anchors {
		href, ok := anchor.attribute("href")
		if !ok || href == articleAdminBasePath+"/" || !strings.HasPrefix(href, articleAdminBasePath+"/") {
			continue
		}
		if _, duplicate := seen[href]; duplicate {
			continue
		}
		seen[href] = struct{}{}
		paths = append(paths, href)
	}
	return paths, nil
}

type articleAdminRenderedError struct {
	field string
	code  string
}

func articleAdminRenderedErrors(body string, fieldOrder []string) ([]articleAdminRenderedError, error) {
	paragraphs, err := articleAdminHTMLTags(body, "p")
	if err != nil {
		return nil, err
	}
	knownFields := map[string]struct{}{string("__all__"): {}}
	for _, field := range fieldOrder {
		knownFields[field] = struct{}{}
	}
	errors := make([]articleAdminRenderedError, 0)
	for _, paragraph := range paragraphs {
		field, fieldOK := paragraph.attribute("data-error-field")
		code, codeOK := paragraph.attribute("data-error-code")
		if !fieldOK && !codeOK {
			continue
		}
		if !fieldOK || !codeOK || field == "" || code == "" {
			return nil, fmt.Errorf("Article Admin rendered error surface is malformed")
		}
		if _, ok := knownFields[field]; !ok {
			return nil, fmt.Errorf("Article Admin rendered error refers to unknown field %q", field)
		}
		errors = append(errors, articleAdminRenderedError{field: field, code: code})
	}
	if len(errors) == 0 {
		return nil, fmt.Errorf("Article Admin invalid form rendered no errors")
	}
	return errors, nil
}

type articleAdminRenderedHistoryEntry struct {
	sequence      int
	action        string
	actor         string
	objectID      int64
	changedFields []string
}

func articleAdminRenderedHistory(body string) ([]articleAdminRenderedHistoryEntry, error) {
	objectIDRaw, ok := articleAdminAttribute(body, "data-object-id")
	if !ok {
		return nil, fmt.Errorf("Article Admin history has no object id")
	}
	objectID, err := strconv.ParseInt(objectIDRaw, 10, 64)
	if err != nil || objectID <= 0 {
		return nil, fmt.Errorf("Article Admin history object id is invalid")
	}
	items, err := articleAdminHTMLTags(body, "li")
	if err != nil {
		return nil, err
	}
	entries := make([]articleAdminRenderedHistoryEntry, 0, len(items))
	for _, item := range items {
		sequenceRaw, sequenceOK := item.attribute("data-sequence")
		action, actionOK := item.attribute("data-action")
		actor, actorOK := item.attribute("data-actor")
		if !sequenceOK && !actionOK && !actorOK {
			continue
		}
		sequence, parseErr := strconv.Atoi(sequenceRaw)
		if !sequenceOK || !actionOK || !actorOK || sequence <= 0 || parseErr != nil || action == "" || actor == "" {
			return nil, fmt.Errorf("Article Admin history entry metadata is malformed")
		}
		if sequence != len(entries)+1 {
			return nil, fmt.Errorf("Article Admin history sequence is not contiguous")
		}
		text := articleAdminHTMLText(item.innerHTML)
		prefix := action + " by " + actor + ": "
		if !strings.HasPrefix(text, prefix) {
			return nil, fmt.Errorf("Article Admin history entry text disagrees with its metadata")
		}
		open, close := strings.LastIndex(text, "["), strings.LastIndex(text, "]")
		if open < len(prefix) || close != len(text)-1 || close < open {
			return nil, fmt.Errorf("Article Admin history changed-field surface is malformed")
		}
		entries = append(entries, articleAdminRenderedHistoryEntry{
			sequence:      sequence,
			action:        action,
			actor:         actor,
			objectID:      objectID,
			changedFields: strings.Fields(text[open+1 : close]),
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Article Admin history rendered no semantic events")
	}
	return entries, nil
}

func articleAdminRenderedHistoryValue(entry articleAdminRenderedHistoryEntry) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"action":         protocol.String(entry.action),
		"actor":          protocol.String(entry.actor),
		"changed_fields": articleAdminStringList(entry.changedFields),
		"object_id":      primaryKeyValue(entry.objectID),
	})
}

func articleAdminHistoryMatchesAudit(rendered articleAdminRenderedHistoryEntry, audit admin.AuditEntry) bool {
	return uint64(rendered.sequence) == audit.Sequence && rendered.action == string(audit.Action) && rendered.actor == audit.ActorID &&
		rendered.objectID == audit.ObjectID && slices.Equal(rendered.changedFields, audit.ChangedFields)
}

type articleAdminRenderedNotice struct {
	tag      string
	affected int
	text     string
}

func articleAdminRenderedAdminNotice(body string) (articleAdminRenderedNotice, bool, error) {
	paragraphs, err := articleAdminHTMLTags(body, "p")
	if err != nil {
		return articleAdminRenderedNotice{}, false, err
	}
	var notice articleAdminRenderedNotice
	found := false
	for _, paragraph := range paragraphs {
		tag, ok := paragraph.attribute("data-admin-message")
		if !ok {
			continue
		}
		if found {
			return articleAdminRenderedNotice{}, false, fmt.Errorf("Article Admin rendered more than one notice")
		}
		affectedRaw, affectedOK := paragraph.attribute("data-affected")
		affected, parseErr := strconv.Atoi(affectedRaw)
		if tag == "" || !affectedOK || parseErr != nil || affected < 0 {
			return articleAdminRenderedNotice{}, false, fmt.Errorf("Article Admin notice is malformed")
		}
		notice = articleAdminRenderedNotice{tag: tag, affected: affected, text: articleAdminHTMLText(paragraph.innerHTML)}
		found = true
	}
	return notice, found, nil
}

func articleAdminNormalizedMessageLevel(notice articleAdminRenderedNotice) (int, bool) {
	if notice.tag != "published" || notice.text != strconv.Itoa(notice.affected)+" object(s) published." {
		return 0, false
	}
	// Django's numeric 25 and GoDj's signed `published` notice are normalized
	// to the shared success category only after observing the complete rendered
	// tag/text/count tuple. An absent or altered public marker cannot produce it.
	return 25, true
}

func articleAdminIntegerAttribute(body, name string) (int, error) {
	raw, ok := articleAdminAttribute(body, name)
	if !ok {
		return 0, fmt.Errorf("Article Admin response is missing %s", name)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("Article Admin %s is not an integer", name)
	}
	return value, nil
}

func articleAdminObjectIDs(body string) ([]int64, error) {
	const marker = `data-object-id="`
	ids := make([]int64, 0)
	remaining := body
	for {
		start := strings.Index(remaining, marker)
		if start < 0 {
			break
		}
		remaining = remaining[start+len(marker):]
		end := strings.IndexByte(remaining, '"')
		if end < 0 {
			return nil, fmt.Errorf("Article Admin response contains malformed object id")
		}
		id, err := strconv.ParseInt(remaining[:end], 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("Article Admin response contains invalid object id")
		}
		ids = append(ids, id)
		remaining = remaining[end+1:]
	}
	return ids, nil
}

func articleAdminRequireStatus(response articleAdminHTTPResponse, want int, operation string) error {
	if response.status != want {
		return fmt.Errorf("Article Admin %s status = %d, want %d", operation, response.status, want)
	}
	return nil
}

func articleAdminRequireMarkers(body string, markers ...string) error {
	for _, marker := range markers {
		if !strings.Contains(body, marker) {
			return fmt.Errorf("Article Admin response is missing semantic marker %q", marker)
		}
	}
	return nil
}

func articleAdminRows(ctx context.Context, backend *sqlite.Backend) ([]models.Article, error) {
	rows, err := models.ArticleObjects.Using(backend).OrderBy(models.ArticleFields.ID.Asc()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read Article Admin database rows: %w", err)
	}
	return rows, nil
}

func articleAdminRowsValue(ctx context.Context, backend *sqlite.Backend) (protocol.Value, error) {
	rows, err := articleAdminRows(ctx, backend)
	if err != nil {
		return protocol.Value{}, err
	}
	return articleList(rows), nil
}

func articleAdminDatabaseState(ctx context.Context, backend *sqlite.Backend) (protocol.Value, error) {
	rows, err := articleAdminRows(ctx, backend)
	if err != nil {
		return protocol.Value{}, err
	}
	return databaseState(rows), nil
}

func articleAdminObservation(
	contract protocol.Contract,
	result protocol.Value,
	dbState *protocol.Value,
	metrics protocol.Value,
) protocol.Observation {
	return protocol.Observation{
		ID:      contract.ID,
		Status:  protocol.StatusObserved,
		Phase:   contract.Phase,
		Result:  valuePointer(result),
		DBState: dbState,
		Metrics: valuePointer(metrics),
	}
}

func articleAdminInt(value int) protocol.Value {
	return protocol.Integer(strconv.Itoa(value))
}

func articleAdminStringList(values []string) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = protocol.String(value)
	}
	return protocol.List(items...)
}

func articleAdminPrimaryKeyList(values []int64) protocol.Value {
	items := make([]protocol.Value, len(values))
	for index, value := range values {
		items[index] = primaryKeyValue(value)
	}
	return protocol.List(items...)
}

func articleAdminAuditValue(entry admin.AuditEntry) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"action":         protocol.String(string(entry.Action)),
		"actor":          protocol.String(entry.ActorID),
		"changed_fields": articleAdminStringList(entry.ChangedFields),
		"object_id":      primaryKeyValue(entry.ObjectID),
	})
}

func articleAdminAccessMatrix(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		anonymousClient := newArticleAdminHTTPClient(fixture.application)
		anonymousAuthenticated, err := anonymousClient.authenticated(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		if anonymousAuthenticated {
			return protocol.Observation{}, fmt.Errorf("Article Admin anonymous access precondition is authenticated")
		}
		anonymous, err := anonymousClient.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(anonymous, http.StatusFound, "anonymous list"); err != nil {
			return protocol.Observation{}, err
		}

		nonstaffClient := newArticleAdminHTTPClient(fixture.application)
		if err := nonstaffClient.forceNonstaff(ctx); err != nil {
			return protocol.Observation{}, err
		}
		nonstaffAuthenticated, err := nonstaffClient.authenticated(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		if !nonstaffAuthenticated {
			return protocol.Observation{}, fmt.Errorf("Article Admin nonstaff access precondition is anonymous")
		}
		nonstaff, err := nonstaffClient.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(nonstaff, http.StatusFound, "nonstaff list"); err != nil {
			return protocol.Observation{}, err
		}

		// The Django scenario's staff_with_view case is a view-only principal,
		// not the CRUD-capable fixture administrator used by mutation scenarios.
		// The denied credential has Admin access + Article view and deliberately
		// lacks add/change/delete/action permission.
		viewOnlyClient := newArticleAdminHTTPClient(fixture.application)
		if err := viewOnlyClient.login(ctx, articleAdminDeniedUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		viewOnlyAuthenticated, err := viewOnlyClient.authenticated(ctx)
		if err != nil {
			return protocol.Observation{}, err
		}
		if !viewOnlyAuthenticated {
			return protocol.Observation{}, fmt.Errorf("Article Admin view-only staff precondition is anonymous")
		}
		staff, err := viewOnlyClient.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(staff, http.StatusOK, "staff list"); err != nil {
			return protocol.Observation{}, err
		}
		if strings.Contains(staff.body, ">Add Article</a>") || strings.Contains(staff.body, ">Change</a>") ||
			strings.Contains(staff.body, ">Delete</a>") || strings.Contains(staff.body, `data-action=`) {
			return protocol.Observation{}, fmt.Errorf("Article Admin view-only staff rendered a mutation surface")
		}
		addDenied, err := viewOnlyClient.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/add/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(addDenied, http.StatusForbidden, "view-only add"); err != nil {
			return protocol.Observation{}, err
		}

		result := protocol.Object(map[string]protocol.Value{
			"anonymous": protocol.Object(map[string]protocol.Value{
				"redirect": protocol.String(articleAdminLocationCategory(anonymous)),
				"status":   articleAdminInt(anonymous.status),
			}),
			"nonstaff": protocol.Object(map[string]protocol.Value{
				"redirect": protocol.String(articleAdminLocationCategory(nonstaff)),
				"status":   articleAdminInt(nonstaff.status),
			}),
			"staff_with_view": protocol.Object(map[string]protocol.Value{
				"status": articleAdminInt(staff.status),
			}),
		})
		metrics := protocol.Object(map[string]protocol.Value{"access_cases": articleAdminInt(3)})
		return articleAdminObservation(contract, result, nil, metrics), nil
	})
}

func articleAdminStableList(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAdminStableRows()...); err != nil {
			return protocol.Observation{}, err
		}
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		response, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(response, http.StatusOK, "stable list"); err != nil {
			return protocol.Observation{}, err
		}
		ids, err := articleAdminObjectIDs(response.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		page, err := articleAdminIntegerAttribute(response.body, "data-page")
		if err != nil {
			return protocol.Observation{}, err
		}
		pageCount, err := articleAdminIntegerAttribute(response.body, "data-page-count")
		if err != nil {
			return protocol.Observation{}, err
		}
		resultCount, err := articleAdminIntegerAttribute(response.body, "data-result-count")
		if err != nil {
			return protocol.Observation{}, err
		}
		columns, err := articleAdminRenderedColumns(response.body, ids)
		if err != nil {
			return protocol.Observation{}, err
		}
		descriptors := fixture.registry.All()
		if len(descriptors) != 1 {
			return protocol.Observation{}, fmt.Errorf("Article Admin registered model count = %d, want 1", len(descriptors))
		}
		descriptor := descriptors[0]
		renderedFields, err := articleAdminAttributeValues(response.body, "data-list-field")
		if err != nil {
			return protocol.Observation{}, err
		}
		if !reflect.DeepEqual(renderedFields, descriptor.ListFields) {
			return protocol.Observation{}, fmt.Errorf("Article Admin rendered fields differ from immutable registry")
		}
		_, actions, err := articleAdminRenderedActions(response.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		registeredActions := make([]string, len(descriptor.Actions))
		for index, action := range descriptor.Actions {
			registeredActions[index] = action.Name
		}
		if !reflect.DeepEqual(actions, registeredActions) {
			return protocol.Observation{}, fmt.Errorf("Article Admin rendered actions differ from immutable registry")
		}
		index, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(index, http.StatusOK, "Admin index"); err != nil {
			return protocol.Observation{}, err
		}
		modelPaths, err := articleAdminRenderedModelPaths(index.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		if len(modelPaths) != len(descriptors) || len(modelPaths) != 1 || modelPaths[0] != articleAdminBasePath+"/articles/" {
			return protocol.Observation{}, fmt.Errorf("Article Admin rendered model index differs from immutable registry")
		}
		state, err := articleAdminDatabaseState(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"actions":      articleAdminStringList(actions),
			"columns":      articleAdminStringList(columns),
			"page":         articleAdminInt(page),
			"page_count":   articleAdminInt(pageCount),
			"result_count": articleAdminInt(resultCount),
			"result_ids":   articleAdminPrimaryKeyList(ids),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"page_size":         articleAdminInt(len(ids)),
			"registered_models": articleAdminInt(len(modelPaths)),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminSearchBoundary(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAdminStableRows()...); err != nil {
			return protocol.Observation{}, err
		}
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		search, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/?q=django", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(search, http.StatusOK, "search list"); err != nil {
			return protocol.Observation{}, err
		}
		searchCount, err := articleAdminIntegerAttribute(search.body, "data-result-count")
		if err != nil {
			return protocol.Observation{}, err
		}
		searchIDs, err := articleAdminObjectIDs(search.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		before, err := articleAdminRowsValue(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		invalid, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/?q=django&p=not-an-integer", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(invalid, http.StatusOK, "invalid page search"); err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireMarkers(invalid.body, `data-query-error="invalid_page"`); err != nil {
			return protocol.Observation{}, err
		}
		after, err := articleAdminRowsValue(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		mutationZero := reflect.DeepEqual(before, after) && fixture.observed.snapshot().writes() == 0
		result := protocol.Object(map[string]protocol.Value{
			"invalid": protocol.Object(map[string]protocol.Value{
				"redirect": protocol.String(articleAdminLocationCategory(invalid)),
				"status":   articleAdminInt(invalid.status),
			}),
			"invalid_mutation_zero": protocol.Boolean(mutationZero),
			"search_count":          articleAdminInt(searchCount),
			"search_ids":            articleAdminPrimaryKeyList(searchIDs),
		})
		state := protocol.Object(map[string]protocol.Value{"after": after, "before": before})
		metrics := protocol.Object(map[string]protocol.Value{"search_terms": articleAdminInt(1)})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminChangeFormShape(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAdminSeed{id: 1, title: "Shape", published: true}); err != nil {
			return protocol.Observation{}, err
		}
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		change, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/change/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(change, http.StatusOK, "change form"); err != nil {
			return protocol.Observation{}, err
		}
		descriptors := fixture.registry.All()
		if len(descriptors) != 1 {
			return protocol.Observation{}, fmt.Errorf("Article Admin registered model count = %d, want 1", len(descriptors))
		}
		registeredFieldOrder := make([]string, len(descriptors[0].FormFields))
		for index, field := range descriptors[0].FormFields {
			registeredFieldOrder[index] = field.Name()
		}
		fieldOrder, err := articleAdminAttributeValues(change.body, "data-field-name")
		if err != nil {
			return protocol.Observation{}, err
		}
		if !reflect.DeepEqual(fieldOrder, registeredFieldOrder) {
			return protocol.Observation{}, fmt.Errorf("Article Admin rendered form fields differ from immutable registry")
		}
		titleInput, titleFound, err := articleAdminInput(change.body, "title")
		if err != nil || !titleFound {
			return protocol.Observation{}, fmt.Errorf("observe Article Admin rendered title input: found=%t: %w", titleFound, err)
		}
		title, titleValueFound := titleInput.attribute("value")
		publishedInput, publishedFound, err := articleAdminInput(change.body, "published")
		if err != nil || !publishedFound {
			return protocol.Observation{}, fmt.Errorf("observe Article Admin rendered published input: found=%t: %w", publishedFound, err)
		}
		publishedType, publishedTypeFound := publishedInput.attribute("type")
		_, published := publishedInput.attribute("checked")
		summaryInput, summaryFound, err := articleAdminInput(change.body, "summary")
		if err != nil || !summaryFound {
			return protocol.Observation{}, fmt.Errorf("observe Article Admin rendered summary input: found=%t: %w", summaryFound, err)
		}
		summaryText, summaryValueFound := summaryInput.attribute("value")
		if !titleValueFound || !publishedTypeFound || publishedType != "checkbox" || !summaryValueFound {
			return protocol.Observation{}, fmt.Errorf("Article Admin rendered initial-value surface is malformed")
		}
		summary := protocol.Null()
		if summaryText != "" {
			summary = protocol.String(summaryText)
		}
		operations := make([]string, 0, 4)
		operationRequests := []struct {
			name   string
			target string
		}{
			{name: "add", target: articleAdminBasePath + "/articles/add/"},
			{name: "change", target: articleAdminBasePath + "/articles/change/?id=1"},
			{name: "delete", target: articleAdminBasePath + "/articles/delete/?id=1"},
			{name: "view", target: articleAdminBasePath + "/articles/"},
		}
		for _, operation := range operationRequests {
			response, requestErr := client.do(ctx, http.MethodGet, operation.target, nil)
			if requestErr != nil {
				return protocol.Observation{}, requestErr
			}
			if response.status == http.StatusOK {
				operations = append(operations, operation.name)
			}
		}
		state, err := articleAdminDatabaseState(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		result := protocol.Object(map[string]protocol.Value{
			"allowed_operations": articleAdminStringList(operations),
			"field_order":        articleAdminStringList(fieldOrder),
			"initial": protocol.Object(map[string]protocol.Value{
				"published": protocol.Boolean(published),
				"summary":   summary,
				"title":     protocol.String(title),
			}),
			"status": articleAdminInt(change.status),
		})
		metrics := protocol.Object(map[string]protocol.Value{"editable_fields": articleAdminInt(len(fieldOrder))})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminInvalidEdit(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAdminSeed{id: 1, title: "Before", summary: articleAdminStringPointer("Stable")}); err != nil {
			return protocol.Observation{}, err
		}
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		change, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/change/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		token, err := articleAdminCSRFToken(change.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		before, err := articleAdminRowsValue(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		tooLongTitle := strings.Repeat("x", 201)
		invalidSummary := "bad\x00summary"
		response, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/change/?id=1", url.Values{
			"csrfmiddlewaretoken": {token},
			"title":               {tooLongTitle},
			"published":           {"on"},
			"summary":             {invalidSummary},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(response, http.StatusOK, "invalid change"); err != nil {
			return protocol.Observation{}, err
		}
		fieldOrder, err := articleAdminAttributeValues(response.body, "data-field-name")
		if err != nil {
			return protocol.Observation{}, err
		}
		renderedErrors, err := articleAdminRenderedErrors(response.body, fieldOrder)
		if err != nil {
			return protocol.Observation{}, err
		}
		errorValues := make([]protocol.Value, len(renderedErrors))
		for index, renderedError := range renderedErrors {
			errorValues[index] = protocol.Object(map[string]protocol.Value{
				"code":  protocol.String(renderedError.code),
				"field": protocol.String(renderedError.field),
			})
		}
		after, err := articleAdminRowsValue(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		counts := fixture.observed.snapshot()
		mutationZero := reflect.DeepEqual(before, after) && counts.writes() == 0 && fixture.audit.Len() == 0
		titleInput, renderedTitleFound, err := articleAdminInput(response.body, "title")
		if err != nil {
			return protocol.Observation{}, err
		}
		renderedTitle, renderedTitleValueFound := titleInput.attribute("value")
		summaryInput, renderedSummaryFound, err := articleAdminInput(response.body, "summary")
		if err != nil {
			return protocol.Observation{}, err
		}
		renderedSummary, renderedSummaryValueFound := summaryInput.attribute("value")
		publishedInput, renderedPublishedFound, err := articleAdminInput(response.body, "published")
		if err != nil {
			return protocol.Observation{}, err
		}
		_, renderedPublishedChecked := publishedInput.attribute("checked")
		stickyTitle := renderedTitleFound && renderedTitleValueFound && renderedTitle == tooLongTitle
		stickyPublished := renderedPublishedFound && renderedPublishedChecked
		stickySummary := renderedSummaryFound && renderedSummaryValueFound && renderedSummary == "bad\uFFFDsummary"
		result := protocol.Object(map[string]protocol.Value{
			"errors":        protocol.List(errorValues...),
			"mutation_zero": protocol.Boolean(mutationZero),
			"status":        articleAdminInt(response.status),
			"sticky": protocol.Object(map[string]protocol.Value{
				"published": protocol.Boolean(stickyPublished),
				"summary":   protocol.Boolean(stickySummary),
				"title":     protocol.Boolean(stickyTitle),
			}),
		})
		state := protocol.Object(map[string]protocol.Value{"after": after, "before": before})
		metrics := protocol.Object(map[string]protocol.Value{
			"audit_events": articleAdminInt(fixture.audit.Len()),
			"writes":       articleAdminInt(counts.writes()),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminValidAdd(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		add, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/add/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		token, err := articleAdminCSRFToken(add.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		before, err := articleAdminRows(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		response, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/add/", url.Values{
			"csrfmiddlewaretoken": {token},
			"title":               {"Created"},
			"published":           {"on"},
			"summary":             {"Created summary"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(response, http.StatusFound, "valid add"); err != nil {
			return protocol.Observation{}, err
		}
		after, err := articleAdminRows(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		entries := fixture.audit.All()
		if len(entries) != 1 {
			return protocol.Observation{}, fmt.Errorf("Article Admin add audit count = %d, want 1", len(entries))
		}
		state := databaseState(after)
		result := protocol.Object(map[string]protocol.Value{
			"event":    articleAdminAuditValue(entries[0]),
			"redirect": protocol.String(articleAdminLocationCategory(response)),
			"status":   articleAdminInt(response.status),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"audit_events": articleAdminInt(len(entries)),
			"rows_added":   articleAdminInt(len(after) - len(before)),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminValidEdit(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx, articleAdminSeed{id: 1, title: "Before", published: false}); err != nil {
			return protocol.Observation{}, err
		}
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		change, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/change/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		token, err := articleAdminCSRFToken(change.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		response, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/change/?id=1", url.Values{
			"csrfmiddlewaretoken": {token},
			"title":               {"After"},
			"published":           {"on"},
			"summary":             {"After summary"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(response, http.StatusFound, "valid edit"); err != nil {
			return protocol.Observation{}, err
		}
		entries := fixture.audit.All()
		if len(entries) != 1 {
			return protocol.Observation{}, fmt.Errorf("Article Admin edit audit count = %d, want 1", len(entries))
		}
		state, err := articleAdminDatabaseState(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		counts := fixture.observed.snapshot()
		result := protocol.Object(map[string]protocol.Value{
			"event":    articleAdminAuditValue(entries[0]),
			"redirect": protocol.String(articleAdminLocationCategory(response)),
			"status":   articleAdminInt(response.status),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"audit_events": articleAdminInt(len(entries)),
			"rows_changed": articleAdminInt(counts.updates),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminDeleteBoundaries(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx,
			articleAdminSeed{id: 1, title: "Delete", published: false},
			articleAdminSeed{id: 2, title: "Denied", published: false},
			articleAdminSeed{id: 3, title: "Unsafe", published: false},
		); err != nil {
			return protocol.Observation{}, err
		}

		staff := newArticleAdminHTTPClient(fixture.application)
		if err := staff.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		deletePage, err := staff.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/delete/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		deleteToken, err := articleAdminCSRFToken(deletePage.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		fixture.observed.reset()
		confirmed, err := staff.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/delete/?id=1", url.Values{
			"csrfmiddlewaretoken": {deleteToken},
			"confirm":             {"yes"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(confirmed, http.StatusFound, "confirmed delete"); err != nil {
			return protocol.Observation{}, err
		}

		denied := newArticleAdminHTTPClient(fixture.application)
		if err := denied.login(ctx, articleAdminDeniedUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		deniedList, err := denied.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		deniedToken, err := articleAdminCSRFToken(deniedList.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		missingPermission, err := denied.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/delete/?id=2", url.Values{
			"csrfmiddlewaretoken": {deniedToken},
			"confirm":             {"yes"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(missingPermission, http.StatusForbidden, "delete without permission"); err != nil {
			return protocol.Observation{}, err
		}

		missingCSRF, err := staff.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/delete/?id=3", url.Values{
			"confirm": {"yes"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(missingCSRF, http.StatusForbidden, "delete without CSRF"); err != nil {
			return protocol.Observation{}, err
		}

		_, confirmedPreserved, err := fixture.service.Get(ctx, 1)
		if err != nil {
			return protocol.Observation{}, err
		}
		_, deniedPreserved, err := fixture.service.Get(ctx, 2)
		if err != nil {
			return protocol.Observation{}, err
		}
		_, unsafePreserved, err := fixture.service.Get(ctx, 3)
		if err != nil {
			return protocol.Observation{}, err
		}
		entries := fixture.audit.All()
		if len(entries) != 1 || confirmedPreserved {
			return protocol.Observation{}, fmt.Errorf("Article Admin delete durable/audit boundary is inconsistent")
		}
		state, err := articleAdminDatabaseState(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		counts := fixture.observed.snapshot()
		result := protocol.Object(map[string]protocol.Value{
			"confirmed": protocol.Object(map[string]protocol.Value{
				"event":  articleAdminAuditValue(entries[0]),
				"status": articleAdminInt(confirmed.status),
			}),
			"missing_csrf": protocol.Object(map[string]protocol.Value{
				"row_preserved": protocol.Boolean(unsafePreserved),
				"status":        articleAdminInt(missingCSRF.status),
			}),
			"missing_permission": protocol.Object(map[string]protocol.Value{
				"row_preserved": protocol.Boolean(deniedPreserved),
				"status":        articleAdminInt(missingPermission.status),
			}),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"audit_events": articleAdminInt(len(entries)),
			"rows_deleted": articleAdminInt(counts.deletes),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminSemanticHistory(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		statuses := make([]int, 0, 3)

		add, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/add/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		addToken, err := articleAdminCSRFToken(add.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		addResult, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/add/", url.Values{
			"csrfmiddlewaretoken": {addToken},
			"title":               {"Lifecycle"},
			"summary":             {"Initial"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(addResult, http.StatusFound, "history add"); err != nil {
			return protocol.Observation{}, err
		}
		statuses = append(statuses, addResult.status)

		change, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/change/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		changeToken, err := articleAdminCSRFToken(change.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		changeResult, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/change/?id=1", url.Values{
			"csrfmiddlewaretoken": {changeToken},
			"title":               {"Lifecycle"},
			"published":           {"on"},
			"summary":             {"Changed"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(changeResult, http.StatusFound, "history change"); err != nil {
			return protocol.Observation{}, err
		}
		statuses = append(statuses, changeResult.status)

		deletePage, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/delete/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		deleteToken, err := articleAdminCSRFToken(deletePage.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		deleteResult, err := client.do(ctx, http.MethodPost, articleAdminBasePath+"/articles/delete/?id=1", url.Values{
			"csrfmiddlewaretoken": {deleteToken},
			"confirm":             {"yes"},
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(deleteResult, http.StatusFound, "history delete"); err != nil {
			return protocol.Observation{}, err
		}
		statuses = append(statuses, deleteResult.status)

		history, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/history/?id=1", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(history, http.StatusOK, "semantic history"); err != nil {
			return protocol.Observation{}, err
		}
		renderedEntries, err := articleAdminRenderedHistory(history.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		entries := fixture.audit.All()
		if len(entries) != len(renderedEntries) {
			return protocol.Observation{}, fmt.Errorf("Article Admin rendered/audit history count = %d/%d", len(renderedEntries), len(entries))
		}
		eventValues := make([]protocol.Value, len(renderedEntries))
		for index, entry := range renderedEntries {
			if !articleAdminHistoryMatchesAudit(entry, entries[index]) {
				return protocol.Observation{}, fmt.Errorf(
					"Article Admin rendered history entry %d differs from the process audit: rendered=%+v audit=%+v",
					index,
					entry,
					entries[index],
				)
			}
			eventValues[index] = articleAdminRenderedHistoryValue(entry)
		}
		statusValues := make([]protocol.Value, len(statuses))
		for index, status := range statuses {
			statusValues[index] = articleAdminInt(status)
		}
		rows, err := articleAdminRows(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		state := databaseState(rows)
		result := protocol.Object(map[string]protocol.Value{
			"events":   protocol.List(eventValues...),
			"statuses": protocol.List(statusValues...),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			"audit_events":   articleAdminInt(len(renderedEntries)),
			"remaining_rows": articleAdminInt(len(rows)),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}

func articleAdminPublishAction(ctx context.Context, contract protocol.Contract) (protocol.Observation, error) {
	return withArticleAdminFixture(ctx, contract, func(ctx context.Context, fixture *articleAdminFixture) (protocol.Observation, error) {
		if err := fixture.seed(ctx,
			articleAdminSeed{id: 1, title: "Selected one", published: false},
			articleAdminSeed{id: 2, title: "Unselected", published: false},
			articleAdminSeed{id: 3, title: "Selected three", published: false},
		); err != nil {
			return protocol.Observation{}, err
		}
		client := newArticleAdminHTTPClient(fixture.application)
		if err := client.login(ctx, articleAdminStaffUsername, articleAdminBasePath+"/articles/"); err != nil {
			return protocol.Observation{}, err
		}
		list, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		token, err := articleAdminCSRFToken(list.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		actionPaths, _, err := articleAdminRenderedActions(list.body)
		if err != nil {
			return protocol.Observation{}, err
		}
		publishPath, publishRendered := actionPaths["publish"]
		if !publishRendered {
			return protocol.Observation{}, fmt.Errorf("Article Admin list rendered no publish action")
		}
		firstPageSelections, err := articleAdminNamedInputValues(list.body, "selected")
		if err != nil {
			return protocol.Observation{}, err
		}
		secondPage, err := client.do(ctx, http.MethodGet, articleAdminBasePath+"/articles/?p=2", nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(secondPage, http.StatusOK, "publish selection page 2"); err != nil {
			return protocol.Observation{}, err
		}
		secondPageSelections, err := articleAdminNamedInputValues(secondPage.body, "selected")
		if err != nil {
			return protocol.Observation{}, err
		}
		if len(firstPageSelections) < 2 || len(secondPageSelections) < 1 {
			return protocol.Observation{}, fmt.Errorf("Article Admin publish selection surface is incomplete")
		}
		firstSelected, err := strconv.ParseInt(firstPageSelections[0], 10, 64)
		if err != nil || firstSelected <= 0 {
			return protocol.Observation{}, fmt.Errorf("Article Admin first-page selection is invalid")
		}
		secondSelected, err := strconv.ParseInt(secondPageSelections[0], 10, 64)
		if err != nil || secondSelected <= 0 {
			return protocol.Observation{}, fmt.Errorf("Article Admin second-page selection is invalid")
		}
		unselectedID, err := strconv.ParseInt(firstPageSelections[1], 10, 64)
		if err != nil || unselectedID <= 0 {
			return protocol.Observation{}, fmt.Errorf("Article Admin unselected row is invalid")
		}
		before, found, err := fixture.service.Get(ctx, unselectedID)
		if err != nil || !found {
			return protocol.Observation{}, fmt.Errorf("read unselected Article before publish: found=%t: %w", found, err)
		}
		fixture.observed.reset()
		selectedIDs := []int64{firstSelected, secondSelected}
		selectedValues := make([]string, len(selectedIDs))
		for index, id := range selectedIDs {
			selectedValues[index] = strconv.FormatInt(id, 10)
		}
		action, err := client.do(ctx, http.MethodPost, publishPath, url.Values{
			"csrfmiddlewaretoken": {token},
			"selected":            selectedValues,
		})
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(action, http.StatusFound, "publish action"); err != nil {
			return protocol.Observation{}, err
		}
		notice, err := client.do(ctx, http.MethodGet, action.header.Get("Location"), nil)
		if err != nil {
			return protocol.Observation{}, err
		}
		if err := articleAdminRequireStatus(notice, http.StatusOK, "publish notice"); err != nil {
			return protocol.Observation{}, err
		}
		renderedNotice, noticeFound, err := articleAdminRenderedAdminNotice(notice.body)
		if err != nil || !noticeFound {
			return protocol.Observation{}, fmt.Errorf("observe Article Admin publish notice: found=%t: %w", noticeFound, err)
		}
		after, found, err := fixture.service.Get(ctx, unselectedID)
		if err != nil || !found {
			return protocol.Observation{}, fmt.Errorf("read unselected Article after publish: found=%t: %w", found, err)
		}
		rows, err := articleAdminRows(ctx, fixture.raw)
		if err != nil {
			return protocol.Observation{}, err
		}
		publishedSelected := 0
		for _, row := range rows {
			if (row.ID == selectedIDs[0] || row.ID == selectedIDs[1]) && row.Published {
				publishedSelected++
			}
		}
		if publishedSelected != renderedNotice.affected {
			return protocol.Observation{}, fmt.Errorf("Article Admin publish durable affected count = %d, notice = %d", publishedSelected, renderedNotice.affected)
		}
		counts := fixture.observed.snapshot()
		publishEntries := fixture.audit.All()
		if len(publishEntries) != renderedNotice.affected {
			return protocol.Observation{}, fmt.Errorf("Article Admin publish audit count = %d, affected = %d", len(publishEntries), renderedNotice.affected)
		}
		for index, entry := range publishEntries {
			if entry.Action != admin.ActionPublish || entry.ActorID != "staff" ||
				!reflect.DeepEqual(entry.ChangedFields, []string{"published"}) || entry.ObjectID != selectedIDs[index] {
				return protocol.Observation{}, fmt.Errorf("Article Admin publish audit entry %d is inconsistent", index)
			}
		}
		state := databaseState(rows)
		messages := make([]protocol.Value, 0, 1)
		if level, normalized := articleAdminNormalizedMessageLevel(renderedNotice); normalized {
			messages = append(messages, protocol.Object(map[string]protocol.Value{
				"affected_count_present": protocol.Boolean(true),
				"level":                  articleAdminInt(level),
				"published_tag":          protocol.Boolean(renderedNotice.tag == "published"),
			}))
		}
		result := protocol.Object(map[string]protocol.Value{
			"affected":             articleAdminInt(renderedNotice.affected),
			"messages":             protocol.List(messages...),
			"redirect":             protocol.String(articleAdminLocationCategory(action)),
			"selected_ids":         articleAdminPrimaryKeyList(selectedIDs),
			"unselected_unchanged": protocol.Boolean(reflect.DeepEqual(before, after)),
		})
		metrics := protocol.Object(map[string]protocol.Value{
			// Publish is the only Article action and its callback crosses the
			// observed db.Atomic port exactly once. HTTP request count would also
			// include list/notice requests and is not an action-call observation.
			"action_calls":  articleAdminInt(counts.atomic),
			"atomic_blocks": articleAdminInt(counts.atomic),
			"messages":      articleAdminInt(len(messages)),
		})
		return articleAdminObservation(contract, result, valuePointer(state), metrics), nil
	})
}
