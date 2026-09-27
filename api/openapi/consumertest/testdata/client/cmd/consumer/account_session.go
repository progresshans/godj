package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"

	ac "example.com/godj-openapi-client/accountsession"
)

type accountEndpoint struct {
	URL           string `json:"url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	NewPassword   string `json:"new_password"`
	Email         string `json:"email"`
	ResetPassword string `json:"reset_password"`
	MailProof     string `json:"mail_proof"`
}
type accountSource struct{ *sessionState }

func (s accountSource) SessionAuth(context.Context, ac.OperationName) (ac.SessionAuth, error) {
	return ac.SessionAuth{APIKey: s.cookie(sessionCookieName)}, nil
}
func (s accountSource) CsrfCookie(context.Context, ac.OperationName) (ac.CsrfCookie, error) {
	return ac.CsrfCookie{APIKey: s.cookie(csrfCookieName)}, nil
}
func (s accountSource) CsrfHeader(context.Context, ac.OperationName) (ac.CsrfHeader, error) {
	return ac.CsrfHeader{APIKey: s.header()}, nil
}

var accountTokenPattern = regexp.MustCompile(`name="csrfmiddlewaretoken" value="([A-Za-z0-9_-]{128})"`)

func accountPage(ctx context.Context, client *http.Client, target accountEndpoint, method, path string, form url.Values) (int, string, error) {
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, target.URL+path, reader)
	if err != nil {
		return 0, "", fail("account request setup")
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", fail("account request transport")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 128<<10))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		return 0, "", fail("account response body")
	}
	for _, secret := range []string{target.Password, target.NewPassword} {
		if strings.Contains(string(body), secret) {
			return 0, "", fail("account password redisplayed")
		}
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		return 0, "", fail("account cache policy")
	}
	if response.StatusCode == 302 && response.Header.Get("Location") != "/account/password/" && response.Header.Get("Location") != "/account/login/" {
		return 0, "", fail("account redirect escaped")
	}
	return response.StatusCode, string(body), nil
}
func accountLogin(ctx context.Context, target accountEndpoint) (*http.Client, *observedTransport, error) {
	client, transport := newHTTPClient()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, transport, fail("account jar")
	}
	client.Jar = jar
	status, body, err := accountPage(ctx, client, target, "GET", "/account/login/", nil)
	match := accountTokenPattern.FindStringSubmatch(body)
	if err != nil || status != 200 || len(match) != 2 {
		return nil, transport, fail("account login form")
	}
	status, _, err = accountPage(ctx, client, target, "POST", "/account/login/", url.Values{"username": {target.Username}, "password": {target.Password}, "next": {"https://attacker.example/"}, "csrfmiddlewaretoken": {match[1]}})
	if err != nil || status != 302 {
		return nil, transport, fail("account ordinary login")
	}
	return client, transport, nil
}
func checkAccountSession(ctx context.Context, target accountEndpoint) error {
	other, otherTransport, err := accountLogin(ctx, target)
	if otherTransport != nil {
		defer otherTransport.base.CloseIdleConnections()
	}
	if err != nil {
		return err
	}
	raw, rawTransport, err := accountLogin(ctx, target)
	if rawTransport != nil {
		defer rawTransport.base.CloseIdleConnections()
	}
	if err != nil {
		return err
	}
	address, err := url.Parse(target.URL)
	if err != nil {
		return fail("account endpoint")
	}
	cookieValue := func(client *http.Client) string {
		for _, c := range client.Jar.Cookies(address) {
			if c.Name == sessionCookieName {
				return c.Value
			}
		}
		return ""
	}
	original, otherID := cookieValue(raw), cookieValue(other)
	if original == "" || otherID == "" || original == otherID {
		return fail("account independent sessions")
	}
	httpClient, transport, state, err := newSessionClient(endpoint{URL: target.URL}, original, "/api/account/password/")
	if err != nil {
		return err
	}
	defer transport.base.CloseIdleConnections()
	state.jar.SetCookies(address, raw.Jar.Cookies(address))
	client, err := ac.NewClient(target.URL, accountSource{state}, ac.WithClient(httpClient))
	if err != nil {
		return fail("account generated setup")
	}
	result, err := client.GodjConformanceAccountAPICsrf(ctx)
	csrf, ok := result.(*ac.GodjConformanceAccountAPICsrfNoContent)
	if err != nil || !ok || !csrf.XGodjCsrftoken.Set || !state.ready(csrf.XGodjCsrftoken.Value) {
		return fail("account generated csrf")
	}
	csrfCookie := state.cookie(csrfCookieName)
	state.setInvalid(true)
	rejected, err := client.GodjConformanceAccountAPIPassword(ctx, &ac.PasswordChange{OldPassword: target.Password, NewPassword: target.NewPassword})
	state.setInvalid(false)
	forbidden, ok := rejected.(*ac.GodjConformanceAccountAPIPasswordForbidden)
	if err != nil || !ok || forbidden.Code != "csrf_rejected" || state.cookie(sessionCookieName) != original {
		return fail("account generated csrf refusal")
	}
	rejected, err = client.GodjConformanceAccountAPIPassword(ctx, &ac.PasswordChange{OldPassword: "wrong", NewPassword: "short"})
	invalid, ok := rejected.(*ac.GodjConformanceAccountAPIPasswordBadRequest)
	if err != nil || !ok || invalid.Code != "validation_error" || len(invalid.Errors) != 2 || invalid.Errors[0].Field != "old_password" || invalid.Errors[0].Code != "password_incorrect" || invalid.Errors[1].Field != "new_password" || invalid.Errors[1].Code != "password_too_short" || state.cookie(sessionCookieName) != original {
		return fail("account generated password errors")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.GodjConformanceAccountAPIPassword(canceled, &ac.PasswordChange{OldPassword: target.Password, NewPassword: target.NewPassword}); !errors.Is(err, context.Canceled) {
		return fail("account generated cancellation")
	}
	previous := original
	for i := 0; i < 2; i++ {
		old := target.Password
		if i == 1 {
			old = target.NewPassword
		}
		changed, err := client.GodjConformanceAccountAPIPassword(ctx, &ac.PasswordChange{OldPassword: old, NewPassword: target.NewPassword})
		committed, ok := changed.(*ac.GodjConformanceAccountAPIPasswordNoContent)
		if err != nil || !ok || committed.SetCookie == "" {
			return fail("account generated committed cookie")
		}
		cookies := (&http.Response{Header: http.Header{"Set-Cookie": {committed.SetCookie}}}).Cookies()
		current := state.cookie(sessionCookieName)
		if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || cookies[0].Value != current || current == previous || state.cookie(csrfCookieName) != csrfCookie {
			return fail("account generated cookie rotation")
		}
		previous = current
	}
	staleHTTP, staleTransport, staleState, err := newSessionClient(endpoint{URL: target.URL}, otherID, "/api/account/csrf/")
	if err != nil {
		return err
	}
	defer staleTransport.base.CloseIdleConnections()
	stale, err := ac.NewClient(target.URL, accountSource{staleState}, ac.WithClient(staleHTTP))
	if err != nil {
		return fail("account stale setup")
	}
	staleResult, err := stale.GodjConformanceAccountAPICsrf(ctx)
	denied, ok := staleResult.(*ac.GodjConformanceAccountAPICsrfForbidden)
	if err != nil || !ok || denied.Code != "not_authenticated" {
		return fail("account generated other session revoked")
	}
	raw.Jar.SetCookies(address, state.jar.Cookies(address))
	status, body, err := accountPage(ctx, raw, target, "GET", "/account/password/done/", nil)
	match := accountTokenPattern.FindStringSubmatch(body)
	if err != nil || status != 200 || len(match) != 2 {
		return fail("account preserved login")
	}
	status, _, err = accountPage(ctx, raw, target, "POST", "/account/logout/", url.Values{"csrfmiddlewaretoken": {match[1]}})
	if err != nil || status != 302 || cookieValue(raw) != "" {
		return fail("account product logout")
	}
	// Synthetic transport proves decoder classification/no automatic retry;
	// real native unknown-commit effects are owned by the two-DB HTTP suite.
	calls := 0
	wireClient, err := ac.NewClient("http://127.0.0.1:1", accountSource{state}, ac.WithClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":"outcome_unknown","errors":[]}`)), Request: request}, nil
	})}))
	if err != nil {
		return fail("account unknown wire setup")
	}
	wire, err := wireClient.GodjConformanceAccountAPIPassword(ctx, &ac.PasswordChange{OldPassword: target.NewPassword, NewPassword: target.NewPassword})
	unknown, ok := wire.(*ac.GodjConformanceAccountAPIPasswordServiceUnavailable)
	if err != nil || !ok || unknown.Code != "outcome_unknown" || calls != 1 {
		return fail("account unknown wire retried or misclassified")
	}
	return nil
}
