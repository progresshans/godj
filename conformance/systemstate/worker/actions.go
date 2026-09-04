package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/examples/article/articleapp"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

var maskedCSRFPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{128}$`)
var adminCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func initialize(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	backend, err := openObservedBackend(ctx, request.Database)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = backend.Close() }()
	applied, err := migrateArticleAndSystem(ctx, request, backend)
	if err != nil {
		return response, SecretBundle{}, err
	}
	site, err := composeWorkerSite(ctx, backend, request.Username, request.Password)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.MigrationApplied = applied
	response.Ready = site.runtime != nil && site.app != nil
	response.Status = http.StatusOK
	response, err = populateCounts(ctx, response, site.runtime)
	return response, SecretBundle{}, err
}

func authenticate(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	principal, err := site.runtime.Authenticator().Authenticate(ctx, request.Username, request.Password)
	if err != nil {
		return response, SecretBundle{}, fail(errorApplication)
	}
	applyPrincipal(&response, principal)
	response.Status = http.StatusOK
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	return response, SecretBundle{}, err
}

func login(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	manager, err := sessions.NewManager(site.runtime.SessionStore(), sessions.Config{})
	if err != nil {
		return response, SecretBundle{}, fail(errorApplication)
	}
	seed, err := manager.Create(ctx, nil)
	if err != nil {
		return response, SecretBundle{}, fail(errorPersistence)
	}
	seedCookie := seed.ID().Encoded()
	harness, err := newHTTPHarness(site.app, CookieBundle{Session: seedCookie})
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()

	firstStatus, err := loginOnce(ctx, harness, request.Username, request.Password, workerAdminBasePath+"/login/?next=/admin/articles/")
	if err != nil || firstStatus != http.StatusFound {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	firstCookies := harness.cookies()
	if firstCookies.Session == "" || firstCookies.CSRF == "" || firstCookies.Session == seedCookie {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	seedRemoved, err := sessionRemoved(ctx, site.runtime, seedCookie)
	if err != nil {
		return response, SecretBundle{}, err
	}

	secondStatus, err := loginOnce(ctx, harness, request.Username, request.Password, workerAdminBasePath+"/articles/")
	if err != nil || secondStatus != http.StatusFound {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	finalCookies := harness.cookies()
	if finalCookies.Session == "" || finalCookies.CSRF == "" {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	firstRemoved, err := sessionRemoved(ctx, site.runtime, firstCookies.Session)
	if err != nil {
		return response, SecretBundle{}, err
	}
	safe, err := harness.do(ctx, http.MethodGet, "/api/articles/", api.JSONContentType, "", "", "")
	if err != nil || safe.status != http.StatusOK {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	token := safe.header.Get(websessionauth.DefaultCSRFHeader)
	if !maskedCSRFPattern.MatchString(token) {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	response.LoginStatus = secondStatus
	response.Status = secondStatus
	response.Rotated = firstCookies.Session != finalCookies.Session && seedCookie != firstCookies.Session
	response.OldSessionRemoved = seedRemoved && firstRemoved
	response.SameCookieHandoff = harness.cookies() == finalCookies
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	return response, SecretBundle{Cookies: harness.cookies(), Token: token}, nil
}

func loginOnce(
	ctx context.Context,
	harness *httpHarness,
	username, password, tokenSource string,
) (int, error) {
	page, err := harness.do(ctx, http.MethodGet, tokenSource, "", "", "", "")
	if err != nil || page.status != http.StatusOK {
		return page.status, fail(errorHTTP)
	}
	token, err := responseCSRFToken(page.body)
	if err != nil {
		return 0, err
	}
	form := url.Values{
		"csrfmiddlewaretoken": {token},
		"username":            {username},
		"password":            {password},
		"next":                {workerAdminBasePath + "/articles/"},
	}
	result, err := harness.do(
		ctx,
		http.MethodPost,
		workerAdminBasePath+"/login/",
		"",
		"application/x-www-form-urlencoded",
		form.Encode(),
		"",
	)
	if err != nil {
		return 0, err
	}
	return result.status, nil
}

func sessionProbe(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	adminResult, err := harness.do(ctx, http.MethodGet, workerAdminBasePath+"/articles/", "", "", "", "")
	if err != nil {
		return response, SecretBundle{}, err
	}
	apiResult, err := harness.do(ctx, http.MethodGet, "/api/articles/", api.JSONContentType, "", "", "")
	if err != nil {
		return response, SecretBundle{}, err
	}
	probeResult, err := harness.do(ctx, http.MethodGet, workerPrincipalProbePath, api.JSONContentType, "", "", "")
	if err != nil {
		return response, SecretBundle{}, err
	}
	var probe principalProbe
	if probeResult.status != http.StatusOK || json.Unmarshal(probeResult.body, &probe) != nil {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	applyProbe(&response, probe)
	response.Status = probeResult.status
	response.AdminStatus = adminResult.status
	response.APIStatus = apiResult.status
	currentCookies := harness.cookies()
	response.SameCookieHandoff = currentCookies == request.Cookies
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	token := apiResult.header.Get(websessionauth.DefaultCSRFHeader)
	if apiResult.status == http.StatusOK && !maskedCSRFPattern.MatchString(token) {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	return response, SecretBundle{Cookies: currentCookies, Token: token}, nil
}

func logout(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	page, err := harness.do(ctx, http.MethodGet, workerAdminBasePath+"/articles/", "", "", "", "")
	if err != nil || page.status != http.StatusOK {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	token, err := responseCSRFToken(page.body)
	if err != nil {
		return response, SecretBundle{}, err
	}
	result, err := harness.do(
		ctx,
		http.MethodPost,
		workerAdminBasePath+"/logout/",
		"",
		"application/x-www-form-urlencoded",
		url.Values{"csrfmiddlewaretoken": {token}}.Encode(),
		"",
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	removed, err := sessionRemoved(ctx, site.runtime, request.Cookies.Session)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = result.status
	response.AdminStatus = result.status
	response.OldSessionRemoved = removed
	response.Resurrected = !removed
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	return response, SecretBundle{Cookies: harness.cookies()}, nil
}

func oldCookieProbe(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	beforeInserts := site.backend.inserts.Load()
	beforeUpdates := site.backend.updates.Load()
	adminResult, err := harness.do(ctx, http.MethodGet, workerAdminBasePath+"/articles/", "", "", "", "")
	if err != nil {
		return response, SecretBundle{}, err
	}
	apiResult, err := harness.do(ctx, http.MethodGet, "/api/articles/", api.JSONContentType, "", "", "")
	if err != nil {
		return response, SecretBundle{}, err
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(apiResult.body, &envelope) != nil || envelope.Code != string(api.CodeNotAuthenticated) {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	removed, err := sessionRemoved(ctx, site.runtime, request.Cookies.Session)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = apiResult.status
	response.AdminStatus = adminResult.status
	response.APIStatus = apiResult.status
	response.APIErrorCode = envelope.Code
	response.Resurrected = !removed
	response.OldSessionRemoved = removed
	response.ResurrectionWrites = site.backend.inserts.Load() - beforeInserts + site.backend.updates.Load() - beforeUpdates
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	return response, SecretBundle{}, err
}

func csrfIssueAndMutate(
	ctx context.Context,
	request Request,
	response Response,
	title string,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	before, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	safe, err := harness.do(ctx, http.MethodGet, "/api/articles/", api.JSONContentType, "", "", "")
	if err != nil {
		return response, SecretBundle{}, err
	}
	token := safe.header.Get(websessionauth.DefaultCSRFHeader)
	if safe.status != http.StatusOK || !maskedCSRFPattern.MatchString(token) {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	payload, _ := json.Marshal(map[string]string{"title": title})
	mutation, err := harness.do(
		ctx,
		http.MethodPost,
		"/api/articles/",
		api.JSONContentType,
		api.JSONContentType,
		string(payload),
		token,
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	after, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = mutation.status
	response.APIStatus = safe.status
	response.MutationStatus = mutation.status
	response.ArticleRowsBefore = before
	response.ArticleRowsAfter = after
	response.ArticleDelta = after - before
	response.SameCookieHandoff = harness.cookies() == request.Cookies
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	return response, SecretBundle{Cookies: harness.cookies(), Token: token}, nil
}

func csrfStale(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	if !maskedCSRFPattern.MatchString(request.Token) {
		return response, SecretBundle{}, fail(errorInvalidRequest)
	}
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	before, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	mutation, err := harness.do(
		ctx,
		http.MethodPost,
		"/api/articles/",
		api.JSONContentType,
		api.JSONContentType,
		`{"title":"Must not persist"}`,
		request.Token,
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(mutation.body, &envelope) != nil || envelope.Code != string(api.CodeCSRFRejected) {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	after, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = mutation.status
	response.APIStatus = mutation.status
	response.MutationStatus = mutation.status
	response.APIErrorCode = envelope.Code
	response.ArticleRowsBefore = before
	response.ArticleRowsAfter = after
	response.ArticleDelta = after - before
	response.SameCookieHandoff = harness.cookies() == request.Cookies
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	return response, SecretBundle{Cookies: harness.cookies()}, nil
}

func auditFault(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	page, err := harness.do(ctx, http.MethodGet, workerAdminBasePath+"/articles/add/", "", "", "", "")
	if err != nil || page.status != http.StatusOK {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	token, err := responseCSRFToken(page.body)
	if err != nil {
		return response, SecretBundle{}, err
	}
	articleBefore, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	auditBefore, err := countRows(ctx, site.runtime, auditTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	failuresBefore := site.backend.auditFailures.Load()
	site.backend.failNextAudit.Store(true)
	mutation, err := harness.do(
		ctx,
		http.MethodPost,
		workerAdminBasePath+"/articles/add/",
		"",
		"application/x-www-form-urlencoded",
		url.Values{
			"csrfmiddlewaretoken": {token},
			"title":               {"Audit fault rollback"},
			"summary":             {"Must not persist"},
		}.Encode(),
		"",
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	articleAfter, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	auditAfter, err := countRows(ctx, site.runtime, auditTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = mutation.status
	response.AdminStatus = mutation.status
	response.MutationStatus = mutation.status
	response.ArticleRowsBefore = articleBefore
	response.ArticleRowsAfter = articleAfter
	response.AuditRowsBefore = auditBefore
	response.AuditRowsAfter = auditAfter
	response.ArticleDelta = articleAfter - articleBefore
	response.AuditDelta = auditAfter - auditBefore
	response.FaultInjected = site.backend.auditFailures.Load()-failuresBefore == 1
	response.RolledBack = articleBefore == articleAfter && auditBefore == auditAfter
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	return response, SecretBundle{Cookies: harness.cookies()}, nil
}

func historyWrite(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	harness, err := newHTTPHarness(site.app, request.Cookies)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer harness.close()
	articleBefore, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	auditBefore, err := countRows(ctx, site.runtime, auditTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	addPage, err := harness.do(ctx, http.MethodGet, workerAdminBasePath+"/articles/add/", "", "", "", "")
	if err != nil || addPage.status != http.StatusOK {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	addToken, err := responseCSRFToken(addPage.body)
	if err != nil {
		return response, SecretBundle{}, err
	}
	added, err := harness.do(
		ctx,
		http.MethodPost,
		workerAdminBasePath+"/articles/add/",
		"",
		"application/x-www-form-urlencoded",
		url.Values{
			"csrfmiddlewaretoken": {addToken},
			"title":               {"Lifecycle"},
			"summary":             {"Initial"},
		}.Encode(),
		"",
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	repository, err := articleapp.NewRepository(site.runtime)
	if err != nil {
		return response, SecretBundle{}, fail(errorApplication)
	}
	page, err := repository.List(ctx, articleapp.ListOptions{Limit: 1, Ordering: articleapp.IDDescending})
	if err != nil || len(page.Articles) != 1 {
		return response, SecretBundle{}, fail(errorPersistence)
	}
	objectID := page.Articles[0].ID
	changePath := workerAdminBasePath + "/articles/change/?id=" + strconv.FormatInt(objectID, 10)
	changePage, err := harness.do(ctx, http.MethodGet, changePath, "", "", "", "")
	if err != nil || changePage.status != http.StatusOK {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	changeToken, err := responseCSRFToken(changePage.body)
	if err != nil {
		return response, SecretBundle{}, err
	}
	changed, err := harness.do(
		ctx,
		http.MethodPost,
		changePath,
		"",
		"application/x-www-form-urlencoded",
		url.Values{
			"csrfmiddlewaretoken": {changeToken},
			"title":               {"Lifecycle"},
			"published":           {"on"},
			"summary":             {"Changed"},
		}.Encode(),
		"",
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	deletePath := workerAdminBasePath + "/articles/delete/?id=" + strconv.FormatInt(objectID, 10)
	deletePage, err := harness.do(ctx, http.MethodGet, deletePath, "", "", "", "")
	if err != nil || deletePage.status != http.StatusOK {
		return response, SecretBundle{}, fail(errorHTTP)
	}
	deleteToken, err := responseCSRFToken(deletePage.body)
	if err != nil {
		return response, SecretBundle{}, err
	}
	deleted, err := harness.do(
		ctx,
		http.MethodPost,
		deletePath,
		"",
		"application/x-www-form-urlencoded",
		url.Values{
			"csrfmiddlewaretoken": {deleteToken},
			"confirm":             {"yes"},
		}.Encode(),
		"",
	)
	if err != nil {
		return response, SecretBundle{}, err
	}
	articleAfter, err := countRows(ctx, site.runtime, articleTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	auditAfter, err := countRows(ctx, site.runtime, auditTable)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = deleted.status
	response.AddStatus = added.status
	response.ChangeStatus = changed.status
	response.DeleteStatus = deleted.status
	response.ObjectID = objectID
	response.ArticleRowsBefore = articleBefore
	response.ArticleRowsAfter = articleAfter
	response.AuditRowsBefore = auditBefore
	response.AuditRowsAfter = auditAfter
	response.ArticleDelta = articleAfter - articleBefore
	response.AuditDelta = auditAfter - auditBefore
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	if err != nil {
		return response, SecretBundle{}, err
	}
	return response, SecretBundle{Cookies: harness.cookies()}, nil
}

func historyRead(
	ctx context.Context,
	request Request,
	response Response,
) (Response, SecretBundle, error) {
	site, err := openWorkerSite(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	defer func() { _ = site.backend.Close() }()
	service, err := adminapp.NewDurableService(site.runtime, site.runtime)
	if err != nil {
		return response, SecretBundle{}, fail(errorApplication)
	}
	all, err := service.HistoryLimited(ctx, request.ObjectID, 3)
	if err != nil {
		return response, SecretBundle{}, fail(errorPersistence)
	}
	newest, err := service.HistoryLimited(ctx, request.ObjectID, 2)
	if err != nil {
		return response, SecretBundle{}, fail(errorPersistence)
	}
	acceptsNonContiguous, err := nonContiguousAuditProbe(ctx, request)
	if err != nil {
		return response, SecretBundle{}, err
	}
	response.Status = http.StatusOK
	response.ObjectID = request.ObjectID
	response.AuditEvents = auditEvents(all)
	response.NewestEvents = auditEvents(newest)
	response.AuditCount = len(all)
	response.StrictlyIncreasing = auditStrictlyIncreasing(all)
	response.Contiguous = auditContiguous(all)
	response.AcceptsNonContiguous = acceptsNonContiguous
	if len(all) != 0 {
		response.NewestSequence = all[len(all)-1].Sequence
	}
	response.Ready = true
	response, err = populateCounts(ctx, response, site.runtime)
	return response, SecretBundle{}, err
}

func nonContiguousAuditProbe(ctx context.Context, request Request) (bool, error) {
	// Keep the probe beside the primary database so the parent can scan this
	// actual durable artifact for the request credential before cleanup.
	backend, err := openObservedBackend(ctx, request.Database+".gap-probe.sqlite3")
	if err != nil {
		return false, err
	}
	defer func() { _ = backend.Close() }()
	if err := migrateSystemOnly(ctx, backend); err != nil {
		return false, err
	}
	hasher, err := auth.NewDefaultPBKDF2()
	if err != nil {
		return false, fail(errorApplication)
	}
	policy, err := workerCredentialPolicy(hasher, []auth.Permission{articleapp.ArticleViewPermission})
	if err != nil {
		return false, fail(errorApplication)
	}
	if err := systemstate.ProvisionOperator(ctx, backend, systemstate.ProvisionOperatorConfig{
		Username:         request.Username,
		Password:         request.Password,
		CredentialPolicy: policy,
	}); err != nil {
		return false, fail(errorApplication)
	}
	runtime, err := systemstate.OpenExisting(ctx, backend, systemstate.RuntimeConfig{
		CredentialPolicy: policy,
		MaxSessions:      8,
		AuditCapacity:    16,
	})
	if err != nil {
		return false, fail(errorApplication)
	}
	for _, seed := range []struct {
		objectID int64
		action   admin.Action
	}{
		{objectID: 7, action: admin.ActionAdd},
		{objectID: 8, action: admin.ActionAdd},
		{objectID: 7, action: admin.ActionChange},
	} {
		event, err := admin.PrepareEvent(workerPrincipalID, articleModel, seed.objectID, seed.action, nil, "Gap probe")
		if err != nil {
			return false, fail(errorApplication)
		}
		if err := runtime.Atomic(ctx, func(session db.Session) error {
			return runtime.AppendAudit(ctx, session, event)
		}); err != nil {
			return false, fail(errorPersistence)
		}
	}
	history, err := runtime.AuditHistory(ctx, articleModel, 7, 3)
	if err != nil {
		return false, fail(errorPersistence)
	}
	return len(history) == 2 && auditStrictlyIncreasing(history) && !auditContiguous(history), nil
}

func responseCSRFToken(body []byte) (string, error) {
	match := adminCSRFPattern.FindSubmatch(body)
	if len(match) != 2 {
		return "", fail(errorHTTP)
	}
	return string(match[1]), nil
}

func sessionRemoved(ctx context.Context, runtime *systemstate.Runtime, encoded string) (bool, error) {
	id, err := sessions.ParseID(encoded)
	if err != nil {
		return false, fail(errorInvalidRequest)
	}
	_, found, err := runtime.SessionStore().Load(ctx, id)
	if err != nil {
		return false, fail(errorPersistence)
	}
	return !found, nil
}

func applyPrincipal(response *Response, principal auth.Principal) {
	permissions := principal.Permissions()
	response.Permissions = make([]string, len(permissions))
	for index := range permissions {
		response.Permissions[index] = string(permissions[index])
	}
	response.Authenticated = principal.Authenticated()
	response.Active = principal.Active()
	response.Permission = principal.Has(articleapp.ArticleViewPermission)
	response.PrincipalID = principal.ID()
}

func applyProbe(response *Response, probe principalProbe) {
	response.Authenticated = probe.Authenticated
	response.Active = probe.Active
	response.Permission = probe.Permission
	response.PrincipalID = probe.PrincipalID
	response.Permissions = append([]string(nil), probe.Permissions...)
}

func auditEvents(entries []admin.AuditEntry) []AuditEvent {
	result := make([]AuditEvent, len(entries))
	for index := range entries {
		result[index] = AuditEvent{
			Sequence:      entries[index].Sequence,
			ActorID:       entries[index].ActorID,
			Model:         entries[index].Model,
			ObjectID:      entries[index].ObjectID,
			Action:        string(entries[index].Action),
			ChangedFields: append([]string(nil), entries[index].ChangedFields...),
			DisplayLabel:  entries[index].DisplayLabel,
		}
	}
	return result
}

func auditStrictlyIncreasing(entries []admin.AuditEntry) bool {
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Sequence >= entries[index].Sequence {
			return false
		}
	}
	return true
}

func auditContiguous(entries []admin.AuditEntry) bool {
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Sequence+1 != entries[index].Sequence {
			return false
		}
	}
	return true
}
