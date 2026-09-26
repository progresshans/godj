package identitytest

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

func verifyManagedPasswordHTTP(t *testing.T, runtime *systemstate.Runtime, f *managementFixture, revision int64) {
	t.Helper()
	h := newIdentityHTTPWithStore(t, runtime.Authenticator(), runtime.SessionStore())
	address, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []int{403, 200, 403} {
		client := h.newClient(t)
		client.Jar.SetCookies(address, []*http.Cookie{{Name: sessionauth.DefaultSessionCookieName, Value: f.ids[index].Encoded(), Path: "/"}})
		h.expect(t, client, "/view/", want, managementNewPassword)
	}
	h.login(t, h.newClient(t), "member", managementOldPassword, 401)
	h.login(t, h.client, "member", managementNewPassword, 200)
	h.expect(t, h.client, "/view/", 200, managementNewPassword)
	if f.stored(t).Revision != revision {
		t.Fatal("HTTP resolution rewrote the user revision")
	}
}
