package siteapp

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/identity/models"
	websessionauth "github.com/progresshans/godj/web/sessionauth"
)

func TestArticleSitePublishesIdentityAPIAndCreatedStaffCanLogin(t *testing.T) {
	fixture := newSiteAppCSRFFixture(t, websessionauth.CSRFKeyRing{})
	path := "/api/identity/users/"
	anonymous := fixture.request(t, "GET", fixture.firstURL+path, api.JSONContentType, "", "")
	if anonymous.status != 403 {
		t.Fatal("anonymous management admission")
	}
	fixture.login(t)
	token := fixture.apiToken(t, fixture.firstURL)
	created := fixture.request(t, "POST", fixture.firstURL+path, api.JSONContentType, `{"username":"created-staff","password":"  untrimmed password  ","staff":true}`, token)
	if created.status != 201 || created.header.Get("Revision") != "1" || strings.Contains(created.body, "password") || strings.Contains(created.body, "principal_id") {
		t.Fatal("management API not composed or private projection")
	}
	var user struct{ ID, Revision int64 }
	if err := json.Unmarshal([]byte(created.body), &user); err != nil || user.ID <= 0 {
		t.Fatal("created response", err)
	}
	other := fixture.request(t, "GET", fixture.secondURL+path, api.JSONContentType, "", "")
	if other.status != 200 || !strings.Contains(other.body, `"username":"created-staff"`) {
		t.Fatal("other runtime cannot read committed management state")
	}
	document := fixture.request(t, "GET", fixture.firstURL+"/api/identity/openapi.json", api.JSONContentType, "", "")
	if document.status != 200 || !strings.Contains(document.body, `"If-Revision"`) {
		t.Fatal("protected identity document not composed")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := *fixture.client
	client.Jar = jar
	member := fixture
	member.client = &client
	member.username = "created-staff"
	member.password = "  untrimmed password  "
	member.login(t)
	denied := member.request(t, http.MethodGet, fixture.firstURL+path, api.JSONContentType, "", "")
	if denied.status != 403 {
		t.Fatal("staff flag granted model management authority")
	}
	row, found, err := models.UserObjects.Using(fixture.firstBackend).Filter(models.UserFields.ID.Exact(user.ID)).OrderBy(models.UserFields.ID.Asc()).First(t.Context())
	if err != nil || !found || !row.Staff || row.Superuser || row.PrincipalID == "created-staff" || row.Revision != 1 {
		t.Fatal("server identity/profile not persisted", err)
	}
}
