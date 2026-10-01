package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/web/sessionauth"
)

func TestAdminAdmissionRequiresActiveStaffIndependentlyOfSuperuserAndGrants(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, staff := range []bool{false, true} {
			for _, superuser := range []bool{false, true} {
				t.Run(fmt.Sprintf("active_%t_staff_%t_superuser_%t", active, staff, superuser), func(t *testing.T) {
					// An explicit old site grant must not replace staff admission either.
					principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "role-user", Active: active, Staff: staff, Superuser: superuser, Permissions: []auth.Permission{"godj.admin.access", "articles.view"}})
					if err != nil {
						t.Fatal(err)
					}
					h := newSiteApplicationHarnessIdentities(t, 2, auth.PrincipalAuthorizer{}, []siteIdentity{{username: "role-user", principal: principal}})
					client := newSiteHTTPClient(h.application)
					page := client.do(http.MethodGet, "/admin/login/", nil)
					token := siteCSRFToken(t, page.Body.String())
					h.store.resetCounts()
					response := client.do(http.MethodPost, "/admin/login/", url.Values{"csrfmiddlewaretoken": {token}, "username": {"role-user"}, "password": {"secret"}, "next": {"/admin/"}})
					allowed := active && staff
					if !allowed {
						counts := h.store.counts()
						if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-login-error="invalid_credentials"`) || client.cookieValue(sessionauth.DefaultSessionCookieName) != "" || counts.creates != 0 || counts.rotates != 0 {
							t.Fatal("non-staff or inactive login published session state", response.Code, counts)
						}
					} else {
						if response.Code != http.StatusFound || client.cookieValue(sessionauth.DefaultSessionCookieName) == "" {
							t.Fatal("active staff login failed", response.Code)
						}
						response = client.do(http.MethodGet, "/admin/", nil)
						if response.Code != http.StatusOK {
							t.Fatal("active staff could not enter Admin", response.Code)
						}
					}
				})
			}
		}
	}
}
