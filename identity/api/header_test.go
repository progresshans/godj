package identityapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/bearerauth"
	"github.com/progresshans/godj/apps"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/settings"
	"github.com/progresshans/godj/web"
)

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

func headerApplication(t *testing.T, allowed bool) (*web.Application, *selectedProbe) {
	t.Helper()
	authentication, err := bearerauth.New(bearerauth.Config{Verifier: headerVerifier{allowed}, Authorizer: auth.PrincipalAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	owner, catalog, backend := selectedOwners(t)
	application, err := NewManagement(owner, catalog, ManagementConfig{Namespace: "headeridentity", Authentication: authentication})
	if err != nil {
		t.Fatal(err)
	}
	configured, err := settings.New(settings.Definition{ProjectName: "header_identity", InstalledApps: []apps.Config{{Name: "example.test/identity", Label: "headeridentity"}}})
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

func TestSelectedManagementHeaderConditionPrecedesBodyAndBusinessIO(t *testing.T) {
	app, backend := headerApplication(t, true)
	var routes []struct{ method, path string }
	for _, kind := range []string{"accounts", "groups", "permissions"} {
		for _, method := range []string{"PUT", "PATCH", "DELETE"} {
			routes = append(routes, struct{ method, path string }{method, BasePath + kind + "/1/"})
		}
	}
	routes = append(routes, struct{ method, path string }{"POST", BasePath + "accounts/1/password/"})
	for _, route := range routes {
		for _, test := range []struct {
			name   string
			header http.Header
			status int
			code   string
		}{
			{"missing", nil, 428, "required"},
			{"empty", http.Header{"If-Revision": {""}}, 400, "invalid"},
			{"duplicate", http.Header{"If-Revision": {"1", "1"}}, 400, "invalid"},
			{"case aliases", http.Header{"If-Revision": {"1"}, "if-revision": {"1"}}, 400, "invalid"},
			{"noncanonical", http.Header{"If-Revision": {"01"}}, 400, "invalid"},
			{"comma", http.Header{"If-Revision": {"1,1"}}, 400, "invalid"},
			{"unwritable maximum", http.Header{"If-Revision": {"9223372036854775807"}}, 400, "invalid"},
			{"overflow", http.Header{"If-Revision": {"9223372036854775808"}}, 400, "invalid"},
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
					Errors []struct{ Field, Code string }
				}
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != test.status || envelope.Code != "validation_error" || len(envelope.Errors) != 1 || envelope.Errors[0].Field != "If-Revision" || envelope.Errors[0].Code != test.code || reader.reads != 0 || reader.closes != 0 || backend.reads.Load() != 0 || backend.writes.Load() != 0 {
					t.Fatal("revision condition order/classification", response.Code, response.Body, reader, err)
				}
				if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Revision") != "" {
					t.Fatal("failed condition published/cacheable state")
				}
			})
		}
	}
}

func TestSelectedManagementAdmissionAndPathQueryRemainBeforeHeader(t *testing.T) {
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
		{"anonymous", BasePath + "accounts/1/", "", app, false, 401, "not_authenticated", false},
		{"forbidden", BasePath + "accounts/1/", "", denied, true, 403, "permission_denied", false},
		{"noncanonical id", BasePath + "accounts/-0/", "", app, true, 400, "parse_error", false},
		{"query", BasePath + "accounts/1/?extra=1", "", app, true, 400, "parse_error", false},
		{"empty query marker", BasePath + "accounts/1/?", "", app, true, 400, "parse_error", false},
		{"canonical before body", BasePath + "accounts/1/", "1", app, true, 400, "parse_error", true},
		{"large exact before body", BasePath + "accounts/1/", "9007199254740993", app, true, 400, "parse_error", true},
		{"last writable before body", BasePath + "accounts/1/", "9223372036854775806", app, true, 400, "parse_error", true},
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
		t.Fatal("rejection reached selected owner I/O")
	}
}

func TestSelectedManagementDeleteRejectsBodyBeforeOwner(t *testing.T) {
	app, probe := headerApplication(t, true)
	for _, resource := range []string{"accounts", "groups", "permissions"} {
		for _, mode := range []string{"known_length", "unknown_length", "chunked"} {
			t.Run(resource+"/"+mode, func(t *testing.T) {
				reader := &headerObservedBody{Reader: strings.NewReader("{}")}
				request := httptest.NewRequest("DELETE", BasePath+resource+"/0/", nil)
				request.Body, request.ContentLength = reader, 2
				if mode != "known_length" {
					request.ContentLength = -1
				}
				if mode == "chunked" {
					request.TransferEncoding = []string{"chunked"}
				}
				request.Header.Set("Authorization", "Bearer fixture")
				request.Header.Set("If-Revision", "1")
				request.Header.Set("Content-Type", api.JSONContentType)
				response := httptest.NewRecorder()
				app.ServeHTTP(response, request)
				var envelope struct{ Code string }
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 400 || envelope.Code != "parse_error" || reader.reads != 0 || reader.closes != 0 || probe.reads.Load() != 0 || probe.writes.Load() != 0 {
					t.Fatal("DELETE body entered its lifecycle owner", response.Code, response.Body, reader, err)
				}
				if response.Header().Get("Revision") != "" || response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("body refusal published/cacheable state")
				}
			})
		}
	}
}
