//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

type reloadArticleClient struct {
	client    *http.Client
	base      string
	csrf      string
	sensitive *[]string
}

func newReloadArticleClient(t *testing.T, process *reloadProcess, username, password string, sensitive *[]string) *reloadArticleClient {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &reloadArticleClient{client: &http.Client{Jar: jar, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{DisableKeepAlives: true}}, base: "http://" + process.address, sensitive: sensitive}
	t.Cleanup(client.client.CloseIdleConnections)
	login, err := requestAuthenticatedRunserverEventually(client.client, "GET", client.base+"/admin/login/", "", "")
	if err != nil || login.status != 200 {
		t.Fatal("reload login page", login.status, err)
	}
	match := runserverAdminLoginCSRFPattern.FindSubmatch(login.body)
	if len(match) != 2 {
		t.Fatal("reload login has no CSRF token")
	}
	*sensitive = append(*sensitive, password, string(match[1]))
	logged, err := requestAuthenticatedRunserver(client.client, "POST", client.base+"/admin/login/", "", url.Values{
		"username": {username}, "password": {password}, "csrfmiddlewaretoken": {string(match[1])}, "next": {"/admin/"},
	}.Encode())
	if err != nil || logged.status != 302 {
		t.Fatal("reload login", logged.status, err)
	}
	for _, cookie := range logged.cookies {
		*sensitive = append(*sensitive, cookie.Value)
	}
	return client
}

func (client *reloadArticleClient) request(t *testing.T, method, path, body, revision string, csrf bool) authenticatedRunserverHTTPResult {
	t.Helper()
	request, err := http.NewRequest(method, client.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if revision != "" {
		request.Header.Set("If-Revision", revision)
	}
	if csrf {
		request.Header.Set(websessionauth.DefaultCSRFHeader, client.csrf)
	}
	response, err := client.client.Do(request)
	if err != nil {
		t.Fatal("reload authenticated HTTP", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > 1<<20 {
		t.Fatal("reload HTTP body", readErr, closeErr)
	}
	if token := response.Header.Get(websessionauth.DefaultCSRFHeader); token != "" {
		if !runserverMaskedCSRFPattern.MatchString(token) {
			t.Fatal("reload CSRF token is invalid")
		}
		client.csrf = token
		*client.sensitive = append(*client.sensitive, token)
	}
	for _, cookie := range response.Cookies() {
		*client.sensitive = append(*client.sensitive, cookie.Value)
	}
	return authenticatedRunserverHTTPResult{status: response.StatusCode, header: response.Header.Clone(), body: data}
}

func (p *reloadProcess) articleMarker(t *testing.T, marker string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	p.await(t, "embedded Article template "+marker, func() bool {
		response, err := client.Get("http://" + p.address + articleListPath)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return err == nil && response.StatusCode == 200 && bytes.Contains(data, []byte(marker))
	})
	p.track(t)
}

func TestGlobalReloadPreservesArticleSessionsRelationsAndCurrentPermissions(t *testing.T) {
	observer := newDevelopmentObserver(t, "article")
	inputs := observer.inputs
	repository := runserverRepositoryRoot(t)
	global := buildGlobalGodj(t, repository)
	project := prepareCopiedRunserverArticleProject(t, repository, false)
	const module = "example.com/godj-runserver-fixture"
	// This is a separate module with its own declarations, generated bundle,
	// routes and templates. Only public framework imports remain external.
	err := filepath.WalkDir(project, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(name) != ".go" {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		// Rebase authored declarations and consumers. The public generator
		// owns replacement of its existing files and validates their prior
		// bytes/modes; editing those files would correctly reject publication.
		if ast.IsGenerated(parsed) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data = bytes.ReplaceAll(data, []byte("github.com/progresshans/godj/examples/article/"), []byte(module+"/"))
		return os.WriteFile(name, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	environment := runserverEnvironment(t, filepath.Join(t.TempDir(), "reload-article.sqlite3"), workspace)
	for _, args := range [][]string{{"generate"}, {"makemigrations"}, {"migrate"}} {
		reloadCLI(t, global, project, environment, args...)
	}
	const adminName, adminPassword = "reload-admin", "Quartz-91!Orbit-Secret"
	provisionRunserverOperator(t, global, project, filepath.Join(project, "godj.toml"), environment, adminName, adminPassword)
	const templateName = "webapp/templates/article_list.html"
	template, err := os.ReadFile(filepath.Join(project, templateName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(template, []byte("</body>")) != 1 {
		t.Fatal("Article template has no unique document end")
	}
	versionTemplate := func(marker string) string {
		return strings.Replace(string(template), "</body>", "<p>"+marker+"</p>\n</body>", 1)
	}
	writeReloadFile(t, project, templateName, versionTemplate("reload-article-one"))
	p := startReloadProcess(t, global, project, environment, "article site listening on http://", true)
	p.articleMarker(t, "reload-article-one")
	sensitive := []string{adminPassword}
	admin := newReloadArticleClient(t, p, adminName, adminPassword, &sensitive)
	if got := admin.request(t, "GET", "/api/articles/", "", "", false); got.status != 200 {
		t.Fatal("initial admin admission", got.status)
	}
	articleInput, err := json.Marshal(map[string]any{"title": inputs.Article.Title, "published": true, "summary": "durable"})
	if err != nil {
		t.Fatal(err)
	}
	csrf := admin.request(t, "POST", "/api/articles/", string(articleInput), "", false)
	if csrf.status != 403 {
		t.Fatal("CSRF-less write admitted", csrf.status)
	}
	observer.record(t, "csrf", map[string]any{"status": csrf.status})
	created := admin.request(t, "POST", "/api/articles/", string(articleInput), "", true)
	var article struct{ ID int64 }
	if created.status != 201 || json.Unmarshal(created.body, &article) != nil || article.ID <= 0 {
		t.Fatal("initial Article write", created.status)
	}
	permissionID := int64(0)
	for offset, pages := 0, 0; ; pages++ {
		if pages >= 20 {
			t.Fatal("permission inventory exceeded its bound")
		}
		page := admin.request(t, "GET", "/api/identity/permissions/?limit=100&offset="+strconv.Itoa(offset), "", "", false)
		var values struct {
			Count         *int
			Offset, Limit int
			Items         []struct {
				ID   int64
				Code string
			}
		}
		if page.status != 200 || json.Unmarshal(page.body, &values) != nil || values.Count == nil ||
			values.Offset != offset || values.Limit != 100 || values.Items == nil ||
			len(values.Items) > values.Limit || *values.Count < offset+len(values.Items) {
			t.Fatal("permission page", page.status)
		}
		for _, value := range values.Items {
			if value.Code == "godj_conformance.view_article" {
				permissionID = value.ID
			}
		}
		offset += len(values.Items)
		if offset == *values.Count {
			break
		}
		if len(values.Items) == 0 {
			t.Fatal("permission page did not advance")
		}
	}
	if permissionID == 0 {
		created := admin.request(t, "POST", "/api/identity/permissions/", `{"code":"godj_conformance.view_article","name":"View reload articles"}`, "", true)
		var permission struct{ ID int64 }
		if created.status != 201 || json.Unmarshal(created.body, &permission) != nil || permission.ID <= 0 {
			t.Fatal("permission creation", created.status)
		}
		permissionID = permission.ID
	}
	groupInput, err := json.Marshal(map[string]any{"name": inputs.Article.Group, "permissions": []int64{permissionID}})
	if err != nil {
		t.Fatal(err)
	}
	groupJSON := string(groupInput)
	groupResponse := admin.request(t, "POST", "/api/identity/groups/", groupJSON, "", true)
	var group struct {
		ID, Revision int64
		Permissions  []int64
		Name         string
	}
	if groupResponse.status != 201 || json.Unmarshal(groupResponse.body, &group) != nil || group.ID <= 0 || group.Revision != 1 || group.Name != inputs.Article.Group || len(group.Permissions) != 1 || group.Permissions[0] != permissionID {
		t.Fatal("group relation creation", groupResponse.status)
	}
	const readerPassword = "Reader-72p!sV9-secret"
	sensitive = append(sensitive, readerPassword)
	userJSON := `{"username":"reload-reader","password":"` + readerPassword + `","staff":true,"groups":[` + strconv.FormatInt(group.ID, 10) + `]}`
	userResponse := admin.request(t, "POST", "/api/identity/users/", userJSON, "", true)
	var user struct {
		ID     int64
		Groups []int64
	}
	if userResponse.status != 201 || json.Unmarshal(userResponse.body, &user) != nil || user.ID <= 0 || len(user.Groups) != 1 || user.Groups[0] != group.ID {
		t.Fatal("user-group relation creation", userResponse.status)
	}
	reader := newReloadArticleClient(t, p, "reload-reader", readerPassword, &sensitive)
	if got := reader.request(t, "GET", "/api/articles/", "", "", false); got.status != 200 {
		t.Fatal("inherited Article view grant", got.status)
	}
	if got := reader.request(t, "POST", "/api/articles/", `{"title":"forbidden","published":true}`, "", true); got.status != 403 {
		t.Fatal("view-only member acquired write authority", got.status)
	}

	runPhase := func(name string, action func(*testing.T)) {
		t.Helper()
		if !t.Run(name, action) {
			t.FailNow()
		}
	}
	runPhase("failed-template-keeps-current-authenticated-app", func(t *testing.T) {
		before := strings.Count(p.stderr.String(), "project_runserver_reload_rejected/project_runtime_readiness_failed\n")
		writeReloadFile(t, project, templateName, "{{")
		p.rejected(t, "project_runtime_readiness_failed", before)
		p.articleMarker(t, "reload-article-one")
		if got := reader.request(t, "GET", "/api/articles/", "", "", false); got.status != 200 {
			t.Fatal("failed startup discarded current session", got.status)
		}
	})
	runPhase("embedded-change-keeps-address-session-and-relations", func(t *testing.T) {
		writeReloadFile(t, project, templateName, versionTemplate("reload-article-two"))
		p.articleMarker(t, "reload-article-two")
		got := reader.request(t, "GET", "/api/articles/"+strconv.FormatInt(article.ID, 10)+"/", "", "", false)
		var stored struct{ Title string }
		if got.status != 200 || json.Unmarshal(got.body, &stored) != nil || stored.Title != inputs.Article.Title {
			t.Fatal("new runtime lost session or durable Article", got.status)
		}
		detail := admin.request(t, "GET", "/api/identity/users/"+strconv.FormatInt(user.ID, 10)+"/", "", "", false)
		var current struct{ Groups []int64 }
		if detail.status != 200 || json.Unmarshal(detail.body, &current) != nil || len(current.Groups) != 1 || current.Groups[0] != group.ID {
			t.Fatal("reload changed membership", detail.status)
		}
		observer.record(t, "session_relation", map[string]any{"status": got.status,
			"member_present": current.Groups[0] == group.ID && group.Name == inputs.Article.Group, "stored_title": stored.Title})
		if got := reader.request(t, "POST", "/api/articles/", `{"title":"still forbidden","published":true}`, "", true); got.status != 403 {
			t.Fatal("reload broadened permission", got.status)
		}
	})
	runPhase("permission-revisions-remain-current-after-reload", func(t *testing.T) {
		path := "/api/identity/groups/" + strconv.FormatInt(group.ID, 10) + "/"
		if got := admin.request(t, "PATCH", path, `{"permissions":[]}`, "1", false); got.status != 403 {
			t.Fatal("reload lost transport CSRF", got.status)
		}
		removed := admin.request(t, "PATCH", path, `{"permissions":[]}`, "1", true)
		if removed.status != 200 || removed.header.Get("Revision") != "2" {
			t.Fatal("relation revision update", removed.status)
		}
		revoked := reader.request(t, "GET", "/api/articles/", "", "", false)
		if revoked.status != 403 {
			t.Fatal("session retained revoked group authority", revoked.status)
		}
		writeReloadFile(t, project, templateName, versionTemplate("reload-article-three"))
		p.articleMarker(t, "reload-article-three")
		afterReload := reader.request(t, "GET", "/api/articles/", "", "", false)
		if afterReload.status != 403 {
			t.Fatal("new runtime restored revoked authority", afterReload.status)
		}
		// The default development app uses a process-local CSRF signing key.
		// Its persistent login survives reload; an old form token must not.
		if got := admin.request(t, "PATCH", path, groupJSON, "1", true); got.status != http.StatusForbidden || !bytes.Contains(got.body, []byte(`"csrf_rejected"`)) {
			t.Fatal("new runtime admitted a previous-process CSRF token", got.status)
		}
		if got := admin.request(t, "GET", path, "", "", false); got.status != http.StatusOK || got.header.Get(websessionauth.DefaultCSRFHeader) == "" {
			t.Fatal("persistent session could not refresh its CSRF token", got.status)
		}
		if got := admin.request(t, "PATCH", path, groupJSON, "1", true); got.status != http.StatusPreconditionFailed {
			t.Fatal("stale relation revision was not rejected by its precondition", got.status)
		}
		if got := admin.request(t, "PATCH", path, groupJSON, "2", true); got.status != 200 || got.header.Get("Revision") != "3" {
			t.Fatal("current relation revision failed", got.status)
		}
		restored := reader.request(t, "GET", "/api/articles/", "", "", false)
		if restored.status != 200 {
			t.Fatal("current restored grant unavailable", restored.status)
		}
		observer.record(t, "current_permission", map[string]any{"revoked": revoked.status, "after_reload": afterReload.status, "restored": restored.status})
	})
	p.stop(t, syscall.SIGINT)
	assertRunserverWorkspaceEmpty(t, workspace)
	assertRunserverOutputExcludesSensitiveValues(t, p.stdout.String(), p.stderr.String(), &sensitive)
	observer.publish(t)
}
