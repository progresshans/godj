package sessionauth

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/progresshans/godj/auth"
)

func TestCookieConfigurationDefaultsSecureAndRequiresExplicitLoopbackOptOut(t *testing.T) {
	t.Parallel()
	secure, err := normalizeCookie(CookieConfig{}, DefaultSessionCookieName, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !secure.Secure || secure.AllowInsecure || secure.SameSite != http.SameSiteLaxMode || secure.Path != "/" {
		t.Fatalf("unexpected secure defaults: %+v", secure)
	}
	loopback, err := normalizeCookie(CookieConfig{AllowInsecure: true}, DefaultSessionCookieName, 0)
	if err != nil {
		t.Fatal(err)
	}
	if loopback.Secure || !loopback.AllowInsecure {
		t.Fatalf("explicit loopback policy was not preserved: %+v", loopback)
	}
	_, err = normalizeCookie(CookieConfig{Secure: true, AllowInsecure: true}, DefaultSessionCookieName, 0)
	if !errors.Is(err, &Error{Code: CodeInvalidConfig}) {
		t.Fatalf("conflicting cookie policy was accepted: %v", err)
	}
}

func TestAuthorizedCannotGrantPermissionMissingFromPrincipalSnapshot(t *testing.T) {
	t.Parallel()
	view, err := auth.NewPermission("articles.view")
	if err != nil {
		t.Fatal(err)
	}
	add, err := auth.NewPermission("articles.add")
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "viewer", Active: true, Permissions: []auth.Permission{view}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{authorizer: grantAllAuthorizer{}}
	allowed, err := runtime.Authorized(context.Background(), principal, add)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("Authorizer granted a permission absent from the principal snapshot")
	}
}

type grantAllAuthorizer struct{}

func (grantAllAuthorizer) Allowed(context.Context, auth.Principal, auth.Permission) (bool, error) {
	return true, nil
}

func TestRuntimeCookiesApplyToUsesRFCPathBoundaryForBothCookies(t *testing.T) {
	t.Parallel()
	runtime := &Runtime{
		sessionCookie: CookieConfig{Path: "/admin"},
		csrfCookie:    CookieConfig{Path: "/admin/"},
	}
	for _, path := range []string{"/admin/", "/admin/login/", "/admin/articles/"} {
		if !runtime.CookiesApplyTo(path) {
			t.Fatalf("CookiesApplyTo(%q) = false", path)
		}
	}
	for _, path := range []string{"/", "/administrator/", "/other/", "/admin?query"} {
		if runtime.CookiesApplyTo(path) {
			t.Fatalf("CookiesApplyTo(%q) = true", path)
		}
	}
	runtime.csrfCookie.Path = "/admin/login/"
	if runtime.CookiesApplyTo("/admin/") {
		t.Fatal("narrow CSRF cookie path covers the Admin root")
	}
}

func TestAllowedNextPathsReturnsSortedDetachedSnapshot(t *testing.T) {
	t.Parallel()
	runtime := &Runtime{allowedNextPaths: map[string]struct{}{"/z/": {}, "/a/": {}}}
	paths := runtime.AllowedNextPaths()
	if !reflect.DeepEqual(paths, []string{"/a/", "/z/"}) {
		t.Fatalf("AllowedNextPaths() = %v", paths)
	}
	paths[0] = "/forged/"
	if !reflect.DeepEqual(runtime.AllowedNextPaths(), []string{"/a/", "/z/"}) {
		t.Fatal("AllowedNextPaths() returned an aliased snapshot")
	}
}
