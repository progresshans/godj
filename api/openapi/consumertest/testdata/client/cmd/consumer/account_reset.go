package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"

	ac "example.com/godj-openapi-client/accountsession"
)

type resetSource struct {
	accountSource
	sessionCalls atomic.Int64
}

func (s *resetSource) SessionAuth(ctx context.Context, operation ac.OperationName) (ac.SessionAuth, error) {
	s.sessionCalls.Add(1)
	return s.accountSource.SessionAuth(ctx, operation)
}

type resetTransport struct {
	base  *http.Transport
	state *sessionState
}

func (t *resetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasPrefix(r.URL.Path, "/api/account/reset/") {
		counts := map[string]int{}
		for _, cookie := range r.Cookies() {
			counts[cookie.Name]++
		}
		expected := 0
		if r.URL.Path != "/api/account/reset/" {
			expected = 1
		}
		if counts[sessionCookieName] != expected || counts[csrfCookieName] > 1 || r.Header.Get("Authorization") != "" {
			return nil, fail("reset generated credential transport")
		}
		if r.Method == "POST" && (counts[csrfCookieName] != 1 || len(r.Header.Values(csrfHeaderName)) != 1) {
			return nil, fail("reset generated csrf transport")
		}
	}
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(r.URL.Path, "/api/account/reset/") || strings.HasPrefix(r.URL.Path, "/account/reset/") {
		if response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Referrer-Policy") != "no-referrer" {
			response.Body.Close()
			return nil, fail("reset response privacy headers")
		}
	}
	t.state.jar.SetCookies(r.URL, response.Cookies())
	return response, nil
}
func checkAccountReset(ctx context.Context, target accountEndpoint) error {
	address, err := url.Parse(target.URL)
	if err != nil {
		return fail("reset endpoint")
	}
	address.Path = "/api/account/reset/"
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fail("reset jar")
	}
	state := &sessionState{jar: jar, cookieURL: address}
	source := &resetSource{accountSource: accountSource{state}}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	transport := &resetTransport{base: base, state: state}
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client, err := ac.NewClient(target.URL, source, ac.WithClient(httpClient))
	if err != nil {
		return fail("reset generated setup")
	}
	result, err := client.GodjConformanceAccountAPIResetCsrf(ctx)
	token, ok := result.(*ac.GodjConformanceAccountAPIResetCsrfNoContent)
	if err != nil || !ok || !token.XGodjCsrftoken.Set || !state.ready(token.XGodjCsrftoken.Value) || source.sessionCalls.Load() != 0 || state.cookie(sessionCookieName) != "" {
		return fail("reset anonymous bootstrap")
	}
	state.setInvalid(true)
	rejected, err := client.GodjConformanceAccountAPIResetRequest(ctx, &ac.PasswordResetRequest{Email: target.Email})
	state.setInvalid(false)
	denied, ok := rejected.(*ac.GodjConformanceAccountAPIResetRequestForbidden)
	if err != nil || !ok || denied.Code != "csrf_rejected" {
		return fail("reset request csrf refusal")
	}
	for _, email := range []string{"unknown@example.test", target.Email} {
		ack, err := client.GodjConformanceAccountAPIResetRequest(ctx, &ac.PasswordResetRequest{Email: email})
		if _, ok := ack.(*ac.GodjConformanceAccountAPIResetRequestNoContent); err != nil || !ok {
			return fail("reset uniform acknowledgement")
		}
	}
	if source.sessionCalls.Load() != 0 || state.cookie(sessionCookieName) != "" {
		return fail("reset request invented login")
	}
	// Only this parent's private fixture transport exposes the memory mailbox.
	request, err := http.NewRequestWithContext(ctx, "GET", target.URL+"/__test/reset-mail/", nil)
	if err != nil {
		return fail("reset mailbox request")
	}
	request.Header.Set("X-Fixture-Proof", target.MailProof)
	mail, err := httpClient.Do(request)
	if err != nil {
		return fail("reset mailbox transport")
	}
	var link string
	err = json.NewDecoder(io.LimitReader(mail.Body, 4096)).Decode(&link)
	mail.Body.Close()
	if err != nil || mail.StatusCode != 200 {
		return fail("reset mailbox receipt")
	}
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "reset.example.test" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fail("reset configured origin")
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) != 6 || parts[1] != "account" || parts[2] != "reset" || parts[3] == "" || parts[4] == "" || parts[5] != "" {
		return fail("reset email link shape")
	}
	uid, rawToken := parts[3], parts[4]
	proofResult, err := client.GodjConformanceAccountAPIResetProof(ctx, ac.GodjConformanceAccountAPIResetProofParams{UID: uid})
	invalid, ok := proofResult.(*ac.GodjConformanceAccountAPIResetProofForbidden)
	if err != nil || !ok || invalid.Response.Code != "invalid_reset_link" {
		return fail("reset proof required before entry")
	}
	browser := &http.Client{Transport: transport, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	entry, err := browser.Get(target.URL + parsed.Path)
	if err != nil {
		return fail("reset product entry")
	}
	body, e := io.ReadAll(io.LimitReader(entry.Body, 65536))
	entry.Body.Close()
	confirmation := "/account/reset/" + uid + "/set-password/"
	if e != nil || entry.StatusCode != 302 || entry.Header.Get("Location") != confirmation || strings.Contains(string(body), rawToken) || state.cookie(sessionCookieName) == "" {
		return fail("reset hidden token exchange")
	}
	proofID := state.cookie(sessionCookieName)
	page, err := browser.Get(target.URL + confirmation)
	if err != nil {
		return fail("reset product confirmation")
	}
	body, e = io.ReadAll(io.LimitReader(page.Body, 65536))
	page.Body.Close()
	if e != nil || page.StatusCode != 200 || strings.Contains(string(body), rawToken) || !strings.Contains(string(body), `data-account-view="reset-confirm"`) {
		return fail("reset token-free form")
	}
	proofResult, err = client.GodjConformanceAccountAPIResetProof(ctx, ac.GodjConformanceAccountAPIResetProofParams{UID: uid})
	proof, ok := proofResult.(*ac.GodjConformanceAccountAPIResetProofNoContent)
	if err != nil || !ok || !proof.XGodjCsrftoken.Set || !state.ready(proof.XGodjCsrftoken.Value) {
		return fail("reset generated proof")
	}
	// Safe generated operations need not send a CSRF cookie and may receive a
	// new pair. The completion command must preserve the pair just obtained.
	csrfCookie := state.cookie(csrfCookieName)
	params := ac.GodjConformanceAccountAPIResetCompleteParams{UID: uid}
	weak, err := client.GodjConformanceAccountAPIResetComplete(ctx, &ac.PasswordResetComplete{NewPassword: "short"}, params)
	failures, ok := weak.(*ac.GodjConformanceAccountAPIResetCompleteBadRequest)
	if err != nil || !ok || failures.Code != "validation_error" || len(failures.Errors) != 1 || failures.Errors[0].Field != "new_password" || failures.Errors[0].Code != "password_too_short" {
		return fail("reset generated password policy")
	}
	committed, err := client.GodjConformanceAccountAPIResetComplete(ctx, &ac.PasswordResetComplete{NewPassword: target.ResetPassword}, params)
	success, ok := committed.(*ac.GodjConformanceAccountAPIResetCompleteNoContent)
	if err != nil || !ok {
		return fail("reset generated committed response")
	}
	if success.SetCookie == "" || state.cookie(sessionCookieName) == "" || state.cookie(sessionCookieName) == proofID {
		return fail("reset generated committed cookie rotation")
	}
	cookies := (&http.Response{Header: http.Header{"Set-Cookie": {success.SetCookie}}}).Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || cookies[0].Value != state.cookie(sessionCookieName) {
		return fail("reset generated committed cookie shape")
	}
	if state.cookie(csrfCookieName) != csrfCookie {
		return fail("reset generated completion changed csrf cookie")
	}
	replay, err := client.GodjConformanceAccountAPIResetComplete(ctx, &ac.PasswordResetComplete{NewPassword: target.ResetPassword}, params)
	refused, ok := replay.(*ac.GodjConformanceAccountAPIResetCompleteForbidden)
	if err != nil || !ok || refused.Code != "invalid_reset_link" {
		return fail("reset generated replay refusal")
	}
	private, err := client.GodjConformanceAccountAPICsrf(ctx)
	if _, ok := private.(*ac.GodjConformanceAccountAPICsrfForbidden); err != nil || !ok {
		return fail("reset created login")
	}
	calls := 0
	wire, err := ac.NewClient("http://127.0.0.1:1", source, ac.WithClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":"outcome_unknown","errors":[]}`)), Request: r}, nil
	})}))
	if err != nil {
		return fail("reset unknown wire setup")
	}
	unknown, err := wire.GodjConformanceAccountAPIResetComplete(ctx, &ac.PasswordResetComplete{NewPassword: target.ResetPassword}, params)
	uncertain, ok := unknown.(*ac.GodjConformanceAccountAPIResetCompleteServiceUnavailable)
	if err != nil || !ok || uncertain.Code != "outcome_unknown" || calls != 1 {
		return fail("reset unknown retried or misclassified")
	}
	return nil
}
