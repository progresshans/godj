package sessionauth

import (
	"errors"
	"net/http"
	"testing"
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
