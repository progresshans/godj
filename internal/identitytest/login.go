package identitytest

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/models"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/sessions"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web"
	"github.com/progresshans/godj/web/sessionauth"
)

//go:embed testdata/login-lifecycle-django61-*.json
var loginReferences embed.FS

var loginInstant = time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)

func loginUser(t *testing.T, backend db.Queryer, id int64) models.User {
	t.Helper()
	row, found, err := models.UserObjects.Using(backend).Filter(models.UserFields.ID.Exact(id)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found {
		t.Fatal("login user missing", err)
	}
	return row
}

func requireLoginTime(t *testing.T, value *time.Time, want time.Time) {
	t.Helper()
	if value == nil || !value.Equal(want) || value.Nanosecond()%1000 != 0 {
		t.Fatal("persisted login observation is missing, stale or noncanonical", value, want)
	}
}

// RunLoginLifecycle compares common native HTTP behavior with independent
// Django observations. Go's always-rotate policy is asserted separately.
func RunLoginLifecycle(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	backend, other := open(t)
	f := newManagementFixture(t, backend, 0)
	if _, err := models.UserObjects.Patch(t.Context(), backend, f.user, models.UserPatch{}.WithStaff(false)); err != nil {
		t.Fatal(err)
	}
	var elapsed atomic.Int64
	clock := func() time.Time { return loginInstant.Add(time.Duration(elapsed.Load())) }
	h := newIdentityHTTPConfigured(t, f.runtime.Authenticator(), f.runtime.SessionStore(), f.runtime.LoginPersistence, nil, clock,
		func(runtime *sessionauth.Runtime) ([]web.Route, []web.Middleware, error) {
			return []web.Route{{Name: "identityprobe:logout", Method: "POST", Path: "/logout/", Handler: func(request *web.Request) (web.Response, error) {
				if err := runtime.VerifyCSRF(request, nil); err != nil {
					return web.NewResponse(403, nil, nil)
				}
				change, err := runtime.Logout(request)
				if err != nil {
					return web.Response{}, err
				}
				response, err := web.NewResponse(200, nil, nil)
				if err != nil {
					return web.Response{}, err
				}
				return change.Apply(response)
			}}}, nil, nil
		})
	manager, err := sessions.NewManager(f.runtime.SessionStore(), sessions.Config{Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	previous, err := manager.Create(t.Context(), map[string]string{"payload": "anonymous"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, _ := url.Parse(h.server.URL)
	h.client.Jar.SetCookies(endpoint, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: previous.ID().Encoded(), Path: "/"}})
	credential, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
	if err != nil || !credential.Principal().Authenticated() || f.stored(t).LastLogin != nil {
		t.Fatal("authentication alone changed last_login", err)
	}
	if _, err := f.runtime.Authenticator().Resolve(t.Context(), f.user.PrincipalID); err != nil || f.stored(t).LastLogin != nil {
		t.Fatal("resolution changed last_login", err)
	}
	beforeRows, beforeAudit := snapshotIdentitySystemRows(t, backend)
	h.login(t, h.client, "member", "wrong", 401)
	_, token := h.request(t, h.client, "GET", "/login/", nil)
	denied, _ := h.request(t, h.client, "POST", "/login/", http.Header{"X-Test-Username": {"member"}, "X-Test-Password-Base64": {base64.RawURLEncoding.EncodeToString([]byte(managementOldPassword))}, "X-Test-Staff": {"required"}, sessionauth.DefaultCSRFHeader: {token}})
	afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
	if denied.StatusCode != 401 || len(denied.Cookies()) != 0 || !reflect.DeepEqual(beforeRows, afterRows) || !reflect.DeepEqual(beforeAudit, afterAudit) || f.stored(t).LastLogin != nil {
		t.Fatal("denied login changed existing session or user")
	}
	observations := []map[string]any{{"stage": "denied", "wrong_status": 401, "nonstaff_status": denied.StatusCode, "last_login": nil, "session_retained": reflect.DeepEqual(beforeRows, afterRows)}}
	observe := func(stage string, id sessions.ID, old sessions.Record, userID int64) sessions.Record {
		t.Helper()
		record, found, err := f.runtime.SessionStore().Load(t.Context(), id)
		if err != nil || !found || id == old.ID() {
			t.Fatal("successful login did not rotate", err)
		}
		if _, found, err := f.runtime.SessionStore().Load(t.Context(), old.ID()); err != nil || found {
			t.Fatal("old bearer remains usable", err)
		}
		stored := loginUser(t, backend, userID)
		requireLoginTime(t, stored.LastLogin, clock())
		if stored.Revision != 1 {
			t.Fatal("observational login changed management revision")
		}
		payload, present := record.Value("payload")
		var value any
		if present {
			value = payload
		}
		response, _ := h.request(t, h.client, "GET", "/view/", nil)
		observations = append(observations, map[string]any{"stage": stage, "status": 200, "last_login": stored.LastLogin.Format("2006-01-02T15:04:05.999999-07:00"), "key_changed": id != old.ID(), "payload": value, "authenticated": response.StatusCode == 200})
		return record
	}
	id := h.login(t, h.client, "member", managementOldPassword, 200)
	current := observe("login", id, previous, f.user.ID)
	if !current.CreatedAt().Equal(previous.CreatedAt()) {
		t.Fatal("anonymous lifetime reset")
	}
	elapsed.Store(int64(time.Second))
	h.request(t, h.client, "GET", "/view/", nil)
	requireLoginTime(t, f.stored(t).LastLogin, loginInstant)
	observations = append(observations, map[string]any{"stage": "access", "last_login": f.stored(t).LastLogin.Format("2006-01-02T15:04:05.999999-07:00")})
	elapsed.Store(int64(2 * time.Second))
	id = h.login(t, h.client, "member", managementOldPassword, 200)
	current = observe("repeat_login", id, current, f.user.ID)
	elapsed.Store(int64(3 * time.Second))
	id = h.login(t, h.client, "manager", managementOldPassword, 200)
	current = observe("different_user", id, current, f.root.ID)
	if !current.CreatedAt().Equal(clock()) {
		t.Fatal("different user did not get a fresh lifetime")
	}
	elapsed.Store(int64(4 * time.Second))
	_, token = h.request(t, h.client, "GET", "/login/", nil)
	logout, _ := h.request(t, h.client, "POST", "/logout/", http.Header{sessionauth.DefaultCSRFHeader: {token}})
	if logout.StatusCode != 200 {
		t.Fatal("logout failed")
	}
	response, _ := h.request(t, h.client, "GET", "/view/", nil)
	root := loginUser(t, backend, f.root.ID)
	requireLoginTime(t, root.LastLogin, loginInstant.Add(3*time.Second))
	if _, found, err := f.runtime.SessionStore().Load(t.Context(), id); err != nil || found {
		t.Fatal("logout retained authenticated session", err)
	}
	observations = append(observations, map[string]any{"stage": "logout", "last_login": root.LastLogin.Format("2006-01-02T15:04:05.999999-07:00"), "authenticated": response.StatusCode == 200, "payload": nil})
	for _, name := range []string{"sqlite", "postgres"} {
		payload, err := loginReferences.ReadFile("testdata/login-lifecycle-django61-" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var reference struct {
			Django       string
			Observations struct{ Lifecycle []map[string]any }
		}
		if err := json.Unmarshal(payload, &reference); err != nil || reference.Django != "6.1" || len(reference.Observations.Lifecycle) != 6 {
			t.Fatal("invalid login reference", err)
		}
		// Explicit policy difference: Django keeps the same authenticated key;
		// GoDj always rotates, and its true result was checked above.
		if reference.Observations.Lifecycle[3]["key_changed"] != false || observations[3]["key_changed"] != true {
			t.Fatal("repeat-key deviation drifted")
		}
		delete(reference.Observations.Lifecycle[3], "key_changed")
		actual := make([]map[string]any, len(observations))
		data, _ := json.Marshal(observations)
		if err := json.Unmarshal(data, &actual); err != nil {
			t.Fatal(err)
		}
		delete(actual[3], "key_changed")
		if !reflect.DeepEqual(actual, reference.Observations.Lifecycle) {
			t.Fatalf("common login observations differ from Django %s: got %v want %v", name, actual, reference.Observations.Lifecycle)
		}
	}
	_, audit := snapshotIdentitySystemRows(t, backend)
	if len(audit) != 0 {
		t.Fatal("login created management audit")
	}
	reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
	if err != nil {
		t.Fatal(err)
	}
	directory, err := identity.NewDirectory(reopened)
	if err != nil {
		t.Fatal(err)
	}
	account, found, err := directory.ByPrincipalID(t.Context(), f.user.PrincipalID)
	if err != nil || !found {
		t.Fatal("reopen lost user", err)
	}
	requireLoginTime(t, account.Profile().LastLogin, loginInstant.Add(2*time.Second))
	adminHTTP := newIdentityAdminHTTP(t, f, f.runtime)
	adminHTTP.loginAdmin(t, adminHTTP.client, "manager", managementOldPassword)
	adminHTTP.call(t, adminHTTP.client, "GET", adminObjectPath("users", "change", f.user.ID), nil, 200).contains(t, `data-field-name="last_login"`, loginInstant.Add(2*time.Second).Format(time.RFC3339Nano))
	apiHTTP, _ := newManagementHTTP(t, f, f.runtime)
	_, body := managementCall(t, apiHTTP, apiHTTP.client, "GET", apiPath("users", f.user.ID), "", "", false, 200)
	var published time.Time
	if err := json.Unmarshal(body["last_login"], &published); err != nil || !published.Equal(loginInstant.Add(2*time.Second)) {
		t.Fatal("API did not expose persisted login time", err)
	}
	before := f.stored(t)
	managementCall(t, apiHTTP, apiHTTP.client, "PATCH", apiPath("users", f.user.ID), `{"last_login":"2000-01-01T00:00:00Z"}`, "1", true, 400)
	details, err := f.manager(t, f.runtime).User(t.Context(), f.actor, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := adminUserData(details)
	data.Set("last_login", "2000-01-01T00:00:00Z")
	adminHTTP.postForm(t, adminObjectPath("users", "change", f.user.ID), data, 400)
	if !reflect.DeepEqual(before, f.stored(t)) {
		t.Fatal("read-only login time became writable")
	}
}

type loginBoundary struct {
	TransitionBackend
	mode          string
	armed         bool
	calls, faults int
	afterCommit   func()
}

func (b *loginBoundary) CoordinatedAtomic(ctx context.Context, callback func(db.Session) error) error {
	if !b.armed {
		return b.TransitionBackend.CoordinatedAtomic(ctx, callback)
	}
	b.calls++
	if b.mode == "zero" {
		return nil
	}
	err := b.TransitionBackend.CoordinatedAtomic(ctx, func(session db.Session) error {
		if b.mode == "nil" {
			return callback(nil)
		}
		wrapped := &loginFaultSession{Session: session, owner: b}
		err := callback(wrapped)
		if b.mode == "swallow" {
			return nil
		}
		if err != nil {
			return err
		}
		if b.mode == "twice" {
			return callback(session)
		}
		if b.mode == "cancel" || b.mode == "unknown_rollback" {
			return context.Canceled
		}
		return nil
	})
	if b.mode == "unknown_rollback" {
		return errors.Join(err, &query.Error{Code: query.CodeTransactionOutcomeUnknown, Detail: "private-login-fault"})
	}
	if b.mode == "unknown_commit" && err == nil {
		return &query.Error{Code: query.CodeCommitOutcomeUnknown, Detail: "private-login-fault"}
	}
	if b.mode == "late_cancel" && err == nil {
		b.afterCommit()
	}
	return err
}

type loginFaultSession struct {
	db.Session
	owner *loginBoundary
}

// Error values need not be comparable. Hosts may return an error backed by a
// slice; ordinary admission failures must still roll back and return an error.
type loginOpaqueError []string

func (loginOpaqueError) Error() string { return "private-login-fault" }

func (s *loginFaultSession) fault() error { s.owner.faults++; return errors.New("private-login-fault") }
func (s *loginFaultSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if s.owner.mode == "swallow" {
		return nil, s.fault()
	}
	return s.Session.Query(ctx, plan)
}
func (s *loginFaultSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if plan.Table() == "godj_system_session" && s.owner.mode == "insert" {
		return 0, s.fault()
	}
	n, err := s.Session.Insert(ctx, plan)
	if err == nil && plan.Table() == "godj_system_session" && s.owner.mode == "after_insert" {
		return 0, s.fault()
	}
	return n, err
}
func (s *loginFaultSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	n, err := s.Session.Delete(ctx, plan)
	if err == nil && plan.Table() == "godj_system_session" && s.owner.mode == "after_delete" {
		return 0, s.fault()
	}
	return n, err
}
func (s *loginFaultSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if plan.Table() == "godj_identity_user" && s.owner.mode == "last_login" {
		return 0, s.fault()
	}
	n, err := s.Session.Update(ctx, plan)
	if err == nil && plan.Table() == "godj_identity_user" && s.owner.mode == "after_last_login" {
		return 0, s.fault()
	}
	return n, err
}

func loginBinding(t *testing.T, runtime *systemstate.Runtime) (*sessions.Manager, auth.LoginPersistence) {
	t.Helper()
	manager, err := sessions.NewManager(runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return loginInstant }})
	if err != nil {
		t.Fatal(err)
	}
	persistence, err := runtime.LoginPersistence(manager)
	if err != nil {
		t.Fatal(err)
	}
	return manager, persistence
}
func loginAdmission(_ context.Context, principal auth.Principal) error {
	if !principal.Authenticated() || !principal.Staff() || !principal.Has("helpdesk.ticket.view") {
		return auth.ErrInvalidCredentials
	}
	return nil
}

func RunLoginBoundaries(t *testing.T, open func(*testing.T) (TransitionBackend, TransitionBackend)) {
	t.Run("expired_snapshot_does_not_publish_cleanup", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		credential, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
		if err != nil {
			t.Fatal(err)
		}
		for _, expired := range []string{"idle", "absolute"} {
			t.Run(expired, func(t *testing.T) {
				now := loginInstant
				manager, err := sessions.NewManager(f.runtime.SessionStore(), sessions.Config{Clock: func() time.Time { return now }, IdleTimeout: time.Second, AbsoluteLifetime: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				previous, err := manager.Create(t.Context(), map[string]string{"private": "retain"})
				if err != nil {
					t.Fatal(err)
				}
				provider, err := f.runtime.LoginPersistence(manager)
				if err != nil {
					t.Fatal(err)
				}
				now = now.Add(2 * time.Second)
				if expired == "absolute" {
					now = now.Add(time.Minute)
				}
				beforeRows, beforeAudit := snapshotIdentitySystemRows(t, backend)
				result, err := provider.Login(t.Context(), auth.SessionLogin{Credential: credential, Previous: previous, At: now, Admit: loginAdmission})
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				if err == nil || result.Record.ID().Valid() || f.stored(t).LastLogin != nil || !reflect.DeepEqual(beforeRows, afterRows) || !reflect.DeepEqual(beforeAudit, afterAudit) {
					t.Fatal("expired login published standalone cleanup or observation", err)
				}
			})
		}
	})
	t.Run("admission_execution_errors_are_not_denials_or_retries", func(t *testing.T) {
		backend, _ := open(t)
		f := newManagementFixture(t, backend, 0)
		boundary := &loginBoundary{TransitionBackend: backend}
		runtime, err := systemstate.OpenIdentity(t.Context(), boundary, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		_, provider := loginBinding(t, runtime)
		credential, err := runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
		if err != nil {
			t.Fatal(err)
		}
		for _, failure := range []error{auth.ErrInvalidCredentials, errors.Join(auth.ErrInvalidCredentials, errors.New("private-login-fault")), errors.Join(auth.ErrInvalidCredentials, &query.Error{Code: query.CodeTransactionOutcomeUnknown}), &sessions.Error{Code: sessions.CodeEntropy}, &sessions.Error{Code: sessions.CodeStoreFull}, context.Canceled, loginOpaqueError{"private-login-fault"}} {
			beforeRows, beforeAudit := snapshotIdentitySystemRows(t, backend)
			boundary.calls = 0
			boundary.armed = true
			result, err := provider.Login(t.Context(), auth.SessionLogin{Credential: credential, At: loginInstant, Admit: func(context.Context, auth.Principal) error { return failure }})
			boundary.armed = false
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if err == nil || result.Record.ID().Valid() || boundary.calls != 1 || !reflect.DeepEqual(beforeRows, afterRows) || !reflect.DeepEqual(beforeAudit, afterAudit) || f.stored(t).LastLogin != nil {
				t.Fatal("admission failure wrote, retried or published", err)
			}
			if (err == auth.ErrInvalidCredentials) != (failure == auth.ErrInvalidCredentials) {
				t.Fatal("execution error became credential denial", err)
			}
		}
	})
	t.Run("concurrent_login_and_profile_edit_keep_timestamp_and_revision", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 0)
		_, provider := loginBinding(t, f.runtime)
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		credential, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
		if err != nil {
			t.Fatal(err)
		}
		entered, release, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
		loginDone, editDone := make(chan error, 1), make(chan error, 1)
		go func() {
			_, err := provider.Login(t.Context(), auth.SessionLogin{Credential: credential, At: loginInstant, Admit: func(ctx context.Context, p auth.Principal) error {
				close(entered)
				<-release
				return loginAdmission(ctx, p)
			}})
			loginDone <- err
		}()
		<-entered
		maintenance := f.manager(t, reopened)
		go func() {
			close(started)
			_, err := maintenance.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithFirstName("Concurrent"))
			editDone <- err
		}()
		<-started
		close(release)
		if err := <-loginDone; err != nil {
			t.Fatal(err)
		}
		if err := <-editDone; err != nil {
			t.Fatal("login caused spurious edit conflict", err)
		}
		row := f.stored(t)
		requireLoginTime(t, row.LastLogin, loginInstant)
		if row.Revision != 2 || row.FirstName != "Concurrent" {
			t.Fatal("concurrent edit lost login observation")
		}
	})
	t.Run("concurrent_password_change_cannot_leave_stale_session", func(t *testing.T) {
		backend, other := open(t)
		f := newManagementFixture(t, backend, 0)
		_, provider := loginBinding(t, f.runtime)
		reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
		if err != nil {
			t.Fatal(err)
		}
		credential, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		type outcome struct {
			result auth.SessionLoginResult
			err    error
		}
		loginDone := make(chan outcome, 1)
		editDone := make(chan error, 1)
		maintenance := f.manager(t, reopened)
		go func() {
			<-start
			result, err := provider.Login(t.Context(), auth.SessionLogin{Credential: credential, At: loginInstant, Admit: loginAdmission})
			loginDone <- outcome{result, err}
		}()
		go func() {
			<-start
			_, err := maintenance.SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
			editDone <- err
		}()
		close(start)
		login := <-loginDone
		if err := <-editDone; err != nil {
			t.Fatal(err)
		}
		if login.err != nil && login.err != auth.ErrInvalidCredentials {
			t.Fatal("concurrent login execution failed", login.err)
		}
		rows, _ := snapshotIdentitySystemRows(t, backend)
		if len(rows) != 0 {
			t.Fatal("password change left stale newly established session")
		}
		row := f.stored(t)
		if row.Revision != 2 {
			t.Fatal("login changed password revision")
		}
		if login.err == nil {
			requireLoginTime(t, row.LastLogin, loginInstant)
		} else if row.LastLogin != nil {
			t.Fatal("denied login recorded time")
		}
	})
	for _, operation := range []string{"create", "rotate", "replace"} {
		for _, mode := range []string{"zero", "nil", "twice", "swallow", "insert", "after_insert", "last_login", "after_last_login", "cancel", "unknown_rollback", "unknown_commit", "late_cancel", "after_delete"} {
			if operation == "create" && mode == "after_delete" {
				continue
			}
			t.Run(operation+"/"+mode, func(t *testing.T) {
				backend, _ := open(t)
				f := newManagementFixture(t, backend, 0)
				boundary := &loginBoundary{TransitionBackend: backend, mode: mode}
				runtime, err := systemstate.OpenIdentity(t.Context(), boundary, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
				if err != nil {
					t.Fatal(err)
				}
				manager, persistence := loginBinding(t, runtime)
				credential, err := runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
				if err != nil {
					t.Fatal(err)
				}
				var previous sessions.Record
				if operation != "create" {
					values := map[string]string{"private": "old payload"}
					if operation == "replace" {
						values[auth.SessionPrincipalIDKey] = "other"
					}
					previous, err = manager.Create(t.Context(), values)
					if err != nil {
						t.Fatal(err)
					}
				}
				beforeRows, beforeAudit := snapshotIdentitySystemRows(t, backend)
				before := f.stored(t)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				boundary.afterCommit = cancel
				boundary.armed = true
				result, err := persistence.Login(ctx, auth.SessionLogin{Credential: credential, Previous: previous, At: loginInstant.Add(999 * time.Nanosecond), Admit: loginAdmission})
				boundary.armed = false
				if boundary.calls != 1 {
					t.Fatal("login write was retried", boundary.calls)
				}
				committed := mode == "unknown_commit" || mode == "late_cancel"
				if mode == "late_cancel" {
					if err != nil || !result.Record.ID().Valid() || ctx.Err() != context.Canceled {
						t.Fatal("late cancellation hid a confirmed commit", err)
					}
				} else {
					if err == nil || result.Record.ID().Valid() || result.Credential.Principal().Authenticated() || err == auth.ErrInvalidCredentials {
						t.Fatal("failed or uncertain login published or became denial", err)
					}
					if strings.Contains(fmt.Sprintf("%+v", err), "private-login-fault") {
						t.Fatal("login leaked private failure")
					}
					if strings.HasPrefix(mode, "unknown_") {
						var typed *query.Error
						if !errors.As(err, &typed) || (mode == "unknown_commit" && typed.Code != query.CodeCommitOutcomeUnknown) || (mode == "unknown_rollback" && typed.Code != query.CodeTransactionOutcomeUnknown) {
							t.Fatal("unknown outcome classification lost", err)
						}
					}
					if mode == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation classification lost", err)
					}
				}
				if mode == "insert" || mode == "after_insert" || mode == "last_login" || mode == "after_last_login" || mode == "after_delete" || mode == "swallow" {
					if boundary.faults != 1 {
						t.Fatal("fault did not reach requested write boundary", boundary.faults)
					}
				}
				afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
				after := f.stored(t)
				if !reflect.DeepEqual(beforeAudit, afterAudit) {
					t.Fatal("login appended audit")
				}
				if committed {
					requireLoginTime(t, after.LastLogin, loginInstant)
					if reflect.DeepEqual(beforeRows, afterRows) {
						t.Fatal("timestamp committed without session")
					}
					after.LastLogin = before.LastLogin
					if !reflect.DeepEqual(before, after) {
						t.Fatal("login changed another user field")
					}
				} else if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeRows, afterRows) {
					t.Fatal("failed login did not restore exact user/session state")
				}
			})
		}
	}
	for _, change := range []string{"password", "inactive", "nonstaff", "grant", "deleted", "profile"} {
		t.Run("current_fence/"+change, func(t *testing.T) {
			backend, other := open(t)
			f := newManagementFixture(t, backend, 0)
			manager, persistence := loginBinding(t, f.runtime)
			credential, err := f.runtime.Authenticator().Authenticate(t.Context(), "member", managementOldPassword)
			if err != nil {
				t.Fatal(err)
			}
			previous, err := manager.Create(t.Context(), map[string]string{"private": "retain on denial"})
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := systemstate.OpenIdentity(t.Context(), other, systemstate.IdentityRuntimeConfig{PasswordHasher: f.hasher.PasswordHasher, MaxSessions: 512})
			if err != nil {
				t.Fatal(err)
			}
			maintenance := f.manager(t, reopened)
			switch change {
			case "password":
				_, err = maintenance.SetPassword(t.Context(), f.actor, f.user.ID, 1, managementNewPassword)
			case "inactive":
				_, err = maintenance.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithActive(false))
			case "nonstaff":
				_, err = maintenance.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithStaff(false))
			case "grant":
				_, err = maintenance.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithPermissions())
			case "profile":
				_, err = maintenance.UpdateUser(t.Context(), f.actor, f.user.ID, 1, identity.UserPatch{}.WithFirstName("Concurrent edit"))
			case "deleted":
				policy := managementHost(t, backend)
				_, err = maintenance.DeleteUser(t.Context(), f.actor, f.user.ID, 1, policy.AccountsUser)
			}
			if err != nil {
				t.Fatal(err)
			}
			beforeRows, beforeAudit := snapshotIdentitySystemRows(t, backend)
			result, err := persistence.Login(t.Context(), auth.SessionLogin{Credential: credential, Previous: previous, At: loginInstant, Admit: loginAdmission})
			afterRows, afterAudit := snapshotIdentitySystemRows(t, backend)
			if change == "profile" {
				if err != nil || !result.Record.ID().Valid() {
					t.Fatal("unrelated profile edit blocked login", err)
				}
				row := f.stored(t)
				requireLoginTime(t, row.LastLogin, loginInstant)
				if row.Revision != 2 || row.FirstName != "Concurrent edit" {
					t.Fatal("login overwrote concurrent profile")
				}
				return
			}
			if err != auth.ErrInvalidCredentials || result.Record.ID().Valid() || !reflect.DeepEqual(beforeRows, afterRows) || !reflect.DeepEqual(beforeAudit, afterAudit) {
				t.Fatal("stale admission published or changed state", err)
			}
			if change != "deleted" && f.stored(t).LastLogin != nil {
				t.Fatal("denied current credential updated last_login")
			}
		})
	}
}
