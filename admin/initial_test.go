package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	formmodel "github.com/progresshans/godj/forms/model"
)

func TestAdminNarrowedInputDisplaysStoredInitialAndValidatesCorrection(t *testing.T) {
	const stored = `long<&"`
	harness := newSiteApplicationHarnessWithAuthorizer(t, 10, auth.PrincipalAuthorizer{}, func(config *ModelConfig[registryArticle]) {
		config.FormOverrides = []formmodel.Override{formmodel.OverrideField("title", formmodel.WithMaxLength(4))}
	})
	harness.state.mu.Lock()
	row := harness.state.articles[2]
	row.title = stored
	harness.state.articles[2] = row
	harness.state.mu.Unlock()
	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/articles/")
	change := client.do(http.MethodGet, "/admin/articles/change/?id=2", nil)
	body := change.Body.String()
	if change.Code != http.StatusOK || !strings.Contains(body, `value="long&lt;&amp;&#34;"`) || strings.Contains(body, stored) {
		t.Fatalf("stored initial was rejected or rendered unsafely: %d %s", change.Code, body)
	}
	token := siteCSRFToken(t, body)
	before := harness.state.mutationCounts()
	invalid := client.do(http.MethodPost, "/admin/articles/change/?id=2", url.Values{
		"csrfmiddlewaretoken": {token}, "title": {stored}, "summary": {""},
	})
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), `data-error-code="max_length"`) || !strings.Contains(invalid.Body.String(), `value="long&lt;&amp;&#34;"`) || harness.state.mutationCounts() != before {
		t.Fatalf("unchanged invalid submission was not rejected safely: %d %s", invalid.Code, invalid.Body.String())
	}
	valid := client.do(http.MethodPost, "/admin/articles/change/?id=2", url.Values{
		"csrfmiddlewaretoken": {token}, "title": {"new"}, "summary": {""},
	})
	if valid.Code != http.StatusFound {
		t.Fatalf("valid correction failed: %d %s", valid.Code, valid.Body.String())
	}
	harness.state.mu.Lock()
	row = harness.state.articles[2]
	harness.state.mu.Unlock()
	if row.title != "new" {
		t.Fatal("valid correction did not reach storage")
	}
}
