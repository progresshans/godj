package identityapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/identity"
	"github.com/progresshans/godj/identity/project"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

type headerBusinessProbe struct {
	identity.ManagementBackend
	reads, writes atomic.Int64
}

func (p *headerBusinessProbe) ReadSnapshot(context.Context, func(db.Queryer) error) error {
	p.reads.Add(1)
	return errors.New("unexpected identity business read")
}
func (p *headerBusinessProbe) CoordinatedAtomic(context.Context, func(db.Session) error) error {
	p.writes.Add(1)
	return errors.New("unexpected identity business write")
}

type headerVerifier struct{ allowed bool }

func (v headerVerifier) Verify(context.Context, bearerauth.Token) (auth.Principal, error) {
	return auth.NewPrincipal(auth.PrincipalConfig{ID: "header-actor", Active: true, Staff: true, Superuser: v.allowed})
}

type headerObservedBody struct {
	io.Reader
	reads, closes int
}

func (b *headerObservedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *headerObservedBody) Close() error               { b.closes++; return nil }

func headerApplication(t *testing.T, allowed bool) (*web.Application, *headerBusinessProbe) {
	t.Helper()
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: headerVerifier{allowed}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPBKDF2(auth.PBKDF2Config{Iterations: 10000})
	if err != nil {
		t.Fatal(err)
	}
	policies, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	backend := &headerBusinessProbe{}
	application, err := New(Config{Namespace: "headeridentity", Backend: backend, PasswordHasher: hasher, Authorizer: auth.PrincipalAuthorizer{}, Authentication: authentication, Users: policies.IdentityUser, Groups: policies.IdentityGroup, Permissions: policies.IdentityPermission})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "header_identity", InstalledApps: []apps.Config{{Name: "example.test/identity", Label: "identity"}}})
	if err != nil {
		t.Fatal(err)
	}
	app, err := web.NewApplication(web.Config{Settings: configured, Routes: application.Routes(), Middleware: application.Middleware()})
	if err != nil {
		t.Fatal(err)
	}
	if backend.reads.Load() != 0 || backend.writes.Load() != 0 {
		t.Fatal("header preparation performed I/O")
	}
	return app, backend
}

func TestIdentityHeaderConditionPrecedesBodyAndBusinessIO(t *testing.T) {
	app, backend := headerApplication(t, true)
	var routes []struct{ method, path string }
	for _, kind := range []string{"users", "groups", "permissions"} {
		for _, method := range []string{"PUT", "PATCH", "DELETE"} {
			routes = append(routes, struct{ method, path string }{method, BasePath + kind + "/1/"})
		}
	}
	routes = append(routes, struct{ method, path string }{"POST", BasePath + "users/1/password/"})
	for _, route := range routes {
		for _, test := range []struct {
			name   string
			header http.Header
			status int
			code   string
		}{
			{"missing", nil, 428, "precondition_required"},
			{"empty", http.Header{"If-Revision": {""}}, 400, "invalid_precondition"},
			{"duplicate", http.Header{"If-Revision": {"1", "1"}}, 400, "invalid_precondition"},
			{"case aliases", http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, 400, "invalid_precondition"},
			{"noncanonical", http.Header{"If-Revision": {"01"}}, 400, "invalid_precondition"},
			{"comma", http.Header{"If-Revision": {"1,1"}}, 400, "invalid_precondition"},
			{"unwritable maximum", http.Header{"If-Revision": {"9223372036854775807"}}, 400, "invalid_precondition"},
			{"overflow", http.Header{"If-Revision": {"9223372036854775808"}}, 400, "invalid_precondition"},
		} {
			t.Run(route.method+route.path+test.name, func(t *testing.T) {
				reader := &headerObservedBody{Reader: strings.NewReader("invalid-json")}
				request := httptest.NewRequest(route.method, route.path, nil)
				request.Body = reader
				request.ContentLength = -1
				request.Header = test.header.Clone()
				if request.Header == nil {
					request.Header = make(http.Header)
				}
				request.Header.Set("Authorization", "Bearer fixture")
				request.Header.Set("Content-Type", api.JSONContentType)
				response := httptest.NewRecorder()
				app.ServeHTTP(response, request)
				var envelope struct {
					Code   string
					Errors []any
				}
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != test.status || envelope.Code != test.code || len(envelope.Errors) != 0 || reader.reads != 0 || reader.closes != 0 || backend.reads.Load() != 0 || backend.writes.Load() != 0 {
					t.Fatal("revision condition order/classification", response.Code, response.Body, reader, err)
				}
				if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Revision") != "" {
					t.Fatal("failed condition published/cacheable state")
				}
			})
		}
	}
}

func TestIdentityAdmissionAndPathQueryRemainBeforeHeader(t *testing.T) {
	app, backend := headerApplication(t, true)
	denied, deniedBackend := headerApplication(t, false)
	for _, test := range []struct {
		name, path, revision string
		application          *web.Application
		authenticated        bool
		status               int
		code                 string
		read                 bool
	}{
		{"anonymous", BasePath + "users/1/", "", app, false, 401, "not_authenticated", false},
		{"forbidden", BasePath + "users/1/", "", denied, true, 403, "permission_denied", false},
		{"zero id", BasePath + "users/0/", "", app, true, 400, "parse_error", false},
		{"query", BasePath + "users/1/?extra=1", "", app, true, 400, "parse_error", false},
		{"empty query marker", BasePath + "users/1/?", "", app, true, 400, "parse_error", false},
		{"canonical before body", BasePath + "users/1/", "1", app, true, 400, "parse_error", true},
		{"large exact before body", BasePath + "users/1/", "9007199254740993", app, true, 400, "parse_error", true},
		{"last writable before body", BasePath + "users/1/", "9223372036854775806", app, true, 400, "parse_error", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &headerObservedBody{Reader: strings.NewReader("invalid-json")}
			request := httptest.NewRequest("PATCH", test.path, nil)
			request.Body = reader
			request.ContentLength = -1
			request.Header.Set("Content-Type", api.JSONContentType)
			if test.authenticated {
				request.Header.Set("Authorization", "Bearer fixture")
			}
			if test.revision != "" {
				request.Header["iF-rEVISION"] = []string{test.revision}
			}
			response := httptest.NewRecorder()
			test.application.ServeHTTP(response, request)
			var envelope struct{ Code string }
			err := json.Unmarshal(response.Body.Bytes(), &envelope)
			if err != nil || response.Code != test.status || envelope.Code != test.code || (reader.reads > 0) != test.read || reader.closes != 0 {
				t.Fatal("admission/path/header/body order", response.Code, response.Body, reader, err)
			}
		})
	}
	if backend.reads.Load() != 0 || backend.writes.Load() != 0 || deniedBackend.reads.Load() != 0 || deniedBackend.writes.Load() != 0 {
		t.Fatal("rejection reached identity manager I/O")
	}
}
