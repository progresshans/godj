//go:build darwin || linux

package runserverproduct_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/progresshans/godj/api"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

var (
	runserverAdminLoginCSRFPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)
	runserverMaskedCSRFPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{128}$`)
)

func TestRunserverPublicOnlyEnvironmentsDiscardAmbientArticleCredentials(t *testing.T) {
	t.Setenv(articleAdminUsernameEnv, "ambient-username-marker")
	t.Setenv(articleAdminPasswordEnv, "ambient-password-marker")

	environments := map[string][]string{
		"SQLite": runserverEnvironment(
			t,
			filepath.Join(t.TempDir(), "article.sqlite3"),
			t.TempDir(),
		),
		"PostgreSQL": runserverPostgresEnvironment(
			t,
			"postgresql://article.invalid/article",
			"article_runtime",
			t.TempDir(),
		),
	}
	for backend, environment := range environments {
		values := environmentMap(environment)
		for _, name := range []string{articleAdminUsernameEnv, articleAdminPasswordEnv} {
			if _, exists := values[name]; exists {
				t.Errorf("%s public-only runserver environment retained %s", backend, name)
			}
		}
	}
}

func TestGlobalRunserverPublishesAuthenticatedArticleAdminAndAPI(t *testing.T) {
	const (
		username = "runserver-publication-admin"
		password = "runserver-publication-secret-7Vq_2zP"
	)
	repository := runserverRepositoryRoot(t)
	articleRoot := filepath.Join(repository, "examples", "article")
	descriptor := filepath.Join(articleRoot, "godj.toml")
	databasePath := filepath.Join(t.TempDir(), "article.sqlite3")
	prepareRunserverArticleDatabase(t, repository, databasePath)
	globalBinary := buildGlobalGodj(t, repository)

	workspaceBase := filepath.Join(t.TempDir(), "runserver-workspaces")
	if err := os.Mkdir(workspaceBase, 0o700); err != nil {
		t.Fatal(err)
	}
	values := environmentMap(runserverEnvironment(t, databasePath, workspaceBase))
	environment := sortedRunserverEnvironment(values)
	assertRunserverOperatorEnvironment(t, environment, password)
	before := snapshotRunserverProjectTree(t, articleRoot)
	provisionRunserverOperator(t, globalBinary, repository, descriptor, environment, username, password)
	assertRunserverWorkspaceEmpty(t, workspaceBase)
	environment, goAuditLog := runserverGoAuditEnvironment(t, environment)
	address := reserveRunserverLoopbackAddress(t, "")
	sensitiveValues := []string{username, password}

	runGlobalArticleServer(
		t,
		globalBinary,
		repository,
		descriptor,
		address,
		environment,
		&sensitiveValues,
		func(readyAddress string) error {
			return exerciseAuthenticatedRunserverArticle(
				readyAddress,
				username,
				password,
				&sensitiveValues,
			)
		},
	)

	assertRunserverWorkspaceEmpty(t, workspaceBase)
	verifyRunserverArticleDatabase(t, databasePath)
	after := snapshotRunserverProjectTree(t, articleRoot)
	if !runserverProjectTreesEqual(before, after) {
		t.Fatal("authenticated global runserver changed the Article project tree")
	}
	assertRunserverGoBuildAudit(t, goAuditLog, []string{"./cmd/projectrunner", "./cmd/site"})
}

const maximumRunserverProvisionOutput = 16 << 10

type runserverProvisionOutput struct {
	mutex     sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (output *runserverProvisionOutput) Write(payload []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	remaining := maximumRunserverProvisionOutput - output.buffer.Len()
	if remaining > 0 {
		kept := payload
		if len(kept) > remaining {
			kept = kept[:remaining]
		}
		_, _ = output.buffer.Write(kept)
	}
	if len(payload) > remaining {
		output.truncated = true
	}
	return len(payload), nil
}

func (output *runserverProvisionOutput) snapshot() (string, bool) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.buffer.String(), output.truncated
}

func provisionRunserverOperator(
	t *testing.T,
	globalBinary, repository, descriptor string,
	environment []string,
	username, password string,
) {
	t.Helper()
	assertRunserverOperatorEnvironment(t, environment, password)

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("open createsuperuser PTY: %v", err)
	}
	defer func() { _ = master.Close() }()

	command := exec.Command(globalBinary, "createsuperuser", "--project", descriptor)
	command.Dir = repository
	command.Env = append([]string(nil), environment...)
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	if err := command.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start createsuperuser: %v", err)
	}
	if err := slave.Close(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("close parent createsuperuser PTY slave: %v", err)
	}

	output := &runserverProvisionOutput{}
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(output, master)
		close(drained)
	}()
	waited := make(chan error, 1)
	exited := make(chan struct{})
	var terminalWaitErr error
	go func() {
		terminalWaitErr = command.Wait()
		waited <- terminalWaitErr
		close(exited)
	}()
	finished := false
	defer func() {
		if finished {
			return
		}
		groups, _ := runserverOwnedProcessGroups(command.Process.Pid)
		_ = interruptAndWaitRunserver(command, waited, 5*time.Second, groups...)
		_ = master.Close()
		select {
		case <-drained:
		case <-time.After(2 * time.Second):
		}
	}()

	awaitRunserverProvisionPrompt(t, output, exited, &terminalWaitErr, "Username: ")
	writeRunserverProvisionLine(t, master, username)
	awaitRunserverProvisionPrompt(t, output, exited, &terminalWaitErr, "Password: ")
	writeRunserverProvisionLine(t, master, password)
	awaitRunserverProvisionPrompt(t, output, exited, &terminalWaitErr, "Password (again): ")
	writeRunserverProvisionLine(t, master, password)

	select {
	case <-exited:
		waitErr := <-waited
		if waitErr != nil {
			t.Fatalf("createsuperuser exited unsuccessfully: %v", waitErr)
		}
		finished = true
	case <-time.After(2 * time.Minute):
		t.Fatal("createsuperuser timed out")
	}
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		_ = master.Close()
		select {
		case <-drained:
		case <-time.After(time.Second):
			t.Fatal("createsuperuser PTY did not drain")
		}
	}
	transcript, truncated := output.snapshot()
	if truncated || !strings.Contains(transcript, "Username: ") ||
		!strings.Contains(transcript, "Password: ") ||
		!strings.Contains(transcript, "Password (again): ") ||
		!strings.Contains(transcript, `{"status":"created"}`) {
		t.Fatalf("createsuperuser transcript shape = prompts:%t/%t/%t success:%t bytes:%d truncated:%t",
			strings.Contains(transcript, "Username: "),
			strings.Contains(transcript, "Password: "),
			strings.Contains(transcript, "Password (again): "),
			strings.Contains(transcript, `{"status":"created"}`),
			len(transcript),
			truncated,
		)
	}
	if strings.Contains(transcript, password) {
		t.Fatal("createsuperuser PTY exposed the raw password")
	}
	if err := waitForRunserverProcessGroupsAbsent([]int{command.Process.Pid}, 2*time.Second); err != nil {
		t.Fatalf("createsuperuser process group remained: %v", err)
	}
}

func awaitRunserverProvisionPrompt(
	t *testing.T,
	output *runserverProvisionOutput,
	exited <-chan struct{},
	waitErr *error,
	marker string,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		transcript, truncated := output.snapshot()
		if strings.Contains(transcript, marker) {
			return
		}
		if truncated {
			t.Fatalf("createsuperuser output exceeded its bound before %q", marker)
		}
		select {
		case <-exited:
			t.Fatalf("createsuperuser exited before %q: %v", marker, *waitErr)
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("createsuperuser did not publish %q", marker)
		}
	}
}

func writeRunserverProvisionLine(t *testing.T, terminal *os.File, value string) {
	t.Helper()
	payload := []byte(value + "\n")
	owned := payload
	defer clear(owned)
	for len(payload) != 0 {
		written, err := terminal.Write(payload)
		if err != nil || written <= 0 || written > len(payload) {
			t.Fatalf("write createsuperuser terminal input: bytes=%d error=%t", written, err != nil)
		}
		payload = payload[written:]
	}
}

func assertRunserverOperatorEnvironment(t *testing.T, environment []string, password string) {
	t.Helper()
	values := environmentMap(environment)
	for _, name := range []string{articleAdminUsernameEnv, articleAdminPasswordEnv} {
		if _, exists := values[name]; exists {
			t.Fatalf("runserver environment retained retired startup credential %s", name)
		}
	}
	for _, entry := range environment {
		if password != "" && strings.Contains(entry, password) {
			t.Fatal("runserver environment exposed the raw operator password")
		}
	}
}

func exerciseAuthenticatedRunserverArticle(
	address, username, password string,
	sensitiveValues *[]string,
) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return errors.New("create authenticated runserver cookie jar")
	}
	baseURL := "http://" + address
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return errors.New("parse authenticated runserver base URL")
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()

	public, err := requestAuthenticatedRunserverEventually(
		client,
		http.MethodGet,
		baseURL+articleListPath,
		"",
		"",
	)
	if err != nil {
		return fmt.Errorf("request public Article route: %w", err)
	}
	if public.status != http.StatusOK || public.contentType != "text/html; charset=utf-8" ||
		!bytes.Contains(public.body, []byte(`data-article-id="1"`)) ||
		!bytes.Contains(public.body, []byte("Go Launch")) {
		return fmt.Errorf(
			"public Article route status/content-type/body-shape = %d/%t/%t",
			public.status,
			public.contentType == "text/html; charset=utf-8",
			len(public.body) > 0,
		)
	}

	anonymous, err := requestAuthenticatedRunserver(
		client,
		http.MethodGet,
		baseURL+"/api/articles/",
		api.JSONContentType,
		"",
	)
	if err != nil {
		return fmt.Errorf("request anonymous Article API: %w", err)
	}
	if anonymous.status != http.StatusForbidden || anonymous.contentType != api.JSONContentType ||
		!bytes.Equal(anonymous.body, []byte(`{"code":"not_authenticated","errors":[]}`)) ||
		anonymous.header.Get("Location") != "" || anonymous.header.Get("WWW-Authenticate") != "" {
		return fmt.Errorf(
			"anonymous Article API status/content-type/body-bytes = %d/%t/%d",
			anonymous.status,
			anonymous.contentType == api.JSONContentType,
			len(anonymous.body),
		)
	}

	loginPage, err := requestAuthenticatedRunserver(
		client,
		http.MethodGet,
		baseURL+"/admin/login/",
		"",
		"",
	)
	if err != nil {
		return fmt.Errorf("request Admin login page: %w", err)
	}
	if loginPage.status != http.StatusOK || loginPage.contentType != "text/html; charset=utf-8" {
		return fmt.Errorf("Admin login page status/content-type = %d/%t", loginPage.status, loginPage.contentType == "text/html; charset=utf-8")
	}
	match := runserverAdminLoginCSRFPattern.FindSubmatch(loginPage.body)
	if len(match) != 2 {
		return errors.New("Admin login page omitted the masked CSRF token")
	}
	preLoginToken := string(match[1])
	preLoginCSRF, err := uniqueAuthenticatedRunserverCookie(loginPage.cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil {
		return fmt.Errorf("Admin login CSRF cookie: %w", err)
	}
	if !preLoginCSRF.HttpOnly || preLoginCSRF.Path != "/" || preLoginCSRF.Value == "" ||
		preLoginCSRF.Value == preLoginToken {
		return errors.New("Admin login page published an unsafe CSRF cookie")
	}
	if value := authenticatedRunserverJarCookie(jar, parsedBaseURL, websessionauth.DefaultCSRFCookieName); value != preLoginCSRF.Value {
		return errors.New("Admin login CSRF cookie was not retained by the browser jar")
	}
	*sensitiveValues = append(*sensitiveValues, preLoginToken, preLoginCSRF.Value)

	form := url.Values{
		"csrfmiddlewaretoken": {preLoginToken},
		"username":            {username},
		"password":            {password},
		"next":                {"/admin/articles/"},
	}
	login, err := requestAuthenticatedRunserver(
		client,
		http.MethodPost,
		baseURL+"/admin/login/",
		"",
		form.Encode(),
	)
	if err != nil {
		return fmt.Errorf("submit Admin login: %w", err)
	}
	if login.status != http.StatusFound || login.header.Get("Location") != "/admin/articles/" ||
		!bytes.Equal(login.body, []byte("Found\n")) {
		return fmt.Errorf("Admin login status/location/body-bytes = %d/%t/%d", login.status, login.header.Get("Location") == "/admin/articles/", len(login.body))
	}
	sessionCookie, err := uniqueAuthenticatedRunserverCookie(login.cookies, websessionauth.DefaultSessionCookieName)
	if err != nil {
		return fmt.Errorf("Admin login session cookie: %w", err)
	}
	rotatedCSRF, err := uniqueAuthenticatedRunserverCookie(login.cookies, websessionauth.DefaultCSRFCookieName)
	if err != nil {
		return fmt.Errorf("Admin rotated CSRF cookie: %w", err)
	}
	if !sessionCookie.HttpOnly || sessionCookie.Path != "/" || sessionCookie.Value == "" ||
		!rotatedCSRF.HttpOnly || rotatedCSRF.Path != "/" || rotatedCSRF.Value == "" ||
		rotatedCSRF.Value == preLoginCSRF.Value || rotatedCSRF.Value == preLoginToken {
		return errors.New("Admin login did not publish safe session and rotated CSRF cookies")
	}
	if authenticatedRunserverJarCookie(jar, parsedBaseURL, websessionauth.DefaultSessionCookieName) != sessionCookie.Value ||
		authenticatedRunserverJarCookie(jar, parsedBaseURL, websessionauth.DefaultCSRFCookieName) != rotatedCSRF.Value {
		return errors.New("Admin login cookies were not retained by the browser jar")
	}
	*sensitiveValues = append(*sensitiveValues, sessionCookie.Value, rotatedCSRF.Value)

	authenticated, err := requestAuthenticatedRunserver(
		client,
		http.MethodGet,
		baseURL+"/api/articles/",
		api.JSONContentType,
		"",
	)
	if err != nil {
		return fmt.Errorf("request authenticated Article API: %w", err)
	}
	if authenticated.status != http.StatusOK || authenticated.contentType != api.JSONContentType {
		return fmt.Errorf("authenticated Article API status/content-type = %d/%t", authenticated.status, authenticated.contentType == api.JSONContentType)
	}
	freshCSRF := authenticated.header.Get(websessionauth.DefaultCSRFHeader)
	*sensitiveValues = append(*sensitiveValues, freshCSRF)
	if !runserverMaskedCSRFPattern.MatchString(freshCSRF) || freshCSRF == preLoginToken ||
		freshCSRF == preLoginCSRF.Value || freshCSRF == rotatedCSRF.Value || freshCSRF == sessionCookie.Value {
		return errors.New("authenticated Article API did not publish a fresh masked CSRF token")
	}
	var page struct {
		Count   int64 `json:"count"`
		Results []struct {
			ID int64 `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(authenticated.body, &page); err != nil {
		return errors.New("decode authenticated Article API response")
	}
	if page.Count != 9 || len(page.Results) != 2 || page.Results[0].ID != 1 || page.Results[1].ID != 2 {
		return fmt.Errorf("authenticated Article API page shape = count %d results %d", page.Count, len(page.Results))
	}
	return nil
}

type authenticatedRunserverHTTPResult struct {
	status      int
	contentType string
	header      http.Header
	cookies     []*http.Cookie
	body        []byte
}

func requestAuthenticatedRunserverEventually(
	client *http.Client,
	method, target, accept, form string,
) (authenticatedRunserverHTTPResult, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err := requestAuthenticatedRunserver(client, method, target, accept, form)
		if err == nil || time.Now().After(deadline) {
			return result, err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requestAuthenticatedRunserver(
	client *http.Client,
	method, target, accept, form string,
) (authenticatedRunserverHTTPResult, error) {
	var body io.Reader
	if form != "" {
		body = strings.NewReader(form)
	}
	request, err := http.NewRequest(method, target, body)
	if err != nil {
		return authenticatedRunserverHTTPResult{}, errors.New("construct authenticated runserver request")
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if form != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := client.Do(request)
	if err != nil {
		return authenticatedRunserverHTTPResult{}, fmt.Errorf("perform authenticated runserver request: %w", err)
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	closeErr := response.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return authenticatedRunserverHTTPResult{}, errors.New("read authenticated runserver response")
	}
	if len(payload) > 1<<20 {
		return authenticatedRunserverHTTPResult{}, errors.New("authenticated runserver response exceeded one mebibyte")
	}
	return authenticatedRunserverHTTPResult{
		status:      response.StatusCode,
		contentType: response.Header.Get("Content-Type"),
		header:      response.Header.Clone(),
		cookies:     response.Cookies(),
		body:        payload,
	}, nil
}

func uniqueAuthenticatedRunserverCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	var found *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("response published duplicate %s cookies", name)
		}
		clone := *cookie
		found = &clone
	}
	if found == nil {
		return nil, fmt.Errorf("response omitted the %s cookie", name)
	}
	return found, nil
}

func authenticatedRunserverJarCookie(jar http.CookieJar, target *url.URL, name string) string {
	for _, cookie := range jar.Cookies(target) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}
