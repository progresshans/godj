package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestChoiceAdminSelectEscapingOldValuesAndMutationValidation(t *testing.T) {
	const stored = `quote"<x>`
	harness := newSiteApplicationHarnessWithAuthorizer(t, 10, auth.PrincipalAuthorizer{}, func(config *ModelConfig[registryArticle]) {
		for index := range config.Model.Fields {
			if config.Model.Fields[index].Name == "title" {
				config.Model.Fields[index].Choices = []ir.Choice{
					schema.Choice("First article", "<b>First & known</b>"),
					schema.Choice(stored, "<Chosen>"),
				}
			}
		}
	})
	client := newSiteHTTPClient(harness.application)
	client.login(t, "admin", "secret", "/admin/articles/")
	list := client.do(http.MethodGet, "/admin/articles/", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "&lt;b&gt;First &amp; known&lt;/b&gt;") || strings.Contains(list.Body.String(), "<b>First") || !strings.Contains(list.Body.String(), "Second article") {
		t.Fatalf("choice list labels or old value: %d %s", list.Code, list.Body.String())
	}
	change := client.do(http.MethodGet, "/admin/articles/change/?id=2", nil)
	body := change.Body.String()
	if change.Code != http.StatusOK || !strings.Contains(body, `<select name="title" required>`) || !strings.Contains(body, `<option value="Second article" selected>Second article (not a current choice)</option>`) || !strings.Contains(body, `quote&#34;&lt;x&gt;`) || strings.Contains(body, `<Chosen>`) {
		t.Fatalf("choice change form: %d %s", change.Code, body)
	}
	if strings.Contains(body, `<input name="title"`) {
		t.Fatal("select duplicated a scalar input")
	}
	token := siteCSRFToken(t, body)
	before := harness.state.mutationCounts()
	invalid := client.do(http.MethodPost, "/admin/articles/change/?id=2", url.Values{
		"csrfmiddlewaretoken": {token}, "title": {`<script>alert(1)</script>`}, "summary": {""},
	})
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), `data-error-code="invalid_choice"`) || strings.Contains(invalid.Body.String(), `<script>`) || !strings.Contains(invalid.Body.String(), `&lt;script&gt;`) || harness.state.mutationCounts() != before {
		t.Fatalf("invalid choice mutated or was not safely rendered: %d %s", invalid.Code, invalid.Body.String())
	}
	valid := client.do(http.MethodPost, "/admin/articles/change/?id=2", url.Values{
		"csrfmiddlewaretoken": {token}, "title": {stored}, "summary": {""},
	})
	if valid.Code != http.StatusFound {
		t.Fatalf("valid choice: %d %s", valid.Code, valid.Body.String())
	}
	harness.state.mu.Lock()
	row := harness.state.articles[2]
	harness.state.mu.Unlock()
	if row.title != stored {
		t.Fatal("display labels replaced stored values")
	}
	list = client.do(http.MethodGet, "/admin/articles/", nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "&lt;Chosen&gt;") {
		t.Fatal("changed choice label not displayed")
	}
	deleted := client.do(http.MethodPost, "/admin/articles/delete/?id=2", url.Values{"csrfmiddlewaretoken": {token}, "confirm": {"yes"}})
	if deleted.Code != http.StatusFound {
		t.Fatalf("delete choice row: %d %s", deleted.Code, deleted.Body.String())
	}
	harness.state.mu.Lock()
	history := append([]AuditEntry(nil), harness.state.history[2]...)
	harness.state.mu.Unlock()
	if len(history) != 1 || history[0].DisplayLabel != stored {
		t.Fatal("display labels replaced audit values")
	}
}
