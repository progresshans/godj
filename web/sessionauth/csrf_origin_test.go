package sessionauth_test

import (
	"net/http"
	"testing"

	"github.com/progresshans/godj/web/sessionauth"
)

func TestCSRFRejectsCrossOriginSignedPairTransplant(t *testing.T) {
	h := newHarness(t, true)
	defer h.Close()
	raw := *h.client
	raw.Jar = nil

	// The other client obtains an authentic anonymous pair from this runtime.
	response, err := raw.Get(h.server.URL + loginPath)
	if err != nil {
		t.Fatal(err)
	}
	otherCookie := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultCSRFCookieName)
	otherToken := readBody(t, response)

	response = h.Do(t, http.MethodGet, loginPath, nil)
	token := readBody(t, response)
	response = h.Do(t, http.MethodPost, loginPath, http.Header{
		"X-Test-Form-Token": {token}, "X-Test-Username": {"admin"}, "X-Test-Password": {"correct"},
	})
	if response.StatusCode != http.StatusFound {
		t.Fatalf("login = %d", response.StatusCode)
	}
	session := namedResponseCookie(t, response.Cookies(), sessionauth.DefaultSessionCookieName)
	closeBody(t, response)

	for _, tc := range []struct {
		name    string
		headers http.Header
	}{
		{"sibling", http.Header{"Sec-Fetch-Site": {"same-site"}, "Origin": {"https://evil.example.test"}}},
		{"cross-site", http.Header{"Sec-Fetch-Site": {"cross-site"}, "Origin": {"https://other.test"}}},
		{"metadata only", http.Header{"Sec-Fetch-Site": {"same-site"}}},
		{"origin fallback", http.Header{"Origin": {"https://evil.example.test"}}},
		{"opaque origin", http.Header{"Origin": {"null"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, h.server.URL+logoutPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header = tc.headers.Clone()
			request.Header.Set(sessionauth.DefaultCSRFHeader, otherToken)
			// Model the browser after only its host-only CSRF cookie expires:
			// the authenticated session and one injected domain cookie remain.
			request.AddCookie(session)
			request.AddCookie(otherCookie)
			response, err := raw.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer closeBody(t, response)
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("transplanted pair = %d", response.StatusCode)
			}
		})
	}

	response = h.Do(t, http.MethodGet, protectedPath, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("denied requests removed the session: %d", response.StatusCode)
	}
	closeBody(t, response)

	// Safe navigation is allowed; it also supplies the current same-origin token.
	response = h.Do(t, http.MethodGet, loginPath, http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("safe navigation = %d", response.StatusCode)
	}
	token = readBody(t, response)
	response = h.Do(t, http.MethodPost, logoutPath, http.Header{
		"Origin": {h.server.URL}, sessionauth.DefaultCSRFHeader: {token},
	})
	if response.StatusCode != http.StatusFound {
		t.Fatalf("same-origin logout = %d", response.StatusCode)
	}
	closeBody(t, response)
	response = h.Do(t, http.MethodGet, protectedPath, nil)
	if response.StatusCode != http.StatusFound {
		t.Fatalf("logout did not remove session: %d", response.StatusCode)
	}
	closeBody(t, response)
}
