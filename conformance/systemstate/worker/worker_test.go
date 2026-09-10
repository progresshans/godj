package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistinctProcessWorkerActionLifecycle(t *testing.T) {
	ctx := context.Background()
	request := Request{
		Database:       filepath.Join(t.TempDir(), "worker.sqlite3"),
		Username:       "worker-admin",
		Password:       "worker-password-marker",
		RepositoryRoot: findRepositoryRoot(),
	}

	initialized, _ := executeAction(t, ctx, request, ActionInitialize)
	if !initialized.Ready || !initialized.MigrationApplied || initialized.CredentialRows != 1 ||
		initialized.SessionRows != 0 || initialized.AuditRows != 0 || initialized.ArticleRows != 0 {
		t.Fatalf("initialize response = %#v", initialized)
	}

	authenticated, _ := executeAction(t, ctx, request, ActionAuthenticate)
	if !authenticated.Authenticated || !authenticated.Active || !authenticated.Permission ||
		authenticated.PrincipalID != workerPrincipalID {
		t.Fatalf("authenticate response = %#v", authenticated)
	}

	loggedIn, loginSecrets := executeAction(t, ctx, request, ActionLogin)
	if loggedIn.LoginStatus != http.StatusFound || !loggedIn.Rotated || !loggedIn.OldSessionRemoved ||
		loggedIn.SessionRows != 1 || loginSecrets.Cookies.Session == "" || loginSecrets.Cookies.CSRF == "" ||
		!maskedCSRFPattern.MatchString(loginSecrets.Token) {
		t.Fatalf("login response = %#v; secrets present=%t/%t/%t", loggedIn,
			loginSecrets.Cookies.Session != "", loginSecrets.Cookies.CSRF != "", loginSecrets.Token != "")
	}
	request.Cookies = loginSecrets.Cookies

	cookieProbe, cookieSecrets := executeAction(t, ctx, request, ActionCookieProbe)
	if cookieProbe.AdminStatus != http.StatusOK || cookieProbe.APIStatus != http.StatusOK ||
		!cookieProbe.Authenticated || !cookieProbe.Permission || !cookieProbe.SameCookieHandoff ||
		cookieSecrets.Cookies != request.Cookies {
		t.Fatalf("cookie probe response = %#v", cookieProbe)
	}

	csrfSetup, csrfSetupSecrets := executeAction(t, ctx, request, ActionCSRFSetup)
	if csrfSetup.MutationStatus != http.StatusCreated || csrfSetup.ArticleDelta != 1 ||
		!csrfSetup.SameCookieHandoff || !maskedCSRFPattern.MatchString(csrfSetupSecrets.Token) {
		t.Fatalf("csrf setup response = %#v", csrfSetup)
	}
	request.Cookies = csrfSetupSecrets.Cookies
	request.Token = csrfSetupSecrets.Token

	stale, staleSecrets := executeAction(t, ctx, request, ActionCSRFStale)
	if stale.MutationStatus != http.StatusForbidden || stale.APIErrorCode != string(apiCSRFRejected()) ||
		stale.ArticleDelta != 0 || !stale.SameCookieHandoff || staleSecrets.Cookies != request.Cookies {
		t.Fatalf("stale CSRF response = %#v", stale)
	}

	fresh, freshSecrets := executeAction(t, ctx, request, ActionCSRFFresh)
	if fresh.MutationStatus != http.StatusCreated || fresh.ArticleDelta != 1 ||
		!fresh.SameCookieHandoff || !maskedCSRFPattern.MatchString(freshSecrets.Token) {
		t.Fatalf("fresh CSRF response = %#v", fresh)
	}
	request.Cookies = freshSecrets.Cookies
	request.Token = ""

	session, sessionSecrets := executeAction(t, ctx, request, ActionSessionProbe)
	if session.AdminStatus != http.StatusOK || session.APIStatus != http.StatusOK ||
		!session.Authenticated || !session.Permission || !session.SameCookieHandoff ||
		session.ArticleRows != 2 || sessionSecrets.Cookies != request.Cookies {
		t.Fatalf("session probe response = %#v", session)
	}

	fault, faultSecrets := executeAction(t, ctx, request, ActionAuditFault)
	if fault.MutationStatus != http.StatusInternalServerError || !fault.FaultInjected || !fault.RolledBack ||
		fault.ArticleDelta != 0 || fault.AuditDelta != 0 || faultSecrets.Cookies != request.Cookies {
		t.Fatalf("audit fault response = %#v", fault)
	}

	written, historySecrets := executeAction(t, ctx, request, ActionHistoryWrite)
	if written.AddStatus != http.StatusFound || written.ChangeStatus != http.StatusFound ||
		written.DeleteStatus != http.StatusFound || written.ObjectID <= 0 || written.AuditDelta != 3 ||
		historySecrets.Cookies != request.Cookies {
		t.Fatalf("history write response = %#v", written)
	}
	request.ObjectID = written.ObjectID

	history, _ := executeAction(t, ctx, request, ActionHistoryRead)
	if history.AuditCount != 3 || len(history.AuditEvents) != 3 || len(history.NewestEvents) != 2 ||
		!history.StrictlyIncreasing || !history.AcceptsNonContiguous || history.NewestSequence == 0 {
		t.Fatalf("history read response = %#v", history)
	}

	oldCookies := request.Cookies
	loggedOut, logoutSecrets := executeAction(t, ctx, request, ActionLogout)
	if loggedOut.AdminStatus != http.StatusFound || !loggedOut.OldSessionRemoved || loggedOut.Resurrected ||
		loggedOut.SessionRows != 0 || logoutSecrets.Cookies.Session != "" {
		t.Fatalf("logout response = %#v", loggedOut)
	}
	request.Cookies = oldCookies
	request.ObjectID = 0

	denied, _ := executeAction(t, ctx, request, ActionOldCookieProbe)
	if denied.AdminStatus != http.StatusFound || denied.APIStatus != http.StatusForbidden ||
		denied.APIErrorCode != string(apiNotAuthenticated()) || denied.Resurrected ||
		denied.ResurrectionWrites != 0 || denied.SessionRows != 0 {
		t.Fatalf("old cookie response = %#v", denied)
	}
}

func TestRunKeepsRequestSecretsOutOfSafeSurfaces(t *testing.T) {
	const marker = "raw-worker-secret-marker"
	request := Request{
		Action:   Action("not-a-worker-action"),
		Database: filepath.Join(t.TempDir(), "secret.sqlite3"),
		Username: "worker-admin",
		Password: marker,
		Cookies: CookieBundle{
			Session: marker + "-session",
			CSRF:    marker + "-csrf",
		},
		Token: marker + "-token",
	}
	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, secret bytes.Buffer
	runErr := Run(context.Background(), bytes.NewReader(input), &stdout, &secret)
	if runErr == nil || !strings.Contains(runErr.Error(), errorUnsupportedAction) {
		t.Fatalf("Run error = %v", runErr)
	}
	for label, rendered := range map[string]string{
		"stdout":  stdout.String(),
		"error":   runErr.Error(),
		"request": fmt.Sprintf("%#v", request),
		"cookies": fmt.Sprintf("%#v", request.Cookies),
	} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("%s exposed request secret", label)
		}
	}
	var safe Response
	if err := json.Unmarshal(stdout.Bytes(), &safe); err != nil || safe.OK || safe.ErrorCode != errorUnsupportedAction {
		t.Fatalf("safe response = (%#v,%v)", safe, err)
	}
	var empty SecretBundle
	if err := json.Unmarshal(secret.Bytes(), &empty); err != nil || empty.Cookies.Session != "" || empty.Token != "" {
		t.Fatalf("error secret bundle = (%#v,%v)", empty, err)
	}
}

func executeAction(
	t *testing.T,
	ctx context.Context,
	base Request,
	action Action,
) (Response, SecretBundle) {
	t.Helper()
	base.Action = action
	response, secret, err := Execute(ctx, base)
	if err != nil {
		t.Fatalf("Execute(%s): %v; response=%#v", action, err, response)
	}
	if !response.OK || response.ErrorCode != "" || response.Action != action || response.PID <= 0 {
		t.Fatalf("Execute(%s) envelope = %#v", action, response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{base.Password, base.Cookies.Session, base.Cookies.CSRF, base.Token} {
		if marker != "" && strings.Contains(string(encoded), marker) {
			t.Fatalf("Execute(%s) safe response exposed a secret", action)
		}
	}
	return response, secret
}

// Keep the worker test independent of API constants in its assertion prose
// while still checking the exact public JSON codes used by the implementation.
func apiCSRFRejected() string     { return "csrf_rejected" }
func apiNotAuthenticated() string { return "not_authenticated" }
